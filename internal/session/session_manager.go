package session

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
)

var (
	ErrSessionNotFound      = errors.New("session not found")
	ErrNilSession           = errors.New("session cannot be nil")
	ErrDuplicateSessionName = errors.New("session name already exists")
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
func (sm *SessionManager) Register(ctx context.Context, sess *Session) error {
	if sess == nil {
		return ErrNilSession
	}

	id := sess.ID()
	if id == "" {
		return errors.New("session ID cannot be empty")
	}

	owner := sess.Owner()
	if !owner.Valid() {
		return errors.New("session owner cluster_id and user_uid are required")
	}

	name := sess.Name()

	sm.mu.Lock()
	if name != "" && sm.hasNameConflictLocked(owner, name, id) {
		sm.mu.Unlock()
		return ErrDuplicateSessionName
	}
	sm.sessions[id] = sess
	sm.mu.Unlock()

	if sm.store != nil {
		if err := sm.store.Save(ctx, sess); err != nil {
			sm.mu.Lock()
			delete(sm.sessions, id)
			sm.mu.Unlock()
			return fmt.Errorf("persist session: %w", err)
		}
	}

	return nil
}

// Save persists the current state of an active session to the backing store.
func (sm *SessionManager) Save(ctx context.Context, sess *Session) error {
	if sess == nil {
		return ErrNilSession
	}
	if sm.store == nil {
		return nil
	}
	return sm.store.Save(ctx, sess)
}

// LoadAll hydrates all stored sessions across all owners from the persistent store into memory.
func (sm *SessionManager) LoadAll(ctx context.Context, eng *engine.Engine) error {
	if sm.store == nil {
		return nil
	}

	snapshots, err := sm.store.ListAll(ctx)
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

// Get retrieves a session by its unique ID if owned by the specified owner.
func (sm *SessionManager) Get(owner Identity, id string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sess, ok := sm.sessions[id]
	if !ok || !sess.Owner().SameOwner(owner) {
		return nil, false
	}
	return sess, true
}

// GetByName retrieves a session by its human-readable slug/name if owned by the specified owner.
func (sm *SessionManager) GetByName(owner Identity, name string) (*Session, bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, false
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, sess := range sm.sessions {
		if sess.Owner().SameOwner(owner) && sess.Name() == trimmed {
			return sess, true
		}
	}
	return nil, false
}

// Resolve retrieves a session owned by owner, matching either by UUID id or slug name.
func (sm *SessionManager) Resolve(owner Identity, identifier string) (*Session, bool) {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return nil, false
	}
	if sess, ok := sm.Get(owner, trimmed); ok {
		return sess, true
	}
	return sm.GetByName(owner, trimmed)
}

// UpdateMetadata updates a session's display title and/or unique slug name and persists the change.
func (sm *SessionManager) UpdateMetadata(ctx context.Context, owner Identity, identifier, newTitle, newName string) (*Session, error) {
	trimmedTitle := strings.TrimSpace(newTitle)
	trimmedName := strings.TrimSpace(newName)
	if trimmedTitle == "" && trimmedName == "" {
		return nil, errors.New("at least one of new_title or new_name is required")
	}

	sess, ok := sm.Resolve(owner, identifier)
	if !ok {
		return nil, ErrSessionNotFound
	}

	if trimmedName != "" {
		sm.mu.Lock()
		if sm.hasNameConflictLocked(owner, trimmedName, sess.ID()) {
			sm.mu.Unlock()
			return nil, ErrDuplicateSessionName
		}
		if err := sess.SetName(trimmedName); err != nil {
			sm.mu.Unlock()
			return nil, err
		}
		sm.mu.Unlock()
	}

	if trimmedTitle != "" {
		if err := sess.Rename(trimmedTitle); err != nil {
			return nil, err
		}
	}

	if err := sm.Save(ctx, sess); err != nil {
		return nil, fmt.Errorf("persist updated session: %w", err)
	}

	return sess, nil
}

// List returns all registered sessions owned by owner ordered by last updated timestamp (newest first).
func (sm *SessionManager) List(owner Identity) []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	list := make([]*Session, 0)
	for _, s := range sm.sessions {
		if s.Owner().SameOwner(owner) {
			list = append(list, s)
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt().After(list[j].UpdatedAt())
	})

	return list
}

// Delete removes a session by ID from memory and the backing store if owned by owner.
func (sm *SessionManager) Delete(ctx context.Context, owner Identity, id string) error {
	sm.mu.Lock()
	sess, ok := sm.sessions[id]
	if !ok || !sess.Owner().SameOwner(owner) {
		sm.mu.Unlock()
		return ErrSessionNotFound
	}
	delete(sm.sessions, id)
	sm.mu.Unlock()

	if sm.store != nil {
		if err := sm.store.Delete(ctx, owner, id); err != nil && !errors.Is(err, ErrSessionNotFound) {
			return fmt.Errorf("delete persisted session: %w", err)
		}
	}

	return nil
}

// Count returns the total number of active sessions managed in memory across all owners.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.sessions)
}

// CountForOwner returns the number of active sessions owned by owner.
func (sm *SessionManager) CountForOwner(owner Identity) int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	count := 0
	for _, s := range sm.sessions {
		if s.Owner().SameOwner(owner) {
			count++
		}
	}
	return count
}

func (sm *SessionManager) hasNameConflictLocked(owner Identity, name, excludeID string) bool {
	for id, existing := range sm.sessions {
		if id == excludeID {
			continue
		}
		if existing.Owner().SameOwner(owner) && existing.Name() == name {
			return true
		}
	}
	return false
}
