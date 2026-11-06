package interceptor

import (
	"context"
	"fmt"
)

type TruncatingInterceptor struct {
	defaultMaxBytes int
}

func NewTruncatingInterceptor(defaultMaxBytes int) *TruncatingInterceptor {
	return &TruncatingInterceptor{defaultMaxBytes: defaultMaxBytes}
}

func (t *TruncatingInterceptor) Name() string {
	return "TruncatingInterceptor"
}

func (t *TruncatingInterceptor) Process(_ context.Context, _, output string, opts Options) (string, error) {
	limit := t.defaultMaxBytes
	if opts.MaxBytes > 0 {
		limit = opts.MaxBytes
	}

	if len(output) > limit {
		return output[:limit] + fmt.Sprintf("\n... [output truncated: %d bytes exceeded]", limit), nil
	}
	return output, nil
}
