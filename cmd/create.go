package cmd

import (
	"context"
	"fmt"

	"github.com/ptone/scion-agent/pkg/agent"
	"github.com/ptone/scion-agent/pkg/api"
	"github.com/ptone/scion-agent/pkg/runtime"
	"github.com/spf13/cobra"
)

// createCmd represents the create command
var createCmd = &cobra.Command{
	Use:   "create <agent-name>",
	Short: "Provision a new scion agent without starting it",
	Long: `Provision a new isolated LLM agent directory to perform a specific task.
The agent will be created from a template.`, 
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentName := args[0]

		effectiveRuntime := agentRuntime
		if effectiveRuntime == "" {
			effectiveRuntime = agent.GetSavedRuntime(agentName, grovePath)
		}

		rt := runtime.GetRuntime(grovePath, effectiveRuntime)
		mgr := agent.NewManager(rt)

		opts := api.StartOptions{
			Name:      agentName,
			Template:  templateName,
			Image:     agentImage,
			GrovePath: grovePath,
		}

		// Check if container already exists

		agents, err := rt.List(context.Background(), nil)
		if err == nil {
			for _, a := range agents {
				if a.ID == agentName || a.Name == agentName {
					fmt.Printf("Agent container '%s' already exists (Status: %s).\n", agentName, a.Status)
					// We continue to check directory
				}
			}
		}

		_, err = mgr.Provision(context.Background(), opts)
		if err != nil {
			return err
		}

		fmt.Printf("Agent '%s' created successfully.\n", agentName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().StringVarP(&templateName, "type", "t", "", "Template to use")
	createCmd.Flags().StringVarP(&agentImage, "image", "i", "", "Container image to use (overrides template)")
	createCmd.Flags().StringVarP(&agentRuntime, "runtime", "r", "", "Runtime to use (local, remote, docker, kubernetes)")
}

