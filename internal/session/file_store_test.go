package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
)

func TestNewFileStore(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "nested", "sessions")
	fs, err := NewFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error creating file store: %v", err)
	}

	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Errorf("expected directory %s to be created", tempDir)
	}
	if fs.Dir() != tempDir {
		t.Errorf("expected dir %s, got %s", tempDir, fs.Dir())
	}
}

func TestFileStore_SaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)
	eng := createTestEngine(&mockLLM{})

	sess, err := New(Config{
		Title:  "CrashLoop Investigation",
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if err := fs.Save(sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// Verify file exists on disk
	filePath := filepath.Join(tempDir, sess.ID()+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("expected file %s on disk", filePath)
	}

	// Verify temporary file is cleaned up
	tmpPath := filepath.Join(tempDir, sess.ID()+".tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected temporary file %s to be removed after atomic rename", tmpPath)
	}

	// Load snapshot
	snap, err := fs.Load(sess.ID())
	if err != nil {
		t.Fatalf("failed to load session: %v", err)
	}

	if snap.ID != sess.ID() {
		t.Errorf("snap ID %s != sess ID %s", snap.ID, sess.ID())
	}
	if snap.Title != "CrashLoop Investigation" {
		t.Errorf("unexpected snap title: %q", snap.Title)
	}
	if snap.Status != StatusActive {
		t.Errorf("unexpected snap status: %v", snap.Status)
	}
	if len(snap.History) != 1 {
		t.Fatalf("expected 1 history message in snap, got %d", len(snap.History))
	}
}

func TestFileStore_ListOrder(t *testing.T) {
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)
	eng := createTestEngine(&mockLLM{})

	s1, _ := New(Config{Title: "Session 1", Engine: eng})
	_ = fs.Save(s1)
	time.Sleep(10 * time.Millisecond)

	s2, _ := New(Config{Title: "Session 2", Engine: eng})
	_ = fs.Save(s2)
	time.Sleep(10 * time.Millisecond)

	s3, _ := New(Config{Title: "Session 3", Engine: eng})
	_ = fs.Save(s3)

	// Update s1 and re-save
	_ = s1.Rename("Session 1 (Updated)")
	_ = fs.Save(s1)

	list, err := fs.List()
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(list))
	}

	// Order: s1 (newest), s3, s2 (oldest)
	if list[0].ID != s1.ID() {
		t.Errorf("expected newest session s1, got %s", list[0].ID)
	}
	if list[1].ID != s3.ID() {
		t.Errorf("expected second session s3, got %s", list[1].ID)
	}
	if list[2].ID != s2.ID() {
		t.Errorf("expected oldest session s2, got %s", list[2].ID)
	}
}

func TestFileStore_Delete(t *testing.T) {
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)
	eng := createTestEngine(&mockLLM{})

	sess, _ := New(Config{Title: "To Delete", Engine: eng})
	_ = fs.Save(sess)

	if err := fs.Delete(sess.ID()); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// File should be gone
	_, err := fs.Load(sess.ID())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound after deletion, got %v", err)
	}

	// Deleting non-existent returns ErrSessionNotFound
	if err := fs.Delete("non-existent"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestRestore_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)

	mock := &mockLLM{
		responses: []*llm.Response{
			{
				Message: llm.AssistantMessage(llm.TextPart{Text: "Answer turn 1"}),
				Usage:   llm.Usage{TotalTokens: 20},
			},
			{
				Message: llm.AssistantMessage(llm.TextPart{Text: "Answer turn 2 (restored)"}),
				Usage:   llm.Usage{TotalTokens: 30},
			},
		},
	}
	eng := createTestEngine(mock)

	sess, _ := New(Config{Title: "Preserved Investigation", Engine: eng})
	_, err := sess.Execute(context.Background(), "First prompt")
	if err != nil {
		t.Fatalf("first execute failed: %v", err)
	}

	// Save to disk
	if err := fs.Save(sess); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Simulate program exit and restart: load snapshot and Restore
	snap, err := fs.Load(sess.ID())
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	restored, err := Restore(*snap, eng)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if restored.ID() != sess.ID() {
		t.Errorf("restored ID %s != original ID %s", restored.ID(), sess.ID())
	}
	if restored.Title() != "Preserved Investigation" {
		t.Errorf("unexpected restored title: %q", restored.Title())
	}
	if restored.TotalUsage().TotalTokens != 20 {
		t.Errorf("expected 20 tokens, got %d", restored.TotalUsage().TotalTokens)
	}

	// Continue conversation on the restored session!
	res2, err := restored.Execute(context.Background(), "Second prompt after restart")
	if err != nil {
		t.Fatalf("execute on restored session failed: %v", err)
	}
	if res2.Answer != "Answer turn 2 (restored)" {
		t.Errorf("unexpected answer: %q", res2.Answer)
	}
	if restored.TotalUsage().TotalTokens != 50 { // 20 + 30
		t.Errorf("expected 50 total tokens after turn 2, got %d", restored.TotalUsage().TotalTokens)
	}
}
