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
	ctx := context.Background()
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)
	eng := createTestEngine(&mockLLM{})
	owner := LocalDevIdentity()

	sess, err := New(Config{
		Name:   "crashloop-debug",
		Title:  "CrashLoop Investigation",
		Owner:  owner,
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if err := fs.Save(ctx, sess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// Verify partitioned path exists on disk: <dir>/<cluster_id>/<user_uid>/<id>.json
	filePath := filepath.Join(tempDir, owner.ClusterID, owner.UserUID, sess.ID()+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("expected partitioned file %s on disk", filePath)
	}

	// Verify temporary file is cleaned up
	tmpPath := filepath.Join(tempDir, owner.ClusterID, owner.UserUID, sess.ID()+".tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected temporary file %s to be removed after atomic rename", tmpPath)
	}

	// Load snapshot
	snap, err := fs.Load(ctx, owner, sess.ID())
	if err != nil {
		t.Fatalf("failed to load session: %v", err)
	}

	if snap.ID != sess.ID() {
		t.Errorf("snap ID %s != sess ID %s", snap.ID, sess.ID())
	}
	if snap.Name != "crashloop-debug" {
		t.Errorf("unexpected snap name: %q", snap.Name)
	}
	if snap.Title != "CrashLoop Investigation" {
		t.Errorf("unexpected snap title: %q", snap.Title)
	}
	if !snap.Owner.SameOwner(owner) {
		t.Errorf("unexpected snap owner: %+v", snap.Owner)
	}
	if snap.Status != StatusActive {
		t.Errorf("unexpected snap status: %v", snap.Status)
	}
	if len(snap.History) != 1 {
		t.Fatalf("expected 1 history message in snap, got %d", len(snap.History))
	}

	// Different owner must not load this session
	otherOwner := Identity{ClusterID: "local", UserUID: "other-user"}
	if _, err := fs.Load(ctx, otherOwner, sess.ID()); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound for different owner, got %v", err)
	}
}

func TestFileStore_ListOrderAndIsolation(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)
	eng := createTestEngine(&mockLLM{})

	alice := Identity{ClusterID: "prod-us", UserUID: "uid-alice", Username: "alice"}
	bob := Identity{ClusterID: "prod-us", UserUID: "uid-bob", Username: "bob"}

	s1, _ := New(Config{Title: "Session 1", Owner: alice, Engine: eng})
	_ = fs.Save(ctx, s1)
	time.Sleep(10 * time.Millisecond)

	s2, _ := New(Config{Title: "Session 2", Owner: alice, Engine: eng})
	_ = fs.Save(ctx, s2)
	time.Sleep(10 * time.Millisecond)

	bobSess, _ := New(Config{Title: "Bob Session", Owner: bob, Engine: eng})
	_ = fs.Save(ctx, bobSess)
	time.Sleep(10 * time.Millisecond)

	s3, _ := New(Config{Title: "Session 3", Owner: alice, Engine: eng})
	_ = fs.Save(ctx, s3)

	// Update s1 and re-save
	_ = s1.Rename("Session 1 (Updated)")
	_ = fs.Save(ctx, s1)

	aliceList, err := fs.List(ctx, alice)
	if err != nil {
		t.Fatalf("failed to list alice sessions: %v", err)
	}
	if len(aliceList) != 3 {
		t.Fatalf("expected 3 snapshots for alice, got %d", len(aliceList))
	}

	// Order: s1 (newest), s3, s2 (oldest)
	if aliceList[0].ID != s1.ID() {
		t.Errorf("expected newest session s1, got %s", aliceList[0].ID)
	}
	if aliceList[1].ID != s3.ID() {
		t.Errorf("expected second session s3, got %s", aliceList[1].ID)
	}
	if aliceList[2].ID != s2.ID() {
		t.Errorf("expected oldest session s2, got %s", aliceList[2].ID)
	}

	// Unseen user gets empty list, not error
	charlieList, err := fs.List(ctx, Identity{ClusterID: "prod-us", UserUID: "uid-charlie"})
	if err != nil {
		t.Fatalf("expected empty list without error, got %v", err)
	}
	if len(charlieList) != 0 {
		t.Errorf("expected 0 sessions for charlie, got %d", len(charlieList))
	}

	// ListAll returns all 4 across all owners
	all, err := fs.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 total snapshots across owners, got %d", len(all))
	}
}

func TestFileStore_DeleteAndPathSafety(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)
	eng := createTestEngine(&mockLLM{})
	owner := Identity{ClusterID: "arn:aws:eks:us-east-1:123:cluster/prod", UserUID: "../evil/uid"}

	sess, _ := New(Config{Title: "To Delete", Owner: owner, Engine: eng})
	if err := fs.Save(ctx, sess); err != nil {
		t.Fatalf("save with special owner characters failed: %v", err)
	}

	// Verify file stayed inside tempDir despite slashes and '..' in owner fields
	loaded, err := fs.Load(ctx, owner, sess.ID())
	if err != nil {
		t.Fatalf("load after save failed: %v", err)
	}
	if loaded.ID != sess.ID() {
		t.Errorf("unexpected loaded ID %s", loaded.ID)
	}

	// Traversal in session ID must be rejected
	if _, err := fs.Load(ctx, owner, "../escape"); err == nil {
		t.Error("expected error for path traversal in session ID")
	}

	if err := fs.Delete(ctx, owner, sess.ID()); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if _, err := fs.Load(ctx, owner, sess.ID()); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound after deletion, got %v", err)
	}

	if err := fs.Delete(ctx, owner, "non-existent"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestRestore_RoundTrip(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	fs, _ := NewFileStore(tempDir)
	owner := LocalDevIdentity()

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

	sess, _ := New(Config{Title: "Preserved Investigation", Owner: owner, Engine: eng})
	_, err := sess.Execute(ctx, "First prompt")
	if err != nil {
		t.Fatalf("first execute failed: %v", err)
	}

	// Save to disk
	if err := fs.Save(ctx, sess); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Simulate program exit and restart: load snapshot and Restore
	snap, err := fs.Load(ctx, owner, sess.ID())
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

	// Continue conversation on the restored session
	res2, err := restored.Execute(ctx, "Second prompt after restart")
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
