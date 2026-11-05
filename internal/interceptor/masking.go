package interceptor

import (
	"context"
	"regexp"
)

type MaskingInterceptor struct {
	patterns []*regexp.Regexp
}

func NewMaskingInterceptor() *MaskingInterceptor {
	return &MaskingInterceptor{
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(bearer\s+[a-zA-Z0-9_\-\.]{15,})`),
			regexp.MustCompile(`(?i)(password|passwd|secret|api_key|token)\s*[:=]\s*["']?([^\s"']+)`),
			regexp.MustCompile(`-----BEGIN [A-Z ]+ PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+ PRIVATE KEY-----`),
			regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), // AWS Access Key ID
		},
	}
}

func (m *MaskingInterceptor) Name() string {
	return "MaskingInterceptor"
}

func (m *MaskingInterceptor) Process(_ context.Context, _, output string, _ InterceptOptions) (string, error) {
	sanitized := output
	for _, pat := range m.patterns {
		sanitized = pat.ReplaceAllString(sanitized, "[REDACTED_SECRET]")
	}
	return sanitized, nil
}
