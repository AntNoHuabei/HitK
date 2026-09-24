package skillruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hitk/hitk/internal/skillmodel"
)

func TestPrepareResolvedPromptIsReadyWithoutEnvironment(t *testing.T) {
	manager := NewManager(t.TempDir(), t.TempDir())
	status, err := manager.PrepareResolvedPlan(context.Background(), skillmodel.ResolvedPlan{
		ID:         "prompt-demo",
		Kind:       skillmodel.SkillKindPrompt,
		SourceHash: "abc",
		PlanHash:   "plan",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != skillmodel.StateReady || status.Environment != "" {
		t.Fatalf("unexpected prompt status: %#v", status)
	}
}

func TestResolvedPlanHashChangesWithLockDigest(t *testing.T) {
	plan := skillmodel.ResolvedPlan{
		ID:              "script-demo",
		Kind:            skillmodel.SkillKindScript,
		Runtime:         skillmodel.RuntimeConstraint{Kind: "python", Constraint: ">=3.10"},
		Environment:     "venv",
		SourceHash:      "source",
		ResolverVersion: "resolver",
		LockDigest:      "one",
	}
	first := ComputeResolvedPlanHash(plan)
	plan.LockDigest = "two"
	second := ComputeResolvedPlanHash(plan)
	if first == second {
		t.Fatal("lock digest did not affect plan hash")
	}
}

func TestCLIExecutionResultParsesWrittenArtifacts(t *testing.T) {
	result := cliExecutionResult("wrote C:\\tmp\\a_wm.jpg\nwrote C:\\tmp\\b_wm.jpg\n")
	if result.Display == "" || len(result.Artifacts) != 2 {
		t.Fatalf("cli output was not parsed: %#v", result)
	}
	if result.Artifacts[0].Name != "a_wm.jpg" || result.Artifacts[0].Operation != "created" {
		t.Fatalf("unexpected artifact: %#v", result.Artifacts[0])
	}
}

func TestPrepareResolvedPlanReusesLockedEnvironmentOffline(t *testing.T) {
	manager := NewManager(t.TempDir(), t.TempDir())
	plan := skillmodel.ResolvedPlan{
		ID:              "script-demo",
		Kind:            skillmodel.SkillKindScript,
		Runtime:         skillmodel.RuntimeConstraint{Kind: "python", Constraint: ">=3.10"},
		Environment:     "venv",
		SourceHash:      "source",
		ResolverVersion: ResolverVersionForTest,
		Dependencies:    []skillmodel.DependencyCandidate{{Ecosystem: "python", PackageName: "Pillow"}},
		Permissions:     skillmodel.SkillPermissions{},
	}
	plan.LockDigest = "locked"
	plan.PlanHash = ComputeResolvedPlanHash(plan)
	if err := os.MkdirAll(manager.resolvedEnvironmentPath(plan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeResolvedPlanFile(manager.resolvedPlanPath(plan), plan); err != nil {
		t.Fatal(err)
	}
	status, err := manager.PrepareResolvedPlan(context.Background(), plan, false)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != skillmodel.StateReady {
		t.Fatalf("expected cached plan to be ready: %#v", status)
	}
	if _, err := os.Stat(filepath.Join(status.Environment)); err != nil {
		t.Fatal(err)
	}
}

const ResolverVersionForTest = "test-resolver"
