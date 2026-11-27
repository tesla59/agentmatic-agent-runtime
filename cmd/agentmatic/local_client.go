package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

type localClient struct {
	rt    *runtime.Runtime
	sm    *session.SessionManager
	owner session.Identity

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newLocalClient(rt *runtime.Runtime, sm *session.SessionManager, owner session.Identity) *localClient {
	return &localClient{
		rt:      rt,
		sm:      sm,
		owner:   owner,
		cancels: make(map[string]context.CancelFunc),
	}
}

func (c *localClient) CreateSession(ctx context.Context, name, title string) (SessionInfo, error) {
	sess, err := session.New(session.Config{
		Name:   name,
		Title:  title,
		Owner:  c.owner,
		Engine: c.rt.Engine,
	})
	if err != nil {
		return SessionInfo{}, err
	}
	if err := c.sm.Register(ctx, sess); err != nil {
		return SessionInfo{}, err
	}
	return sessionInfoFromLocal(sess), nil
}

func (c *localClient) GetSession(_ context.Context, identifier string) (SessionInfo, error) {
	sess, ok := c.sm.Resolve(c.owner, identifier)
	if !ok {
		return SessionInfo{}, session.ErrSessionNotFound
	}
	return sessionInfoFromLocal(sess), nil
}

func (c *localClient) ListSessions(_ context.Context) ([]SessionInfo, error) {
	list := c.sm.List(c.owner)
	out := make([]SessionInfo, 0, len(list))
	for _, s := range list {
		out = append(out, sessionInfoFromLocal(s))
	}
	return out, nil
}

func (c *localClient) RenameSession(ctx context.Context, identifier, newTitle, newName string) (SessionInfo, error) {
	updated, err := c.sm.UpdateMetadata(ctx, c.owner, identifier, newTitle, newName)
	if err != nil {
		return SessionInfo{}, err
	}
	return sessionInfoFromLocal(updated), nil
}

func (c *localClient) RestartSession(ctx context.Context, identifier string) (SessionInfo, error) {
	sess, ok := c.sm.Resolve(c.owner, identifier)
	if !ok {
		return SessionInfo{}, session.ErrSessionNotFound
	}
	if err := sess.Restart(); err != nil {
		return SessionInfo{}, err
	}
	if err := c.sm.Save(ctx, sess); err != nil {
		return SessionInfo{}, err
	}
	return sessionInfoFromLocal(sess), nil
}

func (c *localClient) Execute(ctx context.Context, sessionID, prompt string, cb StreamCallbacks) (ExecuteOutcome, error) {
	var sess *session.Session
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		found, ok := c.sm.Resolve(c.owner, trimmed)
		if !ok {
			return ExecuteOutcome{}, session.ErrSessionNotFound
		}
		sess = found
	} else {
		created, err := session.New(session.Config{
			Owner:  c.owner,
			Engine: c.rt.Engine,
		})
		if err != nil {
			return ExecuteOutcome{}, err
		}
		if err := c.sm.Register(ctx, created); err != nil {
			return ExecuteOutcome{}, err
		}
		sess = created
	}

	execCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancels[sess.ID()] = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.cancels, sess.ID())
		c.mu.Unlock()
		cancel()
	}()

	listenerCtx := engine.WithStepListener(execCtx, func(step engine.Step) {
		stepNum := int32(step.Number)
		if step.Thinking != "" && cb.OnThinking != nil {
			cb.OnThinking(stepNum, step.Thinking)
		}
		if step.Action != nil && cb.OnToolCall != nil {
			cb.OnToolCall(stepNum, step.Action.Name, step.Action.Arguments)
		}
		if (step.Action != nil || step.Observation != "") && cb.OnToolResult != nil {
			toolName := ""
			if step.Action != nil {
				toolName = step.Action.Name
			}
			cb.OnToolResult(stepNum, toolName, step.Observation, step.IsError)
		}
	})

	res, err := sess.Execute(listenerCtx, prompt)
	if err != nil {
		return ExecuteOutcome{SessionID: sess.ID()}, err
	}

	if sess.Title() == "New Session" {
		runes := []rune(strings.TrimSpace(prompt))
		autoTitle := string(runes)
		if len(runes) > 35 {
			autoTitle = string(runes[:35]) + "..."
		}
		_ = sess.Rename(autoTitle)
	}
	if err := c.sm.Save(context.WithoutCancel(ctx), sess); err != nil {
		return ExecuteOutcome{SessionID: sess.ID()}, err
	}

	return ExecuteOutcome{
		SessionID: sess.ID(),
		Answer:    res.Answer,
		Steps:     len(res.Steps),
		Usage:     res.Usage,
	}, nil
}

func (c *localClient) CancelExecution(_ context.Context, sessionID string) error {
	c.mu.Lock()
	cancel, ok := c.cancels[strings.TrimSpace(sessionID)]
	c.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

func sessionInfoFromLocal(s *session.Session) SessionInfo {
	snap := s.Snapshot()
	turns := 0
	for _, msg := range snap.History {
		if msg.Role == "user" {
			turns++
		}
	}
	o := snap.Owner
	return SessionInfo{
		ID:          snap.ID,
		Name:        snap.Name,
		Title:       snap.Title,
		Status:      string(snap.Status),
		OwnerLabel:  fmt.Sprintf("%s (%s/%s)", o.Username, o.ClusterID, o.UserUID),
		TurnCount:   turns,
		TotalTokens: snap.TotalUsage.TotalTokens,
		UpdatedAt:   snap.UpdatedAt,
	}
}
