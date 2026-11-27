package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

// runREPL starts an interactive terminal session loop backed by AgentClient (remote or local).
func runREPL(ctx context.Context, client AgentClient, sessionRef string) error {
	var activeSess SessionInfo
	var err error
	if trimmed := strings.TrimSpace(sessionRef); trimmed != "" {
		activeSess, err = client.GetSession(ctx, trimmed)
		if err != nil {
			return fmt.Errorf("resume session %q: %w", trimmed, err)
		}
	} else {
		activeSess, err = client.CreateSession(ctx, "", "")
		if err != nil {
			return fmt.Errorf("create initial session: %w", err)
		}
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

		if strings.HasPrefix(line, "/") {
			done, err := handleCommand(ctx, client, line, &activeSess)
			if err != nil {
				fmt.Printf("Command error: %v\n\n", err)
			}
			if done {
				fmt.Println("Goodbye!")
				return nil
			}
			continue
		}

		turnCtx, cancel := context.WithCancel(ctx)
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)

		go func() {
			select {
			case <-sigChan:
				fmt.Println("\n[Cancelling execution...]")
				cancelCtx, cancelTimeout := context.WithTimeout(context.Background(), 3*time.Second)
				_ = client.CancelExecution(cancelCtx, activeSess.ID)
				cancelTimeout()
				cancel()
			case <-turnCtx.Done():
			}
		}()

		outcome, err := client.Execute(turnCtx, activeSess.ID, line, terminalStreamCallbacks())
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

		// Refresh session metadata (title/turns/updated_at) after completion.
		if refreshed, refreshErr := client.GetSession(ctx, outcome.SessionID); refreshErr == nil {
			activeSess = refreshed
		}

		fmt.Printf("\n%s\n\n", outcome.Answer)
	}

	return scanner.Err()
}

func handleCommand(ctx context.Context, client AgentClient, cmdLine string, activeSess *SessionInfo) (bool, error) {
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
		created, err := client.CreateSession(ctx, "", "")
		if err != nil {
			return false, fmt.Errorf("create new session: %w", err)
		}
		*activeSess = created
		fmt.Printf("Created and switched to session: %s\n\n", created.ID)
		return false, nil

	case "/sessions", "/list":
		return false, listSessions(ctx, client, activeSess.ID)

	case "/resume":
		if args == "" {
			return false, errors.New("usage: /resume <session-id-or-name>")
		}
		target, ok, err := findSession(ctx, client, args)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("no session found matching %q", args)
		}
		*activeSess = target
		fmt.Printf("Resumed session %s (%s)\n\n", target.ID, target.Title)
		return false, nil

	case "/rename":
		if args == "" {
			return false, errors.New("usage: /rename <new-title>")
		}
		updated, err := client.RenameSession(ctx, activeSess.ID, args, "")
		if err != nil {
			return false, err
		}
		*activeSess = updated
		fmt.Printf("Session renamed to: %s\n\n", updated.Title)
		return false, nil

	case "/restart":
		restarted, err := client.RestartSession(ctx, activeSess.ID)
		if err != nil {
			return false, err
		}
		*activeSess = restarted
		fmt.Println("Session restarted. Conversation history cleared.")
		return false, nil

	case "/info":
		if refreshed, err := client.GetSession(ctx, activeSess.ID); err == nil {
			*activeSess = refreshed
		}
		fmt.Printf("\n--- Active Session ---\n")
		fmt.Printf("ID:         %s\n", activeSess.ID)
		if activeSess.Name != "" {
			fmt.Printf("Name:       %s\n", activeSess.Name)
		}
		fmt.Printf("Title:      %s\n", activeSess.Title)
		if activeSess.OwnerLabel != "" {
			fmt.Printf("Owner:      %s\n", activeSess.OwnerLabel)
		}
		fmt.Printf("Status:     %s\n", activeSess.Status)
		fmt.Printf("Turns:      %d\n", activeSess.TurnCount)
		if activeSess.TotalTokens > 0 {
			fmt.Printf("Tokens:     %d\n", activeSess.TotalTokens)
		}
		fmt.Printf("Updated:    %s\n\n", activeSess.UpdatedAt.Format("2006-01-02 15:04:05"))
		return false, nil

	default:
		return false, fmt.Errorf("unknown command %q. Type /help for available commands", cmd)
	}
}

func listSessions(ctx context.Context, client AgentClient, activeID string) error {
	sessions, err := client.ListSessions(ctx)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("No stored sessions found.")
		return nil
	}

	fmt.Println("\nAvailable Sessions:")
	for _, s := range sessions {
		prefix := "  "
		if s.ID == activeID {
			prefix = "* "
		}
		fmt.Printf("%s%-36s  %-25s  %s  (%d turns)\n",
			prefix,
			s.ID,
			truncateString(s.Title, 25),
			s.UpdatedAt.Format("Jan 02 15:04"),
			s.TurnCount,
		)
	}
	fmt.Println("\nUse /resume <id-or-name> to switch sessions (* marks active).")
	return nil
}

func findSession(ctx context.Context, client AgentClient, target string) (SessionInfo, bool, error) {
	if s, err := client.GetSession(ctx, target); err == nil {
		return s, true, nil
	}

	sessions, err := client.ListSessions(ctx)
	if err != nil {
		return SessionInfo{}, false, err
	}
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, target) || (s.Name != "" && s.Name == target) {
			return s, true, nil
		}
	}
	return SessionInfo{}, false, nil
}

func printBanner(active SessionInfo) {
	fmt.Println("==================================================")
	fmt.Println("        AgentMatic Interactive DevOps Shell       ")
	fmt.Println("==================================================")
	fmt.Printf("Active Session: %s\n", active.ID)
	fmt.Println("Type /help for commands, or ask an infrastructure question.")
	fmt.Println("==================================================")
}

func printHelp() {
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  /new             Start a new conversation session")
	fmt.Println("  /sessions        List all saved sessions")
	fmt.Println("  /resume <id>     Resume/switch to a session by ID, prefix, or name")
	fmt.Println("  /rename <title>  Rename the current session")
	fmt.Println("  /restart         Clear history and restart the current session")
	fmt.Println("  /info            Display current session metadata and status")
	fmt.Println("  /help            Show this help reference")
	fmt.Println("  /exit, /quit     Exit the interactive shell")
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
