package cmd

import (
	"context"
	"fmt"

	"github.com/ptone/scion-agent/pkg/agent"
	"github.com/ptone/scion-agent/pkg/config"
	"github.com/ptone/scion-agent/pkg/runtime"
	"github.com/spf13/cobra"
)

// attachCmd represents the attach command
var attachCmd = &cobra.Command{
	Use:   "attach <agent>",
	Short: "Attach to an agent's interactive session",
	Long: `Attach to the interactive session of a running agent.
If the agent was started with tmux support, this will attach to the tmux session.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentName := args[0]

		// Try to resolve grove info for better error messages
		projectDir, _ := config.GetResolvedProjectDir(grovePath)
		groveName := config.GetGroveName(projectDir)

		// Load agent config to get the runtime
		effectiveRuntime := agent.GetSavedRuntime(agentName, grovePath)

		rt := runtime.GetRuntime(grovePath, effectiveRuntime)

		fmt.Printf("Attaching to agent '%s' (grove: %s)...\n", agentName, groveName)
		err := rt.Attach(context.Background(), agentName)
		if err != nil {
			// If the error is "not found", we can augment it with grove info
			if err.Error() == fmt.Sprintf("agent '%s' not found", agentName) {
				return fmt.Errorf("agent '%s' not found in grove '%s'", agentName, groveName)
			}
			return err
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(attachCmd)
}

