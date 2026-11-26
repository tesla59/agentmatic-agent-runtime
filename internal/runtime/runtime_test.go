package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

type mockLLM struct{}

func (m *mockLLM) Chat(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Message: llm.AssistantMessage(llm.TextPart{Text: "all systems normal"}),
	}, nil
}

func (m *mockLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

func TestRuntimeNew_WithClientOverrides(t *testing.T) {
	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset()
	mockLLMClient := &mockLLM{}

	rt, err := New(ctx, Config{
		K8sClient: fakeK8s,
		LLMClient: mockLLMClient,
	})
	if err != nil {
		t.Fatalf("unexpected error initializing runtime: %v", err)
	}

	if rt.Engine == nil {
		t.Error("expected initialized engine, got nil")
	}
	if rt.Registry == nil {
		t.Error("expected initialized registry, got nil")
	}
	if len(rt.Registry.List()) != 8 {
		t.Errorf("expected 8 registered tools, got %d", len(rt.Registry.List()))
	}
}

func TestRuntimeNew_MissingAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")

	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset()

	_, err := New(ctx, Config{
		K8sClient: fakeK8s,
	})
	if err == nil {
		t.Fatal("expected error when API key is missing, got nil")
	}
}

func TestRuntimeNew_UnsupportedProvider(t *testing.T) {
	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset()

	_, err := New(ctx, Config{
		K8sClient:   fakeK8s,
		LLMProvider: "unsupported-llm",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
}

func TestRuntimeNew_EnforceRBACBlocksUnauthorizedTool(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	podsListCalled := false
	fakeK8s.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		podsListCalled = true
		return false, nil, nil
	})
	fakeK8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: false}
		return true, sar, nil
	})

	rt, err := New(context.Background(), Config{
		K8sClient:   fakeK8s,
		LLMClient:   &mockLLM{},
		EnforceRBAC: true,
	})
	if err != nil {
		t.Fatalf("unexpected error initializing runtime: %v", err)
	}

	tool, ok := rt.Registry.Get("k8s_list_pods")
	if !ok {
		t.Fatal("expected k8s_list_pods in registry")
	}

	// Missing identity fails closed before K8s tool fires
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"namespace":"prod"}`))
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
	if podsListCalled {
		t.Fatal("security violation: pods list executed despite missing identity")
	}

	// Denied SAR fails closed before K8s tool fires
	ctx := auth.WithIdentity(context.Background(), session.Identity{
		ClusterID: "prod",
		UserUID:   "uid-bob",
		Username:  "bob",
	})
	_, err = tool.Execute(ctx, json.RawMessage(`{"namespace":"prod"}`))
	if !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
	if podsListCalled {
		t.Fatal("security violation: pods list executed despite denied SAR")
	}

	// Verify every registered tool has an authorization policy mapped in SARAuthorizer
	allowK8s := fake.NewSimpleClientset()
	allowK8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: true}
		return true, sar, nil
	})
	allowRT, err := New(context.Background(), Config{
		K8sClient:   allowK8s,
		LLMClient:   &mockLLM{},
		EnforceRBAC: true,
	})
	if err != nil {
		t.Fatalf("unexpected error initializing allow runtime: %v", err)
	}
	for _, registeredTool := range allowRT.Registry.List() {
		if err := allowRT.Authorizer.Authorize(ctx, registeredTool.Name(), json.RawMessage(`{}`)); err != nil {
			t.Errorf("registered tool %q missing or failing default SAR policy: %v", registeredTool.Name(), err)
		}
	}
}
