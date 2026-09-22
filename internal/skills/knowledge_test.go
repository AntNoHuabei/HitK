package skills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hitk/hitk/internal/kb"
)

func knowledgeFixture(t *testing.T) (*Context, kb.Collection, kb.Collection) {
	t.Helper()
	store, err := kb.Open(filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	allowed, err := store.CreateCollection("允许库", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateCollection("其他库", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddParsed(context.Background(), allowed.ID, kb.ParseText("协议说明", strings.Repeat("缓存前缀稳定。", 1200)), "test://prompt", "text", kb.IngestOptions{Title: "协议说明"})
	if err != nil {
		t.Fatal(err)
	}
	return &Context{Ctx: context.Background(), KB: store, KnowledgeBases: []string{allowed.ID}}, allowed, other
}

func TestKnowledgeCommandsAndScope(t *testing.T) {
	ctx, _, other := knowledgeFixture(t)
	if _, err := RunKnowledge(ctx, "unknown", nil); err == nil {
		t.Fatal("unknown command was accepted")
	}
	if _, err := RunKnowledge(ctx, "search", []string{"--help"}); err == nil {
		t.Fatal("help argument was accepted")
	}
	if _, err := RunKnowledge(ctx, "search", []string{"--query", "缓存", "--collection", other.ID}); err == nil {
		t.Fatal("out-of-scope collection was accepted")
	}
}

func TestKnowledgeSearchReturnsBoundedShortEvidence(t *testing.T) {
	ctx, _, _ := knowledgeFixture(t)
	result, err := RunKnowledge(ctx, "search", []string{"--query", "缓存前缀", "--limit", "12", "--snippet", "5000"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) > MaxSearchOutputBytes || !utf8.ValidString(result.Content) {
		t.Fatalf("invalid bounded output: bytes=%d utf8=%v", len(result.Content), utf8.ValidString(result.Content))
	}
	if len(result.Refs) == 0 || result.Refs[0].ChunkID == "" {
		t.Fatal("search did not return a navigable reference")
	}
	opened, err := RunKnowledge(ctx, "open", []string{"--chunk-id", result.Refs[0].ChunkID, "--window", "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(opened.Refs) != 1 || opened.Refs[0].DocTitle != "协议说明" {
		t.Fatal("open did not preserve the source reference")
	}
}
