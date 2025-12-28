package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ptone/scion-agent/pkg/api"
	"github.com/ptone/scion-agent/pkg/config"
	"github.com/ptone/scion-agent/pkg/harness"
	"github.com/ptone/scion-agent/pkg/runtime"
	"github.com/ptone/scion-agent/pkg/util"
)

func (m *AgentManager) Start(ctx context.Context, opts api.StartOptions) (*api.AgentInfo, error) {
	// 0. Check if container already exists
	agents, err := m.Runtime.List(ctx, nil)
	if err == nil {
		for _, a := range agents {
			if a.ID == opts.Name || a.Name == opts.Name {
				isRunning := (strings.HasPrefix(a.Status, "Up") || a.Status == "Running")
				if isRunning {
					return &a, nil
				}
				// If it exists but not running, we delete it so we can recreate it
				if err := m.Runtime.Delete(ctx, a.ID); err != nil {
					return nil, fmt.Errorf("failed to cleanup existing container: %w", err)
				}
			}
		}
	}

	projectDir, err := config.GetResolvedProjectDir(opts.GrovePath)
	if err != nil {
		return nil, err
	}
	groveName := config.GetGroveName(projectDir)

	agentDir, agentHome, agentWorkspace, finalScionCfg, err := GetAgent(opts.Name, opts.Template, opts.Image, opts.GrovePath, "")
	if err != nil {
		return nil, err
	}

	if finalScionCfg != nil && finalScionCfg.HarnessProvider == "claude" {
		_ = UpdateClaudeJSON(opts.Name, agentHome, agentWorkspace)
	}

	promptFile := filepath.Join(agentDir, "prompt.md")
	promptFileContent := ""
	if content, err := os.ReadFile(promptFile); err == nil {
		promptFileContent = strings.TrimSpace(string(content))
	}

	task := opts.Task
	if task == "" && promptFileContent == "" {
		return nil, fmt.Errorf("no task provided: prompt.md is empty and no task was given in options")
	}

	if task != "" && promptFileContent != "" && task != promptFileContent {
		return nil, fmt.Errorf("task conflict: both prompt.md and start options provide a task")
	}

	if task == "" {
		task = promptFileContent
	} else if promptFileContent == "" {
		_ = os.WriteFile(promptFile, []byte(task), 0644)
	}

	// Resolve image
	resolvedImage := ""
	if finalScionCfg != nil && finalScionCfg.Image != "" {
		resolvedImage = finalScionCfg.Image
	}
	if opts.Image != "" {
		resolvedImage = opts.Image
	}
	if resolvedImage == "" {
		resolvedImage = "gemini-cli-sandbox"
	}

	harnessProvider := ""
	if finalScionCfg != nil {
		harnessProvider = finalScionCfg.HarnessProvider
	}
	h := harness.New(harnessProvider)

	// 3. Propagate credentials
	var auth api.AuthConfig
	if !opts.NoAuth {
		if opts.Auth != nil {
			auth, err = opts.Auth.GetAuthConfig(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get auth: %w", err)
			}
		} else {
			// Fallback to legacy discovery if no provider given
			auth = h.DiscoverAuth(agentHome)
		}
	}

	// 4. Launch container
	useTmux := false
	resolvedModel := "flash"
	unixUsername := "node"
	detached := true

	if finalScionCfg != nil {
		useTmux = finalScionCfg.IsUseTmux()
		detached = finalScionCfg.IsDetached()
		if finalScionCfg.Model != "" {
			resolvedModel = finalScionCfg.Model
		}
		if finalScionCfg.UnixUsername != "" {
			unixUsername = finalScionCfg.UnixUsername
		}
	}

	if opts.Detached != nil {
		detached = *opts.Detached
	}

	if opts.Model != "" {
		resolvedModel = opts.Model
	}

	if useTmux {
		tmuxImage := resolvedImage
		if !strings.Contains(tmuxImage, ":") {
			tmuxImage = tmuxImage + ":tmux"
		} else {
			parts := strings.SplitN(resolvedImage, ":", 2)
			tmuxImage = parts[0] + ":tmux"
		}
		resolvedImage = tmuxImage
	}

	exists, err := m.Runtime.ImageExists(ctx, resolvedImage)
	if err != nil || !exists {
		if err := m.Runtime.PullImage(ctx, resolvedImage); err != nil {
			if useTmux {
				return nil, fmt.Errorf("tmux support requested but image '%s' not found and pull failed: %w. Please ensure the image has a :tmux tag.", resolvedImage, err)
			}
			return nil, fmt.Errorf("failed to pull image '%s': %w", resolvedImage, err)
		}
	}

	agentEnv := []string{}
	if finalScionCfg != nil && finalScionCfg.Env != nil {
		for k, v := range finalScionCfg.Env {
			agentEnv = append(agentEnv, fmt.Sprintf("%s=%s", k, v))
		}
	}
	// Add opts.Env
	for k, v := range opts.Env {
		agentEnv = append(agentEnv, fmt.Sprintf("%s=%s", k, v))
	}

	template := ""
	if finalScionCfg != nil {
		template = finalScionCfg.Template
	}

	repoRoot := ""
	if util.IsGitRepo() {
		repoRoot, _ = util.RepoRoot()
	}

	runCfg := runtime.RunConfig{
		Name:         opts.Name,
		Template:     template,
		UnixUsername: unixUsername,
		Image:        resolvedImage,
		HomeDir:      agentHome,
		Workspace:    agentWorkspace,
		RepoRoot:     repoRoot,
		Auth:         auth,
		Harness:      h,
		UseTmux:      useTmux,
		Model:        resolvedModel,
		Task:         task,
		Env:          agentEnv,
		Volumes: func() []api.VolumeMount {
			if finalScionCfg != nil {
				return finalScionCfg.Volumes
			}
			return nil
		}(),
		Resume: opts.Resume,
		Labels: map[string]string{
			"scion.agent": "true",
			"scion.name":  opts.Name,
			"scion.grove": groveName,
		},
		Annotations: map[string]string{
			"scion.grove_path": projectDir,
		},
	}
	id, err := m.Runtime.Run(ctx, runCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to launch container: %w", err)
	}

	status := "running"
	if opts.Resume {
		status = "resumed"
	}
	_ = UpdateAgentConfig(opts.Name, opts.GrovePath, status, m.Runtime.Name())
	
			// Fetch fresh info
			allAgents, err := m.Runtime.List(ctx, map[string]string{"scion.name": opts.Name})
			if err == nil {
				for _, a := range allAgents {
					if a.ID == id || a.Name == opts.Name {
						a.Detached = detached
						return &a, nil
					}
				}
			}
		
			return &api.AgentInfo{ID: id, Name: opts.Name, Status: status, Detached: detached}, nil
		}
		