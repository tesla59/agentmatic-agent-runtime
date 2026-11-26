package server

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

// SessionService implements agentmaticv1connect.SessionServiceHandler backed by SessionManager.
type SessionService struct {
	sm  *session.SessionManager
	eng *engine.Engine
}

// NewSessionService creates a ConnectRPC handler for session lifecycle operations.
func NewSessionService(sm *session.SessionManager, eng *engine.Engine) (*SessionService, error) {
	if sm == nil {
		return nil, errors.New("session manager is required")
	}
	if eng == nil {
		return nil, errors.New("engine is required")
	}
	return &SessionService{
		sm:  sm,
		eng: eng,
	}, nil
}

// CreateSession initializes a new session owned by the authenticated caller.
func (s *SessionService) CreateSession(ctx context.Context, req *connect.Request[agentmaticv1.CreateSessionRequest]) (*connect.Response[agentmaticv1.SessionDetails], error) {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}

	sess, err := session.New(session.Config{
		Name:   req.Msg.GetName(),
		Title:  req.Msg.GetTitle(),
		Owner:  owner,
		Engine: s.eng,
	})
	if err != nil {
		return nil, mapError(err)
	}

	if err := s.sm.Register(ctx, sess); err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(sessionDetailsToProto(sess)), nil
}

// GetSession returns full details and turn history for a session owned by the caller.
func (s *SessionService) GetSession(ctx context.Context, req *connect.Request[agentmaticv1.GetSessionRequest]) (*connect.Response[agentmaticv1.SessionDetails], error) {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}

	target, err := extractIdentifier(req.Msg)
	if err != nil {
		return nil, err
	}

	sess, ok := s.sm.Resolve(owner, target)
	if !ok {
		return nil, mapError(session.ErrSessionNotFound)
	}

	return connect.NewResponse(sessionDetailsToProto(sess)), nil
}

// ListSessions returns summaries of all sessions owned by the authenticated caller.
func (s *SessionService) ListSessions(ctx context.Context, _ *connect.Request[agentmaticv1.ListSessionsRequest]) (*connect.Response[agentmaticv1.ListSessionsResponse], error) {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}

	sessions := s.sm.List(owner)
	summaries := make([]*agentmaticv1.SessionSummary, 0, len(sessions))
	for _, sess := range sessions {
		summaries = append(summaries, sessionSummaryToProto(sess))
	}

	return connect.NewResponse(&agentmaticv1.ListSessionsResponse{
		Sessions: summaries,
	}), nil
}

// DeleteSession permanently deletes a session owned by the caller.
func (s *SessionService) DeleteSession(ctx context.Context, req *connect.Request[agentmaticv1.DeleteSessionRequest]) (*connect.Response[agentmaticv1.DeleteSessionResponse], error) {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}

	target, err := extractIdentifier(req.Msg)
	if err != nil {
		return nil, err
	}

	sess, ok := s.sm.Resolve(owner, target)
	if !ok {
		return nil, mapError(session.ErrSessionNotFound)
	}

	if err := s.sm.Delete(ctx, owner, sess.ID()); err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&agentmaticv1.DeleteSessionResponse{Deleted: true}), nil
}

// RenameSession updates the display title and/or unique slug name of a session owned by the caller.
func (s *SessionService) RenameSession(ctx context.Context, req *connect.Request[agentmaticv1.RenameSessionRequest]) (*connect.Response[agentmaticv1.SessionDetails], error) {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}

	target, err := extractIdentifier(req.Msg)
	if err != nil {
		return nil, err
	}

	newTitle := strings.TrimSpace(req.Msg.GetNewTitle())
	newName := strings.TrimSpace(req.Msg.GetNewName())
	if newTitle == "" && newName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one of new_title or new_name is required"))
	}

	updated, err := s.sm.UpdateMetadata(ctx, owner, target, newTitle, newName)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(sessionDetailsToProto(updated)), nil
}

// RestartSession clears conversation history on a session owned by the caller while preserving identity.
func (s *SessionService) RestartSession(ctx context.Context, req *connect.Request[agentmaticv1.RestartSessionRequest]) (*connect.Response[agentmaticv1.SessionDetails], error) {
	owner, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}

	target, err := extractIdentifier(req.Msg)
	if err != nil {
		return nil, err
	}

	sess, ok := s.sm.Resolve(owner, target)
	if !ok {
		return nil, mapError(session.ErrSessionNotFound)
	}

	if err := sess.Restart(); err != nil {
		return nil, mapError(err)
	}
	if err := s.sm.Save(ctx, sess); err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(sessionDetailsToProto(sess)), nil
}

type sessionIdentifier interface {
	GetId() string
	GetName() string
}

func extractIdentifier(req sessionIdentifier) (string, error) {
	if id := strings.TrimSpace(req.GetId()); id != "" {
		return id, nil
	}
	if name := strings.TrimSpace(req.GetName()); name != "" {
		return name, nil
	}
	return "", connect.NewError(connect.CodeInvalidArgument, errors.New("session id or name is required"))
}

func requireIdentity(ctx context.Context) (session.Identity, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return session.Identity{}, connect.NewError(connect.CodeUnauthenticated, auth.ErrUnauthenticated)
	}
	return id, nil
}
