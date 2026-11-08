package llm

import (
	"encoding/json"
	"testing"
)

func TestMessageExtractors(t *testing.T) {
	msg := AssistantMessage(
		ThinkingPart{Thought: "Let me check the pods."},
		TextPart{Text: "Checking pods now..."},
		ToolCallPart{ID: "call_1", Name: "k8s_list_pods", Arguments: `{"namespace":"default"}`},
	)

	if msg.Thinking() != "Let me check the pods." {
		t.Errorf("unexpected thinking: %q", msg.Thinking())
	}
	if msg.Text() != "Checking pods now..." {
		t.Errorf("unexpected text: %q", msg.Text())
	}
	calls := msg.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "k8s_list_pods" {
		t.Errorf("unexpected tool calls: %+v", calls)
	}
}

func TestMessageJSONRoundTrip(t *testing.T) {
	orig := AssistantMessage(
		ThinkingPart{Thought: "Deep reasoning step", ThoughtSignature: []byte("crypto-sig-123")},
		TextPart{Text: "Executing tool..."},
		ToolCallPart{ID: "k8s_list_pods", Name: "k8s_list_pods", Arguments: `{"namespace":"default"}`, ThoughtSignature: []byte("sig-call")},
	)

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	var restored Message
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("failed to unmarshal message: %v", err)
	}

	if restored.Role != orig.Role {
		t.Errorf("role mismatch: %v != %v", restored.Role, orig.Role)
	}
	if len(restored.Parts) != 3 {
		t.Fatalf("parts length mismatch: %d != 3", len(restored.Parts))
	}
	if restored.Thinking() != "Deep reasoning step" {
		t.Errorf("thinking mismatch: %q", restored.Thinking())
	}
	if restored.Text() != "Executing tool..." {
		t.Errorf("text mismatch: %q", restored.Text())
	}

	calls := restored.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "k8s_list_pods" || string(calls[0].ThoughtSignature) != "sig-call" {
		t.Errorf("tool call mismatch: %+v", calls)
	}
}
