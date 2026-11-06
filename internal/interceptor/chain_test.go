package interceptor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

type mockSecretTool struct{}

func (m *mockSecretTool) Name() string                   { return "mock_tool" }
func (m *mockSecretTool) Description() string            { return "mock tool" }
func (m *mockSecretTool) Parameters() *jsonschema.Schema { return &jsonschema.Schema{} }
func (m *mockSecretTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return `{
		"metadata": {
			"name": "my-pod",
			"uid": "1234-abcd",
			"resourceVersion": "5678",
			"managedFields": [{"manager": "kubectl"}]
		},
		"spec": {
			"env": "password: supersecret123"
		}
	}`, nil
}

func TestChainAndWrap(t *testing.T) {
	ctx := context.Background()
	chain := NewDefaultChain(300)

	// 1. Test Wrap delegation
	wrapped := chain.Wrap(&mockSecretTool{})
	if wrapped.Name() != "mock_tool" {
		t.Fatalf("expected tool name 'mock_tool', got %s", wrapped.Name())
	}

	// 2. Test Default Execution (Pruning + Masking)
	sanitized, err := wrapped.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool execution failed: %v", err)
	}

	if strings.Contains(sanitized, "supersecret123") {
		t.Errorf("expected secret to be masked, got: %s", sanitized)
	}
	if strings.Contains(sanitized, "managedFields") {
		t.Errorf("expected managedFields to be pruned, got: %s", sanitized)
	}
	if strings.Contains(sanitized, "uid") {
		t.Errorf("expected uid to be pruned, got: %s", sanitized)
	}
	if !strings.Contains(sanitized, "my-pod") {
		t.Errorf("expected pod name to be preserved, got: %s", sanitized)
	}

	// 3. Test FullOutput: true (Bypasses pruning, but STILL masks secrets!)
	fullSanitized, err := wrapped.Execute(ctx, json.RawMessage(`{"full_output": true}`))
	if err != nil {
		t.Fatalf("tool execution with full_output failed: %v", err)
	}

	if strings.Contains(fullSanitized, "supersecret123") {
		t.Errorf("security breach: secret should still be masked in full output!")
	}
	if !strings.Contains(fullSanitized, "managedFields") {
		t.Errorf("expected managedFields to be preserved when FullOutput=true")
	}

	// 4. Test Truncation
	longChain := NewDefaultChain(50)
	truncated, err := longChain.Execute(ctx, "logs", strings.Repeat("A", 300), Options{})
	if err != nil {
		t.Fatalf("truncation failed: %v", err)
	}
	if !strings.Contains(truncated, "[output truncated: 50 bytes exceeded]") {
		t.Errorf("expected truncation notice, got: %s", truncated)
	}
}
