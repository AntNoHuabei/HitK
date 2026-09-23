package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hitk/hitk/internal/document"
)

func TestDocumentSkillExtractAndRejectsHelp(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "input.txt")
	if err := os.WriteFile(path, []byte("runtime skill content"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &DocumentContext{Context: &Context{Ctx: context.Background()}, Documents: document.NewEngine(nil), Workspace: workspace}
	result, err := RunDocument(ctx, "extract", []string{"--path", path})
	if err != nil || !strings.Contains(result.Content, "runtime skill content") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, err := RunDocument(ctx, "extract", []string{"--help"}); err == nil {
		t.Fatal("help argument should be rejected")
	}
	opened, err := RunDocument(ctx, "open", []string{"--path", path, "--offset", "8", "--limit", "1024"})
	if err != nil || !strings.Contains(opened.Content, "skill content") {
		t.Fatalf("open result=%#v err=%v", opened, err)
	}
}
