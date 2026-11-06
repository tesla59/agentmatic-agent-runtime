package llm

// StreamEventType classifies events emitted during a streaming completion.
type StreamEventType string

const (
	EventContentDelta  StreamEventType = "content_delta"   // Incremental answer text
	EventThinkingDelta StreamEventType = "thinking_delta"  // Incremental reasoning / CoT
	EventToolCallDelta StreamEventType = "tool_call_delta" // Incremental tool call arguments
	EventDone          StreamEventType = "done"             // Stream completed; carries final Response & Usage
)

// StreamEvent represents a single incremental update from the model.
type StreamEvent struct {
	Type     StreamEventType
	Delta    string        // Text fragment for content, thinking, or tool arguments
	ToolCall *ToolCallPart // Populated when a complete or partial tool call is recognized
	Response *Response     // Populated on EventDone with final assembled response and token metrics
}

// Stream represents an active streaming session from an LLM.
type Stream interface {
	// Next returns the next event in the stream, or io.EOF when generation finishes.
	Next() (*StreamEvent, error)
	// Close terminates the stream early and cleans up resources.
	Close() error
}
