package llm

import (
	"encoding/json"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Role defines the participant responsible for a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// PartType classifies individual content fragments within a message.
type PartType string

const (
	PartTypeText       PartType = "text"
	PartTypeThinking   PartType = "thinking"
	PartTypeToolCall   PartType = "tool_call"
	PartTypeToolResult PartType = "tool_result"
)

// Part is the interface implemented by all message content fragments.
type Part interface {
	Type() PartType
}

// TextPart represents standard model text or user input.
type TextPart struct {
	Text string `json:"text"`
}

func (t TextPart) Type() PartType { return PartTypeText }

// ThinkingPart contains extended reasoning or Chain-of-Thought (e.g. Gemini 2.0 / Claude 3.7 / R1).
type ThinkingPart struct {
	Thought          string `json:"thought"`
	ThoughtSignature []byte `json:"thought_signature,omitempty"`
}

func (t ThinkingPart) Type() PartType { return PartTypeThinking }

// ToolCallPart represents an LLM's request to execute an external tool.
type ToolCallPart struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Arguments        string `json:"arguments"` // Raw JSON string
	ThoughtSignature []byte `json:"thought_signature,omitempty"`
}

func (t ToolCallPart) Type() PartType { return PartTypeToolCall }

// ToolResultPart represents the output of a completed tool execution.
type ToolResultPart struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

func (t ToolResultPart) Type() PartType { return PartTypeToolResult }

// Message represents a single multi-part turn in a conversation.
type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Text extracts and joins all TextPart contents in the message.
func (m Message) Text() string {
	var sb strings.Builder
	for _, p := range m.Parts {
		if tp, ok := p.(TextPart); ok {
			sb.WriteString(tp.Text)
		}
	}
	return sb.String()
}

// Thinking extracts and joins all ThinkingPart contents in the message.
func (m Message) Thinking() string {
	var sb strings.Builder
	for _, p := range m.Parts {
		if tp, ok := p.(ThinkingPart); ok {
			sb.WriteString(tp.Thought)
		}
	}
	return sb.String()
}

// ToolCalls collects all ToolCallParts from the message.
func (m Message) ToolCalls() []ToolCallPart {
	var calls []ToolCallPart
	for _, p := range m.Parts {
		if tc, ok := p.(ToolCallPart); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

type rawPart struct {
	Type             PartType `json:"type"`
	Text             string   `json:"text,omitempty"`
	Thought          string   `json:"thought,omitempty"`
	ThoughtSignature []byte   `json:"thought_signature,omitempty"`
	ID               string   `json:"id,omitempty"`
	Name             string   `json:"name,omitempty"`
	Arguments        string   `json:"arguments,omitempty"`
	ToolCallID       string   `json:"tool_call_id,omitempty"`
	Content          string   `json:"content,omitempty"`
	IsError          bool     `json:"is_error,omitempty"`
}

// MarshalJSON provides custom polymorphic serialization for Message.Parts.
func (m Message) MarshalJSON() ([]byte, error) {
	rawParts := make([]rawPart, len(m.Parts))
	for i, p := range m.Parts {
		switch v := p.(type) {
		case TextPart:
			rawParts[i] = rawPart{Type: PartTypeText, Text: v.Text}
		case ThinkingPart:
			rawParts[i] = rawPart{Type: PartTypeThinking, Thought: v.Thought, ThoughtSignature: v.ThoughtSignature}
		case ToolCallPart:
			rawParts[i] = rawPart{Type: PartTypeToolCall, ID: v.ID, Name: v.Name, Arguments: v.Arguments, ThoughtSignature: v.ThoughtSignature}
		case ToolResultPart:
			rawParts[i] = rawPart{Type: PartTypeToolResult, ToolCallID: v.ToolCallID, Content: v.Content, IsError: v.IsError}
		}
	}

	return json.Marshal(&struct {
		Role  Role      `json:"role"`
		Parts []rawPart `json:"parts"`
	}{
		Role:  m.Role,
		Parts: rawParts,
	})
}

// UnmarshalJSON provides custom polymorphic deserialization for Message.Parts.
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role  Role      `json:"role"`
		Parts []rawPart `json:"parts"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.Role = raw.Role
	m.Parts = make([]Part, len(raw.Parts))
	for i, p := range raw.Parts {
		switch p.Type {
		case PartTypeThinking:
			m.Parts[i] = ThinkingPart{
				Thought:          p.Thought,
				ThoughtSignature: p.ThoughtSignature,
			}
		case PartTypeToolCall:
			m.Parts[i] = ToolCallPart{
				ID:               p.ID,
				Name:             p.Name,
				Arguments:        p.Arguments,
				ThoughtSignature: p.ThoughtSignature,
			}
		case PartTypeToolResult:
			m.Parts[i] = ToolResultPart{
				ToolCallID: p.ToolCallID,
				Content:    p.Content,
				IsError:    p.IsError,
			}
		case PartTypeText:
			m.Parts[i] = TextPart{Text: p.Text}
		default:
			// Fallback: detect by populated fields if type was omitted
			if p.Thought != "" {
				m.Parts[i] = ThinkingPart{Thought: p.Thought, ThoughtSignature: p.ThoughtSignature}
			} else if p.Name != "" || p.Arguments != "" {
				m.Parts[i] = ToolCallPart{ID: p.ID, Name: p.Name, Arguments: p.Arguments, ThoughtSignature: p.ThoughtSignature}
			} else if p.ToolCallID != "" {
				m.Parts[i] = ToolResultPart{ToolCallID: p.ToolCallID, Content: p.Content, IsError: p.IsError}
			} else {
				m.Parts[i] = TextPart{Text: p.Text}
			}
		}
	}
	return nil
}

// Helper constructors for common message patterns
func SystemMessage(text string) Message {
	return Message{Role: RoleSystem, Parts: []Part{TextPart{Text: text}}}
}

func UserMessage(text string) Message {
	return Message{Role: RoleUser, Parts: []Part{TextPart{Text: text}}}
}

func AssistantMessage(parts ...Part) Message {
	return Message{Role: RoleAssistant, Parts: parts}
}

func ToolResultMessage(toolCallID, content string, isError bool) Message {
	return Message{
		Role: RoleTool,
		Parts: []Part{
			ToolResultPart{
				ToolCallID: toolCallID,
				Content:    content,
				IsError:    isError,
			},
		},
	}
}

// Usage captures token counts for accounting and cost tracking.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Response represents a non-streaming completion result.
type Response struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop", "tool_calls", "max_tokens"
	Usage        Usage   `json:"usage"`
}

// Request bundles all parameters for an LLM invocation.
type Request struct {
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
}

// ToolDefinition provides the model with metadata and JSON Schema for callable tools.
type ToolDefinition struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Parameters  *jsonschema.Schema `json:"parameters"`
}
