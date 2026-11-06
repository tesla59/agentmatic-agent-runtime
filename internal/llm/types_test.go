package llm

import (
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
