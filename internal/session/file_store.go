package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store defines the persistence contract for multi-tenant session storage.
type Store interface {
	Save(ctx context.Context, sess *Session) error
	Load(ctx context.Context, owner Identity, id string) (*Snapshot, error)
	List(ctx context.Context, owner Identity) ([]*Snapshot, error)
	ListAll(ctx context.Context) ([]*Snapshot, error)
	Delete(ctx context.Context, owner Identity, id string) error
}

// FileStore persists sessions as partitioned JSON files under <dir>/<cluster_id>/<user_uid>/<session_id>.json.
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

// Dir returns the underlying root storage directory.
func (fs *FileStore) Dir() string {
	return fs.dir
}

// Save atomically writes a Session snapshot under its owner's partitioned directory.
func (fs *FileStore) Save(ctx context.Context, sess *Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess == nil {
		return ErrNilSession
	}

	snap := sess.Snapshot()
	if err := validateSessionID(snap.ID); err != nil {
		return err
	}
	ownerDir, err := fs.ownerDir(snap.Owner)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ownerDir, 0700); err != nil {
		return fmt.Errorf("create owner sessions directory: %w", err)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session snapshot: %w", err)
	}

	targetPath := filepath.Join(ownerDir, snap.ID+".json")
	tmpPath := filepath.Join(ownerDir, snap.ID+".tmp")

	// Write to temporary file first for atomic persistence.
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temporary session file: %w", err)
	}

	// Atomic rename replaces targetPath safely.
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename session file: %w", err)
	}

	return nil
}

// Load reads and deserializes a Session snapshot belonging to owner from disk.
func (fs *FileStore) Load(ctx context.Context, owner Identity, id string) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targetPath, err := fs.sessionPath(owner, id)
	if err != nil {
		return nil, err
	}

	return loadSnapshotFile(targetPath)
}

// List returns all stored session snapshots for a specific owner ordered by UpdatedAt descending.
func (fs *FileStore) List(ctx context.Context, owner Identity) ([]*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ownerDir, err := fs.ownerDir(owner)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(ownerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Snapshot{}, nil
		}
		return nil, fmt.Errorf("read owner sessions directory: %w", err)
	}

	var snapshots []*Snapshot
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		snap, err := loadSnapshotFile(filepath.Join(ownerDir, entry.Name()))
		if err != nil {
			// Skip corrupted or unreadable files gracefully.
			continue
		}
		snapshots = append(snapshots, snap)
	}

	sortSnapshotsNewestFirst(snapshots)
	return snapshots, nil
}

// ListAll returns all stored session snapshots across all owners ordered by UpdatedAt descending.
func (fs *FileStore) ListAll(ctx context.Context) ([]*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	clusters, err := os.ReadDir(fs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Snapshot{}, nil
		}
		return nil, fmt.Errorf("read sessions root directory: %w", err)
	}

	var snapshots []*Snapshot
	for _, clusterEntry := range clusters {
		if !clusterEntry.IsDir() {
			continue
		}
		clusterPath := filepath.Join(fs.dir, clusterEntry.Name())
		users, err := os.ReadDir(clusterPath)
		if err != nil {
			continue
		}
		for _, userEntry := range users {
			if !userEntry.IsDir() {
				continue
			}
			userPath := filepath.Join(clusterPath, userEntry.Name())
			files, err := os.ReadDir(userPath)
			if err != nil {
				continue
			}
			for _, fileEntry := range files {
				if fileEntry.IsDir() || !strings.HasSuffix(fileEntry.Name(), ".json") {
					continue
				}
				snap, err := loadSnapshotFile(filepath.Join(userPath, fileEntry.Name()))
				if err != nil {
					continue
				}
				snapshots = append(snapshots, snap)
			}
		}
	}

	sortSnapshotsNewestFirst(snapshots)
	return snapshots, nil
}

// Delete removes the persisted session file belonging to owner from disk.
func (fs *FileStore) Delete(ctx context.Context, owner Identity, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := fs.sessionPath(owner, id)
	if err != nil {
		return err
	}

	if err := os.Remove(targetPath); err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("remove session file: %w", err)
	}

	return nil
}

func (fs *FileStore) ownerDir(owner Identity) (string, error) {
	if !owner.Valid() {
		return "", errors.New("owner cluster_id and user_uid are required")
	}
	return filepath.Join(fs.dir, encodeSegment(owner.ClusterID), encodeSegment(owner.UserUID)), nil
}

func (fs *FileStore) sessionPath(owner Identity, id string) (string, error) {
	if err := validateSessionID(id); err != nil {
		return "", err
	}
	ownerDir, err := fs.ownerDir(owner)
	if err != nil {
		return "", err
	}
	return filepath.Join(ownerDir, strings.TrimSpace(id)+".json"), nil
}

func validateSessionID(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.New("session ID cannot be empty")
	}
	if strings.ContainsAny(trimmed, `/\`) || strings.Contains(trimmed, "..") || filepath.Base(trimmed) != trimmed {
		return fmt.Errorf("invalid session ID %q", id)
	}
	return nil
}

// encodeSegment keeps safe path characters readable while percent-encoding separators and dots to prevent traversal or collisions.
func encodeSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		isSafe := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_'
		if isSafe {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func loadSnapshotFile(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
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

func sortSnapshotsNewestFirst(snapshots []*Snapshot) {
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].UpdatedAt.After(snapshots[j].UpdatedAt)
	})
}
