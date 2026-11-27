package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
	"github.com/tesla59/agentmatic-agent-runtime/internal/server"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

type scriptedLLM struct {
	mu    sync.Mutex
	calls int
	fn    func(ctx context.Context, req llm.Request, callIdx int) (*llm.Response, error)
}

func (s *scriptedLLM) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	s.mu.Lock()
	idx := s.calls
	s.calls++
	s.mu.Unlock()
	return s.fn(ctx, req, idx)
}

func (s *scriptedLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

type inMemHandlerClient struct {
	handler http.Handler
}

func (c *inMemHandlerClient) Do(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	rw := &memResponseWriter{
		header:       make(http.Header),
		pw:           pw,
		status:       http.StatusOK,
		headersReady: make(chan struct{}),
	}

	go func() {
		defer func() {
			rw.signalHeaders()
			_ = pw.Close()
		}()
		c.handler.ServeHTTP(rw, req)
	}()

	select {
	case <-rw.headersReady:
		return &http.Response{
			StatusCode: rw.status,
			Header:     rw.header.Clone(),
			Body:       pr,
			Request:    req,
		}, nil
	case <-req.Context().Done():
		_ = pr.Close()
		return nil, req.Context().Err()
	}
}

type memResponseWriter struct {
	header       http.Header
	pw           *io.PipeWriter
	status       int
	once         sync.Once
	headersReady chan struct{}
}

func (w *memResponseWriter) Header() http.Header         { return w.header }
func (w *memResponseWriter) WriteHeader(code int)        { w.status = code; w.signalHeaders() }
func (w *memResponseWriter) Flush()                      { w.signalHeaders() }
func (w *memResponseWriter) Write(p []byte) (int, error) { w.signalHeaders(); return w.pw.Write(p) }
func (w *memResponseWriter) signalHeaders()              { w.once.Do(func() { close(w.headersReady) }) }

func TestCLICommand_FlagsEnvConfigAndValidation(t *testing.T) {
	t.Setenv("AGENTMATIC_SERVER", "https://agentmatic.internal")
	t.Setenv("AGENTMATIC_TOKEN", "env-token")

	var capturedCfg cliConfig
	var capturedArgs []string
	cmd := newCLIRootCommand(func(_ context.Context, cfg cliConfig, args []string) error {
		capturedCfg = cfg
		capturedArgs = args
		return nil
	})
	cmd.SetArgs([]string{"--session", "my-slug", "check", "pods"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if capturedCfg.ServerURL != "https://agentmatic.internal" || capturedCfg.Token != "env-token" {
		t.Errorf("unexpected env-bound config: %+v", capturedCfg)
	}
	if capturedCfg.SessionRef != "my-slug" || strings.Join(capturedArgs, " ") != "check pods" {
		t.Errorf("unexpected args/session: cfg=%+v args=%v", capturedCfg, capturedArgs)
	}

	// Mutually exclusive --local and --server must fail
	errCmd := newCLIRootCommand(func(context.Context, cliConfig, []string) error { return nil })
	errCmd.SetArgs([]string{"--local", "--server", "http://localhost:8080"})
	if err := errCmd.Execute(); err == nil {
		t.Error("expected error when both --local and --server are set")
	}
}

func TestResolveBearerToken(t *testing.T) {
	// 1. Explicit token takes precedence
	tok, err := resolveBearerToken("  explicit-tok  ", "/nonexistent", "")
	if err != nil || tok != "explicit-tok" {
		t.Fatalf("expected explicit token, got %q err=%v", tok, err)
	}

	// 2. Kubeconfig with static token
	tmpDir := t.TempDir()
	kcTokenPath := filepath.Join(tmpDir, "kc-token.yaml")
	kcTokenContent := `apiVersion: v1
kind: Config
clusters:
- name: dev
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: dev
  context:
    cluster: dev
    user: dev-user
current-context: dev
users:
- name: dev-user
  user:
    token: kubeconfig-bearer-123
`
	if err := os.WriteFile(kcTokenPath, []byte(kcTokenContent), 0600); err != nil {
		t.Fatalf("write kubeconfig failed: %v", err)
	}

	tok, err = resolveBearerToken("", kcTokenPath, "")
	if err != nil || tok != "kubeconfig-bearer-123" {
		t.Fatalf("expected kubeconfig token, got %q err=%v", tok, err)
	}

	// 3. Kubeconfig with client certs only (no bearer token) returns "" without error
	kcCertPath := filepath.Join(tmpDir, "kc-cert.yaml")
	kcCertContent := `apiVersion: v1
kind: Config
clusters:
- name: minikube
  cluster:
    server: https://127.0.0.1:8443
contexts:
- name: minikube
  context:
    cluster: minikube
    user: minikube
current-context: minikube
users:
- name: minikube
  user:
    client-certificate-data: ZmFrZS1jZXJ0
    client-key-data: ZmFrZS1rZXk=
`
	if err := os.WriteFile(kcCertPath, []byte(kcCertContent), 0600); err != nil {
		t.Fatalf("write cert kubeconfig failed: %v", err)
	}

	tok, err = resolveBearerToken("", kcCertPath, "")
	if err != nil || tok != "" {
		t.Fatalf("expected empty token for cert-only kubeconfig without error, got %q err=%v", tok, err)
	}
}

func TestRemoteClient_EndToEndAgainstServer(t *testing.T) {
	ctx := context.Background()
	fakeK8s := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-pod", Namespace: "default"},
	})
	fakeK8s.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		tr := action.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
		if tr.Spec.Token == "alice-token" {
			tr.Status = authenticationv1.TokenReviewStatus{
				Authenticated: true,
				User: authenticationv1.UserInfo{
					Username: "alice@company.com",
					UID:      "uid-alice",
					Groups:   []string{"sre"},
				},
			}
		}
		return true, tr, nil
	})
	fakeK8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: true}
		return true, sar, nil
	})

	llmMock := &scriptedLLM{
		fn: func(_ context.Context, _ llm.Request, callIdx int) (*llm.Response, error) {
			if callIdx == 0 {
				return &llm.Response{
					Message: llm.AssistantMessage(
						llm.ThinkingPart{Thought: "Listing pods"},
						llm.ToolCallPart{ID: "c1", Name: "k8s_list_pods", Arguments: `{"namespace":"default"}`},
					),
					Usage: llm.Usage{TotalTokens: 10},
				}, nil
			}
			return &llm.Response{
				Message: llm.AssistantMessage(llm.TextPart{Text: "api-pod is running."}),
				Usage:   llm.Usage{TotalTokens: 20},
			}, nil
		},
	}

	rt, err := runtime.New(ctx, runtime.Config{
		K8sClient:   fakeK8s,
		LLMClient:   llmMock,
		EnforceRBAC: true,
	})
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	authn, err := auth.NewTokenReviewAuthenticator(auth.TokenReviewConfig{
		Client:    fakeK8s,
		ClusterID: "prod-us",
	})
	if err != nil {
		t.Fatalf("NewTokenReviewAuthenticator failed: %v", err)
	}

	store, _ := session.NewFileStore(t.TempDir())
	sm := session.NewSessionManager(store)
	srv, err := server.New(server.Config{
		Runtime:        rt,
		SessionManager: sm,
		Authenticator:  authn,
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	httpCli := &inMemHandlerClient{handler: srv.Handler()}

	// Unauthenticated client receives helpful hint
	unauth := newRemoteClient("http://agentmatic.local", "", httpCli)
	if _, err := unauth.ListSessions(ctx); err == nil || !strings.Contains(err.Error(), "--token") {
		t.Errorf("expected unauthenticated hint error, got %v", err)
	}

	// Authenticated client full lifecycle + Execute streaming + OneShot
	cli, err := buildAgentClient(ctx, cliConfig{
		ServerURL: "http://agentmatic.local",
		Token:     "alice-token",
	}, httpCli)
	if err != nil {
		t.Fatalf("buildAgentClient failed: %v", err)
	}

	created, err := cli.CreateSession(ctx, "pod-check", "Pod Check")
	if err != nil || created.Name != "pod-check" {
		t.Fatalf("CreateSession failed: got=%+v err=%v", created, err)
	}

	var sawThought, sawCall, sawResult bool
	outcome, err := cli.Execute(ctx, created.ID, "check pods", StreamCallbacks{
		OnThinking:   func(_ int32, _ string) { sawThought = true },
		OnToolCall:   func(_ int32, name, _ string) { sawCall = name == "k8s_list_pods" },
		OnToolResult: func(_ int32, _ string, res string, isErr bool) { sawResult = !isErr && strings.Contains(res, "api-pod") },
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !sawThought || !sawCall || !sawResult {
		t.Errorf("missing streaming callbacks: thought=%v call=%v result=%v", sawThought, sawCall, sawResult)
	}
	if outcome.Answer != "api-pod is running." || outcome.SessionID != created.ID {
		t.Errorf("unexpected outcome: %+v", outcome)
	}

	// Rename & Restart & Get by name
	renamed, err := cli.RenameSession(ctx, created.ID, "Updated Title", "")
	if err != nil || renamed.Title != "Updated Title" {
		t.Fatalf("RenameSession failed: got=%+v err=%v", renamed, err)
	}
	byName, err := cli.GetSession(ctx, "pod-check")
	if err != nil || byName.ID != created.ID {
		t.Fatalf("GetSession by name failed: got=%+v err=%v", byName, err)
	}
	restarted, err := cli.RestartSession(ctx, created.ID)
	if err != nil || restarted.TurnCount != 0 {
		t.Fatalf("RestartSession failed: got=%+v err=%v", restarted, err)
	}
}

func TestLocalClient_ExecuteAndOneShot(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.New(ctx, runtime.Config{
		K8sClient: fake.NewSimpleClientset(),
		LLMClient: &scriptedLLM{
			fn: func(_ context.Context, _ llm.Request, callIdx int) (*llm.Response, error) {
				if callIdx == 0 {
					return &llm.Response{
						Message: llm.AssistantMessage(
							llm.ThinkingPart{Thought: "Thinking locally"},
							llm.ToolCallPart{ID: "1", Name: "k8s_list_pods", Arguments: `{}`},
						),
					}, nil
				}
				return &llm.Response{
					Message: llm.AssistantMessage(llm.TextPart{Text: "Local answer."}),
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	store, _ := session.NewFileStore(t.TempDir())
	sm := session.NewSessionManager(store)
	localCli := newLocalClient(rt, sm, session.LocalDevIdentity())

	if err := runOneShot(ctx, localCli, "", "Check local pods"); err != nil {
		t.Fatalf("runOneShot failed: %v", err)
	}

	list, err := localCli.ListSessions(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 persisted local session, got %v err=%v", list, err)
	}
	if list[0].Title != "Check local pods" {
		t.Errorf("expected auto-title 'Check local pods', got %q", list[0].Title)
	}
	if _, err := json.Marshal(list[0]); err != nil {
		t.Errorf("marshal SessionInfo failed: %v", err)
	}
}
