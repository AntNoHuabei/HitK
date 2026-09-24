//go:build integration

package skillruntime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hitk/hitk/internal/skillcatalog"
	"github.com/hitk/hitk/internal/skillmodel"
	"github.com/hitk/hitk/internal/skillruntime"
)

// TestSkillHubWatermarkIntegration exercises the complete compatibility path:
// source import -> catalog -> dependency analysis -> uv lock -> isolated
// environment -> real watermark CLI -> output validation.
//
// Run explicitly with:
//
//	$env:HITK_WATERMARK_ZIP='...\watermarkskill-1.0.0.zip'
//	$env:HITK_RUNTIME_ROOT='D:\code\HitK\build\windows\runtime-stage'
//	go test -tags=integration ./internal/skillruntime -run TestSkillHubWatermarkIntegration -v
func TestSkillHubWatermarkIntegration(t *testing.T) {
	zipPath := strings.TrimSpace(os.Getenv("HITK_WATERMARK_ZIP"))
	runtimeRoot := strings.TrimSpace(os.Getenv("HITK_RUNTIME_ROOT"))
	if zipPath == "" || runtimeRoot == "" {
		t.Skip("HITK_WATERMARK_ZIP and HITK_RUNTIME_ROOT are required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	desktop := filepath.Join(home, "Desktop")
	var images []string
	for _, pattern := range []string{"*.jpg", "*.jpeg", "*.png", "*.webp", "*.bmp", "*.tif", "*.tiff"} {
		matches, globErr := filepath.Glob(filepath.Join(desktop, pattern))
		if globErr != nil {
			t.Fatal(globErr)
		}
		images = append(images, matches...)
	}
	if len(images) == 0 {
		t.Fatalf("no desktop images found")
	}
	sort.Strings(images)
	limit := 4
	if raw := strings.TrimSpace(os.Getenv("HITK_WATERMARK_LIMIT")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			t.Fatalf("invalid HITK_WATERMARK_LIMIT=%q", raw)
		}
	}
	if limit > 0 && len(images) > limit {
		images = images[:limit]
	}

	dataRoot := strings.TrimSpace(os.Getenv("HITK_DATA_ROOT"))
	if dataRoot == "" {
		dataRoot = t.TempDir()
	}
	installer := skillcatalog.NewInstaller(dataRoot)
	if _, err := installer.Install(context.Background(), skillcatalog.InstallRequest{
		ID:        "watermarkskill",
		Name:      "watermark",
		Version:   "1.0.0",
		Source:    "zip",
		Path:      zipPath,
		SHA256:    integrationFileDigest(t, zipPath),
		Publisher: "user_2096565c",
		URL:       "https://skillhub.cn/skills/user_2096565c/watermarkskill",
	}); err != nil {
		t.Fatal(err)
	}

	catalog := skillcatalog.NewCatalog(runtimeRoot, dataRoot)
	catalog.SetMode(skillmodel.CompatFull)
	pkg, err := catalog.Get(context.Background(), "watermark")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.Invocation != skillmodel.InvocationArgv || pkg.Entry != "scripts/watermark.py" {
		t.Fatalf("watermark package was not classified as argv CLI: %#v", pkg.Manifest)
	}

	resolver := skillcatalog.NewResolver(runtimeRoot)
	analysis, err := resolver.Analyze(context.Background(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(context.Background(), pkg, analysis)
	if err != nil {
		t.Fatal(err)
	}
	plan.ID = pkg.ID
	plan.SourceHash = pkg.SourceHash
	plan.SourceRoot = pkg.Root

	manager := skillruntime.NewManager(runtimeRoot, dataRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	status, err := manager.PrepareResolvedPlan(ctx, plan, true)
	if err != nil {
		t.Fatalf("prepare watermark environment: %v\nstatus=%#v", err, status)
	}
	if status.State != skillmodel.StateReady {
		t.Fatalf("watermark environment not ready: %#v", status)
	}
	locked, err := manager.LoadResolvedPlan(pkg.ID, pkg.SourceHash, pkg.Root)
	if err != nil {
		t.Fatal(err)
	}

	outputDir := strings.TrimSpace(os.Getenv("HITK_WATERMARK_OUTPUT"))
	if outputDir == "" {
		outputDir = filepath.Join(dataRoot, "output")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	font := `C:\Windows\Fonts\msyh.ttc`
	for index, image := range images {
		before := integrationFileDigest(t, image)
		result, err := manager.ExecuteResolved(ctx, locked, skillmodel.SkillRequest{
			Args: []string{
				"--input", image,
				"--text", "HitK SkillHub 测试",
				"--font-path", font,
				"--position", "bottom-right",
				"--opacity", "0.55",
				"--output-dir", outputDir,
				"--suffix", "_hitk_test",
				"--overwrite",
			},
			Workspace: outputDir,
		})
		if err != nil {
			t.Fatalf("execute image %d (%s): %v", index+1, filepath.Base(image), err)
		}
		if len(result.Artifacts) == 0 {
			t.Fatalf("image %d produced no artifacts: %#v", index+1, result)
		}
		for _, artifact := range result.Artifacts {
			if integrationFileDigest(t, artifact.Path) == before {
				t.Fatalf("output was not changed: %s", artifact.Path)
			}
		}
	}
}

func integrationFileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
