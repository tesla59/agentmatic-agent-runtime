package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// runOneShot executes a single task prompt to completion via AgentClient and prints the final answer.
func runOneShot(ctx context.Context, client AgentClient, sessionRef, task string) error {
	var sessionID string
	if trimmed := strings.TrimSpace(sessionRef); trimmed != "" {
		sess, err := client.GetSession(ctx, trimmed)
		if err != nil {
			return fmt.Errorf("resolve session %q: %w", trimmed, err)
		}
		sessionID = sess.ID
	}

	slog.Info("starting agent task", "task", task, "session_id", sessionID)

	outcome, err := client.Execute(ctx, sessionID, task, terminalStreamCallbacks())
	if err != nil {
		return fmt.Errorf("agent execution failed: %w", err)
	}

	slog.Info("agent task finished",
		"session_id", outcome.SessionID,
		"steps", outcome.Steps,
		"total_tokens", outcome.Usage.TotalTokens,
		"prompt_tokens", outcome.Usage.PromptTokens,
		"completion_tokens", outcome.Usage.CompletionTokens,
	)

	fmt.Println("\n=== Final Answer ===")
	fmt.Println(outcome.Answer)
	return nil
}
