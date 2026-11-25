package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionManager_RegisterAndGet(t *testing.T) {
	ctx := context.Background()
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})
	alice := Identity{ClusterID: "prod", UserUID: "uid-alice", Username: "alice"}
	bob := Identity{ClusterID: "prod", UserUID: "uid-bob", Username: "bob"}

	if err := sm.Register(ctx, nil); !errors.Is(err, ErrNilSession) {
		t.Fatalf("expected ErrNilSession, got %v", err)
	}

	sess, err := New(Config{
		Name:   "prod-outage",
		Title:  "Production Outage Investigation",
		Owner:  alice,
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if err := sm.Register(ctx, sess); err != nil {
		t.Fatalf("failed to register session: %v", err)
	}

	if sm.Count() != 1 || sm.CountForOwner(alice) != 1 || sm.CountForOwner(bob) != 0 {
		t.Errorf("unexpected counts: total=%d alice=%d bob=%d", sm.Count(), sm.CountForOwner(alice), sm.CountForOwner(bob))
	}

	// Owner can retrieve by ID, Name, and Resolve
	retrieved, ok := sm.Get(alice, sess.ID())
	if !ok || retrieved.ID() != sess.ID() {
		t.Fatalf("failed to get session by id: %s", sess.ID())
	}
	byName, ok := sm.GetByName(alice, "prod-outage")
	if !ok || byName.ID() != sess.ID() {
		t.Fatalf("failed to get session by name")
	}
	resolvedByID, ok := sm.Resolve(alice, sess.ID())
	if !ok || resolvedByID.ID() != sess.ID() {
		t.Error("Resolve failed for UUID")
	}
	resolvedByName, ok := sm.Resolve(alice, "prod-outage")
	if !ok || resolvedByName.ID() != sess.ID() {
		t.Error("Resolve failed for name")
	}

	// Different owner cannot access Alice's session by ID or name
	if _, ok := sm.Get(bob, sess.ID()); ok {
		t.Error("expected cross-owner Get to fail")
	}
	if _, ok := sm.GetByName(bob, "prod-outage"); ok {
		t.Error("expected cross-owner GetByName to fail")
	}

	// Non-existent ID
	if _, ok := sm.Get(alice, "non-existent-uuid"); ok {
		t.Error("expected ok=false for non-existent session ID")
	}
}

func TestSessionManager_NameUniquenessPerOwner(t *testing.T) {
	ctx := context.Background()
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})
	alice := Identity{ClusterID: "prod", UserUID: "uid-alice"}
	bob := Identity{ClusterID: "prod", UserUID: "uid-bob"}

	s1, _ := New(Config{Name: "api-debug", Owner: alice, Engine: eng})
	if err := sm.Register(ctx, s1); err != nil {
		t.Fatalf("register s1 failed: %v", err)
	}

	// Same owner + same name must be rejected
	sDup, _ := New(Config{Name: "api-debug", Owner: alice, Engine: eng})
	if err := sm.Register(ctx, sDup); !errors.Is(err, ErrDuplicateSessionName) {
		t.Fatalf("expected ErrDuplicateSessionName, got %v", err)
	}

	// Different owner + same name must succeed
	sBob, _ := New(Config{Name: "api-debug", Owner: bob, Engine: eng})
	if err := sm.Register(ctx, sBob); err != nil {
		t.Fatalf("expected different owner with same slug name to succeed, got %v", err)
	}

	// UpdateMetadata checks duplicate name and updates title/name
	s2, _ := New(Config{Name: "db-debug", Owner: alice, Engine: eng})
	_ = sm.Register(ctx, s2)

	if _, err := sm.UpdateMetadata(ctx, alice, s2.ID(), "", "api-debug"); !errors.Is(err, ErrDuplicateSessionName) {
		t.Fatalf("expected ErrDuplicateSessionName on rename, got %v", err)
	}

	updated, err := sm.UpdateMetadata(ctx, alice, "db-debug", "Database Latency", "db-latency")
	if err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}
	if updated.Title() != "Database Latency" || updated.Name() != "db-latency" {
		t.Errorf("unexpected updated metadata: title=%q name=%q", updated.Title(), updated.Name())
	}
}

func TestSessionManager_ListOrderAndIsolation(t *testing.T) {
	ctx := context.Background()
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})
	alice := Identity{ClusterID: "prod", UserUID: "uid-alice"}
	bob := Identity{ClusterID: "prod", UserUID: "uid-bob"}

	s1, _ := New(Config{Title: "Session 1", Owner: alice, Engine: eng})
	_ = sm.Register(ctx, s1)
	time.Sleep(10 * time.Millisecond)

	s2, _ := New(Config{Title: "Session 2", Owner: alice, Engine: eng})
	_ = sm.Register(ctx, s2)
	time.Sleep(10 * time.Millisecond)

	b1, _ := New(Config{Title: "Bob Session", Owner: bob, Engine: eng})
	_ = sm.Register(ctx, b1)
	time.Sleep(10 * time.Millisecond)

	s3, _ := New(Config{Title: "Session 3", Owner: alice, Engine: eng})
	_ = sm.Register(ctx, s3)

	// Update s1 so it becomes the most recently updated
	_ = s1.Rename("Session 1 (Updated)")

	list := sm.List(alice)
	if len(list) != 3 {
		t.Fatalf("expected 3 sessions for alice, got %d", len(list))
	}

	// Order should be: s1 (just updated), s3, s2
	if list[0].ID() != s1.ID() {
		t.Errorf("expected newest session to be s1 (%s), got %s", s1.ID(), list[0].ID())
	}
	if list[1].ID() != s3.ID() {
		t.Errorf("expected second session to be s3 (%s), got %s", s3.ID(), list[1].ID())
	}
	if list[2].ID() != s2.ID() {
		t.Errorf("expected oldest session to be s2 (%s), got %s", s2.ID(), list[2].ID())
	}

	bobList := sm.List(bob)
	if len(bobList) != 1 || bobList[0].ID() != b1.ID() {
		t.Errorf("unexpected bob list: %+v", bobList)
	}
}

func TestSessionManager_Delete(t *testing.T) {
	ctx := context.Background()
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})
	alice := Identity{ClusterID: "prod", UserUID: "uid-alice"}
	bob := Identity{ClusterID: "prod", UserUID: "uid-bob"}

	sess, _ := New(Config{Title: "Temporary", Owner: alice, Engine: eng})
	_ = sm.Register(ctx, sess)

	// Bob cannot delete Alice's session
	if err := sm.Delete(ctx, bob, sess.ID()); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for cross-owner delete, got %v", err)
	}
	if sm.Count() != 1 {
		t.Fatalf("expected session to still exist after unauthorized delete")
	}

	if err := sm.Delete(ctx, alice, sess.ID()); err != nil {
		t.Fatalf("failed to delete session: %v", err)
	}

	if sm.Count() != 0 {
		t.Errorf("expected count 0 after deletion, got %d", sm.Count())
	}

	if err := sm.Delete(ctx, alice, sess.ID()); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSessionManager_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})
	owner := LocalDevIdentity()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := New(Config{Owner: owner, Engine: eng})
			if err != nil {
				t.Errorf("create failed: %v", err)
				return
			}
			if err := sm.Register(ctx, sess); err != nil {
				t.Errorf("register failed: %v", err)
				return
			}
			_, _ = sm.Get(owner, sess.ID())
			_ = sm.List(owner)
		}()
	}
	wg.Wait()

	if sm.Count() != 20 {
		t.Errorf("expected 20 sessions registered concurrently, got %d", sm.Count())
	}
}

func TestSessionManager_WithFileStore(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	fs, err := NewFileStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create file store: %v", err)
	}

	eng := createTestEngine(&mockLLM{})
	sm := NewSessionManager(fs)
	owner := Identity{ClusterID: "prod-eu", UserUID: "uid-alice", Username: "alice"}

	sess, err := New(Config{
		Name:   "persistent-check",
		Title:  "Persistent Session",
		Owner:  owner,
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if err := sm.Register(ctx, sess); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	// Create a new session manager pointing to the same disk store and LoadAll
	smRestored := NewSessionManager(fs)
	if smRestored.Count() != 0 {
		t.Errorf("expected empty in-memory state before LoadAll, got %d", smRestored.Count())
	}

	if err := smRestored.LoadAll(ctx, eng); err != nil {
		t.Fatalf("load all failed: %v", err)
	}

	if smRestored.Count() != 1 {
		t.Fatalf("expected 1 session loaded from disk, got %d", smRestored.Count())
	}

	loadedSess, ok := smRestored.Resolve(owner, "persistent-check")
	if !ok {
		t.Fatalf("failed to resolve loaded session by name")
	}
	if loadedSess.Title() != "Persistent Session" {
		t.Errorf("unexpected loaded title: %q", loadedSess.Title())
	}

	if err := smRestored.Delete(ctx, owner, sess.ID()); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if _, err = fs.Load(ctx, owner, sess.ID()); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound on disk after delete, got %v", err)
	}
}
