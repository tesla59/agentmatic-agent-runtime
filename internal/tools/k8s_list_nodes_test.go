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

func TestListNodesTool(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-worker-1",
			Labels: map[string]string{
				"node-role.kubernetes.io/worker": "",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
					Reason: "KubeletReady",
				},
			},
		},
	})

	tool := NewListNodesTool(fakeClient)

	if tool.Name() != "k8s_list_nodes" {
		t.Fatalf("expected tool name 'k8s_list_nodes', got %q", tool.Name())
	}
	if tool.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(out, "node-worker-1") {
		t.Errorf("expected output to contain 'node-worker-1', got: %s", out)
	}
	if !strings.Contains(out, "KubeletReady") {
		t.Errorf("expected output to contain 'KubeletReady', got: %s", out)
	}
}
