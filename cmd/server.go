package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ptone/scion-agent/pkg/agent"
	"github.com/ptone/scion-agent/pkg/api"
	"github.com/ptone/scion-agent/pkg/config"
	"github.com/ptone/scion-agent/pkg/hub"
	"github.com/ptone/scion-agent/pkg/runtime"
	"github.com/ptone/scion-agent/pkg/runtimehost"
	"github.com/ptone/scion-agent/pkg/store"
	"github.com/ptone/scion-agent/pkg/store/sqlite"
	"github.com/spf13/cobra"
)

// GlobalGroveName is the special name for the default grove when hub and runtime-host run together
const GlobalGroveName = "global"

var (
	serverConfigPath  string
	hubPort           int
	hubHost           string
	enableHub         bool
	enableRuntimeHost bool
	runtimeHostPort   int
	runtimeHostMode   string
	dbURL             string
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the Scion server components",
	Long: `Commands for managing the Scion server components.

The server provides:
- Hub API: Central registry for groves, agents, and templates (port 9810)
- Runtime Host API: Agent lifecycle management on compute nodes (port 9800)
- Web Frontend: Browser-based UI (coming soon, port 9820)`,
}

// serverStartCmd represents the server start command
var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Scion server components",
	Long: `Start one or more Scion server components.

Server Components:
- Hub API (--enable-hub): Central coordination for groves, agents, templates
- Runtime Host API (--enable-runtime-host): Agent lifecycle on this compute node

Configuration can be provided via:
- Config file (--config flag or ~/.scion/server.yaml)
- Environment variables (SCION_SERVER_* prefix)
- Command-line flags

Examples:
  # Start Hub API only
  scion server start --enable-hub

  # Start Runtime Host API only
  scion server start --enable-runtime-host

  # Start both Hub and Runtime Host
  scion server start --enable-hub --enable-runtime-host

  # Start Runtime Host with custom port
  scion server start --enable-runtime-host --runtime-host-port 9800`,
	RunE: runServerStart,
}

func runServerStart(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.LoadGlobalConfig(serverConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Override with command-line flags if specified
	if cmd.Flags().Changed("port") {
		cfg.Hub.Port = hubPort
	}
	if cmd.Flags().Changed("host") {
		cfg.Hub.Host = hubHost
	}
	if cmd.Flags().Changed("db") {
		cfg.Database.URL = dbURL
	}
	if cmd.Flags().Changed("enable-hub") {
		// If explicitly set, use the flag value
		// (enableHub is the variable, it's already set by cobra)
	}
	if cmd.Flags().Changed("enable-runtime-host") {
		cfg.RuntimeHost.Enabled = enableRuntimeHost
	}
	if cmd.Flags().Changed("runtime-host-port") {
		cfg.RuntimeHost.Port = runtimeHostPort
	}
	if cmd.Flags().Changed("runtime-host-mode") {
		cfg.RuntimeHost.Mode = runtimeHostMode
	}

	// Check if at least one server is enabled
	if !enableHub && !cfg.RuntimeHost.Enabled {
		return fmt.Errorf("no server components enabled; use --enable-hub or --enable-runtime-host")
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v, shutting down...", sig)
		cancel()
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	// Initialize store (needed for Hub and for global grove registration)
	var s store.Store
	if enableHub {
		switch cfg.Database.Driver {
		case "sqlite":
			sqliteStore, err := sqlite.New(cfg.Database.URL)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			s = sqliteStore
			defer s.Close()

			// Run migrations
			if err := s.Migrate(context.Background()); err != nil {
				return fmt.Errorf("failed to run migrations: %w", err)
			}
		default:
			return fmt.Errorf("unsupported database driver: %s", cfg.Database.Driver)
		}

		// Verify database connectivity
		if err := s.Ping(context.Background()); err != nil {
			return fmt.Errorf("database ping failed: %w", err)
		}
	}

	// Variables to track runtime host info for co-located registration
	var hostID string
	var hostName string
	var rt runtime.Runtime
	var hubSrv *hub.Server
	var mgr agent.Manager

	// Start Hub API if enabled
	if enableHub {
		// Create Hub server configuration
		hubCfg := hub.ServerConfig{
			Port:               cfg.Hub.Port,
			Host:               cfg.Hub.Host,
			ReadTimeout:        cfg.Hub.ReadTimeout,
			WriteTimeout:       cfg.Hub.WriteTimeout,
			CORSEnabled:        cfg.Hub.CORSEnabled,
			CORSAllowedOrigins: cfg.Hub.CORSAllowedOrigins,
			CORSAllowedMethods: cfg.Hub.CORSAllowedMethods,
			CORSAllowedHeaders: cfg.Hub.CORSAllowedHeaders,
			CORSMaxAge:         cfg.Hub.CORSMaxAge,
		}

		// Create Hub server
		hubSrv = hub.New(hubCfg, s)

		log.Printf("Starting Hub API server on %s:%d", cfg.Hub.Host, cfg.Hub.Port)
		log.Printf("Database: %s (%s)", cfg.Database.Driver, cfg.Database.URL)

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := hubSrv.Start(ctx); err != nil {
				errCh <- fmt.Errorf("hub server error: %w", err)
			}
		}()
	}

	// Start Runtime Host API if enabled
	if cfg.RuntimeHost.Enabled {
		// Initialize runtime (auto-detect based on environment)
		rt = runtime.GetRuntime("", "")

		// Create agent manager
		mgr = agent.NewManager(rt)

		// Generate host ID if not set
		hostID = cfg.RuntimeHost.HostID
		if hostID == "" {
			hostID = api.NewUUID()
		}

		// Set host name
		hostName = cfg.RuntimeHost.HostName
		if hostName == "" {
			if hostname, err := os.Hostname(); err == nil {
				hostName = hostname
			} else {
				hostName = "runtime-host"
			}
		}

		// Create Runtime Host server configuration
		rhCfg := runtimehost.ServerConfig{
			Port:               cfg.RuntimeHost.Port,
			Host:               cfg.RuntimeHost.Host,
			ReadTimeout:        cfg.RuntimeHost.ReadTimeout,
			WriteTimeout:       cfg.RuntimeHost.WriteTimeout,
			Mode:               cfg.RuntimeHost.Mode,
			HubEndpoint:        cfg.RuntimeHost.HubEndpoint,
			HostID:             hostID,
			HostName:           hostName,
			CORSEnabled:        cfg.RuntimeHost.CORSEnabled,
			CORSAllowedOrigins: cfg.RuntimeHost.CORSAllowedOrigins,
			CORSAllowedMethods: cfg.RuntimeHost.CORSAllowedMethods,
			CORSAllowedHeaders: cfg.RuntimeHost.CORSAllowedHeaders,
			CORSMaxAge:         cfg.RuntimeHost.CORSMaxAge,
		}

		// Create Runtime Host server
		rhSrv := runtimehost.New(rhCfg, mgr, rt)

		log.Printf("Starting Runtime Host API server on %s:%d (mode: %s)",
			cfg.RuntimeHost.Host, cfg.RuntimeHost.Port, cfg.RuntimeHost.Mode)

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rhSrv.Start(ctx); err != nil {
				errCh <- fmt.Errorf("runtime host server error: %w", err)
			}
		}()
	}

	// When both Hub and Runtime Host are enabled together, set up the dispatcher
	// for automatic agent handoff and register the global grove.
	if enableHub && cfg.RuntimeHost.Enabled && s != nil && hubSrv != nil && mgr != nil {
		// Set up the dispatcher to enable automatic agent handoff
		dispatcher := newAgentDispatcherAdapter(mgr, s)
		hubSrv.SetDispatcher(dispatcher)
		log.Printf("Agent dispatcher configured for co-located runtime host")

		// Register global grove and runtime host
		if err := registerGlobalGroveAndHost(ctx, s, hostID, hostName, rt); err != nil {
			log.Printf("Warning: failed to register global grove: %v", err)
		} else {
			log.Printf("Registered global grove with runtime host %s", hostName)
		}
	}

	// Wait for either an error or context cancellation
	select {
	case err := <-errCh:
		cancel() // Stop other servers
		return err
	case <-ctx.Done():
		// Wait for all servers to shutdown
		wg.Wait()
		return nil
	}
}

// registerGlobalGroveAndHost creates the global grove and registers this
// runtime host as a contributor. This enables automatic agent handoff.
func registerGlobalGroveAndHost(ctx context.Context, s store.Store, hostID, hostName string, rt runtime.Runtime) error {
	// Check if global grove already exists
	globalGrove, err := s.GetGroveBySlug(ctx, GlobalGroveName)
	if err != nil && err != store.ErrNotFound {
		return fmt.Errorf("failed to check for global grove: %w", err)
	}

	// Create global grove if it doesn't exist
	if globalGrove == nil {
		globalGrove = &store.Grove{
			ID:         api.NewUUID(),
			Name:       "Global",
			Slug:       GlobalGroveName,
			Visibility: store.VisibilityPrivate,
			Labels: map[string]string{
				"scion.io/system": "true",
				"scion.io/global": "true",
			},
		}

		if err := s.CreateGrove(ctx, globalGrove); err != nil {
			return fmt.Errorf("failed to create global grove: %w", err)
		}
	}

	// Create or update the runtime host record
	runtimeType := "docker"
	if rt != nil {
		runtimeType = rt.Name()
	}

	host, err := s.GetRuntimeHost(ctx, hostID)
	if err != nil && err != store.ErrNotFound {
		return fmt.Errorf("failed to check for runtime host: %w", err)
	}

	if host == nil {
		host = &store.RuntimeHost{
			ID:              hostID,
			Name:            hostName,
			Slug:            api.Slugify(hostName),
			Type:            runtimeType,
			Mode:            store.HostModeConnected,
			Version:         "0.1.0",
			Status:          store.HostStatusOnline,
			ConnectionState: "connected",
			Capabilities: &store.HostCapabilities{
				WebPTY: false,
				Sync:   true,
				Attach: true,
			},
			SupportedHarnesses: []string{"claude", "gemini", "opencode", "generic"},
			Runtimes: []store.HostRuntime{
				{Type: runtimeType, Available: true},
			},
		}

		if err := s.CreateRuntimeHost(ctx, host); err != nil {
			return fmt.Errorf("failed to create runtime host: %w", err)
		}
	} else {
		// Update existing host status
		host.Status = store.HostStatusOnline
		host.ConnectionState = "connected"
		host.LastHeartbeat = time.Now()
		if err := s.UpdateRuntimeHost(ctx, host); err != nil {
			return fmt.Errorf("failed to update runtime host: %w", err)
		}
	}

	// Add runtime host as contributor to global grove
	contrib := &store.GroveContributor{
		GroveID:  globalGrove.ID,
		HostID:   hostID,
		HostName: hostName,
		Mode:     store.HostModeConnected,
		Status:   store.HostStatusOnline,
		Profiles: []string{}, // All profiles
		LastSeen: time.Now(),
	}

	if err := s.AddGroveContributor(ctx, contrib); err != nil {
		// Ignore duplicate contributor errors
		if err != store.ErrAlreadyExists {
			return fmt.Errorf("failed to add grove contributor: %w", err)
		}
		// Update contributor status
		if err := s.UpdateContributorStatus(ctx, globalGrove.ID, hostID, store.HostStatusOnline); err != nil {
			log.Printf("Warning: failed to update contributor status: %v", err)
		}
	}

	return nil
}

// agentDispatcherAdapter adapts the agent.Manager to the hub.AgentDispatcher interface.
// This enables the Hub to dispatch agent creation to a co-located runtime host.
type agentDispatcherAdapter struct {
	manager agent.Manager
	store   store.Store
}

// newAgentDispatcherAdapter creates a new dispatcher adapter.
func newAgentDispatcherAdapter(mgr agent.Manager, s store.Store) *agentDispatcherAdapter {
	return &agentDispatcherAdapter{
		manager: mgr,
		store:   s,
	}
}

// DispatchAgentCreate implements hub.AgentDispatcher.
// It starts the agent on the runtime host and updates the hub store with runtime info.
func (d *agentDispatcherAdapter) DispatchAgentCreate(ctx context.Context, hubAgent *store.Agent) error {
	// Build StartOptions from the hub agent record
	env := make(map[string]string)
	if hubAgent.AppliedConfig != nil && hubAgent.AppliedConfig.Env != nil {
		env = hubAgent.AppliedConfig.Env
	}

	// Add grove ID label for tracking
	if hubAgent.Labels == nil {
		hubAgent.Labels = make(map[string]string)
	}
	hubAgent.Labels["scion.grove"] = hubAgent.GroveID

	opts := api.StartOptions{
		Name:     hubAgent.Name,
		Template: hubAgent.Template,
		Image:    hubAgent.Image,
		Env:      env,
		Detached: &hubAgent.Detached,
	}

	if hubAgent.AppliedConfig != nil {
		opts.Template = hubAgent.AppliedConfig.Harness
	}

	// Start the agent on the runtime host
	agentInfo, err := d.manager.Start(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}

	// Update the hub agent record with runtime information
	hubAgent.Status = store.AgentStatusRunning
	hubAgent.ContainerStatus = agentInfo.ContainerStatus
	if agentInfo.ID != "" {
		hubAgent.RuntimeState = "container:" + agentInfo.ID
	}
	hubAgent.LastSeen = time.Now()

	if err := d.store.UpdateAgent(ctx, hubAgent); err != nil {
		log.Printf("Warning: failed to update agent with runtime info: %v", err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.AddCommand(serverStartCmd)

	// Server start flags
	serverStartCmd.Flags().StringVarP(&serverConfigPath, "config", "c", "", "Path to server configuration file")

	// Hub API flags
	serverStartCmd.Flags().BoolVar(&enableHub, "enable-hub", false, "Enable the Hub API")
	serverStartCmd.Flags().IntVar(&hubPort, "port", 9810, "Hub API port")
	serverStartCmd.Flags().StringVar(&hubHost, "host", "0.0.0.0", "Hub API host to bind")
	serverStartCmd.Flags().StringVar(&dbURL, "db", "", "Database URL/path")

	// Runtime Host API flags
	serverStartCmd.Flags().BoolVar(&enableRuntimeHost, "enable-runtime-host", false, "Enable the Runtime Host API")
	serverStartCmd.Flags().IntVar(&runtimeHostPort, "runtime-host-port", 9800, "Runtime Host API port")
	serverStartCmd.Flags().StringVar(&runtimeHostMode, "runtime-host-mode", "connected", "Runtime Host mode (connected, read-only)")
}
