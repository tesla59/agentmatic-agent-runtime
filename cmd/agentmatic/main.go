package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/tesla59/agentmatic-agent-runtime/internal/logger"
)

func main() {
	logger.Setup(logger.ConfigFromEnv())
	ctx := context.Background()

	// One-shot mode: task arguments passed on the command line
	if len(os.Args) > 1 {
		task := strings.Join(os.Args[1:], " ")
		if err := runOneShotCLI(ctx, task); err != nil {
			slog.Error("execution failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Interactive REPL mode: no command line arguments
	if err := runREPL(ctx); err != nil {
		slog.Error("repl exited with error", "error", err)
		os.Exit(1)
	}
}
