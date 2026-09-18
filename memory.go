package agentkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/richardwooding/llmkit/core"

	"github.com/richardwooding/agentkit/internal/atomicfile"
)

// Store persists conversations by session ID. Load returns nil for an unknown
// session. Stores hold the lossless transcript; compaction never touches them.
type Store interface {
	Load(ctx context.Context, sessionID string) ([]core.Message, error)
	Append(ctx context.Context, sessionID string, msgs ...core.Message) error
}

// Deleter is implemented by stores that can forget a session.
type Deleter interface {
	Delete(ctx context.Context, sessionID string) error
}

// MemoryStore keeps sessions in process memory.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string][]core.Message
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string][]core.Message{}}
}

// Load returns a copy of the session's messages.
func (s *MemoryStore) Load(_ context.Context, sessionID string) ([]core.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.Message(nil), s.sessions[sessionID]...), nil
}

// Append adds messages to the session.
func (s *MemoryStore) Append(_ context.Context, sessionID string, msgs ...core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = append(s.sessions[sessionID], msgs...)
	return nil
}

// Delete forgets the session.
func (s *MemoryStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	return nil
}

// FileStore keeps one JSON file per session under a directory, written
// atomically. The directory is created on first Append.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore returns a FileStore rooted at dir; no I/O happens here.
func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

func (s *FileStore) path(sessionID string) string {
	return filepath.Join(s.dir, url.PathEscape(sessionID)+".json")
}

// Load reads the session file; a missing file is an empty session.
func (s *FileStore) Load(_ context.Context, sessionID string) ([]core.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(sessionID)
}

func (s *FileStore) read(sessionID string) ([]core.Message, error) {
	b, err := os.ReadFile(s.path(sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentkit: read session: %w", err)
	}
	var msgs []core.Message
	if err := json.Unmarshal(b, &msgs); err != nil {
		return nil, fmt.Errorf("agentkit: decode session %q: %w", sessionID, err)
	}
	return msgs, nil
}

// Append rewrites the session file with the new messages added.
func (s *FileStore) Append(_ context.Context, sessionID string, msgs ...core.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.read(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("agentkit: create store dir: %w", err)
	}
	b, err := json.Marshal(append(existing, msgs...))
	if err != nil {
		return fmt.Errorf("agentkit: encode session: %w", err)
	}
	return atomicfile.WriteFile(s.path(sessionID), b, 0o600)
}

// Delete removes the session file.
func (s *FileStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(sessionID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agentkit: delete session: %w", err)
	}
	return nil
}
