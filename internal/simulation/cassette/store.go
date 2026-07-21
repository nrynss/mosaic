package cassette

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store persists and retrieves cassette recordings by key.
type Store interface {
	Get(ctx context.Context, key string) (*Recording, error)
	Put(ctx context.Context, rec *Recording) error
}

// MemoryStore is an in-memory Store suitable for tests and short-lived demos.
type MemoryStore struct {
	mu    sync.RWMutex
	byKey map[string]*Recording
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byKey: make(map[string]*Recording)}
}

// Get returns a deep copy of the recording for key, or ErrReplayMiss.
func (s *MemoryStore) Get(_ context.Context, key string) (*Recording, error) {
	if s == nil {
		return nil, ErrStoreRequired
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byKey[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrReplayMiss, key)
	}
	return cloneRecording(rec), nil
}

// Put stores a deep copy of rec under rec.Key.
func (s *MemoryStore) Put(_ context.Context, rec *Recording) error {
	if s == nil {
		return ErrStoreRequired
	}
	if rec == nil {
		return errors.New("cassette: recording is required")
	}
	if strings.TrimSpace(rec.Key) == "" {
		return errors.New("cassette: recording key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byKey == nil {
		s.byKey = make(map[string]*Recording)
	}
	s.byKey[rec.Key] = cloneRecording(rec)
	return nil
}

// Len reports how many recordings are stored (tests / diagnostics).
func (s *MemoryStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byKey)
}

// FileStore writes one JSON file per recording under Dir.
// Key path segments map to nested directories; the final segment is the filename
// stem with a .json suffix.
type FileStore struct {
	Dir string

	mu sync.Mutex
}

// NewFileStore prepares a file-backed store. Dir is created on first Put.
func NewFileStore(dir string) (*FileStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("cassette: file store directory is required")
	}
	return &FileStore{Dir: dir}, nil
}

// Get loads the JSON recording for key from disk.
func (s *FileStore) Get(_ context.Context, key string) (*Recording, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return nil, ErrStoreRequired
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrReplayMiss, key)
		}
		return nil, fmt.Errorf("cassette: read %s: %w", path, err)
	}
	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("cassette: decode %s: %w", path, err)
	}
	return &rec, nil
}

// Put writes rec as pretty JSON under the key-derived path.
func (s *FileStore) Put(_ context.Context, rec *Recording) error {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return ErrStoreRequired
	}
	if rec == nil {
		return errors.New("cassette: recording is required")
	}
	if strings.TrimSpace(rec.Key) == "" {
		return errors.New("cassette: recording key is required")
	}
	path, err := s.pathForKey(rec.Key)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cassette: mkdir for %s: %w", path, err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("cassette: encode recording: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("cassette: write temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cassette: rename %s: %w", path, err)
	}
	return nil
}

func (s *FileStore) pathForKey(key string) (string, error) {
	segments := strings.Split(key, "/")
	if len(segments) == 0 {
		return "", errors.New("cassette: empty key")
	}
	safe := make([]string, 0, len(segments))
	for _, seg := range segments {
		seg = sanitizeSegment(seg)
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("cassette: invalid key segment in %q", key)
		}
		safe = append(safe, seg)
	}
	// Final segment is the file stem.
	last := safe[len(safe)-1] + ".json"
	parts := append([]string{s.Dir}, safe[:len(safe)-1]...)
	parts = append(parts, last)
	return filepath.Join(parts...), nil
}

func cloneRecording(rec *Recording) *Recording {
	if rec == nil {
		return nil
	}
	out := *rec
	out.InsightJSON = cloneRaw(rec.InsightJSON)
	out.RecommendationJSON = cloneRaw(rec.RecommendationJSON)
	return &out
}
