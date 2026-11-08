package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetDeploymentTool(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-server",
			Namespace: "prod",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "api",
							Image: "nginx:1.25.3",
						},
					},
				},
			},
		},
	})

	tool := NewGetDeploymentTool(fakeClient)

	if tool.Name() != "k8s_get_deployment" {
		t.Fatalf("expected tool name 'k8s_get_deployment', got %q", tool.Name())
	}
	if tool.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}

	// 1. Success case
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"name": "api-server", "namespace": "prod"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(out, "nginx:1.25.3") {
		t.Errorf("expected output to contain container image 'nginx:1.25.3', got: %s", out)
	}

	// 2. Missing name validation
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"namespace": "prod"}`))
	if err == nil {
		t.Error("expected error when deployment name is missing, got nil")
	}

	// 3. Non-existent deployment
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"name": "non-existent"}`))
	if err == nil {
		t.Error("expected error for non-existent deployment, got nil")
	}
}
