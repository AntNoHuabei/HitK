package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hitk/hitk/internal/protocol"
)

// SessionStore persists conversation threads as one JSON file each.
//
// Files rather than a database: a thread is written once per turn and read once
// on open, so the access pattern gains nothing from an index, and a plain file
// keeps a user's conversation history inspectable and easy to back up.
type SessionStore struct {
	dir string

	mu    sync.Mutex
	cache map[string]*protocol.Thread
}

// NewSessionStore opens (creating if needed) the thread directory.
func NewSessionStore(dir string) (*SessionStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &SessionStore{dir: dir, cache: map[string]*protocol.Thread{}}, nil
}

// Dir reports where threads live.
func (s *SessionStore) Dir() string { return s.dir }

func (s *SessionStore) path(id string) string {
	// Thread ids are generated, but sanitising anyway prevents a malformed id
	// from escaping the thread directory.
	return filepath.Join(s.dir, sanitizeID(id)+".json")
}

// Create starts a new empty thread.
func (s *SessionStore) Create(mode protocol.Mode, provider, model string) (*protocol.Thread, error) {
	now := time.Now().UnixMilli()
	t := &protocol.Thread{
		ThreadSummary: protocol.ThreadSummary{
			ID:        "th_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16],
			Title:     "新会话",
			Mode:      mode,
			Provider:  provider,
			Model:     model,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Items: []*protocol.Item{},
	}
	if err := s.Save(t); err != nil {
		return nil, err
	}
	return t, nil
}

// Get loads a thread, preferring the in-memory copy.
func (s *SessionStore) Get(id string) (*protocol.Thread, error) {
	s.mu.Lock()
	if t, ok := s.cache[id]; ok {
		s.mu.Unlock()
		return t, nil
	}
	s.mu.Unlock()

	raw, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, fmt.Errorf("会话不存在或已删除")
	}
	var t protocol.Thread
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("会话文件损坏: %w", err)
	}
	if t.Items == nil {
		t.Items = []*protocol.Item{}
	}

	s.mu.Lock()
	s.cache[id] = &t
	s.mu.Unlock()
	return &t, nil
}

// Save writes a thread to disk and refreshes its summary fields.
func (s *SessionStore) Save(t *protocol.Thread) error {
	t.UpdatedAt = time.Now().UnixMilli()
	t.ItemCount = len(t.Items)
	if t.Preview == "" {
		for _, it := range t.Items {
			if it.Type == protocol.ItemUserMessage && strings.TrimSpace(it.Text) != "" {
				t.Preview = clipRunes(it.Text, 80)
				break
			}
		}
	}
	if t.Title == "" || t.Title == "新会话" {
		for _, it := range t.Items {
			if it.Type == protocol.ItemUserMessage && strings.TrimSpace(it.Text) != "" {
				t.Title = conversationLabel(it.Text)
				break
			}
		}
	}

	raw, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.cache[t.ID] = t
	path := s.path(t.ID)
	s.mu.Unlock()

	// Write to a temporary file and rename, so a crash mid-write cannot leave
	// a truncated thread that fails to parse on next launch.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// List returns thread summaries, most recently updated first.
func (s *SessionStore) List() ([]protocol.ThreadSummary, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.ThreadSummary, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var t protocol.Thread
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		// The cached copy is authoritative while a turn is streaming.
		s.mu.Lock()
		if cached, ok := s.cache[t.ID]; ok {
			t.ThreadSummary = cached.ThreadSummary
		}
		s.mu.Unlock()
		out = append(out, t.ThreadSummary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

// Delete removes a thread.
func (s *SessionStore) Delete(id string) error {
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
	err := os.Remove(s.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Rename sets a thread's title.
func (s *SessionStore) Rename(id, title string) error {
	t, err := s.Get(id)
	if err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("标题不能为空")
	}
	t.Title = clipRunes(title, 60)
	return s.Save(t)
}

// Pin toggles a thread's pinned flag.
func (s *SessionStore) Pin(id string, pinned bool) error {
	t, err := s.Get(id)
	if err != nil {
		return err
	}
	t.Pinned = pinned
	return s.Save(t)
}

// sanitizeID keeps only characters that are safe in a filename.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "thread"
	}
	return b.String()
}
