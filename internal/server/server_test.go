package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"k8s.io/client-go/kubernetes/fake"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1/agentmaticv1connect"
	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

func TestNewServer_Validation(t *testing.T) {
	rt := &runtime.Runtime{} // missing Engine
	sm := session.NewSessionManager()
	authn := auth.NewLocalDevAuthenticator()

	if _, err := New(Config{SessionManager: sm, Authenticator: authn}); err == nil {
		t.Error("expected error when Runtime is nil")
	}
	if _, err := New(Config{Runtime: rt, SessionManager: sm, Authenticator: authn}); err == nil {
		t.Error("expected error when Runtime.Engine is nil")
	}

	fullRT, err := runtime.New(context.Background(), runtime.Config{
		K8sClient: fake.NewSimpleClientset(),
		LLMClient: &stubLLM{},
	})
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	if _, err := New(Config{Runtime: fullRT, Authenticator: authn}); err == nil {
		t.Error("expected error when SessionManager is nil")
	}
	if _, err := New(Config{Runtime: fullRT, SessionManager: sm}); err == nil {
		t.Error("expected error when Authenticator is nil")
	}
}

func TestServer_HealthzCORSAndEndToEndRPC(t *testing.T) {
	rt, err := runtime.New(context.Background(), runtime.Config{
		K8sClient: fake.NewSimpleClientset(),
		LLMClient: &scriptedLLM{
			handler: func(_ context.Context, _ llm.Request, _ int) (*llm.Response, error) {
				return &llm.Response{Message: llm.AssistantMessage(llm.TextPart{Text: "Cluster healthy."})}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	store, err := session.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	sm := session.NewSessionManager(store)

	srv, err := New(Config{
		Runtime:        rt,
		SessionManager: sm,
		Authenticator:  auth.NewLocalDevAuthenticator(),
		AllowedOrigins: []string{"http://localhost:4200"},
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	if srv.Addr() != defaultAddr {
		t.Errorf("expected default addr %q, got %q", defaultAddr, srv.Addr())
	}

	// 1. /healthz works without Authorization header
	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK || !strings.Contains(healthRec.Body.String(), `"status":"ok"`) {
		t.Errorf("unexpected healthz response: code=%d body=%s", healthRec.Code, healthRec.Body.String())
	}

	// 2. CORS preflight for allowed Angular dev origin exposes SessionHeaderKey
	preflightReq := httptest.NewRequest(http.MethodOptions, agentmaticv1connect.RuntimeServiceExecuteProcedure, nil)
	preflightReq.Header.Set("Origin", "http://localhost:4200")
	preflightRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(preflightRec, preflightReq)
	if preflightRec.Code != http.StatusNoContent {
		t.Errorf("expected 204 on OPTIONS preflight, got %d", preflightRec.Code)
	}
	if got := preflightRec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4200" {
		t.Errorf("expected Access-Control-Allow-Origin 'http://localhost:4200', got %q", got)
	}
	if exposed := preflightRec.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(exposed, SessionHeaderKey) {
		t.Errorf("expected exposed headers to contain %q, got %q", SessionHeaderKey, exposed)
	}

	// 3. Disallowed origin does not receive Access-Control-Allow-Origin
	badOriginReq := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	badOriginReq.Header.Set("Origin", "https://untrusted.example.com")
	badOriginRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badOriginRec, badOriginReq)
	if got := badOriginRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected empty Access-Control-Allow-Origin for untrusted origin, got %q", got)
	}

	// 4. End-to-end ConnectRPC calls through assembled Server.Handler()
	client := &streamingHandlerClient{handler: srv.Handler()}
	rtClient := agentmaticv1connect.NewRuntimeServiceClient(client, "http://agentmatic.local")
	sessClient := agentmaticv1connect.NewSessionServiceClient(client, "http://agentmatic.local")

	stream, err := rtClient.Execute(context.Background(), connect.NewRequest(&agentmaticv1.ExecuteRequest{
		Prompt: "Check health",
	}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var answer string
	for stream.Receive() {
		if fa := stream.Msg().GetFinalAnswer(); fa != nil {
			answer = fa.Answer
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	_ = stream.Close()

	if answer != "Cluster healthy." {
		t.Errorf("unexpected answer %q", answer)
	}

	listResp, err := sessClient.ListSessions(context.Background(), connect.NewRequest(&agentmaticv1.ListSessionsRequest{}))
	if err != nil || len(listResp.Msg.Sessions) != 1 {
		t.Fatalf("expected 1 persisted session, got %v err=%v", listResp, err)
	}
}
