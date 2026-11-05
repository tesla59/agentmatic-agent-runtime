package interceptor

import (
	"context"
	"fmt"
)

// InterceptOptions carries flags governing output transformation.
type InterceptOptions struct {
	FullOutput bool // If true, bypasses metadata pruning
	MaxBytes   int  // Maximum bytes allowed before truncation
}

// Interceptor processes and sanitizes tool outputs before returning them to the agent.
type Interceptor interface {
	Name() string
	Process(ctx context.Context, toolName, output string, opts InterceptOptions) (string, error)
}

// Pipeline executes a sequential chain of interceptors.
type Pipeline struct {
	interceptors []Interceptor
}

func NewPipeline(interceptors ...Interceptor) *Pipeline {
	return &Pipeline{interceptors: interceptors}
}

// NewDefaultPipeline sets up the standard 3-stage chain: Masking -> Pruning -> Truncating.
func NewDefaultPipeline(maxBytes int) *Pipeline {
	if maxBytes <= 0 {
		maxBytes = 8192 // 8KB default
	}
	return NewPipeline(
		NewMaskingInterceptor(),
		NewPruningInterceptor(),
		NewTruncatingInterceptor(maxBytes),
	)
}

func (p *Pipeline) Execute(ctx context.Context, toolName, rawOutput string, opts InterceptOptions) (string, error) {
	out := rawOutput
	var err error
	for _, i := range p.interceptors {
		out, err = i.Process(ctx, toolName, out, opts)
		if err != nil {
			return "", fmt.Errorf("interceptor %q failed: %w", i.Name(), err)
		}
	}
	return out, nil
}
