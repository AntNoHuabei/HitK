package document

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextExtractionAndFallbackConversion(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "notes.md")
	if err := os.WriteFile(path, []byte("# 标题\n\n需要保留的内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(nil)
	extracted, err := engine.Extract(context.Background(), path, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if extracted.Format != "md" || !strings.Contains(extracted.Text, "需要保留") {
		t.Fatalf("unexpected extraction: %#v", extracted)
	}
	converted, err := engine.Convert(context.Background(), path, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Artifacts) != 2 {
		t.Fatalf("artifacts = %#v", converted.Artifacts)
	}
	for _, artifact := range converted.Artifacts {
		rel, relErr := filepath.Rel(workspace, artifact.Path)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("artifact escaped workspace: %s", artifact.Path)
		}
		if _, statErr := os.Stat(artifact.Path); statErr != nil {
			t.Fatal(statErr)
		}
	}
}

func TestOfficeAndImageExtensionsAreSupported(t *testing.T) {
	for _, name := range []string{"deck.pptx", "table.xlsx", "scan.png"} {
		if !Supported(name) {
			t.Errorf("%s should be supported", name)
		}
	}
}
