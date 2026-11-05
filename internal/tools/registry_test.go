package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

type mockTool struct {
	name string
}

func (m *mockTool) Name() string                     { return m.name }
func (m *mockTool) Description() string              { return "mock description" }
func (m *mockTool) Parameters() *jsonschema.Schema   { return &jsonschema.Schema{} }
func (m *mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "ok", nil
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	tool := &mockTool{name: "test_tool"}

	// 1. Test Register
	if err := r.Register(tool); err != nil {
		t.Fatalf("failed to register tool: %v", err)
	}

	// 2. Test duplicate registration error
	if err := r.Register(tool); err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}

	// 3. Test Get
	got, ok := r.Get("test_tool")
	if !ok || got.Name() != "test_tool" {
		t.Fatalf("failed to get registered tool")
	}

	// 4. Test List
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 tool in list, got %d", len(list))
	}
}
