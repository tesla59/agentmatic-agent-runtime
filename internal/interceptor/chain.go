package interceptor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tesla59/agentmatic-agent-runtime/internal/tools"
)

// Options carries flags governing output transformation.
type Options struct {
	FullOutput bool // If true, bypasses metadata pruning
	MaxBytes   int  // Maximum bytes allowed before truncation
}

// Interceptor processes and sanitizes tool outputs before returning them to the agent.
type Interceptor interface {
	Name() string
	Process(ctx context.Context, toolName, output string, opts Options) (string, error)
}

// Chain executes a sequential chain of interceptors.
type Chain struct {
	interceptors []Interceptor
}

// NewChain creates a new Chain with the provided interceptors in order.
func NewChain(interceptors ...Interceptor) *Chain {
	return &Chain{interceptors: interceptors}
}

// NewDefaultChain sets up the standard 3-stage chain: Masking -> Pruning -> Truncating.
func NewDefaultChain(maxBytes int) *Chain {
	if maxBytes <= 0 {
		maxBytes = 8192 // 8KB default
	}
	return NewChain(
		NewMaskingInterceptor(),
		NewPruningInterceptor(),
		NewTruncatingInterceptor(maxBytes),
	)
}

// Execute passes raw tool output through every interceptor in the chain.
func (c *Chain) Execute(ctx context.Context, toolName, rawOutput string, opts Options) (string, error) {
	out := rawOutput
	var err error
	for _, i := range c.interceptors {
		out, err = i.Process(ctx, toolName, out, opts)
		if err != nil {
			return "", fmt.Errorf("interceptor %q failed: %w", i.Name(), err)
		}
	}
	return out, nil
}

// Wrap decorates any tools.Tool so its execution automatically runs through this chain.
func (c *Chain) Wrap(t tools.Tool) tools.Tool {
	return &tool{
		Tool:  t,
		chain: c,
	}
}

// tool wraps a tools.Tool and pipes its execution through an interceptor chain.
type tool struct {
	tools.Tool
	chain *Chain
}

func (t *tool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	output, err := t.Tool.Execute(ctx, rawArgs)
	if err != nil {
		sanitized, _ := t.chain.Execute(ctx, t.Tool.Name(), output, Options{})
		return sanitized, err
	}

	var opts Options
	var argsMap map[string]any
	if len(rawArgs) > 0 && json.Unmarshal(rawArgs, &argsMap) == nil {
		if full, ok := argsMap["full_output"].(bool); ok {
			opts.FullOutput = full
		}
	}

	return t.chain.Execute(ctx, t.Tool.Name(), output, opts)
}
