package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ptone/scion-agent/pkg/util"
)

// buildCommonRunArgs constructs the common arguments for 'run' command across different runtimes.
func buildCommonRunArgs(config RunConfig) ([]string, error) {
	args := []string{"run", "-d", "-i", "-t"}
	addArg := func(flag string, values ...string) {
		for _, v := range values {
			args = append(args, flag, v)
		}
	}
	addEnv := func(name, value string) {
		if value != "" {
			addArg("-e", fmt.Sprintf("%s=%s", name, value))
		}
	}

	addArg("--name", config.Name)

	if config.HomeDir != "" {
		addArg("-v", fmt.Sprintf("%s:/home/%s", config.HomeDir, config.UnixUsername))
	}
	if config.RepoRoot != "" && config.Workspace != "" {
		relWorkspace, err := filepath.Rel(config.RepoRoot, config.Workspace)
		if err == nil && !strings.HasPrefix(relWorkspace, "..") {
			// Mount .git
			addArg("-v", fmt.Sprintf("%s/.git:/repo-root/.git", config.RepoRoot))
			// Mount workspace at same relative path
			containerWorkspace := filepath.Join("/repo-root", relWorkspace)
			addArg("-v", fmt.Sprintf("%s:%s", config.Workspace, containerWorkspace))
			addArg("--workdir", containerWorkspace)
		} else {
			// Fallback if workspace is outside repo root
			addArg("-v", fmt.Sprintf("%s:/workspace", config.Workspace))
			addArg("--workdir", "/workspace")
		}
	} else if config.Workspace != "" {
		addArg("-v", fmt.Sprintf("%s:/workspace", config.Workspace))
		addArg("--workdir", "/workspace")
	}

	// Propagate Auth
	propagateFile := func(src, containerPath, authType string) error {
		if src == "" {
			return nil
		}
		if config.HomeDir != "" {
			// containerPath is absolute, e.g. /home/scion/.gemini/oauth_creds.json
			// relative path from home: .gemini/oauth_creds.json
			rel, err := filepath.Rel(fmt.Sprintf("/home/%s", config.UnixUsername), containerPath)
			if err != nil {
				return err
			}
			dst := filepath.Join(config.HomeDir, rel)
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return err
			}
			if err := util.CopyFile(src, dst); err != nil {
				return fmt.Errorf("failed to copy %s: %w", authType, err)
			}
		} else {
			addArg("-v", fmt.Sprintf("%s:%s:ro", src, containerPath))
		}
		if authType != "" {
			addEnv("GEMINI_DEFAULT_AUTH_TYPE", authType)
		}
		return nil
	}

	addEnv("GEMINI_API_KEY", config.Auth.GeminiAPIKey)
	addEnv("GOOGLE_API_KEY", config.Auth.GoogleAPIKey)
	if config.Auth.GeminiAPIKey != "" || config.Auth.GoogleAPIKey != "" {
		addEnv("GEMINI_DEFAULT_AUTH_TYPE", "gemini-api-key")
	}

	addEnv("VERTEX_API_KEY", config.Auth.VertexAPIKey)
	if config.Auth.VertexAPIKey != "" {
		addEnv("GEMINI_DEFAULT_AUTH_TYPE", "vertex-ai")
	}

	oauthPath := fmt.Sprintf("/home/%s/.gemini/oauth_creds.json", config.UnixUsername)
	if err := propagateFile(config.Auth.OAuthCreds, oauthPath, "oauth-personal"); err != nil {
		return nil, err
	}

	addEnv("GOOGLE_CLOUD_PROJECT", config.Auth.GoogleCloudProject)

	adcPath := fmt.Sprintf("/home/%s/.config/gcp/application_default_credentials.json", config.UnixUsername)
	if config.Auth.GoogleAppCredentials != "" {
		if err := propagateFile(config.Auth.GoogleAppCredentials, adcPath, "compute-default-credentials"); err != nil {
			return nil, err
		}
		addEnv("GOOGLE_APPLICATION_CREDENTIALS", adcPath)
	}

	addEnv("GEMINI_MODEL", config.Model)

	// Mount gcloud config if it exists
	home, _ := os.UserHomeDir()
	gcloudConfigDir := filepath.Join(home, ".config", "gcloud")
	if _, err := os.Stat(gcloudConfigDir); err == nil {
		addArg("-v", fmt.Sprintf("%s:/home/%s/.config/gcloud:ro", gcloudConfigDir, config.UnixUsername))
	}

	for _, e := range config.Env {
		addArg("-e", e)
	}

	for k, v := range config.Labels {
		addArg("--label", fmt.Sprintf("%s=%s", k, v))
	}
	if config.Template != "" {
		addArg("--label", fmt.Sprintf("scion.template=%s", config.Template))
	}
	if config.UseTmux {
		addArg("--label", "scion.tmux=true")
	}

	args = append(args, config.Image)

	geminiArgs := []string{"gemini", "--yolo"}
	if config.Resume {
		geminiArgs = append(geminiArgs, "--resume")
	}
	geminiArgs = append(geminiArgs, "--prompt-interactive", config.Task)

	if config.UseTmux {
		geminiCmd := strings.Join(geminiArgs, " ")
		// Re-quote the task for the shell inside tmux
		geminiCmd = fmt.Sprintf("gemini --yolo %s--prompt-interactive %q", 
			func() string { if config.Resume { return "--resume " }; return "" }(), 
			config.Task)
		args = append(args, "tmux", "new-session", "-s", "scion", geminiCmd)
	} else {
		args = append(args, geminiArgs...)
	}

	return args, nil
}

func runSimpleCommand(ctx context.Context, command string, args ...string) (string, error) {
	if os.Getenv("SCION_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "Debug: %s %s\n", command, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, command, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s failed: %w", command, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func runInteractiveCommand(command string, args ...string) error {
	if os.Getenv("SCION_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "Debug: %s %s\n", command, strings.Join(args, " "))
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
