package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

// SessionHeaderKey carries the resolved or newly created session UUID on Execute response headers.
const SessionHeaderKey = "X-Agentmatic-Session-Id"

type activeExecution struct {
	owner  session.Identity
	cancel context.CancelFunc
}

// RuntimeService implements agentmaticv1connect.RuntimeServiceHandler, streaming
// reasoning steps, tool invocations, and final answers over ConnectRPC.
type RuntimeService struct {
	sm  *session.SessionManager
	eng *engine.Engine

	mu     sync.Mutex
	active map[string]activeExecution
}

// NewRuntimeService creates a ConnectRPC handler for task execution and cancellation.
func NewRuntimeService(sm *session.SessionManager, eng *engine.Engine) (*RuntimeService, error) {
	if sm == nil {
		return nil, errors.New("session manager is required")
	}
	if eng == nil {
		return nil, errors.New("engine is required")
	}
	return &RuntimeService{
		sm:     sm,
		eng:    eng,
		active: make(map[string]activeExecution),
	}, nil
}

// Execute resolves or creates a session, executes the prompt through the reasoning engine,
// and streams ExecutionEvent frames to the client.
func (r *RuntimeService) Execute(ctx context.Context, req *connect.Request[agentmaticv1.ExecuteRequest], stream *connect.ServerStream[agentmaticv1.ExecutionEvent]) error {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return err
	}

	prompt := strings.TrimSpace(req.Msg.GetPrompt())
	if prompt == "" {
		return mapError(session.ErrEmptyPrompt)
	}

	sess, err := r.resolveOrCreateSession(ctx, owner, req.Msg)
	if err != nil {
		return err
	}

	execCtx, cancel, err := r.registerExecution(ctx, sess.ID(), owner)
	if err != nil {
		return err
	}
	defer r.unregisterExecution(sess.ID())
	defer cancel()

	// Expose resolved/created session ID in response headers before streaming any events.
	stream.ResponseHeader().Set(SessionHeaderKey, sess.ID())

	var streamMu sync.Mutex
	var sendErr error
	var lastStep int32

	listenerCtx := engine.WithStepListener(execCtx, func(step engine.Step) {
		nowMs := time.Now().UnixMilli()
		events := stepToExecutionEvents(step, nowMs)

		streamMu.Lock()
		defer streamMu.Unlock()

		if sendErr != nil {
			return
		}
		if int32(step.Number) > lastStep {
			lastStep = int32(step.Number)
		}
		for _, ev := range events {
			if err := stream.Send(ev); err != nil {
				sendErr = err
				cancel()
				return
			}
		}
	})

	res, execErr := sess.Execute(listenerCtx, prompt)
	if sendErr != nil {
		return mapError(sendErr)
	}
	if execErr != nil {
		return mapError(execErr)
	}

	if sess.Title() == "New Session" {
		_ = sess.Rename(buildAutoTitle(prompt))
	}
	if err := r.sm.Save(context.WithoutCancel(ctx), sess); err != nil {
		return mapError(err)
	}

	streamMu.Lock()
	defer streamMu.Unlock()
	if err := stream.Send(finalAnswerEvent(lastStep+1, res, time.Now().UnixMilli())); err != nil {
		return mapError(err)
	}

	return nil
}

// CancelExecution stops an active in-flight execution owned by the caller.
func (r *RuntimeService) CancelExecution(ctx context.Context, req *connect.Request[agentmaticv1.CancelExecutionRequest]) (*connect.Response[agentmaticv1.CancelExecutionResponse], error) {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}

	target, err := extractIdentifier(req.Msg)
	if err != nil {
		return nil, err
	}

	sess, ok := r.sm.Resolve(owner, target)
	if !ok {
		return nil, mapError(session.ErrSessionNotFound)
	}

	r.mu.Lock()
	active, exists := r.active[sess.ID()]
	r.mu.Unlock()

	if !exists || !active.owner.SameOwner(owner) {
		return connect.NewResponse(&agentmaticv1.CancelExecutionResponse{Cancelled: false}), nil
	}

	active.cancel()
	return connect.NewResponse(&agentmaticv1.CancelExecutionResponse{Cancelled: true}), nil
}

func (r *RuntimeService) resolveOrCreateSession(ctx context.Context, owner session.Identity, msg *agentmaticv1.ExecuteRequest) (*session.Session, error) {
	switch target := msg.SessionTarget.(type) {
	case *agentmaticv1.ExecuteRequest_SessionId:
		id := strings.TrimSpace(target.SessionId)
		if id == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_id cannot be empty"))
		}
		sess, ok := r.sm.Get(owner, id)
		if !ok {
			return nil, mapError(session.ErrSessionNotFound)
		}
		return sess, nil

	case *agentmaticv1.ExecuteRequest_SessionName:
		name := strings.TrimSpace(target.SessionName)
		if name == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session_name cannot be empty"))
		}
		sess, ok := r.sm.GetByName(owner, name)
		if !ok {
			return nil, mapError(session.ErrSessionNotFound)
		}
		return sess, nil

	default:
		sess, err := session.New(session.Config{
			Owner:  owner,
			Engine: r.eng,
		})
		if err != nil {
			return nil, mapError(err)
		}
		if err := r.sm.Register(ctx, sess); err != nil {
			return nil, mapError(err)
		}
		return sess, nil
	}
}

func (r *RuntimeService) registerExecution(ctx context.Context, sessionID string, owner session.Identity) (context.Context, context.CancelFunc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, running := r.active[sessionID]; running {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("session is already executing another turn"))
	}

	execCtx, cancel := context.WithCancel(ctx)
	r.active[sessionID] = activeExecution{
		owner:  owner,
		cancel: cancel,
	}
	return execCtx, cancel, nil
}

func (r *RuntimeService) unregisterExecution(sessionID string) {
	r.mu.Lock()
	delete(r.active, sessionID)
	r.mu.Unlock()
}

func buildAutoTitle(prompt string) string {
	runes := []rune(prompt)
	if len(runes) <= 35 {
		return prompt
	}
	return string(runes[:35]) + "..."
}
