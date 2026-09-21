package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/protocol"
)

// ChatService drives conversations from the frontend.
//
// Every method here is either a query the UI can render directly or a
// submission that starts asynchronous work. Nothing blocks on the model: a turn
// is started and the UI learns what happened through the event stream, exactly
// as the Codex submission/event split intends.
type ChatService struct{ c *Container }

// NewChatService builds the service.
func NewChatService(c *Container) *ChatService { return &ChatService{c: c} }

// ServiceName implements [application.ServiceName].
func (*ChatService) ServiceName() string { return "Chat" }

// ListThreads returns the conversation list.
func (s *ChatService) ListThreads() []protocol.ThreadSummary {
	threads, err := s.c.Sessions.List()
	if err != nil {
		return []protocol.ThreadSummary{}
	}
	return threads
}

// CreateThread starts a new conversation using the current defaults.
func (s *ChatService) CreateThread(mode string) (protocol.Thread, error) {
	cfg := s.c.Config.Get()
	m := protocol.Mode(mode)
	if !m.Valid() {
		m = cfg.Mode
		if !m.Valid() {
			m = protocol.ModeWork
		}
	}
	t, err := s.c.Sessions.Create(m, cfg.Provider, cfg.Model)
	if err != nil {
		return protocol.Thread{}, err
	}
	return *t, nil
}

// GetThread loads a conversation with its items.
func (s *ChatService) GetThread(id string) (protocol.Thread, error) {
	t, err := s.c.Sessions.Get(id)
	if err != nil {
		return protocol.Thread{}, err
	}
	return *t, nil
}

// DeleteThread removes a conversation.
func (s *ChatService) DeleteThread(id string) error {
	return s.c.Sessions.Delete(id)
}

// RenameThread retitles a conversation.
func (s *ChatService) RenameThread(id, title string) error {
	return s.c.Sessions.Rename(id, title)
}

// PinThread pins or unpins a conversation.
func (s *ChatService) PinThread(id string, pinned bool) error {
	return s.c.Sessions.Pin(id, pinned)
}

// ClearThreadItems empties a conversation without deleting it.
func (s *ChatService) ClearThreadItems(id string) error {
	t, err := s.c.Sessions.Get(id)
	if err != nil {
		return err
	}
	t.Items = nil
	t.Preview = ""
	return s.c.Sessions.Save(t)
}

// SubmitTurn starts a turn. The protocol.Op carries the mode, model, knowledge
// base scope and approval policy for this specific submission, so the frontend
// can vary them per message without touching global settings.
func (s *ChatService) SubmitTurn(op protocol.Op) error {
	if op.Kind == "" {
		op.Kind = protocol.OpTurnStart
	}
	switch op.Kind {
	case protocol.OpTurnStart:
		return s.c.Agent.StartTurn(op)
	case protocol.OpInterrupt:
		return s.c.Agent.Interrupt(op.ThreadID)
	default:
		return fmt.Errorf("不支持的提交类型: %s", op.Kind)
	}
}

// Interrupt stops the turn running on a thread.
func (s *ChatService) Interrupt(threadID string) error {
	return s.c.Agent.Interrupt(threadID)
}

// RespondApproval answers a pending tool approval request.
func (s *ChatService) RespondApproval(threadID, approvalID string, approve, remember bool) error {
	return s.c.Agent.RespondApproval(threadID, approvalID, approve, remember)
}

// Running reports whether a thread currently has work in flight, so a reopened
// conversation can restore its "generating" state.
func (s *ChatService) Running(threadID string) bool {
	return s.c.Agent.Running(threadID)
}

// UpdateThreadSettings stores the provider/model/mode choice on the thread.
func (s *ChatService) UpdateThreadSettings(threadID, providerID, model, mode string) error {
	t, err := s.c.Sessions.Get(threadID)
	if err != nil {
		return err
	}
	if providerID != "" {
		t.Provider = providerID
	}
	if model != "" {
		t.Model = model
	}
	if m := protocol.Mode(mode); m.Valid() {
		t.Mode = m
	}
	return s.c.Sessions.Save(t)
}

// ExportThread renders a conversation as Markdown and optionally writes it to
// disk. It returns the Markdown either way so the UI can offer a copy button.
func (s *ChatService) ExportThread(threadID string, saveToFile bool) (string, error) {
	t, err := s.c.Sessions.Get(threadID)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", t.Title)
	fmt.Fprintf(&b, "> 模型：%s / %s　创建于 %s\n\n", t.Provider, t.Model, formatTime(t.CreatedAt))

	// Citations are collected so the export ends with a source list — the same
	// contract the model follows in its answers.
	seen := map[string]bool{}
	var sources []string

	for _, it := range t.Items {
		switch it.Type {
		case protocol.ItemUserMessage:
			fmt.Fprintf(&b, "## 用户\n\n%s\n\n", it.Text)
		case protocol.ItemAgentMessage:
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			fmt.Fprintf(&b, "## HitK\n\n%s\n\n", it.Text)
		case protocol.ItemToolCall:
			if it.Tool == nil {
				continue
			}
			fmt.Fprintf(&b, "<details><summary>工具调用：%s</summary>\n\n```json\n%s\n```\n\n", toolLabel(it), it.Tool.Arguments)
			if it.Tool.Result != "" {
				fmt.Fprintf(&b, "```\n%s\n```\n\n", clip(it.Tool.Result, 4000))
			}
			b.WriteString("</details>\n\n")
		case protocol.ItemNotice:
			fmt.Fprintf(&b, "> %s\n\n", it.Text)
		case protocol.ItemError:
			fmt.Fprintf(&b, "> ⚠️ %s\n\n", it.Error)
		}
		for _, ref := range it.Refs {
			key := ref.DocTitle + "|" + ref.Path
			if ref.DocTitle == "" || seen[key] {
				continue
			}
			seen[key] = true
			label := ref.DocTitle
			if ref.Path != "" {
				label += " — " + ref.Path
			}
			sources = append(sources, label)
		}
	}

	if len(sources) > 0 {
		sort.Strings(sources)
		b.WriteString("## 参考来源\n\n")
		for i, src := range sources {
			fmt.Fprintf(&b, "%d. %s\n", i+1, src)
		}
	}

	content := b.String()
	if saveToFile {
		name := sanitizeFilename(t.Title) + ".md"
		path, err := pickSaveFile("导出会话", name)
		if err != nil || path == "" {
			return content, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return content, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return content, err
		}
	}
	return content, nil
}

func toolLabel(it *protocol.Item) string {
	if it.Tool.DisplayName != "" {
		return it.Tool.DisplayName
	}
	if it.Tool.Name != "" {
		return it.Tool.Name
	}
	return "工具"
}

func formatTime(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return timeFromMS(ms).Format("2006-01-02 15:04")
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "conversation"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", "\n", " ",
	)
	out := replacer.Replace(s)
	if len([]rune(out)) > 50 {
		out = string([]rune(out)[:50])
	}
	return strings.TrimSpace(out)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…（已截断）"
}
