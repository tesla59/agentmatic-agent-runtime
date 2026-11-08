package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

// runREPL starts an interactive terminal session loop.
func runREPL(ctx context.Context) error {
	rt, err := runtime.New(ctx, runtime.Config{
		StepListener: replStepListener,
	})
	if err != nil {
		return fmt.Errorf("initialize runtime: %w", err)
	}

	store, err := session.NewFileStore("")
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	sm := session.NewSessionManager(store)
	if err := sm.LoadAll(rt.Engine); err != nil {
		slog.Warn("failed to load some sessions from disk", "error", err)
	}

	// Initialize active session
	activeSess, err := session.New(session.Config{Engine: rt.Engine})
	if err != nil {
		return fmt.Errorf("create initial session: %w", err)
	}
	if err := sm.Register(activeSess); err != nil {
		return fmt.Errorf("register initial session: %w", err)
	}

	printBanner(activeSess)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("agentmatic> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Handle slash commands
		if strings.HasPrefix(line, "/") {
			done, err := handleCommand(line, &activeSess, sm, rt.Engine)
			if err != nil {
				fmt.Printf("Command error: %v\n\n", err)
			}
			if done {
				fmt.Println("Goodbye!")
				return nil
			}
			continue
		}

		// Execute user prompt with turn cancellation support (Ctrl+C cancels turn, not REPL)
		turnCtx, cancel := context.WithCancel(ctx)
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)

		go func() {
			select {
			case <-sigChan:
				fmt.Println("\n[Execution cancelled by user]")
				cancel()
			case <-turnCtx.Done():
			}
		}()

		res, err := activeSess.Execute(turnCtx, line)
		signal.Stop(sigChan)
		cancel()

		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println()
				continue
			}
			fmt.Printf("\nExecution error: %v\n\n", err)
			continue
		}

		// Persist turn to disk
		_ = sm.Save(activeSess)

		// Auto-title fresh sessions on first prompt
		if activeSess.Title() == "New Session" {
			autoTitle := line
			if len(autoTitle) > 35 {
				autoTitle = autoTitle[:35] + "..."
			}
			_ = activeSess.Rename(autoTitle)
			_ = sm.Save(activeSess)
		}

		fmt.Printf("\n%s\n\n", res.Answer)
	}

	return scanner.Err()
}

func handleCommand(cmdLine string, activeSess **session.Session, sm *session.SessionManager, eng *engine.Engine) (bool, error) {
	parts := strings.Fields(cmdLine)
	cmd := strings.ToLower(parts[0])
	args := strings.TrimSpace(strings.TrimPrefix(cmdLine, parts[0]))

	switch cmd {
	case "/exit", "/quit":
		return true, nil

	case "/help":
		printHelp()
		return false, nil

	case "/new":
		newSess, err := session.New(session.Config{Engine: eng})
		if err != nil {
			return false, fmt.Errorf("create new session: %w", err)
		}
		if err := sm.Register(newSess); err != nil {
			return false, fmt.Errorf("register session: %w", err)
		}
		*activeSess = newSess
		fmt.Printf("Created and switched to session: %s\n\n", newSess.ID())
		return false, nil

	case "/sessions", "/list":
		listSessions(sm, (*activeSess).ID())
		return false, nil

	case "/resume":
		if args == "" {
			return false, errors.New("usage: /resume <session-id>")
		}
		target, ok := findSession(sm, args)
		if !ok {
			return false, fmt.Errorf("no session found matching %q", args)
		}
		*activeSess = target
		fmt.Printf("Resumed session %s (%s)\n\n", target.ID(), target.Title())
		return false, nil

	case "/rename":
		if args == "" {
			return false, errors.New("usage: /rename <new-title>")
		}
		if err := (*activeSess).Rename(args); err != nil {
			return false, err
		}
		_ = sm.Save(*activeSess)
		fmt.Printf("Session renamed to: %s\n\n", args)
		return false, nil

	case "/restart":
		if err := (*activeSess).Restart(); err != nil {
			return false, err
		}
		_ = sm.Save(*activeSess)
		fmt.Println("Session restarted. Conversation history cleared.")
		return false, nil

	case "/info":
		s := *activeSess
		usage := s.TotalUsage()
		fmt.Printf("\n--- Active Session ---\n")
		fmt.Printf("ID:         %s\n", s.ID())
		fmt.Printf("Title:      %s\n", s.Title())
		fmt.Printf("Status:     %s\n", s.Status())
		fmt.Printf("Messages:   %d\n", len(s.History()))
		fmt.Printf("Tokens:     %d (prompt: %d, completion: %d)\n", usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
		fmt.Printf("Updated:    %s\n\n", s.UpdatedAt().Format("2006-01-02 15:04:05"))
		return false, nil

	default:
		return false, fmt.Errorf("unknown command %q. Type /help for available commands", cmd)
	}
}

func listSessions(sm *session.SessionManager, activeID string) {
	sessions := sm.List()
	if len(sessions) == 0 {
		fmt.Println("No stored sessions found.")
		return
	}

	fmt.Println("\nAvailable Sessions:")
	for _, s := range sessions {
		prefix := "  "
		if s.ID() == activeID {
			prefix = "* "
		}
		fmt.Printf("%s%-36s  %-25s  %s  (%d tokens)\n",
			prefix,
			s.ID(),
			truncateString(s.Title(), 25),
			s.UpdatedAt().Format("Jan 02 15:04"),
			s.TotalUsage().TotalTokens,
		)
	}
	fmt.Println("\nUse /resume <id> to switch sessions (* marks active).")
}

func findSession(sm *session.SessionManager, target string) (*session.Session, bool) {
	// Exact ID match
	if s, ok := sm.Get(target); ok {
		return s, true
	}

	// Prefix match on UUID (convenient for typing first few chars)
	for _, s := range sm.List() {
		if strings.HasPrefix(s.ID(), target) {
			return s, true
		}
	}
	return nil, false
}

func printBanner(active *session.Session) {
	fmt.Println("==================================================")
	fmt.Println("        AgentMatic Interactive DevOps Shell       ")
	fmt.Println("==================================================")
	fmt.Printf("Active Session: %s\n", active.ID())
	fmt.Println("Type /help for commands, or ask an infrastructure question.")
	fmt.Println("==================================================")
}

func printHelp() {
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  /new             Start a new conversation session")
	fmt.Println("  /sessions        List all saved sessions from disk")
	fmt.Println("  /resume <id>     Resume/switch to a session by ID or prefix")
	fmt.Println("  /rename <title>  Rename the current session")
	fmt.Println("  /restart         Clear history and restart the current session")
	fmt.Println("  /info            Display current session metrics and status")
	fmt.Println("  /help            Show this help reference")
	fmt.Println("  /exit, /quit     Exit the interactive shell")
}

func replStepListener(step engine.Step) {
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
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
