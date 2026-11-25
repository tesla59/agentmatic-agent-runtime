package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
)

// Status represents the operational state of a session.
type Status string

const (
	StatusActive Status = "active"
	StatusPaused Status = "paused"
)

var (
	ErrSessionPaused = errors.New("session is paused")
	ErrEmptyPrompt   = errors.New("prompt cannot be empty")
)

// Identity holds the verified Kubernetes identity attributes of a session owner.
type Identity struct {
	ClusterID   string   `json:"cluster_id"`
	UserUID     string   `json:"user_uid"`
	Username    string   `json:"username"`
	Groups      []string `json:"groups,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Email       string   `json:"email,omitempty"`
}

// LocalDevIdentity returns the default owner identity for standalone local debugging.
func LocalDevIdentity() Identity {
	return Identity{
		ClusterID: "local",
		UserUID:   "local-dev",
		Username:  "tesla59",
	}
}

// SameOwner reports whether both identities identify the same cluster and user UID.
func (i Identity) SameOwner(other Identity) bool {
	return i.ClusterID != "" && i.UserUID != "" &&
		i.ClusterID == other.ClusterID && i.UserUID == other.UserUID
}

func cloneIdentity(id Identity) Identity {
	cloned := id
	if len(id.Groups) > 0 {
		cloned.Groups = make([]string, len(id.Groups))
		copy(cloned.Groups, id.Groups)
	}
	return cloned
}

// Config configures a new Session instance.
type Config struct {
	ID           string
	Name         string
	Title        string
	Owner        Identity
	SystemPrompt string
	Engine       *engine.Engine
}

// Snapshot is a detached serializable point-in-time representation of a Session.
type Snapshot struct {
	ID           string        `json:"id"`
	Name         string        `json:"name,omitempty"`
	Title        string        `json:"title"`
	Owner        Identity      `json:"owner"`
	Status       Status        `json:"status"`
	History      []llm.Message `json:"history"`
	SystemPrompt string        `json:"system_prompt,omitempty"`
	TotalUsage   llm.Usage     `json:"total_usage"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// Session encapsulates an ongoing interactive multi-turn agent conversation.
type Session struct {
	mu           sync.RWMutex
	id           string
	name         string
	title        string
	owner        Identity
	status       Status
	history      []llm.Message
	systemPrompt string
	engine       *engine.Engine
	totalUsage   llm.Usage
	createdAt    time.Time
	updatedAt    time.Time
}

// New creates and initializes a new Session with the specified configuration.
func New(cfg Config) (*Session, error) {
	if cfg.Engine == nil {
		return nil, fmt.Errorf("engine is required")
	}

	// TODO(stage-3b): remove fallback and strictly require both ClusterID and UserUID once repl.go and store tests pass LocalDevIdentity() explicitly.
	owner := cfg.Owner
	if owner.ClusterID == "" && owner.UserUID == "" {
		owner = LocalDevIdentity()
	} else if owner.ClusterID == "" || owner.UserUID == "" {
		return nil, fmt.Errorf("owner cluster_id and user_uid are both required")
	}

	id := cfg.ID
	if id == "" {
		id = uuid.NewString()
	}

	name := strings.TrimSpace(cfg.Name)

	title := strings.TrimSpace(cfg.Title)
	if title == "" {
		title = "New Session"
	}

	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = cfg.Engine.SystemPrompt()
	}

	now := time.Now()
	var initialHistory []llm.Message
	if sysPrompt != "" {
		initialHistory = []llm.Message{llm.SystemMessage(sysPrompt)}
	}

	return &Session{
		id:           id,
		name:         name,
		title:        title,
		owner:        cloneIdentity(owner),
		status:       StatusActive,
		history:      initialHistory,
		systemPrompt: sysPrompt,
		engine:       cfg.Engine,
		createdAt:    now,
		updatedAt:    now,
	}, nil
}

// ID returns the unique session identifier.
func (s *Session) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// Name returns the optional human-readable slug/name of the session.
func (s *Session) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

// Title returns the human-readable session title.
func (s *Session) Title() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.title
}

// Owner returns a defensive copy of the verified session owner identity.
func (s *Session) Owner() Identity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneIdentity(s.owner)
}

// Status returns the current operational status of the session.
func (s *Session) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// TotalUsage returns the cumulative token usage across all turns in this session.
func (s *Session) TotalUsage() llm.Usage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalUsage
}

// History returns a defensive copy of the conversation message history.
func (s *Session) History() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hist := make([]llm.Message, len(s.history))
	copy(hist, s.history)
	return hist
}

// CreatedAt returns the session creation timestamp.
func (s *Session) CreatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.createdAt
}

// UpdatedAt returns the timestamp of the last activity on this session.
func (s *Session) UpdatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

// Snapshot returns an immutable point-in-time copy of the session state.
func (s *Session) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hist := make([]llm.Message, len(s.history))
	copy(hist, s.history)

	return Snapshot{
		ID:           s.id,
		Name:         s.name,
		Title:        s.title,
		Owner:        cloneIdentity(s.owner),
		Status:       s.status,
		History:      hist,
		SystemPrompt: s.systemPrompt,
		TotalUsage:   s.totalUsage,
		CreatedAt:    s.createdAt,
		UpdatedAt:    s.updatedAt,
	}
}

// Restore re-instantiates an existing Session from a stored Snapshot.
func Restore(snap Snapshot, eng *engine.Engine) (*Session, error) {
	if eng == nil {
		return nil, fmt.Errorf("engine is required")
	}
	if snap.ID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if snap.Owner.ClusterID == "" || snap.Owner.UserUID == "" {
		return nil, fmt.Errorf("session owner cluster_id and user_uid are required")
	}

	hist := make([]llm.Message, len(snap.History))
	copy(hist, snap.History)

	return &Session{
		id:           snap.ID,
		name:         strings.TrimSpace(snap.Name),
		title:        snap.Title,
		owner:        cloneIdentity(snap.Owner),
		status:       snap.Status,
		history:      hist,
		systemPrompt: snap.SystemPrompt,
		engine:       eng,
		totalUsage:   snap.TotalUsage,
		createdAt:    snap.CreatedAt,
		updatedAt:    snap.UpdatedAt,
	}, nil
}

// Rename updates the title of the session.
func (s *Session) Rename(title string) error {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return errors.New("session title cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.title = trimmed
	s.updatedAt = time.Now()
	return nil
}

// SetName updates the human-readable slug/name of the session.
func (s *Session) SetName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("session name cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.name = trimmed
	s.updatedAt = time.Now()
	return nil
}

// Pause transitions the session into a paused state, preventing further executions.
func (s *Session) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status = StatusPaused
	s.updatedAt = time.Now()
	return nil
}

// Resume transitions a paused session back to the active state.
func (s *Session) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status = StatusActive
	s.updatedAt = time.Now()
	return nil
}

// Restart clears the conversation history, resetting to the initial system prompt and active status.
func (s *Session) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var initialHistory []llm.Message
	if s.systemPrompt != "" {
		initialHistory = []llm.Message{llm.SystemMessage(s.systemPrompt)}
	}

	s.history = initialHistory
	s.status = StatusActive
	s.totalUsage = llm.Usage{}
	s.updatedAt = time.Now()
	return nil
}

// Execute submits a user prompt to the session, running the agent loop with the full conversation history.
func (s *Session) Execute(ctx context.Context, prompt string) (*engine.RunResult, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return nil, ErrEmptyPrompt
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status == StatusPaused {
		return nil, ErrSessionPaused
	}

	// Append the new user prompt
	s.history = append(s.history, llm.UserMessage(trimmed))

	// Run engine with the updated history
	res, err := s.engine.RunWithHistory(ctx, s.history)
	if err != nil {
		// Revert appended user message on failure to keep history consistent
		s.history = s.history[:len(s.history)-1]
		return nil, err
	}

	// Update conversation history with the complete turn (user, tool calls/results, assistant answer)
	s.history = res.History
	s.totalUsage.PromptTokens += res.Usage.PromptTokens
	s.totalUsage.CompletionTokens += res.Usage.CompletionTokens
	s.totalUsage.TotalTokens += res.Usage.TotalTokens
	s.updatedAt = time.Now()

	return res, nil
}
