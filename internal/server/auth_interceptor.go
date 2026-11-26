package server

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
)

type authInterceptor struct {
	authn auth.Authenticator
}

// NewAuthInterceptor creates a ConnectRPC interceptor that authenticates unary and
// streaming requests using authn and attaches the verified session.Identity to ctx.
func NewAuthInterceptor(authn auth.Authenticator) (connect.Interceptor, error) {
	if authn == nil {
		return nil, errors.New("authenticator is required")
	}
	return &authInterceptor{authn: authn}, nil
}

func (i *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			return next(ctx, req)
		}
		authedCtx, err := i.authenticateContext(ctx, req.Header())
		if err != nil {
			return nil, err
		}
		return next(authedCtx, req)
	}
}

func (i *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		authedCtx, err := i.authenticateContext(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(authedCtx, conn)
	}
}

func (i *authInterceptor) authenticateContext(ctx context.Context, headers http.Header) (context.Context, error) {
	id, err := i.authn.Authenticate(ctx, headers)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, connect.NewError(connect.CodeCanceled, err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, connect.NewError(connect.CodeDeadlineExceeded, err)
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication service error"))
	}
	return auth.WithIdentity(ctx, id), nil
}
