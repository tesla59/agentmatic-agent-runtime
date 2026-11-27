package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
)

type mockLLM struct{}

func (m *mockLLM) Chat(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return &llm.Response{Message: llm.AssistantMessage(llm.TextPart{Text: "ok"})}, nil
}

func (m *mockLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

func TestServerCommand_FlagAndEnvBinding(t *testing.T) {
	t.Setenv("AGENTMATIC_ADDR", ":9090")
	t.Setenv("AGENTMATIC_CLUSTER_ID", "prod-eu")
	t.Setenv("AGENTMATIC_ALLOWED_ORIGINS", "http://localhost:4200,https://ui.example.com")

	var captured serverConfig
	cmd := newRootCommand(func(_ context.Context, cfg serverConfig) error {
		captured = cfg
		return nil
	})
	cmd.SetArgs([]string{"--auth-mode", "k8s", "--max-steps", "12"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command execute failed: %v", err)
	}

	if captured.Addr != ":9090" {
		t.Errorf("expected env addr ':9090', got %q", captured.Addr)
	}
	if captured.AuthMode != "k8s" {
		t.Errorf("expected auth-mode 'k8s', got %q", captured.AuthMode)
	}
	if captured.ClusterID != "prod-eu" {
		t.Errorf("expected cluster-id 'prod-eu', got %q", captured.ClusterID)
	}
	if captured.MaxSteps != 12 {
		t.Errorf("expected max-steps 12, got %d", captured.MaxSteps)
	}
	if len(captured.AllowedOrigins) != 2 || captured.AllowedOrigins[0] != "http://localhost:4200" {
		t.Errorf("unexpected allowed origins: %v", captured.AllowedOrigins)
	}
}

func TestBuildServer_AuthModesAndFailClosed(t *testing.T) {
	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset()

	// 1. Default auth-mode=k8s without cluster-id must fail closed
	if _, err := buildServer(ctx, serverConfig{
		SessionDir: t.TempDir(),
		K8sClient:  fakeK8s,
		LLMClient:  &mockLLM{},
	}); err == nil || !strings.Contains(err.Error(), "cluster-id") {
		t.Errorf("expected cluster-id error when auth-mode defaults to k8s, got %v", err)
	}

	// 2. Unsupported auth-mode must fail closed
	if _, err := buildServer(ctx, serverConfig{
		AuthMode:   "invalid",
		SessionDir: t.TempDir(),
		K8sClient:  fakeK8s,
		LLMClient:  &mockLLM{},
	}); err == nil {
		t.Error("expected error for unsupported auth-mode")
	}

	// 3. auth-mode=k8s with cluster-id builds TokenReview + SAR server
	k8sSrv, err := buildServer(ctx, serverConfig{
		Addr:       ":8443",
		AuthMode:   "k8s",
		ClusterID:  "prod-us",
		SessionDir: t.TempDir(),
		K8sClient:  fakeK8s,
		LLMClient:  &mockLLM{},
	})
	if err != nil {
		t.Fatalf("buildServer k8s mode failed: %v", err)
	}
	if k8sSrv.Addr() != ":8443" {
		t.Errorf("expected addr ':8443', got %q", k8sSrv.Addr())
	}

	// Verify /healthz works while unauthenticated RPC fails closed
	rec := httptest.NewRecorder()
	k8sSrv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on /healthz, got %d", rec.Code)
	}

	unauthRec := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodPost, "/agentmatic.v1.SessionService/ListSessions", strings.NewReader(`{}`))
	unauthReq.Header.Set("Content-Type", "application/json")
	k8sSrv.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without bearer token in k8s mode, got %d", unauthRec.Code)
	}

	// 4. auth-mode=local builds dev server
	localSrv, err := buildServer(ctx, serverConfig{
		AuthMode:   "local",
		SessionDir: t.TempDir(),
		K8sClient:  fakeK8s,
		LLMClient:  &mockLLM{},
	})
	if err != nil {
		t.Fatalf("buildServer local mode failed: %v", err)
	}
	localRec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agentmatic.v1.SessionService/ListSessions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	localSrv.Handler().ServeHTTP(localRec, req)
	if localRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK in local auth mode, got %d (%s)", localRec.Code, localRec.Body.String())
	}
}
