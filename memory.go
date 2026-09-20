package agentkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

// SessionInfo summarizes a stored session without loading it.
type SessionInfo struct {
	ID       string
	Created  time.Time
	Updated  time.Time
	Messages int
	Title    string // first line of the first user message, at most 80 runes
}

// Lister is implemented by stores that can enumerate their sessions, most
// recently updated first.
type Lister interface {
	List(ctx context.Context) ([]SessionInfo, error)
}

const titleMaxRunes = 80

// sessionTitle derives a title from the first user message with text.
func sessionTitle(msgs []core.Message) string {
	for _, m := range msgs {
		if m.Role != core.RoleUser {
			continue
		}
		text := strings.TrimSpace(m.Text())
		if text == "" {
			continue
		}
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		if utf8.RuneCountInString(text) > titleMaxRunes {
			runes := []rune(text)
			text = string(runes[:titleMaxRunes-1]) + "…"
		}
		return text
	}
	return ""
}

func sortSessions(infos []SessionInfo) {
	sort.Slice(infos, func(i, j int) bool {
		if !infos[i].Updated.Equal(infos[j].Updated) {
			return infos[i].Updated.After(infos[j].Updated)
		}
		return infos[i].ID < infos[j].ID
	})
}

type memSession struct {
	created time.Time
	updated time.Time
	msgs    []core.Message
}

// MemoryStore keeps sessions in process memory.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]*memSession
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string]*memSession{}}
}

// Load returns a copy of the session's messages.
func (s *MemoryStore) Load(_ context.Context, sessionID string) ([]core.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ses, ok := s.sessions[sessionID]; ok {
		return append([]core.Message(nil), ses.msgs...), nil
	}
	return nil, nil
}

// Append adds messages to the session.
func (s *MemoryStore) Append(_ context.Context, sessionID string, msgs ...core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	ses, ok := s.sessions[sessionID]
	if !ok {
		ses = &memSession{created: now}
		s.sessions[sessionID] = ses
	}
	ses.updated = now
	ses.msgs = append(ses.msgs, msgs...)
	return nil
}

// Delete forgets the session.
func (s *MemoryStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	return nil
}

// List implements Lister.
func (s *MemoryStore) List(_ context.Context) ([]SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	infos := make([]SessionInfo, 0, len(s.sessions))
	for id, ses := range s.sessions {
		infos = append(infos, SessionInfo{ID: id, Created: ses.created, Updated: ses.updated, Messages: len(ses.msgs), Title: sessionTitle(ses.msgs)})
	}
	sortSessions(infos)
	return infos, nil
}

const (
	sessionExt       = ".jsonl"
	legacySessionExt = ".json"
)

// record is one line of a session file.
type record struct {
	T time.Time    `json:"t"`
	M core.Message `json:"m"`
}

// FileStore keeps one append-only JSON Lines file per session under a
// directory: each line is {"t":"<RFC3339Nano>","m":<Message>} and Append
// writes with O_APPEND and fsync, so an interrupted write costs at most the
// line being written, which Load drops. Sessions written by earlier versions
// as one JSON array (<id>.json) are still readable and are migrated to the
// line format on their first Append. The directory is created on first Append.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore returns a FileStore rooted at dir; no I/O happens here.
func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

func (s *FileStore) path(sessionID string) string { return s.file(sessionID, false) }

func (s *FileStore) legacyPath(sessionID string) string { return s.file(sessionID, true) }

func (s *FileStore) file(sessionID string, legacy bool) string {
	ext := sessionExt
	if legacy {
		ext = legacySessionExt
	}
	return filepath.Join(s.dir, url.PathEscape(sessionID)+ext)
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
		return s.readLegacy(sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("agentkit: read session: %w", err)
	}
	recs, err := decodeLines(b)
	if err != nil {
		return nil, fmt.Errorf("agentkit: decode session %q: %w", sessionID, err)
	}
	msgs := make([]core.Message, len(recs))
	for i, r := range recs {
		msgs[i] = r.M
	}
	return msgs, nil
}

func (s *FileStore) readLegacy(sessionID string) ([]core.Message, error) {
	b, err := os.ReadFile(s.legacyPath(sessionID))
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

// decodeLines parses records, dropping a torn trailing line but rejecting
// corruption anywhere else.
func decodeLines(b []byte) ([]record, error) {
	lines := bytes.Split(b, []byte{'\n'})
	recs := make([]record, 0, len(lines))
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			if i == len(lines)-1 {
				break
			}
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

func encodeLines(msgs []core.Message, at time.Time) ([]byte, error) {
	var buf bytes.Buffer
	for _, m := range msgs {
		line, err := json.Marshal(record{T: at, M: m})
		if err != nil {
			return nil, fmt.Errorf("agentkit: encode session: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// Append adds msgs to the session file.
func (s *FileStore) Append(_ context.Context, sessionID string, msgs ...core.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("agentkit: create store dir: %w", err)
	}
	data, err := encodeLines(msgs, time.Now())
	if err != nil {
		return err
	}
	if _, err := os.Stat(s.path(sessionID)); errors.Is(err, os.ErrNotExist) {
		if migrated, err := s.migrate(sessionID, data); err != nil || migrated {
			return err
		}
	} else if err := s.repairTail(sessionID); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("agentkit: open session: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("agentkit: write session: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("agentkit: sync session: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("agentkit: close session: %w", err)
	}
	return nil
}

// migrate rewrites a legacy JSON-array session as lines followed by data,
// atomically, then removes the legacy file. It reports false when there is
// nothing to migrate.
func (s *FileStore) migrate(sessionID string, data []byte) (bool, error) {
	legacy := s.legacyPath(sessionID)
	info, err := os.Stat(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("agentkit: stat session: %w", err)
	}
	old, err := s.readLegacy(sessionID)
	if err != nil {
		return false, err
	}
	head, err := encodeLines(old, info.ModTime())
	if err != nil {
		return false, err
	}
	if err := atomicfile.WriteFile(s.path(sessionID), append(head, data...), 0o600); err != nil {
		return false, err
	}
	if err := os.Remove(legacy); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("agentkit: remove legacy session: %w", err)
	}
	return true, nil
}

// tailScanChunk is how much repairTail reads at a time when it has to look
// for the last line boundary.
const tailScanChunk = 64 << 10

// repairTail truncates a torn trailing line so the next append starts on a
// line boundary instead of gluing itself to the fragment.
//
// It reads the final byte rather than the whole file. Append calls this every
// time, and a session grows with every call, so reading all of it made
// appending to a session cost time proportional to its size — which no caller
// noticed while a whole run was one append, and every caller would notice now
// that a run appends once per step. A file that was closed cleanly ends in a
// newline, so the common case is one byte.
func (s *FileStore) repairTail(sessionID string) error {
	f, err := os.Open(s.path(sessionID))
	if err != nil {
		return fmt.Errorf("agentkit: read session: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("agentkit: read session: %w", err)
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], size-1); err != nil {
		return fmt.Errorf("agentkit: read session: %w", err)
	}
	if last[0] == '\n' {
		return nil
	}
	keep, err := lastLineEnd(f, size)
	if err != nil {
		return err
	}
	if err := os.Truncate(s.path(sessionID), keep); err != nil {
		return fmt.Errorf("agentkit: repair session: %w", err)
	}
	return nil
}

// lastLineEnd returns the offset just past the file's last newline, scanning
// backwards. It walks to the start of the file rather than giving up at a
// window: one record can be larger than any chunk size — a tool result holding
// a whole file, say — and a torn line that long must still be found.
func lastLineEnd(f *os.File, size int64) (int64, error) {
	buf := make([]byte, tailScanChunk)
	for end := size; end > 0; {
		start := max(end-tailScanChunk, 0)
		n := int(end - start)
		if _, err := f.ReadAt(buf[:n], start); err != nil {
			return 0, fmt.Errorf("agentkit: read session: %w", err)
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	// No newline anywhere: the whole file is one torn line.
	return 0, nil
}

// Delete removes the session file (and any legacy file).
func (s *FileStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range []string{s.path(sessionID), s.legacyPath(sessionID)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("agentkit: delete session: %w", err)
		}
	}
	return nil
}

// List implements Lister by scanning the directory; a missing directory is
// an empty store. Created comes from the first record (or the file's mtime for
// legacy files), Updated from the mtime.
func (s *FileStore) List(_ context.Context) ([]SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentkit: list sessions: %w", err)
	}
	byID := map[string]SessionInfo{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != sessionExt && ext != legacySessionExt {
			continue
		}
		id, err := url.PathUnescape(strings.TrimSuffix(e.Name(), ext))
		if err != nil {
			continue
		}
		if _, seen := byID[id]; seen && ext == legacySessionExt {
			continue // a leftover legacy file next to its migrated copy
		}
		info, err := s.info(id, ext == legacySessionExt)
		if err != nil {
			return nil, err
		}
		byID[id] = info
	}
	infos := make([]SessionInfo, 0, len(byID))
	for _, info := range byID {
		infos = append(infos, info)
	}
	sortSessions(infos)
	return infos, nil
}

func (s *FileStore) info(sessionID string, legacy bool) (SessionInfo, error) {
	st, err := os.Stat(s.file(sessionID, legacy))
	if err != nil {
		return SessionInfo{}, fmt.Errorf("agentkit: stat session: %w", err)
	}
	info := SessionInfo{ID: sessionID, Created: st.ModTime(), Updated: st.ModTime()}
	if legacy {
		msgs, err := s.readLegacy(sessionID)
		if err != nil {
			return SessionInfo{}, err
		}
		info.Messages, info.Title = len(msgs), sessionTitle(msgs)
		return info, nil
	}
	b, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		return SessionInfo{}, fmt.Errorf("agentkit: read session: %w", err)
	}
	recs, err := decodeLines(b)
	if err != nil {
		return SessionInfo{}, fmt.Errorf("agentkit: decode session %q: %w", sessionID, err)
	}
	msgs := make([]core.Message, len(recs))
	for i, r := range recs {
		msgs[i] = r.M
	}
	if len(recs) > 0 && !recs[0].T.IsZero() {
		info.Created = recs[0].T
		if info.Updated.Before(info.Created) {
			info.Updated = info.Created // the kernel's mtime clock is coarser than time.Now
		}
	}
	info.Messages, info.Title = len(recs), sessionTitle(msgs)
	return info, nil
}
