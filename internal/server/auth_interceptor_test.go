package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1/agentmaticv1connect"
	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

type handlerClient struct {
	handler http.Handler
}

func (c *handlerClient) Do(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

type stubAuthn struct {
	fn func(ctx context.Context, headers http.Header) (session.Identity, error)
}

func (s *stubAuthn) Authenticate(ctx context.Context, headers http.Header) (session.Identity, error) {
	return s.fn(ctx, headers)
}

type stubHandlers struct {
	agentmaticv1connect.UnimplementedSessionServiceHandler
	agentmaticv1connect.UnimplementedRuntimeServiceHandler
	seenUnaryIdentity  session.Identity
	seenStreamIdentity session.Identity
}

func (h *stubHandlers) ListSessions(ctx context.Context, _ *connect.Request[agentmaticv1.ListSessionsRequest]) (*connect.Response[agentmaticv1.ListSessionsResponse], error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("missing identity in unary ctx"))
	}
	h.seenUnaryIdentity = id
	return connect.NewResponse(&agentmaticv1.ListSessionsResponse{}), nil
}

func (h *stubHandlers) Execute(ctx context.Context, _ *connect.Request[agentmaticv1.ExecuteRequest], stream *connect.ServerStream[agentmaticv1.ExecutionEvent]) error {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeInternal, errors.New("missing identity in stream ctx"))
	}
	h.seenStreamIdentity = id
	return stream.Send(&agentmaticv1.ExecutionEvent{StepIndex: 1})
}

func TestAuthInterceptor_UnaryAndStreaming(t *testing.T) {
	if _, err := NewAuthInterceptor(nil); err == nil {
		t.Fatal("expected error when authenticator is nil")
	}

	alice := session.Identity{
		ClusterID: "prod-us",
		UserUID:   "uid-alice",
		Username:  "alice@company.com",
		Groups:    []string{"sre"},
	}

	authn := &stubAuthn{
		fn: func(_ context.Context, h http.Header) (session.Identity, error) {
			switch h.Get("Authorization") {
			case "Bearer good-token":
				return alice, nil
			case "Bearer api-failure":
				return session.Identity{}, errors.New("apiserver down")
			default:
				return session.Identity{}, auth.ErrUnauthenticated
			}
		},
	}

	interceptor, err := NewAuthInterceptor(authn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h := &stubHandlers{}
	mux := http.NewServeMux()
	sp, sh := agentmaticv1connect.NewSessionServiceHandler(h, connect.WithInterceptors(interceptor))
	rp, rh := agentmaticv1connect.NewRuntimeServiceHandler(h, connect.WithInterceptors(interceptor))
	mux.Handle(sp, sh)
	mux.Handle(rp, rh)

	httpClient := &handlerClient{handler: mux}
	sessClient := agentmaticv1connect.NewSessionServiceClient(httpClient, "http://agentmatic.local")
	rtClient := agentmaticv1connect.NewRuntimeServiceClient(httpClient, "http://agentmatic.local")

	// 1. Unary missing token -> CodeUnauthenticated
	_, err = sessClient.ListSessions(context.Background(), connect.NewRequest(&agentmaticv1.ListSessionsRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated on unary, got %v (%v)", connect.CodeOf(err), err)
	}

	// 2. Unary infra error -> CodeInternal without leaking backend detail
	badReq := connect.NewRequest(&agentmaticv1.ListSessionsRequest{})
	badReq.Header().Set("Authorization", "Bearer api-failure")
	_, err = sessClient.ListSessions(context.Background(), badReq)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Errorf("expected CodeInternal on auth infra failure, got %v", connect.CodeOf(err))
	}

	// 3. Unary valid token -> handler receives verified identity
	goodUnaryReq := connect.NewRequest(&agentmaticv1.ListSessionsRequest{})
	goodUnaryReq.Header().Set("Authorization", "Bearer good-token")
	if _, err := sessClient.ListSessions(context.Background(), goodUnaryReq); err != nil {
		t.Fatalf("unary call failed: %v", err)
	}
	if !h.seenUnaryIdentity.SameOwner(alice) {
		t.Errorf("expected alice identity in unary handler, got %+v", h.seenUnaryIdentity)
	}

	// 4. Streaming missing token -> CodeUnauthenticated
	streamErrResp, err := rtClient.Execute(context.Background(), connect.NewRequest(&agentmaticv1.ExecuteRequest{Prompt: "hi"}))
	if err == nil {
		if streamErrResp.Receive() {
			t.Fatal("expected stream to fail auth")
		}
		err = streamErrResp.Err()
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("expected CodeUnauthenticated on stream, got %v (%v)", connect.CodeOf(err), err)
	}

	// 5. Streaming valid token -> handler receives verified identity
	goodStreamReq := connect.NewRequest(&agentmaticv1.ExecuteRequest{Prompt: "hi"})
	goodStreamReq.Header().Set("Authorization", "Bearer good-token")
	stream, err := rtClient.Execute(context.Background(), goodStreamReq)
	if err != nil {
		t.Fatalf("stream init failed: %v", err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatalf("expected event on stream, err=%v", stream.Err())
	}
	if !h.seenStreamIdentity.SameOwner(alice) {
		t.Errorf("expected alice identity in stream handler, got %+v", h.seenStreamIdentity)
	}
}
