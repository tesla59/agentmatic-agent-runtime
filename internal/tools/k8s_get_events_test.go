package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetEventsTool(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(&corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-1",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod",
			Name: "test-pod",
		},
		Type:          "Warning",
		Reason:        "FailedScheduling",
		Message:       "Insufficient memory",
		Count:         3,
		LastTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
	})

	tool := NewGetEventsTool(fakeClient)

	if tool.Name() != "k8s_get_events" {
		t.Fatalf("unexpected tool name: %s", tool.Name())
	}

	// 1. Fetch compact events
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"namespace": "default"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(out, "FailedScheduling") {
		t.Errorf("expected FailedScheduling event, got: %s", out)
	}
	if !strings.Contains(out, "Insufficient memory") {
		t.Errorf("expected Insufficient memory message, got: %s", out)
	}

	// 2. Filter by involved_object_name
	filteredOut, err := tool.Execute(context.Background(), json.RawMessage(`{"namespace": "default", "involved_object_name": "other-pod"}`))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if strings.Contains(filteredOut, "FailedScheduling") {
		t.Errorf("expected no events for 'other-pod', got: %s", filteredOut)
	}
}
