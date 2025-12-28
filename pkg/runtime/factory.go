package runtime

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/ptone/scion-agent/pkg/api"
	"github.com/ptone/scion-agent/pkg/config"
	"github.com/ptone/scion-agent/pkg/k8s"
)

// GetRuntime returns the appropriate Runtime implementation based on environment,
// agent configuration (if available via GetAgentSettings), and grove/global settings.
func GetRuntime(grovePath string, runtimeOverride string) Runtime {
	var runtimeType string

	// We resolve the project dir from grovePath to load settings correctly
	// If grovePath is empty, GetResolvedProjectDir handles it by looking for .scion or falling back to global
	projectDir, _ := config.GetResolvedProjectDir(grovePath)
	s, _ := config.LoadSettings(projectDir)

	if runtimeOverride != "" {
		runtimeType = runtimeOverride
	} else if s != nil && s.DefaultRuntime != "" {
		runtimeType = s.DefaultRuntime
	}

	if runtimeType == "local" {
		if runtime.GOOS == "darwin" {
			runtimeType = "container"
		} else {
			runtimeType = "docker"
		}
	}

	if runtimeType == "remote" {
		runtimeType = "kubernetes"
	}

	switch runtimeType {
	case "container":
		return NewAppleContainerRuntime()
	case "docker":
		return NewDockerRuntime()
	case "kubernetes", "k8s":
		k8sClient, err := k8s.NewClient(os.Getenv("KUBECONFIG"))
		if err != nil {
			return &ErrorRuntime{Err: err}
		}
		rt := NewKubernetesRuntime(k8sClient)
		if s != nil && s.Kubernetes.DefaultNamespace != "" {
			rt.DefaultNamespace = s.Kubernetes.DefaultNamespace
		}
		return rt
	case "true":
		// Fall through to auto-detection
	}

	// Auto-detection: check for available runtimes
	// On macOS, 'container' is often preferred for performance if available,
	// but both are fully supported.
	if _, err := exec.LookPath("container"); err == nil {
		return NewAppleContainerRuntime()
	}

	if _, err := exec.LookPath("docker"); err == nil {
		return NewDockerRuntime()
	}

	// Default return - the caller will handle the error if the binary is missing
	return NewAppleContainerRuntime()
}

type ErrorRuntime struct {
	Err error
}

func (e *ErrorRuntime) Name() string {
	return "error"
}

func (e *ErrorRuntime) Run(ctx context.Context, config RunConfig) (string, error) {
	return "", e.Err
}

func (e *ErrorRuntime) Stop(ctx context.Context, id string) error {
	return e.Err
}

func (e *ErrorRuntime) Delete(ctx context.Context, id string) error {
	return e.Err
}

func (e *ErrorRuntime) List(ctx context.Context, labelFilter map[string]string) ([]api.AgentInfo, error) {
	return nil, e.Err
}

func (e *ErrorRuntime) GetLogs(ctx context.Context, id string) (string, error) {
	return "", e.Err
}

func (e *ErrorRuntime) Attach(ctx context.Context, id string) error {
	return e.Err
}

func (e *ErrorRuntime) ImageExists(ctx context.Context, image string) (bool, error) {
	return false, e.Err
}

func (e *ErrorRuntime) PullImage(ctx context.Context, image string) error {
	return e.Err
}
