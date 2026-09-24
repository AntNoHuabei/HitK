//go:build integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hitk/hitk/internal/skillruntime"
)

// TestDoclingKnowledgeBaseInitializationIntegration exercises the complete
// first-use path: a knowledge-base file upload asks for the document component,
// waits for approval, downloads and verifies the real component, installs it,
// then retries extraction and indexes the result.
//
// Run explicitly with:
//
//	$env:HITK_DOCLING_COMPONENT_ZIP='D:\path\to\document-windows-amd64-1.0.0.zip'
//	$env:HITK_DOCLING_INPUT='C:\path\to\scan.jpg'
//	go test -tags=integration ./internal/service -run TestDoclingKnowledgeBaseInitializationIntegration -v
func TestDoclingKnowledgeBaseInitializationIntegration(t *testing.T) {
	componentZip := strings.TrimSpace(os.Getenv("HITK_DOCLING_COMPONENT_ZIP"))
	inputPath := strings.TrimSpace(os.Getenv("HITK_DOCLING_INPUT"))
	if componentZip == "" || inputPath == "" {
		t.Skip("HITK_DOCLING_COMPONENT_ZIP and HITK_DOCLING_INPUT are required")
	}
	if _, err := os.Stat(componentZip); err != nil {
		t.Fatalf("component zip: %v", err)
	}
	if _, err := os.Stat(inputPath); err != nil {
		t.Fatalf("input file: %v", err)
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sourceRuntime := strings.TrimSpace(os.Getenv("HITK_DOCLING_RUNTIME_ROOT"))
	if sourceRuntime == "" {
		sourceRuntime = filepath.Join(repoRoot, "build", "windows", "runtime-stage")
	}
	if _, err := os.Stat(filepath.Join(sourceRuntime, "python", "python.exe")); err != nil {
		t.Skipf("staged Python runtime is unavailable: %v", err)
	}

	runtimeRoot := t.TempDir()
	copyTree(t, filepath.Join(sourceRuntime, "python"), filepath.Join(runtimeRoot, "python"))
	copyTree(t, filepath.Join(sourceRuntime, "skills", "document"), filepath.Join(runtimeRoot, "skills", "document"))

	manifestPath := filepath.Join(sourceRuntime, "runtime-manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest skillruntime.RuntimeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Components) != 1 {
		t.Fatalf("runtime manifest has %d components, want 1", len(manifest.Components))
	}
	component := &manifest.Components[0]
	if digest := integrationDigest(t, componentZip); !strings.EqualFold(digest, component.SHA256) {
		t.Fatalf("component digest mismatch: file=%s manifest=%s", digest, component.SHA256)
	}
	component.Source = "integration-local"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, componentZip)
	}))
	defer server.Close()
	component.URL = server.URL + "/document.zip"

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "runtime-manifest.json"), manifestData, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HITK_RUNTIME_ROOT", runtimeRoot)
	container, _, _ := newTestContainer(t, nil)
	kbService := NewKBService(container)
	collection, err := kbService.CreateCollection("Docling integration", "")
	if err != nil {
		t.Fatal(err)
	}

	type importResult struct {
		result ImportResult
		err    error
	}
	importDone := make(chan importResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	go func() {
		result, err := kbService.AddFiles(collection.ID, []string{inputPath})
		importDone <- importResult{result: result, err: err}
	}()

	waitForState(t, container.Runtime, "document", "approval_required", 30*time.Second)
	status, err := container.Runtime.PrepareSkillEnvironment(ctx, "document", true)
	if err != nil {
		t.Fatalf("prepare document component: %v\nstatus=%#v", err, status)
	}
	if status.State != "ready" || !dirExists(status.Environment) {
		t.Fatalf("document component is not ready: %#v", status)
	}
	if !regularFile(filepath.Join(status.Environment, "site-packages", "docling", "__init__.py")) {
		t.Fatalf("installed component does not contain Docling: %s", status.Environment)
	}

	select {
	case completed := <-importDone:
		if completed.err != nil {
			t.Fatal(completed.err)
		}
		if completed.result.Added != 1 || len(completed.result.Failed) != 0 {
			t.Fatalf("knowledge-base import did not resume after initialization: %#v", completed.result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	documents, err := kbService.Documents(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].ChunkCount == 0 {
		t.Fatalf("converted document was not indexed: %#v", documents)
	}
	content, err := kbService.DocumentContent(documents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(content) == "" {
		t.Fatal("Docling returned empty content")
	}
}

func waitForState(t *testing.T, manager *skillruntime.Manager, skill, state string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if manager.GetSkillEnvironment(skill).State == state {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s state %q: %#v", skill, state, manager.GetSkillEnvironment(skill))
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		copyFile(t, source, destination)
		return
	}
	if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		childSource := filepath.Join(source, entry.Name())
		childDestination := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			copyTree(t, childSource, childDestination)
			continue
		}
		copyFile(t, childSource, childDestination)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func integrationDigest(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
