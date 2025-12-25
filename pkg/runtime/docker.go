package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type DockerRuntime struct {
	Command string
}

func NewDockerRuntime() *DockerRuntime {
	return &DockerRuntime{Command: "docker"}
}

func (r *DockerRuntime) Run(ctx context.Context, config RunConfig) (string, error) {
	args, err := buildCommonRunArgs(config)
	if err != nil {
		return "", err
	}

	// Docker supports --init, which we want to use if possible.
	// We insert it after 'run'
	newArgs := []string{"run", "--init"}
	newArgs = append(newArgs, args[1:]...)

	out, err := runSimpleCommand(ctx, r.Command, newArgs...)
	if err != nil {
		return "", fmt.Errorf("docker run failed: %w (output: %s)", err, out)
	}
	return out, nil
}

func (r *DockerRuntime) Stop(ctx context.Context, id string) error {
	_, err := runSimpleCommand(ctx, r.Command, "stop", id)
	return err
}

func (r *DockerRuntime) Delete(ctx context.Context, id string) error {
	_, err := runSimpleCommand(ctx, r.Command, "rm", id)
	return err
}

type dockerListOutput struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Status string `json:"Status"`
	Image  string `json:"Image"`
	Labels string `json:"Labels"`
}

func (r *DockerRuntime) List(ctx context.Context, labelFilter map[string]string) ([]AgentInfo, error) {
	args := []string{"ps", "-a", "--no-trunc", "--format", "{{json .}}"}
	cmd := exec.CommandContext(ctx, r.Command, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %w (output: %s)", err, string(out))
	}

	var agents []AgentInfo
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var data dockerListOutput
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			continue
		}

		// Parse Labels string "key1=val1,key2=val2"
		labels := make(map[string]string)
		if data.Labels != "" {
			pairs := strings.Split(data.Labels, ",")
			for _, pair := range pairs {
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) == 2 {
					labels[kv[0]] = kv[1]
				}
			}
		}

		// Filter by labels if requested
		if len(labelFilter) > 0 {
			match := true
			for k, v := range labelFilter {
				if lv, ok := labels[k]; !ok || lv != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		agents = append(agents, AgentInfo{
			ID:        data.ID,
			Name:      data.Names,
			Template:  labels["scion.template"],
			Grove:     labels["scion.grove"],
			GrovePath: labels["scion.grove_path"],
			Labels:    labels,
			Status:    data.Status,
			Image:     data.Image,
		})
	}
	return agents, nil
}

func (r *DockerRuntime) GetLogs(ctx context.Context, id string) (string, error) {
	return runSimpleCommand(ctx, r.Command, "logs", id)
}

func (r *DockerRuntime) Attach(ctx context.Context, id string) error {
	// Check if the container exists and get its labels
	agents, err := r.List(ctx, nil)
	var agent *AgentInfo
	if err == nil {
		for _, a := range agents {
			// Match by full ID, short ID (12 chars), or name (with or without leading slash)
			if a.ID == id || (len(id) >= 12 && strings.HasPrefix(a.ID, id)) || (len(a.ID) >= 12 && strings.HasPrefix(id, a.ID)) ||
				a.Name == id || a.Name == "/"+id || strings.TrimPrefix(a.Name, "/") == id {
				agent = &a
				break
			}
		}
	}

	if agent == nil {
		return fmt.Errorf("agent '%s' not found", id)
	}

	// Check if the container is using tmux
	useTmux := agent.Labels["scion.tmux"] == "true"

	if useTmux {
		return runInteractiveCommand(r.Command, "exec", "-it", id, "tmux", "attach", "-t", "scion")
	} else {
		return runInteractiveCommand(r.Command, "attach", id)
	}
}

func (r *DockerRuntime) ImageExists(ctx context.Context, image string) (bool, error) {
	_, err := runSimpleCommand(ctx, r.Command, "image", "inspect", image)
	return err == nil, nil
}
