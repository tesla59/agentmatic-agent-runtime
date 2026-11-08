package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/tools"
)

type mockLLM struct {
	turns []*llm.Response
	index int
}

func (m *mockLLM) Chat(_ context.Context, _ llm.Request) (*llm.Response, error) {
	if m.index >= len(m.turns) {
		return &llm.Response{
			Message: llm.AssistantMessage(llm.TextPart{Text: "default fallback answer"}),
		}, nil
	}
	resp := m.turns[m.index]
	m.index++
	return resp, nil
}

func (m *mockLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

type testTool struct{}

func (t *testTool) Name() string                   { return "k8s_test_tool" }
func (t *testTool) Description() string            { return "test tool" }
func (t *testTool) Parameters() *jsonschema.Schema { return &jsonschema.Schema{} }
func (t *testTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return `[{"pod": "web", "status": "Running"}]`, nil
}

func TestEngineDirectAnswer(t *testing.T) {
	mock := &mockLLM{
		turns: []*llm.Response{
			{
				Message: llm.AssistantMessage(llm.TextPart{Text: "Everything is healthy."}),
				Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
	}

	reg := tools.NewRegistry()
	eng, err := New(Config{
		Client:   mock,
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	res, err := eng.Run(context.Background(), "Check cluster health")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if res.Answer != "Everything is healthy." {
		t.Errorf("unexpected answer: %q", res.Answer)
	}
	if len(res.Steps) != 0 {
		t.Errorf("expected 0 steps for direct answer, got %d", len(res.Steps))
	}
	if res.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", res.Usage.TotalTokens)
	}
}

func TestEngineWithToolExecution(t *testing.T) {
	mock := &mockLLM{
		turns: []*llm.Response{
			// Turn 1: Model asks to call k8s_test_tool
			{
				Message: llm.AssistantMessage(
					llm.ThinkingPart{Thought: "I need to inspect the pods first."},
					llm.ToolCallPart{ID: "call_1", Name: "k8s_test_tool", Arguments: `{}`},
				),
				Usage: llm.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
			},
			// Turn 2: Model observes output and provides final answer
			{
				Message: llm.AssistantMessage(
					llm.TextPart{Text: "Found 1 running pod: web."},
				),
				Usage: llm.Usage{PromptTokens: 35, CompletionTokens: 10, TotalTokens: 45},
			},
		},
	}

	reg := tools.NewRegistry()
	if err := reg.Register(&testTool{}); err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}

	var stepNotifications []Step
	eng, err := New(Config{
		Client:   mock,
		Registry: reg,
		OnStep: func(s Step) {
			stepNotifications = append(stepNotifications, s)
		},
	})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	res, err := eng.Run(context.Background(), "Check pods")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if res.Answer != "Found 1 running pod: web." {
		t.Errorf("unexpected answer: %q", res.Answer)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(res.Steps))
	}
	if res.Steps[0].Action.Name != "k8s_test_tool" {
		t.Errorf("unexpected action: %s", res.Steps[0].Action.Name)
	}
	if res.Usage.TotalTokens != 75 { // 30 + 45
		t.Errorf("expected 75 total tokens, got %d", res.Usage.TotalTokens)
	}
	if len(stepNotifications) != 1 {
		t.Errorf("expected 1 step notification, got %d", len(stepNotifications))
	}
}

func TestEngineMaxStepsExceeded(t *testing.T) {
	// Infinite loop: model keeps calling the tool on every step
	infiniteMock := &mockLLM{
		turns: []*llm.Response{
			{
				Message: llm.AssistantMessage(
					llm.ToolCallPart{ID: "call_inf", Name: "k8s_test_tool", Arguments: `{}`},
				),
			},
			{
				Message: llm.AssistantMessage(
					llm.ToolCallPart{ID: "call_inf", Name: "k8s_test_tool", Arguments: `{}`},
				),
			},
			{
				Message: llm.AssistantMessage(
					llm.ToolCallPart{ID: "call_inf", Name: "k8s_test_tool", Arguments: `{}`},
				),
			},
		},
	}

	reg := tools.NewRegistry()
	_ = reg.Register(&testTool{})

	eng, _ := New(Config{
		Client:   infiniteMock,
		Registry: reg,
		MaxSteps: 2, // Hard limit at 2 steps
	})

	_, err := eng.Run(context.Background(), "Loop task")
	if err == nil {
		t.Fatal("expected max steps exceeded error, got nil")
	}
}
