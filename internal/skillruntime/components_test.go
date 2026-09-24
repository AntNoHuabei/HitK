package skillruntime

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestComponentDownloadUsesManifestAndBecomesReady(t *testing.T) {
	runtimeRoot, dataRoot := t.TempDir(), t.TempDir()
	writeRuntimeVersion(t, runtimeRoot, "python", "3.12.14")

	archive := componentZip(t, map[string]string{"site-packages/docling/__init__.py": "VERSION = 'test'\n"})
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	skillDir := filepath.Join(runtimeRoot, "skills", "document")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(skillDir, "skill.json"), SkillManifest{
		SchemaVersion: ManifestSchemaVersion,
		Name:          "document",
		Builtin:       true,
		Runtime:       RuntimeConstraint{Kind: "python", Constraint: "==3.12.*"},
		Entry:         "main.py",
		Assets:        []string{"site-packages/docling"},
	})
	if err := os.WriteFile(filepath.Join(skillDir, "main.py"), []byte("print('{}')"), 0o644); err != nil {
		t.Fatal(err)
	}
	component := ComponentSpec{
		ID:              "document-docling",
		Skill:           "document",
		Version:         "1.0.0",
		Platform:        runtime.GOOS + "-" + runtime.GOARCH,
		Source:          "test",
		URL:             server.URL,
		SHA256:          hex.EncodeToString(sum[:]),
		ArchiveType:     "zip",
		CompressedBytes: int64(len(archive)),
		UnpackedBytes:   int64(len("VERSION = 'test'\n")),
	}
	writeJSON(t, filepath.Join(runtimeRoot, "runtime-manifest.json"), RuntimeManifest{
		SchemaVersion: ManifestSchemaVersion,
		BundleVersion: "test",
		Platform:      component.Platform,
		Runtimes:      []RuntimeSpec{{Name: "python", Version: "3.12.14"}},
		Components:    []ComponentSpec{component},
	})

	manager := NewManager(runtimeRoot, dataRoot)
	if status := manager.GetSkillEnvironment("document"); status.State != "approval_required" || status.ComponentVersion != "1.0.0" {
		t.Fatalf("unexpected initial status: %#v", status)
	}
	if _, err := manager.PrepareSkillEnvironment(context.Background(), "document", false); !errors.Is(err, ErrNetworkApprovalRequired) {
		t.Fatalf("missing approval error: %v", err)
	}

	var mu sync.Mutex
	var states []string
	manager.SetProgressPublisher(func(progress RuntimeProgress) {
		if progress.Skill == "document" {
			mu.Lock()
			states = append(states, progress.Status.State)
			mu.Unlock()
		}
	})
	status, err := manager.PrepareSkillEnvironment(context.Background(), "document", true)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "ready" || !dirExists(status.Environment) {
		t.Fatalf("component not ready: %#v", status)
	}
	if !fileExists(filepath.Join(status.Environment, "site-packages", "docling", "__init__.py")) {
		t.Fatal("component payload was not installed")
	}
	if fileExists(filepath.Join(manager.componentRoot(component), "document-"+component.SHA256[:12]+".zip")) {
		t.Fatal("downloaded archive should be removed after installation")
	}
	mu.Lock()
	joined := strings.Join(states, ",")
	mu.Unlock()
	if !strings.Contains(joined, "downloading") || !strings.Contains(joined, "verifying") || !strings.Contains(joined, "installing") || !strings.Contains(joined, "ready") {
		t.Fatalf("missing progress states: %s", joined)
	}
}

func componentZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeRuntimeVersion(t *testing.T, root, name, version string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "python.exe"), []byte("runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComponentManifestRoundTripsInRuntimeManifest(t *testing.T) {
	data, err := json.Marshal(RuntimeManifest{Components: []ComponentSpec{{ID: "x", Skill: "document"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"components"`) {
		t.Fatalf("component manifest omitted: %s", data)
	}
}
