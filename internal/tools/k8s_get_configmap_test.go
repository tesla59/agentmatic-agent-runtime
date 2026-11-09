package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetConfigMapTool(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-config",
			Namespace: "default",
		},
		Data: map[string]string{
			"app.properties": "server.port=8080\nenv=production",
			"config.json":    `{"features":{"beta":true}}`,
		},
	})

	tool := NewGetConfigMapTool(fakeClient)

	if tool.Name() != "k8s_get_configmap" {
		t.Fatalf("expected tool name 'k8s_get_configmap', got %q", tool.Name())
	}
	if tool.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}

	// 1. Success case
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"name": "app-config", "namespace": "default"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(out, "app-config") {
		t.Errorf("expected output to contain 'app-config', got: %s", out)
	}
	if !strings.Contains(out, "server.port=8080") {
		t.Errorf("expected output to contain config data 'server.port=8080', got: %s", out)
	}

	// 2. Missing name validation
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"namespace": "default"}`))
	if err == nil {
		t.Fatal("expected error when name is missing, got nil")
	}

	// 3. Not found case
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"name": "non-existent"}`))
	if err == nil {
		t.Fatal("expected error when configmap not found, got nil")
	}
}
