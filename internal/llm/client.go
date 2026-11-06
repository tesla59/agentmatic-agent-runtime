package llm

import "context"

// Client represents any LLM provider capable of chat completions with tool use.
type Client interface {
	// Chat performs a blocking request and returns the full model response.
	Chat(ctx context.Context, req Request) (*Response, error)

	// ChatStream initiates a streaming request emitting tokens, thinking, and tool events in real time.
	ChatStream(ctx context.Context, req Request) (Stream, error)
}
