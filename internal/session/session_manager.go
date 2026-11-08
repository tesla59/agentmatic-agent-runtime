package session

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrNilSession      = errors.New("session cannot be nil")
)

// SessionManager coordinates and stores active interactive sessions in memory and optional persistent storage.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	store    Store
}

// NewSessionManager creates a new session manager with an optional backing persistence Store.
// If store is omitted or nil, sessions are kept purely in memory.
func NewSessionManager(store ...Store) *SessionManager {
	var s Store
	if len(store) > 0 {
		s = store[0]
	}

	return &SessionManager{
		sessions: make(map[string]*Session),
		store:    s,
	}
}

// Register adds a session to the manager and persists it if a store is configured.
func (sm *SessionManager) Register(sess *Session) error {
	if sess == nil {
		return ErrNilSession
	}

	id := sess.ID()
	if id == "" {
		return errors.New("session ID cannot be empty")
	}

	sm.mu.Lock()
	sm.sessions[id] = sess
	sm.mu.Unlock()

	if sm.store != nil {
		if err := sm.store.Save(sess); err != nil {
			return fmt.Errorf("persist session: %w", err)
		}
	}

	return nil
}

// Save persists the current state of an active session to the backing store.
func (sm *SessionManager) Save(sess *Session) error {
	if sess == nil {
		return ErrNilSession
	}
	if sm.store == nil {
		return nil
	}
	return sm.store.Save(sess)
}

// LoadAll hydrates all stored sessions from the persistent store into memory.
func (sm *SessionManager) LoadAll(eng *engine.Engine) error {
	if sm.store == nil {
		return nil
	}

	snapshots, err := sm.store.List()
	if err != nil {
		return fmt.Errorf("list stored sessions: %w", err)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, snap := range snapshots {
		sess, err := Restore(*snap, eng)
		if err != nil {
			continue
		}
		sm.sessions[sess.ID()] = sess
	}

	return nil
}

// Get retrieves a session by its unique ID.
func (sm *SessionManager) Get(id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sess, ok := sm.sessions[id]
	return sess, ok
}

// List returns all registered sessions ordered by last updated timestamp (newest first).
func (sm *SessionManager) List() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	list := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		list = append(list, s)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt().After(list[j].UpdatedAt())
	})

	return list
}

// Delete removes a session by ID from memory and the backing store.
func (sm *SessionManager) Delete(id string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.sessions[id]; !ok {
		return ErrSessionNotFound
	}

	delete(sm.sessions, id)

	if sm.store != nil {
		_ = sm.store.Delete(id)
	}

	return nil
}

// Count returns the number of active sessions managed in memory.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.sessions)
}
