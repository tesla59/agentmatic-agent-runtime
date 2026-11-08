package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store defines the persistence contract for session storage.
type Store interface {
	Save(sess *Session) error
	Load(id string) (*Snapshot, error)
	List() ([]*Snapshot, error)
	Delete(id string) error
}

// FileStore persists sessions as JSON files in a directory on disk.
type FileStore struct {
	dir string
}

// NewFileStore initializes a new FileStore in the specified directory.
// If dir is empty, it defaults to ~/.agentmatic/sessions.
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("locate home directory: %w", err)
		}
		dir = filepath.Join(home, ".agentmatic", "sessions")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create sessions directory: %w", err)
	}

	return &FileStore{dir: dir}, nil
}

// Dir returns the underlying storage directory.
func (fs *FileStore) Dir() string {
	return fs.dir
}

// Save atomically writes a Session snapshot to disk as JSON.
func (fs *FileStore) Save(sess *Session) error {
	if sess == nil {
		return ErrNilSession
	}

	snap := sess.Snapshot()
	if snap.ID == "" {
		return errors.New("cannot save session with empty ID")
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session snapshot: %w", err)
	}

	targetPath := filepath.Join(fs.dir, snap.ID+".json")
	tmpPath := filepath.Join(fs.dir, snap.ID+".tmp")

	// Write to temporary file first for atomic persistence
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temporary session file: %w", err)
	}

	// Atomic rename replaces targetPath safely
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename session file: %w", err)
	}

	return nil
}

// Load reads and deserializes a Session snapshot from disk.
func (fs *FileStore) Load(id string) (*Snapshot, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, errors.New("session ID cannot be empty")
	}

	targetPath := filepath.Join(fs.dir, trimmed+".json")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("read session file: %w", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal session file: %w", err)
	}

	return &snap, nil
}

// List returns all stored session snapshots ordered by UpdatedAt descending.
func (fs *FileStore) List() ([]*Snapshot, error) {
	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}

	var snapshots []*Snapshot
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		snap, err := fs.Load(id)
		if err != nil {
			// Skip corrupted or unreadable files gracefully
			continue
		}
		snapshots = append(snapshots, snap)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].UpdatedAt.After(snapshots[j].UpdatedAt)
	})

	return snapshots, nil
}

// Delete removes the persisted session file from disk.
func (fs *FileStore) Delete(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.New("session ID cannot be empty")
	}

	targetPath := filepath.Join(fs.dir, trimmed+".json")
	if err := os.Remove(targetPath); err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("remove session file: %w", err)
	}

	return nil
}
