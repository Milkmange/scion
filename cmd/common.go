package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/ptone/scion-agent/pkg/agent"
	"github.com/ptone/scion-agent/pkg/api"
	"github.com/ptone/scion-agent/pkg/runtime"
)

var (
	templateName string
	agentImage   string
	noAuth       bool
	attach       bool
	model        string
	agentRuntime string
)

func RunAgent(cmd *cobra.Command, args []string, resume bool) error {
	agentName := args[0]
	task := strings.Join(args[1:], " ")

	effectiveRuntime := agentRuntime
	if effectiveRuntime == "" {
		effectiveRuntime = agent.GetSavedRuntime(agentName, grovePath)
	}

	rt := runtime.GetRuntime(grovePath, effectiveRuntime)
	mgr := agent.NewManager(rt)

	// Flag takes ultimate precedence
	resolvedImage := agentImage

	var detached *bool
	if cmd.Flags().Changed("attach") {
		val := !attach
		detached = &val
	}

	opts := api.StartOptions{
		Name:      agentName,
		Task:      task,
		Template:  templateName,
		Image:     resolvedImage,
		GrovePath: grovePath,
		Resume:    resume,
		Model:     model,
		Detached:  detached,
		NoAuth:    noAuth,
	}

	// We still might want to show some progress in the CLI
	if resume {
		fmt.Printf("Resuming agent '%s'...\n", agentName)
	} else {
		fmt.Printf("Starting agent '%s'...\n", agentName)
	}

	info, err := mgr.Start(context.Background(), opts)
	if err != nil {
		return err
	}

	if !info.Detached {
		fmt.Printf("Attaching to agent '%s'...\n", agentName)
		return rt.Attach(context.Background(), info.ID)
	}

	displayStatus := "launched"
	if resume {
		displayStatus = "resumed"
	}
	fmt.Printf("Agent '%s' %s successfully (ID: %s)\n", agentName, displayStatus, info.ID)

	return nil
}

