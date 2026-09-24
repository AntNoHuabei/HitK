//go:build integration

package skillruntime_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hitk/hitk/internal/skillcatalog"
	"github.com/hitk/hitk/internal/skillmodel"
	"github.com/hitk/hitk/internal/skillruntime"
)

func TestSkillHubRandomSimpleSkills(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("HITK_SKILLHUB_RANDOM_ROOT"))
	runtimeRoot := strings.TrimSpace(os.Getenv("HITK_RUNTIME_ROOT"))
	if root == "" || runtimeRoot == "" {
		t.Skip("HITK_SKILLHUB_RANDOM_ROOT and HITK_RUNTIME_ROOT are required")
	}
	dataRoot := t.TempDir()
	installer := skillcatalog.NewInstaller(dataRoot)
	zips := map[string]string{
		"markdown":               filepath.Join(root, "markdown", "markdown.zip"),
		"text":                   filepath.Join(root, "text", "text.zip"),
		"text-diff":              filepath.Join(root, "text-diff", "text-diff.zip"),
		"cn-timestamp-converter": filepath.Join(root, "cn-timestamp-converter", "cn-timestamp-converter.zip"),
		"encoding-converter":     filepath.Join(root, "encoding-converter", "encoding-converter.zip"),
	}
	for id, archive := range zips {
		if _, err := installer.Install(context.Background(), skillcatalog.InstallRequest{
			ID: id, Name: id, Source: "zip", Path: archive, SHA256: integrationFileDigest(t, archive),
		}); err != nil {
			t.Fatalf("install %s: %v", id, err)
		}
	}
	catalog := skillcatalog.NewCatalog(runtimeRoot, dataRoot)
	catalog.SetMode(skillmodel.CompatFull)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for _, id := range []string{"markdown", "text", "encoding-converter"} {
		pkg, err := catalog.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if pkg.Kind != skillmodel.SkillKindPrompt || strings.TrimSpace(pkg.Body) == "" {
			t.Fatalf("%s should be a non-empty prompt skill: %#v", id, pkg)
		}
	}

	manager := skillruntime.NewManager(runtimeRoot, dataRoot)
	resolver := skillcatalog.NewResolver(runtimeRoot)

	diffPkg, err := catalog.Get(ctx, "text-diff")
	if err != nil {
		t.Fatal(err)
	}
	if diffPkg.Entry != "text_diff.py" || diffPkg.Manifest.Invocation != skillmodel.InvocationArgv {
		t.Fatalf("text-diff entry inference failed: %#v", diffPkg)
	}
	diffPlan := analyzeResolve(t, ctx, resolver, diffPkg)
	if _, err := manager.PrepareResolvedPlan(ctx, diffPlan, true); err != nil {
		t.Fatalf("prepare text-diff: %v", err)
	}
	diffPlan, err = manager.LoadResolvedPlan(diffPkg.ID, diffPkg.SourceHash, diffPkg.Root)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	oldFile := filepath.Join(workspace, "old.txt")
	newFile := filepath.Join(workspace, "new.txt")
	if err := os.WriteFile(oldFile, []byte("alpha\nold line\nomega\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("alpha\nnew line\nomega\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffResult, err := manager.ExecuteResolved(ctx, diffPlan, skillmodel.SkillRequest{
		Command: "unified",
		Args: []string{
			oldFile,
			newFile,
			"--no-color",
		},
		Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("execute text-diff: %v", err)
	}
	if !strings.Contains(diffResult.Content, "-old line") || !strings.Contains(diffResult.Content, "+new line") {
		t.Fatalf("unexpected diff output: %s", diffResult.Content)
	}

	timestampPkg, err := catalog.Get(ctx, "cn-timestamp-converter")
	if err != nil {
		t.Fatal(err)
	}
	if timestampPkg.Entry != "scripts/timestamp_converter.py" {
		t.Fatalf("timestamp entry inference failed: %#v", timestampPkg)
	}
	timestampPlan := analyzeResolve(t, ctx, resolver, timestampPkg)
	if _, err := manager.PrepareResolvedPlan(ctx, timestampPlan, true); err != nil {
		t.Fatalf("prepare timestamp: %v", err)
	}
	timestampPlan, err = manager.LoadResolvedPlan(timestampPkg.ID, timestampPkg.SourceHash, timestampPkg.Root)
	if err != nil {
		t.Fatal(err)
	}
	timestampResult, err := manager.ExecuteResolved(ctx, timestampPlan, skillmodel.SkillRequest{
		Args:      []string{"1745800000"},
		Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("execute timestamp: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(timestampResult.Content), &payload); err != nil {
		t.Fatalf("timestamp output is not JSON: %v\n%s", err, timestampResult.Content)
	}
	if payload["unix"].(float64) != 1745800000 {
		t.Fatalf("unexpected timestamp payload: %#v", payload)
	}
}

func analyzeResolve(t *testing.T, ctx context.Context, resolver *skillcatalog.Resolver, pkg skillmodel.Package) skillmodel.ResolvedPlan {
	t.Helper()
	analysis, err := resolver.Analyze(ctx, pkg)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(ctx, pkg, analysis)
	if err != nil {
		t.Fatal(err)
	}
	plan.ID = pkg.ID
	plan.SourceHash = pkg.SourceHash
	plan.SourceRoot = pkg.Root
	return plan
}
