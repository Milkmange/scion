package cmd

import (
	"context"
	"fmt"

	"github.com/ptone/scion-agent/pkg/agent"
	"github.com/ptone/scion-agent/pkg/runtime"
	"github.com/spf13/cobra"
)

// deleteCmd represents the delete command
var deleteCmd = &cobra.Command{
	Use:     "delete <agent>",
	Aliases: []string{"rm"},
	Short:   "Delete an agent",
	Long:    `Stop and remove an agent container and its associated files and worktree.`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentName := args[0]

		effectiveRuntime := agentRuntime
		if effectiveRuntime == "" {
			effectiveRuntime = agent.GetSavedRuntime(agentName, grovePath)
		}

		rt := runtime.GetRuntime(grovePath, effectiveRuntime)
		mgr := agent.NewManager(rt)


		fmt.Printf("Deleting agent '%s'...\n", agentName)
		
		// Try to stop first, ignore error if already stopped
		_ = mgr.Stop(context.Background(), agentName)

		if err := mgr.Delete(context.Background(), agentName, true, grovePath); err != nil {
			return err
		}

		fmt.Printf("Agent '%s' deleted.\n", agentName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().StringVarP(&agentRuntime, "runtime", "r", "", "Runtime to use (local, remote, docker, kubernetes)")
}

