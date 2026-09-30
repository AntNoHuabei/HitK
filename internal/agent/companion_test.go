package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

type fakeCompanionRuntime struct {
	mu        sync.Mutex
	context   string
	userText  string
	assistant string
}

func (f *fakeCompanionRuntime) PrepareCompanionTurn(context.Context, string, string, string, string) (string, error) {
	return f.context, nil
}

func (f *fakeCompanionRuntime) CompleteCompanionTurn(_, _, _, userText, assistantText string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userText = userText
	f.assistant = assistantText
}

func (f *fakeCompanionRuntime) FailCompanionTurn(_, _, _, _ string) {}

func TestCompanionModeInjectsContextWithoutTools(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{{
		{Kind: provider.ChunkText, Text: "<action>她合上电脑。</action><speech>我刚看完一部电影。</speech>"},
		{Kind: provider.ChunkDone},
	}})
	companion := &fakeCompanionRuntime{context: "【当前状态】\n心情轻快。"}
	h.agent.companion = companion

	if err := h.agent.StartTurn(protocol.Op{
		Kind:            protocol.OpTurnStart,
		ThreadID:        h.thread.ID,
		Input:           []protocol.InputPart{{Type: "text", Text: "今天怎么样？"}},
		Mode:            protocol.ModeCompanion,
		Provider:        "scripted",
		Model:           "test-model",
		CompanionTaskID: "task_test",
	}); err != nil {
		t.Fatal(err)
	}
	h.events.waitFor(t, "companion turn complete", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnCompleted)
	})

	requests := h.scripted.requests()
	if len(requests) == 0 {
		t.Fatal("model was not called")
	}
	if !strings.Contains(requests[0].System, "心情轻快") {
		t.Fatalf("system prompt lost companion context: %q", requests[0].System)
	}
	if len(requests[0].Tools) != 0 {
		t.Fatalf("companion turn exposed %d tools, want 0", len(requests[0].Tools))
	}
	companion.mu.Lock()
	defer companion.mu.Unlock()
	if companion.userText != "今天怎么样？" || companion.assistant != "<action>她合上电脑。</action>\n<speech>我刚看完一部电影。</speech>" {
		t.Fatalf("recorded turn = %q / %q", companion.userText, companion.assistant)
	}
}

func TestCompanionHistoryCutoffKeepsRecentTurns(t *testing.T) {
	items := make([]*protocol.Item, 0, 12)
	for i := 0; i < 6; i++ {
		items = append(items,
			&protocol.Item{Type: protocol.ItemUserMessage, Text: "user"},
			&protocol.Item{Type: protocol.ItemAgentMessage, Text: "assistant"},
		)
	}
	if got := companionHistoryCutoff(items, 6); got != 6 {
		t.Fatalf("cutoff = %d, want 6", got)
	}
	if got := companionHistoryCutoff(items[:4], 6); got != 0 {
		t.Fatalf("short history cutoff = %d, want 0", got)
	}
}
