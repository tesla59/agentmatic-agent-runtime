package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionManager_RegisterAndGet(t *testing.T) {
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})

	// Nil session registration should fail
	if err := sm.Register(nil); !errors.Is(err, ErrNilSession) {
		t.Fatalf("expected ErrNilSession, got %v", err)
	}

	sess, err := New(Config{
		Title:  "Production Outage Investigation",
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if err := sm.Register(sess); err != nil {
		t.Fatalf("failed to register session: %v", err)
	}

	if sm.Count() != 1 {
		t.Errorf("expected count 1, got %d", sm.Count())
	}

	retrieved, ok := sm.Get(sess.ID())
	if !ok {
		t.Fatalf("failed to get session by id: %s", sess.ID())
	}
	if retrieved.ID() != sess.ID() {
		t.Errorf("retrieved ID %s != expected %s", retrieved.ID(), sess.ID())
	}
	if retrieved.Title() != "Production Outage Investigation" {
		t.Errorf("unexpected session title: %q", retrieved.Title())
	}

	// Non-existent ID
	_, ok = sm.Get("non-existent-uuid")
	if ok {
		t.Error("expected ok=false for non-existent session ID")
	}
}

func TestSessionManager_ListOrder(t *testing.T) {
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})

	s1, _ := New(Config{Title: "Session 1", Engine: eng})
	_ = sm.Register(s1)
	time.Sleep(10 * time.Millisecond)

	s2, _ := New(Config{Title: "Session 2", Engine: eng})
	_ = sm.Register(s2)
	time.Sleep(10 * time.Millisecond)

	s3, _ := New(Config{Title: "Session 3", Engine: eng})
	_ = sm.Register(s3)

	// Update s1 so it becomes the most recently updated
	_ = s1.Rename("Session 1 (Updated)")

	list := sm.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(list))
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
}

func TestSessionManager_Delete(t *testing.T) {
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})

	sess, _ := New(Config{Title: "Temporary", Engine: eng})
	_ = sm.Register(sess)

	if sm.Count() != 1 {
		t.Fatalf("expected count 1, got %d", sm.Count())
	}

	if err := sm.Delete(sess.ID()); err != nil {
		t.Fatalf("failed to delete session: %v", err)
	}

	if sm.Count() != 0 {
		t.Errorf("expected count 0 after deletion, got %d", sm.Count())
	}

	// Deleting again should return ErrSessionNotFound
	err := sm.Delete(sess.ID())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSessionManager_ConcurrentAccess(t *testing.T) {
	sm := NewSessionManager()
	eng := createTestEngine(&mockLLM{})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sess, err := New(Config{Engine: eng})
			if err != nil {
				t.Errorf("create failed: %v", err)
				return
			}
			if err := sm.Register(sess); err != nil {
				t.Errorf("register failed: %v", err)
				return
			}
			_, _ = sm.Get(sess.ID())
			_ = sm.List()
		}(i)
	}
	wg.Wait()

	if sm.Count() != 20 {
		t.Errorf("expected 20 sessions registered concurrently, got %d", sm.Count())
	}
}
