package tools

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func TestGetPodLogsToolValidation(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	tool := NewGetPodLogsTool(fakeClient)

	if tool.Name() != "k8s_get_pod_logs" {
		t.Fatalf("unexpected tool name: %s", tool.Name())
	}
	if tool.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}

	// Missing required 'pod' argument should return error
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"namespace": "default"}`))
	if err == nil {
		t.Fatal("expected error when 'pod' argument is missing, got nil")
	}
}
