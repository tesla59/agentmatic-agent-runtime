package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"

	"google.golang.org/genai"
)

// GeminiConfig holds settings for initializing the Gemini client.
type GeminiConfig struct {
	APIKey string
	Model  string // Defaults to "gemini-flash-latest" if empty
}

// GeminiClient implements Client using the official Google GenAI Go SDK.
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient creates a new native Gemini client using the official GenAI SDK.
func NewGeminiClient(ctx context.Context, cfg GeminiConfig) (*GeminiClient, error) {
	if cfg.Model == "" {
		cfg.Model = "gemini-flash-latest"
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	return &GeminiClient{
		client: client,
		model:  cfg.Model,
	}, nil
}

// Chat executes a blocking request to the Gemini API.
func (g *GeminiClient) Chat(ctx context.Context, req Request) (*Response, error) {
	contents, config, err := g.buildPayload(req)
	if err != nil {
		return nil, err
	}

	rawResp, err := g.client.Models.GenerateContent(ctx, g.model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("gemini generate content failed: %w", err)
	}

	return g.convertResponse(rawResp)
}

// ChatStream executes a streaming request to the Gemini API.
func (g *GeminiClient) ChatStream(ctx context.Context, req Request) (Stream, error) {
	contents, config, err := g.buildPayload(req)
	if err != nil {
		return nil, err
	}

	seq := g.client.Models.GenerateContentStream(ctx, g.model, contents, config)
	return newGeminiStream(ctx, seq), nil
}

// buildPayload translates an internal Request into Gemini contents and configuration.
// Gemini requires system prompts in SystemInstruction, and only accepts "user" and "model"
// roles in conversation turns (both user prompts and tool responses map to "user").
func (g *GeminiClient) buildPayload(req Request) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	var contents []*genai.Content
	var systemInstruction *genai.Content

	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			systemInstruction = &genai.Content{
				Parts: []*genai.Part{{Text: msg.Text()}},
			}
			continue
		}

		// Gemini conversation turns only accept "user" and "model".
		// RoleUser and RoleTool (function responses) are both sent under "user".
		genaiRole := "user"
		if msg.Role == RoleAssistant {
			genaiRole = "model"
		}

		var parts []*genai.Part
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case TextPart:
				parts = append(parts, &genai.Part{Text: p.Text})
			case ThinkingPart:
				parts = append(parts, &genai.Part{
					Text:             p.Thought,
					Thought:          true,
					ThoughtSignature: p.ThoughtSignature,
				})
			case ToolCallPart:
				// genai uses FunctionCall for client-side tools executed by our harness.
				// (genai.ToolCall is reserved for server-side tools like Google Search).
				var args map[string]any
				if len(p.Arguments) > 0 {
					if err := json.Unmarshal([]byte(p.Arguments), &args); err != nil {
						return nil, nil, fmt.Errorf("invalid tool call arguments for %s: %w", p.Name, err)
					}
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: p.Name,
						Args: args,
					},
					ThoughtSignature: p.ThoughtSignature,
				})
			case ToolResultPart:
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name: p.ToolCallID,
						Response: map[string]any{
							"output": p.Content,
							"error":  p.IsError,
						},
					},
				})
			}
		}

		if len(parts) > 0 {
			contents = append(contents, &genai.Content{
				Role:  genaiRole,
				Parts: parts,
			})
		}
	}

	config := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		config.Temperature = &temp
	}
	if req.MaxTokens != nil {
		config.MaxOutputTokens = int32(*req.MaxTokens)
	}

	// Attach tools if provided
	if len(req.Tools) > 0 {
		var decls []*genai.FunctionDeclaration
		for _, t := range req.Tools {
			decls = append(decls, &genai.FunctionDeclaration{
				Name:                 t.Name,
				Description:          t.Description,
				ParametersJsonSchema: t.Parameters,
			})
		}
		config.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
	}

	return contents, config, nil
}

func (g *GeminiClient) convertResponse(raw *genai.GenerateContentResponse) (*Response, error) {
	if len(raw.Candidates) == 0 {
		return nil, fmt.Errorf("gemini returned no candidates")
	}

	candidate := raw.Candidates[0]
	var parts []Part

	if candidate.Content != nil {
		for _, p := range candidate.Content.Parts {
			if p.Thought {
				parts = append(parts, ThinkingPart{
					Thought:          p.Text,
					ThoughtSignature: p.ThoughtSignature,
				})
			} else if p.Text != "" {
				parts = append(parts, TextPart{Text: p.Text})
			}
			if p.FunctionCall != nil {
				argsBytes, err := json.Marshal(p.FunctionCall.Args)
				if err != nil {
					argsBytes = []byte("{}")
				}
				parts = append(parts, ToolCallPart{
					ID:               p.FunctionCall.Name, // Gemini uses function name as identity in single-turn
					Name:             p.FunctionCall.Name,
					Arguments:        string(argsBytes),
					ThoughtSignature: p.ThoughtSignature,
				})
			}
		}
	}

	var usage Usage
	if raw.UsageMetadata != nil {
		usage = Usage{
			PromptTokens:     int(raw.UsageMetadata.PromptTokenCount),
			CompletionTokens: int(raw.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int(raw.UsageMetadata.TotalTokenCount),
		}
	}

	finishReason := string(candidate.FinishReason)

	return &Response{
		Message: Message{
			Role:  RoleAssistant,
			Parts: parts,
		},
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}

// --- Gemini Stream Implementation ---

type geminiStream struct {
	ctx        context.Context
	cancel     context.CancelFunc
	eventChan  chan *StreamEvent
	err        error
	totalUsage Usage
}

func newGeminiStream(ctx context.Context, seq iter.Seq2[*genai.GenerateContentResponse, error]) *geminiStream {
	ctx, cancel := context.WithCancel(ctx)
	s := &geminiStream{
		ctx:       ctx,
		cancel:    cancel,
		eventChan: make(chan *StreamEvent, 16),
	}

	go s.consume(seq)
	return s
}

func (s *geminiStream) consume(seq iter.Seq2[*genai.GenerateContentResponse, error]) {
	defer close(s.eventChan)

	var accumulatedParts []Part

	for chunk, err := range seq {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		if err != nil {
			s.err = err
			return
		}

		if chunk.UsageMetadata != nil {
			s.totalUsage.PromptTokens = int(chunk.UsageMetadata.PromptTokenCount)
			s.totalUsage.CompletionTokens = int(chunk.UsageMetadata.CandidatesTokenCount)
			s.totalUsage.TotalTokens = int(chunk.UsageMetadata.TotalTokenCount)
		}

		if len(chunk.Candidates) == 0 || chunk.Candidates[0].Content == nil {
			continue
		}

		for _, p := range chunk.Candidates[0].Content.Parts {
			if p.Thought {
				accumulatedParts = append(accumulatedParts, ThinkingPart{
					Thought:          p.Text,
					ThoughtSignature: p.ThoughtSignature,
				})
				if !s.send(&StreamEvent{Type: EventThinkingDelta, Delta: p.Text}) {
					return
				}
			} else if p.Text != "" {
				accumulatedParts = append(accumulatedParts, TextPart{Text: p.Text})
				if !s.send(&StreamEvent{Type: EventContentDelta, Delta: p.Text}) {
					return
				}
			}
			if p.FunctionCall != nil {
				argsBytes, _ := json.Marshal(p.FunctionCall.Args)
				tc := &ToolCallPart{
					ID:               p.FunctionCall.Name,
					Name:             p.FunctionCall.Name,
					Arguments:        string(argsBytes),
					ThoughtSignature: p.ThoughtSignature,
				}
				accumulatedParts = append(accumulatedParts, *tc)
				if !s.send(&StreamEvent{Type: EventToolCallDelta, ToolCall: tc, Delta: string(argsBytes)}) {
					return
				}
			}
		}
	}

	// Final completion event with full assembled response
	finalResp := &Response{
		Message:      Message{Role: RoleAssistant, Parts: accumulatedParts},
		FinishReason: "stop",
		Usage:        s.totalUsage,
	}
	s.send(&StreamEvent{Type: EventDone, Response: finalResp})
}

func (s *geminiStream) send(ev *StreamEvent) bool {
	select {
	case <-s.ctx.Done():
		return false
	case s.eventChan <- ev:
		return true
	}
}

func (s *geminiStream) Next() (*StreamEvent, error) {
	select {
	case ev, ok := <-s.eventChan:
		if !ok {
			if s.err != nil {
				return nil, s.err
			}
			return nil, io.EOF
		}
		return ev, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *geminiStream) Close() error {
	s.cancel()
	return nil
}
