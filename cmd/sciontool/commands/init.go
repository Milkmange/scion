/*
Copyright 2025 The Scion Authors.
*/

package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ptone/scion-agent/pkg/sciontool/hooks"
	"github.com/ptone/scion-agent/pkg/sciontool/hooks/handlers"
	"github.com/ptone/scion-agent/pkg/sciontool/hub"
	"github.com/ptone/scion-agent/pkg/sciontool/log"
	"github.com/ptone/scion-agent/pkg/sciontool/supervisor"
	"github.com/ptone/scion-agent/pkg/sciontool/telemetry"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	gracePeriod time.Duration
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init [--] <command> [args...]",
	Short: "Run as container init (PID 1) and supervise child processes",
	Long: `The init command runs sciontool as the container's init process (PID 1).

It provides:
  - Zombie process reaping (critical for PID 1)
  - Signal forwarding to child processes
  - Graceful shutdown with configurable grace period
  - Child process exit code propagation

The command after -- is executed as the child process. If no command is
specified, sciontool will exit with an error.

Examples:
  sciontool init -- gemini
  sciontool init -- tmux new-session -A -s main
  sciontool init --grace-period=30s -- claude`,
	DisableFlagParsing: false,
	Run: func(cmd *cobra.Command, args []string) {
		exitCode := runInit(args)
		os.Exit(exitCode)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().DurationVar(&gracePeriod, "grace-period", 10*time.Second,
		"Time to wait after SIGTERM before sending SIGKILL")

	// Override the default SCION_GRACE_PERIOD env var if set
	if envGrace := os.Getenv("SCION_GRACE_PERIOD"); envGrace != "" {
		if d, err := time.ParseDuration(envGrace); err == nil {
			gracePeriod = d
		}
	}
}

func runInit(args []string) int {
	// Start the reaper goroutine for zombie process cleanup.
	// This is critical when running as PID 1 in a container.
	supervisor.StartReaper()

	// Extract the child command (everything after --)
	childArgs := extractChildCommand(args)
	if len(childArgs) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no command specified after --")
		fmt.Fprintln(os.Stderr, "Usage: sciontool init [--] <command> [args...]")
		return 1
	}

	// Log startup
	log.Info("sciontool init starting as PID %d", os.Getpid())
	log.Info("Child command: %v", childArgs)
	log.Info("Grace period: %s", gracePeriod)

	// Set up scion user UID/GID to match host user
	targetUID, targetGID := setupHostUser()

	// Chown the log file so the scion user can write to it even if it was created by root
	if targetUID != 0 {
		if err := log.Chown(targetUID, targetGID); err != nil {
			log.Error("Failed to chown log file: %v", err)
		}
	}

	// Start telemetry pipeline if configured
	var telemetryPipeline *telemetry.Pipeline
	if pipeline := telemetry.New(); pipeline != nil {
		telemetryCtx, telemetryCancel := context.WithCancel(context.Background())
		if err := pipeline.Start(telemetryCtx); err != nil {
			log.Error("Failed to start telemetry: %v", err)
			telemetryCancel()
			// Continue anyway - telemetry failure shouldn't block agent
		} else {
			telemetryPipeline = pipeline
			log.Info("Telemetry pipeline started")
		}
		defer func() {
			if telemetryPipeline != nil {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := telemetryPipeline.Stop(shutdownCtx); err != nil {
					log.Error("Failed to stop telemetry: %v", err)
				}
				shutdownCancel()
			}
			telemetryCancel()
		}()
	}

	// Initialize lifecycle hooks manager
	lifecycleManager := hooks.NewLifecycleManager()

	// Register status and logging handlers for lifecycle events
	// These handlers update agent-info.json and agent.log on container lifecycle events
	statusHandler := handlers.NewStatusHandler()
	loggingHandler := handlers.NewLoggingHandler()

	for _, eventName := range []string{hooks.EventPreStart, hooks.EventPostStart, hooks.EventPreStop, hooks.EventSessionEnd} {
		lifecycleManager.RegisterHandler(eventName, statusHandler.Handle)
		lifecycleManager.RegisterHandler(eventName, loggingHandler.Handle)
	}

	// Create telemetry handler for hook-to-span conversion
	// Note: The hook command is invoked separately by harnesses, so telemetry
	// handler registration happens in hook.go. This handler is for lifecycle events.
	var telemetryHandler *handlers.TelemetryHandler
	var lifecycleProviders *telemetry.Providers
	if telemetryPipeline != nil && telemetryPipeline.Config() != nil {
		redactor := telemetry.NewRedactor(telemetryPipeline.Config().Redaction)

		// Create real providers for span + log export (batch mode for long-lived init)
		provCtx := context.Background()
		var provErr error
		lifecycleProviders, provErr = telemetry.NewProviders(provCtx, telemetryPipeline.Config(), true)
		if provErr != nil {
			log.Error("Failed to create lifecycle telemetry providers: %v", provErr)
		}

		var tp trace.TracerProvider
		var lp otellog.LoggerProvider
		var mp metric.MeterProvider
		if lifecycleProviders != nil {
			tp = lifecycleProviders.TracerProvider
			lp = lifecycleProviders.LoggerProvider
			if lifecycleProviders.MeterProvider != nil {
				mp = lifecycleProviders.MeterProvider
			}
		}
		telemetryHandler = handlers.NewTelemetryHandler(tp, lp, redactor, mp)
		log.Info("Telemetry handler initialized for hook-to-span conversion")

		// Register telemetry handler for lifecycle events
		for _, eventName := range []string{hooks.EventPreStart, hooks.EventPostStart, hooks.EventPreStop, hooks.EventSessionEnd} {
			lifecycleManager.RegisterHandler(eventName, telemetryHandler.Handle)
		}
	}
	if lifecycleProviders != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := lifecycleProviders.Shutdown(shutdownCtx); err != nil {
				log.Error("Failed to shutdown lifecycle telemetry providers: %v", err)
			}
		}()
	}

	// Run pre-start hooks (after setup, before child process)
	log.Info("Running pre-start hooks...")
	if err := lifecycleManager.RunPreStart(); err != nil {
		log.Error("Pre-start hooks failed: %v", err)
		// Continue anyway - hooks failing shouldn't prevent startup
	}

	// Create supervisor with configuration
	config := supervisor.Config{
		GracePeriod: gracePeriod,
		UID:         targetUID,
		GID:         targetGID,
		Username:    "scion",
	}
	sup := supervisor.New(config)

	// Create a cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling with pre-stop hook for graceful shutdown
	sigHandler := supervisor.NewSignalHandler(sup, cancel).
		WithPreStopHook(func() error {
			log.Info("Running pre-stop hooks...")
			return lifecycleManager.RunPreStop()
		})
	sigHandler.Start()
	defer sigHandler.Stop()

	// Run the child process under supervision
	// We use a goroutine to allow post-start hooks to run after process starts
	exitChan := make(chan struct {
		code int
		err  error
	}, 1)

	go func() {
		code, err := sup.Run(ctx, childArgs)
		exitChan <- struct {
			code int
			err  error
		}{code, err}
	}()

	// Heartbeat control variables - declared here so they're accessible during shutdown
	var heartbeatCancel context.CancelFunc
	var heartbeatDone <-chan struct{}

	// Wait a moment for process to start, then run post-start hooks
	// Use a short timeout to detect immediate startup failures
	select {
	case result := <-exitChan:
		// Child exited immediately - likely a startup error
		if result.err != nil {
			log.Error("Supervisor error: %v", result.err)
			return 1
		}
		log.Info("Child exited with code %d", result.code)
		return result.code
	case <-time.After(100 * time.Millisecond):
		// Process appears to be running, execute post-start hooks
		log.Info("Running post-start hooks...")
		if err := lifecycleManager.RunPostStart(); err != nil {
			log.Error("Post-start hooks failed: %v", err)
			// Continue anyway
		}

		// Report running status to Hub if in hosted mode
		hubClient := hub.NewClient()
		log.Debug("Hub client check: client=%v, configured=%v", hubClient != nil, hubClient != nil && hubClient.IsConfigured())
		log.Debug("Hub env: SCION_HUB_URL=%q, SCION_HUB_TOKEN=%v, SCION_AGENT_ID=%q",
			os.Getenv("SCION_HUB_URL"), os.Getenv("SCION_HUB_TOKEN") != "", os.Getenv("SCION_AGENT_ID"))
		if hubClient != nil && hubClient.IsConfigured() {
			hubCtx, hubCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := hubClient.ReportRunning(hubCtx, "Agent started"); err != nil {
				log.Error("Failed to report running status to Hub: %v", err)
			} else {
				log.Info("Reported running status to Hub")
			}
			hubCancel()

			// Start heartbeat loop in background
			var heartbeatCtx context.Context
			heartbeatCtx, heartbeatCancel = context.WithCancel(context.Background())
			heartbeatDone = hubClient.StartHeartbeat(heartbeatCtx, &hub.HeartbeatConfig{
				Interval: hub.DefaultHeartbeatInterval,
				Timeout:  hub.DefaultHeartbeatTimeout,
				OnError: func(err error) {
					log.Error("Heartbeat failed: %v", err)
				},
				OnSuccess: func() {
					log.Debug("Heartbeat sent successfully")
				},
			})
			log.Info("Started Hub heartbeat loop (interval: %s)", hub.DefaultHeartbeatInterval)
		} else {
			log.Debug("Hub client not configured - skipping status report")
		}
	}

	// Wait for child to exit
	result := <-exitChan

	// Stop heartbeat before reporting shutdown status to prevent races
	if heartbeatCancel != nil {
		heartbeatCancel()
		<-heartbeatDone
		log.Debug("Heartbeat loop stopped")
	}

	// Report shutting down to Hub if in hosted mode
	if hubClient := hub.NewClient(); hubClient != nil && hubClient.IsConfigured() {
		hubCtx, hubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hubClient.ReportShuttingDown(hubCtx, "Agent shutting down"); err != nil {
			log.Error("Failed to report shutdown status to Hub: %v", err)
		}
		hubCancel()
	}

	// Run session-end hooks (graceful shutdown)
	log.Info("Running session-end hooks...")
	if err := lifecycleManager.RunSessionEnd(); err != nil {
		log.Error("Session-end hooks failed: %v", err)
	}

	// Report final stopped status to Hub
	if hubClient := hub.NewClient(); hubClient != nil && hubClient.IsConfigured() {
		hubCtx, hubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hubClient.ReportStopped(hubCtx, "Agent stopped"); err != nil {
			log.Error("Failed to report stopped status to Hub: %v", err)
		} else {
			log.Info("Reported stopped status to Hub")
		}
		hubCancel()
	}

	if result.err != nil {
		log.Error("Supervisor error: %v", result.err)
		return 1
	}

	log.Info("Child exited with code %d", result.code)
	return result.code
}

// extractChildCommand extracts the command arguments.
// Cobra handles -- separator, so args contains everything after --.
func extractChildCommand(args []string) []string {
	return args
}

// setupHostUser modifies the scion user's UID/GID to match the host user.
// This is only done when running as root and SCION_HOST_UID/GID are set.
// Returns the target UID/GID for the child process (0 = no change).
func setupHostUser() (int, int) {
	// Only run if we're root and env vars are set
	if os.Getuid() != 0 {
		log.Debug("Not running as root, skipping user setup")
		return 0, 0
	}

	hostUID := os.Getenv("SCION_HOST_UID")
	hostGID := os.Getenv("SCION_HOST_GID")

	if hostUID == "" || hostGID == "" {
		log.Debug("SCION_HOST_UID/GID not set, skipping user setup")
		return 0, 0 // Continue as root
	}

	uid, err := strconv.Atoi(hostUID)
	if err != nil {
		log.Error("Invalid SCION_HOST_UID: %v", err)
		return 0, 0
	}
	gid, err := strconv.Atoi(hostGID)
	if err != nil {
		log.Error("Invalid SCION_HOST_GID: %v", err)
		return 0, 0
	}

	// Skip if UID/GID already match (1001 is the default)
	currentInfo, _ := user.Lookup("scion")
	if currentInfo != nil {
		currentUID, _ := strconv.Atoi(currentInfo.Uid)
		currentGID, _ := strconv.Atoi(currentInfo.Gid)
		log.Debug("Current scion user: UID=%d, GID=%d (Target: UID=%d, GID=%d)", currentUID, currentGID, uid, gid)
		if currentUID == uid && currentGID == gid {
			log.Debug("scion user already has correct UID/GID")
			return uid, gid
		}
	} else {
		log.Error("scion user not found in system")
	}

	log.Info("Adjusting scion user to UID=%d, GID=%d", uid, gid)

	// Modify group first (if different from current)
	if err := exec.Command("groupmod", "-o", "-g", hostGID, "scion").Run(); err != nil {
		log.Error("Failed to modify scion group to %s: %v", hostGID, err)
	}

	// Modify user UID and primary group
	if err := exec.Command("usermod", "-o", "-u", hostUID, "-g", hostGID, "scion").Run(); err != nil {
		log.Error("Failed to modify scion user to UID %s, GID %s: %v", hostUID, hostGID, err)
		return 0, 0
	}

	// Verify the change
	if updatedInfo, err := user.Lookup("scion"); err == nil {
		log.Info("Successfully adjusted scion user: UID=%s, GID=%s", updatedInfo.Uid, updatedInfo.Gid)
	} else {
		log.Error("Failed to verify scion user after adjustment: %v", err)
	}

	return uid, gid
}
