package runtime

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
)

type mockLLM struct{}

func (m *mockLLM) Chat(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Message: llm.AssistantMessage(llm.TextPart{Text: "all systems normal"}),
	}, nil
}

func (m *mockLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

func TestRuntimeNew_WithClientOverrides(t *testing.T) {
	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset()
	mockLLMClient := &mockLLM{}

	rt, err := New(ctx, Config{
		K8sClient: fakeK8s,
		LLMClient: mockLLMClient,
	})
	if err != nil {
		t.Fatalf("unexpected error initializing runtime: %v", err)
	}

	if rt.Engine == nil {
		t.Error("expected initialized engine, got nil")
	}
	if rt.Registry == nil {
		t.Error("expected initialized registry, got nil")
	}
	if len(rt.Registry.List()) != 6 {
		t.Errorf("expected 6 registered tools, got %d", len(rt.Registry.List()))
	}
}

func TestRuntimeNew_MissingAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")

	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset()

	_, err := New(ctx, Config{
		K8sClient: fakeK8s,
	})
	if err == nil {
		t.Fatal("expected error when API key is missing, got nil")
	}
}

func TestRuntimeNew_UnsupportedProvider(t *testing.T) {
	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset()

	_, err := New(ctx, Config{
		K8sClient:   fakeK8s,
		LLMProvider: "unsupported-llm",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
}
