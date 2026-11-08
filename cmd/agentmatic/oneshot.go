package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
)

// oneShotStepListener logs agent progress steps during execution.
func oneShotStepListener(step engine.Step) {
	if step.Action != nil {
		slog.Info("agent action", "step", step.Number, "tool", step.Action.Name, "args", step.Action.Arguments)
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

	fmt.Println("\n" + result.Answer)
	return nil
}
