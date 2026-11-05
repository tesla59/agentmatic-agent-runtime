package interceptor

import (
	"context"
	"encoding/json"
	"strings"
)

type PruningInterceptor struct{}

func NewPruningInterceptor() *PruningInterceptor {
	return &PruningInterceptor{}
}

func (p *PruningInterceptor) Name() string {
	return "PruningInterceptor"
}

func (p *PruningInterceptor) Process(_ context.Context, _, output string, opts InterceptOptions) (string, error) {
	if opts.FullOutput {
		return output, nil
	}

	trimmed := strings.TrimSpace(output)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return output, nil
	}

	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return output, nil
	}

	pruneObject(parsed)

	prunedJSON, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return output, nil
	}
	return string(prunedJSON), nil
}

func pruneObject(obj any) {
	switch v := obj.(type) {
	case map[string]any:
		if metadata, ok := v["metadata"].(map[string]any); ok {
			delete(metadata, "managedFields")
			delete(metadata, "resourceVersion")
			delete(metadata, "generation")
			delete(metadata, "uid")
		}
		for _, child := range v {
			pruneObject(child)
		}
	case []any:
		for _, item := range v {
			pruneObject(item)
		}
	}
}
