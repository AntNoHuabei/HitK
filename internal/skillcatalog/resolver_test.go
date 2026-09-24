package skillcatalog

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hitk/hitk/internal/skillmodel"
)

func TestAnalyzePythonImportsAndHints(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.py"), `import os
from PIL import Image
import cv2
import unknown_package

def install():
    _auto_install("yaml", "PyYAML")
`)
	pkg := skillmodel.Package{
		ID:         "python-demo",
		Name:       "python-demo",
		Kind:       skillmodel.SkillKindScript,
		Manifest:   skillmodel.SkillManifest{Runtime: skillmodel.RuntimeConstraint{Kind: "python", Constraint: ">=3.10"}, Entry: "main.py"},
		SourceHash: "source",
		Root:       root,
	}
	resolver := NewResolver(t.TempDir())
	plan, err := resolver.Analyze(context.Background(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, dependency := range plan.Dependencies {
		joined += dependency.PackageName + ","
	}
	for _, expected := range []string{"Pillow", "opencv-python", "PyYAML"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s in %s", expected, joined)
		}
	}
	if len(plan.Unresolved) != 1 || plan.Unresolved[0].ImportName != "unknown_package" {
		t.Fatalf("unexpected unresolved dependencies: %#v", plan.Unresolved)
	}
}

func TestAnalyzeNodeImportsAndScopes(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "index.mjs"), `import express from "express";
import z from "node:fs";
import sub from "@scope/pkg/sub";
const lodash = require("lodash/fp");
`)
	pkg := skillmodel.Package{
		ID:         "node-demo",
		Name:       "node-demo",
		Kind:       skillmodel.SkillKindScript,
		Manifest:   skillmodel.SkillManifest{Runtime: skillmodel.RuntimeConstraint{Kind: "node", Constraint: ">=20"}},
		SourceHash: "node-source",
		Root:       root,
	}
	resolver := NewResolver(t.TempDir())
	plan, err := resolver.Analyze(context.Background(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, dependency := range plan.Dependencies {
		joined += dependency.PackageName + ","
	}
	for _, expected := range []string{"express", "lodash", "@scope/pkg"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s in %s", expected, joined)
		}
	}
	if strings.Contains(joined, "node:fs") || strings.Contains(joined, "fs,") {
		t.Fatalf("node builtin leaked into dependencies: %s", joined)
	}
}

func TestResolvedPlanHashIsStable(t *testing.T) {
	plan := skillmodel.ResolvedPlan{
		Kind:            skillmodel.SkillKindScript,
		Runtime:         skillmodel.RuntimeConstraint{Kind: "python", Constraint: ">=3.10"},
		Environment:     "venv",
		SourceHash:      "abc",
		ResolverVersion: ResolverVersion,
		Dependencies: []skillmodel.DependencyCandidate{
			{Ecosystem: "python", PackageName: "Pillow", Version: "==12.0"},
		},
	}
	first := ComputePlanHash(plan)
	second := ComputePlanHash(plan)
	if first != second || first == "" {
		t.Fatalf("plan hash is not stable: %q %q", first, second)
	}
	plan.Dependencies[0].Version = "==13.0"
	if third := ComputePlanHash(plan); third == first {
		t.Fatal("plan hash did not change with dependencies")
	}
}
