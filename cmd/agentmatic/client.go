package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
)

// SessionInfo represents a client-facing summary/detail snapshot of a session.
type SessionInfo struct {
	ID          string
	Name        string
	Title       string
	Status      string
	OwnerLabel  string
	TurnCount   int
	TotalTokens int
	UpdatedAt   time.Time
}

// ExecuteOutcome captures the final completion result of an Execute turn.
type ExecuteOutcome struct {
	SessionID string
	Answer    string
	Steps     int
	Usage     llm.Usage
}

// StreamCallbacks receives real-time reasoning, tool dispatch, and observation updates.
type StreamCallbacks struct {
	OnThinking   func(step int32, thought string)
	OnToolCall   func(step int32, toolName, argsJSON string)
	OnToolResult func(step int32, toolName, result string, isError bool)
}

// AgentClient abstracts local embedded execution vs remote ConnectRPC execution for CLI and REPL.
type AgentClient interface {
	CreateSession(ctx context.Context, name, title string) (SessionInfo, error)
	GetSession(ctx context.Context, identifier string) (SessionInfo, error)
	ListSessions(ctx context.Context) ([]SessionInfo, error)
	RenameSession(ctx context.Context, identifier, newTitle, newName string) (SessionInfo, error)
	RestartSession(ctx context.Context, identifier string) (SessionInfo, error)
	Execute(ctx context.Context, sessionID, prompt string, cb StreamCallbacks) (ExecuteOutcome, error)
	CancelExecution(ctx context.Context, sessionID string) error
}

// terminalStreamCallbacks returns shared stdout callbacks for both one-shot and REPL modes.
func terminalStreamCallbacks() StreamCallbacks {
	return StreamCallbacks{
		OnThinking: func(step int32, thought string) {
			fmt.Printf("\n[Step %d]\nThinking:\n%s\n", step, strings.TrimSpace(thought))
			slog.Debug("agent thinking", "step", step, "thought", thought)
		},
		OnToolCall: func(step int32, toolName, argsJSON string) {
			fmt.Printf("Action: %s(%s)\n", toolName, argsJSON)
			slog.Debug("agent action", "step", step, "tool", toolName, "args", argsJSON)
		},
		OnToolResult: func(step int32, toolName, result string, isError bool) {
			obs := strings.TrimSpace(result)
			if len(obs) > 500 {
				obs = obs[:500] + "... [truncated]"
			}
			label := "Observation"
			if isError {
				label = "Observation (error)"
			}
			fmt.Printf("%s:\n%s\n", label, obs)
			slog.Debug("tool observation", "step", step, "tool", toolName, "is_error", isError, "output", result)
		},
	}
}
