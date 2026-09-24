//go:build integration

package skillruntime_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hitk/hitk/internal/skillcatalog"
	"github.com/hitk/hitk/internal/skillmodel"
	"github.com/hitk/hitk/internal/skillruntime"
)

func TestSkillHubBatch2SimpleSkills(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("HITK_SKILLHUB_BATCH2_ROOT"))
	runtimeRoot := strings.TrimSpace(os.Getenv("HITK_RUNTIME_ROOT"))
	if root == "" || runtimeRoot == "" {
		t.Skip("HITK_SKILLHUB_BATCH2_ROOT and HITK_RUNTIME_ROOT are required")
	}
	dataRoot := t.TempDir()
	installer := skillcatalog.NewInstaller(dataRoot)
	names := []string{"json", "csv", "regex", "json-processor", "uuid-generator-tool", "color-converter"}
	for _, name := range names {
		archive := filepath.Join(root, name, name+".zip")
		if _, err := installer.Install(context.Background(), skillcatalog.InstallRequest{
			ID: name, Name: name, Source: "zip", Path: archive, SHA256: integrationFileDigest(t, archive),
		}); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
	}
	catalog := skillcatalog.NewCatalog(runtimeRoot, dataRoot)
	catalog.SetMode(skillmodel.CompatFull)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for _, name := range []string{"json", "csv", "regex"} {
		pkg, err := catalog.Get(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if pkg.Kind != skillmodel.SkillKindPrompt || strings.TrimSpace(pkg.Body) == "" {
			t.Fatalf("%s should be prompt-only: %#v", name, pkg)
		}
	}

	manager := skillruntime.NewManager(runtimeRoot, dataRoot)
	resolver := skillcatalog.NewResolver(runtimeRoot)
	workspace := t.TempDir()

	jsonFile := filepath.Join(workspace, "data.json")
	if err := os.WriteFile(jsonFile, []byte(`{"store":{"book":[{"title":"HitK"},{"title":"SkillHub"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonResult := executeNamedSkill(t, ctx, catalog, resolver, manager, "json-processor", skillmodel.SkillRequest{
		Command: "query", Args: []string{jsonFile, "$.store.book[0].title"}, Workspace: workspace,
	})
	if !strings.Contains(jsonResult.Content, "HitK") {
		t.Fatalf("unexpected JSON query output: %s", jsonResult.Content)
	}

	uuidResult := executeNamedSkill(t, ctx, catalog, resolver, manager, "uuid-generator-tool", skillmodel.SkillRequest{
		Args: []string{"--count", "3"}, Workspace: workspace,
	})
	lines := strings.Fields(strings.TrimSpace(uuidResult.Content))
	if len(lines) < 3 {
		t.Fatalf("unexpected UUID output: %s", uuidResult.Content)
	}

	colorResult := executeNamedSkill(t, ctx, catalog, resolver, manager, "color-converter", skillmodel.SkillRequest{
		Args: []string{"#3B82F6", "--output", "hsl"}, Workspace: workspace,
	})
	if !strings.Contains(strings.ToLower(colorResult.Content), "hsl") {
		t.Fatalf("unexpected color output: %s", colorResult.Content)
	}
}

func executeNamedSkill(
	t *testing.T,
	ctx context.Context,
	catalog *skillcatalog.Catalog,
	resolver *skillcatalog.Resolver,
	manager *skillruntime.Manager,
	name string,
	request skillmodel.SkillRequest,
) skillmodel.SkillExecutionResult {
	t.Helper()
	pkg, err := catalog.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	plan := analyzeResolve(t, ctx, resolver, pkg)
	if _, err := manager.PrepareResolvedPlan(ctx, plan, true); err != nil {
		t.Fatalf("prepare %s: %v", name, err)
	}
	plan, err = manager.LoadResolvedPlan(pkg.ID, pkg.SourceHash, pkg.Root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ExecuteResolved(ctx, plan, request)
	if err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return result
}
