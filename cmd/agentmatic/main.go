package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tesla59/agentmatic-agent-runtime/internal/logger"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
)

func main() {
	logger.Setup(logger.ConfigFromEnv())

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: agentmatic <task...>\nExample: agentmatic why is nginx crashing in default?\n")
		os.Exit(1)
	}
	task := strings.Join(os.Args[1:], " ")

	ctx := context.Background()
	rt, err := runtime.New(ctx, runtime.Config{
		StepListener: oneShotStepListener,
	})
	if err != nil {
		slog.Error("failed to initialize runtime", "error", err)
		os.Exit(1)
	}

	if err := runOneShot(ctx, rt.Engine, task); err != nil {
		slog.Error("execution failed", "error", err)
		os.Exit(1)
	}
}
