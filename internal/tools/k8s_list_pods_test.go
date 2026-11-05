package tools

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestListPodsTool(t *testing.T) {
	// Create fake k8s client with a pod
	fakeClient := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	})

	tool := NewListPodsTool(fakeClient)

	// Verify Name & Schema
	if tool.Name() != "k8s_list_pods" {
		t.Fatalf("expected tool name 'k8s_list_pods', got %q", tool.Name())
	}
	if tool.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}

	// Test Execute
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"namespace": "default"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
}
