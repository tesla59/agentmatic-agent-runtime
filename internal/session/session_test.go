package session

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/tools"
)

type mockLLM struct {
	responses []*llm.Response
	calls     []llm.Request
	index     int
}

func (m *mockLLM) Chat(_ context.Context, req llm.Request) (*llm.Response, error) {
	m.calls = append(m.calls, req)
	if m.index >= len(m.responses) {
		return nil, errors.New("no more mocked responses")
	}
	resp := m.responses[m.index]
	m.index++
	return resp, nil
}

func (m *mockLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

func createTestEngine(mock *mockLLM) *engine.Engine {
	reg := tools.NewRegistry()
	eng, _ := engine.New(engine.Config{
		Client:       mock,
		Registry:     reg,
		SystemPrompt: "Test System Prompt",
	})
	return eng
}

func TestNewSession(t *testing.T) {
	mock := &mockLLM{}
	eng := createTestEngine(mock)

	sess, err := New(Config{
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("unexpected error creating session: %v", err)
	}

	if sess.ID() == "" {
		t.Error("expected non-empty session ID")
	}
	if _, err := uuid.Parse(sess.ID()); err != nil {
		t.Errorf("expected valid UUID, got: %s", sess.ID())
	}
	if sess.Title() != "New Session" {
		t.Errorf("expected title 'New Session', got %q", sess.Title())
	}
	if sess.Status() != StatusActive {
		t.Errorf("expected status 'active', got %q", sess.Status())
	}

	hist := sess.History()
	if len(hist) != 1 {
		t.Fatalf("expected 1 initial system message, got %d", len(hist))
	}
	if hist[0].Role != llm.RoleSystem || hist[0].Text() != "Test System Prompt" {
		t.Errorf("unexpected initial message: %+v", hist[0])
	}
}

func TestSessionExecute_MultiTurn(t *testing.T) {
	mock := &mockLLM{
		responses: []*llm.Response{
			{
				Message: llm.AssistantMessage(llm.TextPart{Text: "Response to turn 1"}),
				Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
			{
				Message: llm.AssistantMessage(llm.TextPart{Text: "Response to turn 2"}),
				Usage:   llm.Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28},
			},
		},
	}
	eng := createTestEngine(mock)
	sess, err := New(Config{Engine: eng})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Turn 1
	res1, err := sess.Execute(context.Background(), "Hello turn 1")
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}
	if res1.Answer != "Response to turn 1" {
		t.Errorf("unexpected answer 1: %q", res1.Answer)
	}

	// Turn 2
	res2, err := sess.Execute(context.Background(), "Hello turn 2")
	if err != nil {
		t.Fatalf("turn 2 failed: %v", err)
	}
	if res2.Answer != "Response to turn 2" {
		t.Errorf("unexpected answer 2: %q", res2.Answer)
	}

	// Verify cumulative usage
	totalUsage := sess.TotalUsage()
	if totalUsage.TotalTokens != 43 { // 15 + 28
		t.Errorf("expected 43 total tokens, got %d", totalUsage.TotalTokens)
	}

	// Verify full multi-turn history preserved
	hist := sess.History()
	if len(hist) != 5 {
		t.Fatalf("expected 5 messages in history, got %d", len(hist))
	}
	if hist[1].Text() != "Hello turn 1" || hist[2].Text() != "Response to turn 1" {
		t.Errorf("turn 1 history corrupted: %+v, %+v", hist[1], hist[2])
	}
	if hist[3].Text() != "Hello turn 2" || hist[4].Text() != "Response to turn 2" {
		t.Errorf("turn 2 history corrupted: %+v, %+v", hist[3], hist[4])
	}
}

func TestSessionPauseResume(t *testing.T) {
	mock := &mockLLM{
		responses: []*llm.Response{
			{Message: llm.AssistantMessage(llm.TextPart{Text: "OK"})},
		},
	}
	eng := createTestEngine(mock)
	sess, err := New(Config{Engine: eng})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Pause
	if err := sess.Pause(); err != nil {
		t.Fatalf("failed to pause session: %v", err)
	}
	if sess.Status() != StatusPaused {
		t.Fatalf("expected status paused, got %v", sess.Status())
	}

	// Execute while paused must fail
	_, err = sess.Execute(context.Background(), "Will fail")
	if !errors.Is(err, ErrSessionPaused) {
		t.Fatalf("expected ErrSessionPaused, got %v", err)
	}

	// Resume
	if err := sess.Resume(); err != nil {
		t.Fatalf("failed to resume session: %v", err)
	}
	if sess.Status() != StatusActive {
		t.Fatalf("expected status active, got %v", sess.Status())
	}

	// Execute should now succeed
	res, err := sess.Execute(context.Background(), "Will succeed")
	if err != nil {
		t.Fatalf("execution after resume failed: %v", err)
	}
	if res.Answer != "OK" {
		t.Errorf("unexpected answer: %q", res.Answer)
	}
}

func TestSessionRestart(t *testing.T) {
	mock := &mockLLM{
		responses: []*llm.Response{
			{
				Message: llm.AssistantMessage(llm.TextPart{Text: "Answer before restart"}),
				Usage:   llm.Usage{TotalTokens: 25},
			},
		},
	}
	eng := createTestEngine(mock)
	sess, _ := New(Config{Engine: eng})

	_, err := sess.Execute(context.Background(), "Turn before restart")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if len(sess.History()) != 3 {
		t.Fatalf("expected 3 messages before restart, got %d", len(sess.History()))
	}

	if err := sess.Restart(); err != nil {
		t.Fatalf("restart failed: %v", err)
	}

	// History should be reset to only initial system prompt
	hist := sess.History()
	if len(hist) != 1 {
		t.Fatalf("expected 1 message after restart, got %d", len(hist))
	}
	if hist[0].Role != llm.RoleSystem {
		t.Errorf("expected system message, got %v", hist[0].Role)
	}
	if sess.TotalUsage().TotalTokens != 0 {
		t.Errorf("expected 0 total tokens after restart, got %d", sess.TotalUsage().TotalTokens)
	}
	if sess.Status() != StatusActive {
		t.Errorf("expected active status after restart, got %v", sess.Status())
	}
}

func TestSessionRename(t *testing.T) {
	eng := createTestEngine(&mockLLM{})
	sess, _ := New(Config{Engine: eng, Title: "Original"})

	if err := sess.Rename("Updated Investigation"); err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if sess.Title() != "Updated Investigation" {
		t.Errorf("expected title 'Updated Investigation', got %q", sess.Title())
	}

	if err := sess.Rename("   "); err == nil {
		t.Error("expected error for empty title, got nil")
	}
}

func TestSessionExecute_ErrorRollback(t *testing.T) {
	mock := &mockLLM{
		responses: []*llm.Response{}, // empty, will cause chat error
	}
	eng := createTestEngine(mock)
	sess, _ := New(Config{Engine: eng})

	initialLen := len(sess.History())

	_, err := sess.Execute(context.Background(), "Failing turn")
	if err == nil {
		t.Fatal("expected execution error, got nil")
	}

	// History should be rolled back to initial state
	if len(sess.History()) != initialLen {
		t.Errorf("history length changed after failed execute: got %d, expected %d", len(sess.History()), initialLen)
	}
}
