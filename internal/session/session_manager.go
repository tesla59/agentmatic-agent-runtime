package session

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrNilSession      = errors.New("session cannot be nil")
)

// SessionManager coordinates and stores active interactive sessions in memory.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager creates a new in-memory session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// Register adds a session to the manager. Returns an error if the session is nil or has an empty ID.
func (sm *SessionManager) Register(sess *Session) error {
	if sess == nil {
		return ErrNilSession
	}

	id := sess.ID()
	if id == "" {
		return errors.New("session ID cannot be empty")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.sessions[id] = sess
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

// Delete removes a session by ID. Returns ErrSessionNotFound if the session does not exist.
func (sm *SessionManager) Delete(id string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.sessions[id]; !ok {
		return ErrSessionNotFound
	}

	delete(sm.sessions, id)
	return nil
}

// Count returns the number of active sessions managed.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.sessions)
}
