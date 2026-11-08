package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
)

// oneShotStepListener prints intermediate agent reasoning, tool selection, and observations to the terminal.
func oneShotStepListener(step engine.Step) {
	fmt.Printf("\n[Step %d]\n", step.Number)
	if step.Thinking != "" {
		fmt.Printf("Thinking:\n%s\n", strings.TrimSpace(step.Thinking))
	}
	if step.Message != "" {
		fmt.Printf("Model:\n%s\n", strings.TrimSpace(step.Message))
	}
	if step.Action != nil {
		fmt.Printf("Action: %s(%s)\n", step.Action.Name, step.Action.Arguments)
	}
	if step.Observation != "" {
		obs := strings.TrimSpace(step.Observation)
		if len(obs) > 500 {
			obs = obs[:500] + "... [truncated]"
		}
		fmt.Printf("Observation:\n%s\n", obs)
	}

	// Also record to debug logs if enabled
	if step.Action != nil {
		slog.Debug("agent action", "step", step.Number, "tool", step.Action.Name, "args", step.Action.Arguments)
	}
	if step.Thinking != "" {
		slog.Debug("agent thinking", "step", step.Number, "thought", step.Thinking)
	}
	slog.Debug("tool observation", "step", step.Number, "output", step.Observation)
}

// runOneShot executes a single task prompt to completion and prints the result to stdout.
func runOneShot(ctx context.Context, eng *engine.Engine, task string) error {
	slog.Info("starting agent task", "task", task)

	result, err := eng.Run(ctx, task)
	if err != nil {
		return fmt.Errorf("agent execution failed: %w", err)
	}

	slog.Info("agent task finished",
		"steps", len(result.Steps),
		"total_tokens", result.Usage.TotalTokens,
		"prompt_tokens", result.Usage.PromptTokens,
		"completion_tokens", result.Usage.CompletionTokens,
	)

	fmt.Println("\n=== Final Answer ===")
	fmt.Println(result.Answer)
	return nil
}
