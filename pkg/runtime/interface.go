package runtime

import (
	"context"

	"github.com/ptone/scion-agent/pkg/api"
	"github.com/ptone/scion-agent/pkg/harness"
)

type RunConfig struct {
	Name         string
	Template     string
	UnixUsername string
	Image        string
	HomeDir      string
	Workspace    string
	RepoRoot     string
	Env          []string
	Volumes      []api.VolumeMount
	Labels       map[string]string
	Annotations  map[string]string
	Auth         api.AuthConfig
	Harness      harness.Harness
	UseTmux      bool
	Model        string
	Task         string
	Resume       bool
}

type Runtime interface {
	Name() string
	Run(ctx context.Context, config RunConfig) (string, error)
	Stop(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, labelFilter map[string]string) ([]api.AgentInfo, error)
	GetLogs(ctx context.Context, id string) (string, error)
	Attach(ctx context.Context, id string) error
	ImageExists(ctx context.Context, image string) (bool, error)
	PullImage(ctx context.Context, image string) error
}
