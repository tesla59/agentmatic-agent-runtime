package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/jsonschema-go/jsonschema"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1/agentmaticv1connect"
	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
	"github.com/tesla59/agentmatic-agent-runtime/internal/tools"
)

// streamingHandlerClient adapts an http.Handler to connect.HTTPClient over an in-memory io.Pipe
// so streaming tests (including real-time cancellation) run without binding TCP ports.
type streamingHandlerClient struct {
	handler http.Handler
}

func (c *streamingHandlerClient) Do(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	rw := newPipeResponseWriter(pw)

	go func() {
		defer func() {
			rw.sendHeaders()
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

type pipeResponseWriter struct {
	header       http.Header
	pw           *io.PipeWriter
	status       int
	once         sync.Once
	headersReady chan struct{}
}

func newPipeResponseWriter(pw *io.PipeWriter) *pipeResponseWriter {
	return &pipeResponseWriter{
		header:       make(http.Header),
		pw:           pw,
		status:       http.StatusOK,
		headersReady: make(chan struct{}),
	}
}

func (w *pipeResponseWriter) Header() http.Header { return w.header }

func (w *pipeResponseWriter) WriteHeader(code int) {
	w.status = code
	w.sendHeaders()
}

func (w *pipeResponseWriter) Write(p []byte) (int, error) {
	w.sendHeaders()
	return w.pw.Write(p)
}

func (w *pipeResponseWriter) Flush() { w.sendHeaders() }

func (w *pipeResponseWriter) sendHeaders() {
	w.once.Do(func() {
		close(w.headersReady)
	})
}

type mockTool struct {
	fn func(ctx context.Context, args json.RawMessage) (string, error)
}

func (m *mockTool) Name() string                   { return "k8s_list_pods" }
func (m *mockTool) Description() string            { return "list pods" }
func (m *mockTool) Parameters() *jsonschema.Schema { return &jsonschema.Schema{} }
func (m *mockTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return m.fn(ctx, args)
}

type scriptedLLM struct {
	mu      sync.Mutex
	handler func(ctx context.Context, req llm.Request, callIdx int) (*llm.Response, error)
	calls   int
}

func (s *scriptedLLM) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	s.mu.Lock()
	idx := s.calls
	s.calls++
	h := s.handler
	s.mu.Unlock()
	return h(ctx, req, idx)
}

func (s *scriptedLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

func setupTestServer(t *testing.T, llmClient llm.Client, reg *tools.Registry) (agentmaticv1connect.SessionServiceClient, agentmaticv1connect.RuntimeServiceClient, *session.SessionManager) {
	t.Helper()

	alice := session.Identity{ClusterID: "prod", UserUID: "uid-alice", Username: "alice@company.com"}
	bob := session.Identity{ClusterID: "prod", UserUID: "uid-bob", Username: "bob@company.com"}

	authn := &stubAuthn{
		fn: func(_ context.Context, h http.Header) (session.Identity, error) {
			switch h.Get("Authorization") {
			case "Bearer alice":
				return alice, nil
			case "Bearer bob":
				return bob, nil
			default:
				return session.Identity{}, auth.ErrUnauthenticated
			}
		},
	}

	interceptor, err := NewAuthInterceptor(authn)
	if err != nil {
		t.Fatalf("NewAuthInterceptor failed: %v", err)
	}

	store, err := session.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	sm := session.NewSessionManager(store)

	eng, err := engine.New(engine.Config{
		Client:   llmClient,
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("engine.New failed: %v", err)
	}

	sessSvc, err := NewSessionService(sm, eng)
	if err != nil {
		t.Fatalf("NewSessionService failed: %v", err)
	}
	rtSvc, err := NewRuntimeService(sm, eng)
	if err != nil {
		t.Fatalf("NewRuntimeService failed: %v", err)
	}

	mux := http.NewServeMux()
	sp, sh := agentmaticv1connect.NewSessionServiceHandler(sessSvc, connect.WithInterceptors(interceptor))
	rp, rh := agentmaticv1connect.NewRuntimeServiceHandler(rtSvc, connect.WithInterceptors(interceptor))
	mux.Handle(sp, sh)
	mux.Handle(rp, rh)

	client := &streamingHandlerClient{handler: mux}
	return agentmaticv1connect.NewSessionServiceClient(client, "http://agentmatic.local"),
		agentmaticv1connect.NewRuntimeServiceClient(client, "http://agentmatic.local"),
		sm
}

func reqWithToken[T any](msg *T, token string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+token)
	return req
}

func TestSessionService_LifecycleAndIsolation(t *testing.T) {
	sessClient, _, _ := setupTestServer(t, &scriptedLLM{
		handler: func(_ context.Context, _ llm.Request, _ int) (*llm.Response, error) {
			return &llm.Response{Message: llm.AssistantMessage(llm.TextPart{Text: "ok"})}, nil
		},
	}, tools.NewRegistry())
	ctx := context.Background()

	// Create session as Alice
	name := "api-502"
	title := "API 502 Errors"
	created, err := sessClient.CreateSession(ctx, reqWithToken(&agentmaticv1.CreateSessionRequest{
		Name:  &name,
		Title: &title,
	}, "alice"))
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if created.Msg.GetName() != "api-502" || created.Msg.Title != "API 502 Errors" {
		t.Errorf("unexpected created session: %+v", created.Msg)
	}

	// Duplicate slug name for Alice -> CodeAlreadyExists
	_, err = sessClient.CreateSession(ctx, reqWithToken(&agentmaticv1.CreateSessionRequest{Name: &name}, "alice"))
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("expected CodeAlreadyExists, got %v", connect.CodeOf(err))
	}

	// Get by name as Alice
	got, err := sessClient.GetSession(ctx, reqWithToken(&agentmaticv1.GetSessionRequest{
		Identifier: &agentmaticv1.GetSessionRequest_Name{Name: "api-502"},
	}, "alice"))
	if err != nil || got.Msg.Id != created.Msg.Id {
		t.Fatalf("GetSession by name failed: got=%+v err=%v", got, err)
	}

	// Bob cannot view or delete Alice's session
	_, err = sessClient.GetSession(ctx, reqWithToken(&agentmaticv1.GetSessionRequest{
		Identifier: &agentmaticv1.GetSessionRequest_Id{Id: created.Msg.Id},
	}, "bob"))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("expected CodeNotFound for Bob reading Alice's session, got %v", connect.CodeOf(err))
	}

	// ListSessions returns 1 for Alice, 0 for Bob
	aliceList, err := sessClient.ListSessions(ctx, reqWithToken(&agentmaticv1.ListSessionsRequest{}, "alice"))
	if err != nil || len(aliceList.Msg.Sessions) != 1 {
		t.Fatalf("expected 1 session for alice, got %v err=%v", aliceList, err)
	}
	bobList, err := sessClient.ListSessions(ctx, reqWithToken(&agentmaticv1.ListSessionsRequest{}, "bob"))
	if err != nil || len(bobList.Msg.Sessions) != 0 {
		t.Fatalf("expected 0 sessions for bob, got %v err=%v", bobList, err)
	}

	// RenameSession updates title & slug
	newTitle := "API 502 Resolved"
	newSlug := "api-502-fixed"
	renamed, err := sessClient.RenameSession(ctx, reqWithToken(&agentmaticv1.RenameSessionRequest{
		Identifier: &agentmaticv1.RenameSessionRequest_Id{Id: created.Msg.Id},
		NewTitle:   &newTitle,
		NewName:    &newSlug,
	}, "alice"))
	if err != nil || renamed.Msg.Title != newTitle || renamed.Msg.GetName() != newSlug {
		t.Fatalf("RenameSession failed: got=%+v err=%v", renamed, err)
	}

	// Missing identifier -> CodeInvalidArgument
	_, err = sessClient.GetSession(ctx, reqWithToken(&agentmaticv1.GetSessionRequest{}, "alice"))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument for empty identifier, got %v", connect.CodeOf(err))
	}

	// RestartSession & DeleteSession by new slug
	restarted, err := sessClient.RestartSession(ctx, reqWithToken(&agentmaticv1.RestartSessionRequest{
		Identifier: &agentmaticv1.RestartSessionRequest_Name{Name: newSlug},
	}, "alice"))
	if err != nil || restarted.Msg.TurnCount != 0 {
		t.Fatalf("RestartSession failed: got=%+v err=%v", restarted, err)
	}

	delResp, err := sessClient.DeleteSession(ctx, reqWithToken(&agentmaticv1.DeleteSessionRequest{
		Identifier: &agentmaticv1.DeleteSessionRequest_Name{Name: newSlug},
	}, "alice"))
	if err != nil || !delResp.Msg.Deleted {
		t.Fatalf("DeleteSession failed: got=%+v err=%v", delResp, err)
	}
}

func TestRuntimeService_ExecuteStreamAndMultiTurn(t *testing.T) {
	reg := tools.NewRegistry()
	_ = reg.Register(&mockTool{
		fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"pods":["web-1"]}`, nil
		},
	})

	llmMock := &scriptedLLM{
		handler: func(_ context.Context, _ llm.Request, callIdx int) (*llm.Response, error) {
			switch callIdx {
			case 0:
				return &llm.Response{
					Message: llm.AssistantMessage(
						llm.ThinkingPart{Thought: "Inspecting pods in prod"},
						llm.ToolCallPart{ID: "call-1", Name: "k8s_list_pods", Arguments: `{"namespace":"prod"}`},
					),
					Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}, nil
			case 1:
				return &llm.Response{
					Message: llm.AssistantMessage(llm.TextPart{Text: "Pod web-1 is running."}),
					Usage:   llm.Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
				}, nil
			default:
				return &llm.Response{
					Message: llm.AssistantMessage(llm.TextPart{Text: "Follow-up confirmed."}),
					Usage:   llm.Usage{PromptTokens: 30, CompletionTokens: 5, TotalTokens: 35},
				}, nil
			}
		},
	}

	sessClient, rtClient, _ := setupTestServer(t, llmMock, reg)
	ctx := context.Background()

	// 1. Execute without session_target -> auto-creates session and streams thinking, call, result, answer
	stream, err := rtClient.Execute(ctx, reqWithToken(&agentmaticv1.ExecuteRequest{
		Prompt: "Check pods in prod",
	}, "alice"))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var events []*agentmaticv1.ExecutionEvent
	for stream.Receive() {
		events = append(events, stream.Msg())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	_ = stream.Close()

	sessionID := stream.ResponseHeader().Get(SessionHeaderKey)
	if sessionID == "" {
		t.Fatalf("expected %s response header", SessionHeaderKey)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 streaming events (thinking, tool_call, tool_result, final_answer), got %d", len(events))
	}
	if events[0].GetThinking().GetThought() != "Inspecting pods in prod" {
		t.Errorf("unexpected event 0: %+v", events[0])
	}
	if events[1].GetToolCall().GetArguments().AsMap()["namespace"] != "prod" {
		t.Errorf("unexpected event 1 args: %+v", events[1])
	}
	if events[2].GetToolResult().GetResult() == nil || events[2].GetToolResult().GetIsError() {
		t.Errorf("unexpected event 2 result: %+v", events[2])
	}
	fa := events[3].GetFinalAnswer()
	if fa == nil || fa.Answer != "Pod web-1 is running." || fa.Usage.TotalTokens != 40 {
		t.Errorf("unexpected final answer event: %+v", events[3])
	}

	// 2. Follow-up turn on same session ID
	stream2, err := rtClient.Execute(ctx, reqWithToken(&agentmaticv1.ExecuteRequest{
		SessionTarget: &agentmaticv1.ExecuteRequest_SessionId{SessionId: sessionID},
		Prompt:        "Thanks, confirm status",
	}, "alice"))
	if err != nil {
		t.Fatalf("follow-up Execute failed: %v", err)
	}
	for stream2.Receive() {
	}
	if err := stream2.Err(); err != nil {
		t.Fatalf("follow-up stream error: %v", err)
	}
	_ = stream2.Close()

	// Verify auto-title and 2 persisted turns via SessionService
	details, err := sessClient.GetSession(ctx, reqWithToken(&agentmaticv1.GetSessionRequest{
		Identifier: &agentmaticv1.GetSessionRequest_Id{Id: sessionID},
	}, "alice"))
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if details.Msg.Title != "Check pods in prod" {
		t.Errorf("expected auto-title 'Check pods in prod', got %q", details.Msg.Title)
	}
	if details.Msg.TurnCount != 2 {
		t.Fatalf("expected 2 persisted turns, got %d", details.Msg.TurnCount)
	}
}

func TestRuntimeService_ConcurrencyLockAndCancel(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	llmMock := &scriptedLLM{
		handler: func(ctx context.Context, _ llm.Request, _ int) (*llm.Response, error) {
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return &llm.Response{Message: llm.AssistantMessage(llm.TextPart{Text: "done"})}, nil
			}
		},
	}

	sessClient, rtClient, _ := setupTestServer(t, llmMock, tools.NewRegistry())
	ctx := context.Background()

	sessResp, err := sessClient.CreateSession(ctx, reqWithToken(&agentmaticv1.CreateSessionRequest{}, "alice"))
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	sessID := sessResp.Msg.Id

	// Start long-running Execute in background
	streamDone := make(chan error, 1)
	go func() {
		stream, err := rtClient.Execute(ctx, reqWithToken(&agentmaticv1.ExecuteRequest{
			SessionTarget: &agentmaticv1.ExecuteRequest_SessionId{SessionId: sessID},
			Prompt:        "Long running task",
		}, "alice"))
		if err != nil {
			streamDone <- err
			return
		}
		defer stream.Close()
		for stream.Receive() {
		}
		streamDone <- stream.Err()
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for execution to start")
	}

	// Concurrent Execute on the same session must fail with CodeFailedPrecondition
	dupStream, err := rtClient.Execute(ctx, reqWithToken(&agentmaticv1.ExecuteRequest{
		SessionTarget: &agentmaticv1.ExecuteRequest_SessionId{SessionId: sessID},
		Prompt:        "Concurrent prompt",
	}, "alice"))
	if err == nil {
		for dupStream.Receive() {
		}
		err = dupStream.Err()
		_ = dupStream.Close()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("expected CodeFailedPrecondition for concurrent execution on same session, got %v (%v)", connect.CodeOf(err), err)
	}

	// Bob cannot cancel Alice's session
	_, err = rtClient.CancelExecution(ctx, reqWithToken(&agentmaticv1.CancelExecutionRequest{
		Identifier: &agentmaticv1.CancelExecutionRequest_Id{Id: sessID},
	}, "bob"))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("expected CodeNotFound for Bob cancelling Alice's session, got %v", connect.CodeOf(err))
	}

	// Alice cancels her execution
	cancelResp, err := rtClient.CancelExecution(ctx, reqWithToken(&agentmaticv1.CancelExecutionRequest{
		Identifier: &agentmaticv1.CancelExecutionRequest_Id{Id: sessID},
	}, "alice"))
	if err != nil || !cancelResp.Msg.Cancelled {
		t.Fatalf("CancelExecution failed: resp=%+v err=%v", cancelResp, err)
	}

	select {
	case err := <-streamDone:
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("expected CodeCanceled after cancellation, got %v (%v)", connect.CodeOf(err), err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled stream to exit")
	}

	close(release)
}
