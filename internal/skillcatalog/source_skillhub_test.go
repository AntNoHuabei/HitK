package skillcatalog

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallLocalSkill(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: demo\n---\nDemo\n")
	installer := NewInstaller(t.TempDir())
	path, err := installer.Install(context.Background(), InstallRequest{ID: "demo", Source: "local", Path: source})
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(path, "SKILL.md")) {
		t.Fatal("installed Skill is missing SKILL.md")
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("bad"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(archive, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(archive, t.TempDir()); err == nil {
		t.Fatal("expected traversal ZIP to be rejected")
	}
}

func TestInstallerRollbackAndPrune(t *testing.T) {
	dataRoot := t.TempDir()
	installer := NewInstaller(dataRoot)
	first := t.TempDir()
	writeTestFile(t, filepath.Join(first, "SKILL.md"), "---\nname: demo\nversion: 1\n---\nOne\n")
	if _, err := installer.Install(context.Background(), InstallRequest{ID: "demo", Source: "local", Path: first}); err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	writeTestFile(t, filepath.Join(second, "SKILL.md"), "---\nname: demo\nversion: 2\n---\nTwo\n")
	if _, err := installer.Install(context.Background(), InstallRequest{ID: "demo", Source: "local", Path: second}); err != nil {
		t.Fatal(err)
	}
	if err := installer.Rollback("demo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dataRoot, "skill-catalog", "demo", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("One")) {
		t.Fatalf("rollback restored wrong version: %s", data)
	}
	if err := installer.Prune("demo"); err != nil {
		t.Fatal(err)
	}
}
