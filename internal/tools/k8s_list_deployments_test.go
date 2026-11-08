package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestListDeploymentsTool(t *testing.T) {
	replicas := int32(3)
	fakeClient := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-deploy",
			Namespace: "default",
			Labels:    map[string]string{"app": "web"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas:     3,
			AvailableReplicas: 3,
		},
	})

	tool := NewListDeploymentsTool(fakeClient)

	if tool.Name() != "k8s_list_deployments" {
		t.Fatalf("expected tool name 'k8s_list_deployments', got %q", tool.Name())
	}
	if tool.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"namespace": "default"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(out, "web-deploy") {
		t.Errorf("expected output to contain 'web-deploy', got: %s", out)
	}
}
