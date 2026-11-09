package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestListServicesTool(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-service",
			Namespace: "default",
			Labels:    map[string]string{"app": "web"},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       80,
					TargetPort: intstr.FromInt(8080),
				},
			},
			Selector: map[string]string{"app": "web"},
		},
	})

	tool := NewListServicesTool(fakeClient)

	if tool.Name() != "k8s_list_services" {
		t.Fatalf("expected tool name 'k8s_list_services', got %q", tool.Name())
	}
	if tool.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"namespace": "default"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(out, "web-service") {
		t.Errorf("expected output to contain 'web-service', got: %s", out)
	}
	if !strings.Contains(out, "ClusterIP") {
		t.Errorf("expected output to contain 'ClusterIP', got: %s", out)
	}
}
