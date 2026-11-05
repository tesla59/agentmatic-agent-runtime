package interceptor

import (
	"context"
	"strings"
	"testing"
)

func TestPipeline(t *testing.T) {
	ctx := context.Background()
	pipeline := NewDefaultPipeline(200)

	rawOutput := `{
		"metadata": {
			"name": "my-pod",
			"uid": "1234-abcd",
			"resourceVersion": "5678",
			"managedFields": [{"manager": "kubectl"}]
		},
		"spec": {
			"env": "password: supersecret123"
		}
	}`

	// 1. Test Default: Pruning + Masking
	sanitized, err := pipeline.Execute(ctx, "k8s_get", rawOutput, InterceptOptions{FullOutput: false})
	if err != nil {
		t.Fatalf("pipeline execution failed: %v", err)
	}

	if strings.Contains(sanitized, "supersecret123") {
		t.Errorf("expected secret to be masked, got: %s", sanitized)
	}
	if strings.Contains(sanitized, "managedFields") {
		t.Errorf("expected managedFields to be pruned, got: %s", sanitized)
	}
	if strings.Contains(sanitized, "uid") {
		t.Errorf("expected uid to be pruned, got: %s", sanitized)
	}
	if !strings.Contains(sanitized, "my-pod") {
		t.Errorf("expected pod name to be preserved, got: %s", sanitized)
	}

	// 2. Test FullOutput: true (Bypasses pruning, but STILL masks secrets!)
	fullSanitized, err := pipeline.Execute(ctx, "k8s_get", rawOutput, InterceptOptions{FullOutput: true, MaxBytes: 1000})
	if err != nil {
		t.Fatalf("pipeline execution failed: %v", err)
	}

	if strings.Contains(fullSanitized, "supersecret123") {
		t.Errorf("security breach: secret should still be masked in full output!")
	}
	if !strings.Contains(fullSanitized, "managedFields") {
		t.Errorf("expected managedFields to be preserved when FullOutput=true")
	}

	// 3. Test Truncation
	longText := strings.Repeat("A", 300)
	truncated, err := pipeline.Execute(ctx, "logs", longText, InterceptOptions{MaxBytes: 50})
	if err != nil {
		t.Fatalf("truncation failed: %v", err)
	}
	if !strings.Contains(truncated, "[output truncated: 50 bytes exceeded]") {
		t.Errorf("expected truncation notice, got: %s", truncated)
	}
}
