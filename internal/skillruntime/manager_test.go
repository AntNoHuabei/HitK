package skillruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimePayloadVerificationDetectsTampering(t *testing.T) {
	root := t.TempDir()
	pythonDir := filepath.Join(root, "python")
	if err := os.MkdirAll(pythonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pythonDir, "python.exe"), []byte("runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pythonDir, "VERSION"), []byte("3.12.14"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := RuntimeManifest{SchemaVersion: 1, Platform: "windows-amd64", Runtimes: []RuntimeSpec{{Name: "python", Version: "3.12.14"}}}
	manifest.PayloadDigest = testPayloadDigest(t, root)
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "runtime-manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(root, t.TempDir())
	status := m.ListRuntimes()
	if len(status) != 1 || !status[0].Verified {
		t.Fatalf("runtime not verified: %#v", status)
	}
	if err := os.WriteFile(filepath.Join(pythonDir, "python.exe"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	status = m.ListRuntimes()
	if status[0].Verified || !strings.Contains(status[0].Message, "mismatch") {
		t.Fatalf("tampering not detected: %#v", status[0])
	}
}

func TestNetworkInstallRequiresExplicitApprovalAndLeavesNoPartial(t *testing.T) {
	runtimeRoot, dataRoot := t.TempDir(), t.TempDir()
	skillDir := filepath.Join(dataRoot, "skills", "remote")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := SkillManifest{SchemaVersion: 1, Name: "remote", Runtime: RuntimeConstraint{Kind: "python", Constraint: "==3.12.*"}, Entry: "main.py", PackageManager: PackageManager{Name: "uv", DependencyFile: "pyproject.toml", Lockfile: "uv.lock"}, Permissions: SkillPermissions{InstallNetwork: true}}
	writeJSON(t, filepath.Join(skillDir, "skill.json"), manifest)
	for name, content := range map[string]string{"main.py": "print('{}')", "pyproject.toml": "[project]\nname='x'\nversion='1'", "uv.lock": "version = 1"} {
		if err := os.WriteFile(filepath.Join(skillDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(runtimeRoot, dataRoot)
	_, err := m.PrepareSkillEnvironment(context.Background(), "remote", false)
	if err == nil || !strings.Contains(err.Error(), "联网确认") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dataRoot, "skill-environments", "remote")); !os.IsNotExist(statErr) {
		t.Fatalf("partial environment left behind: %v", statErr)
	}
}

func TestRuntimeEnvironmentPinsBundledPythonAndScrubsCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret")
	env := installerEnv(`C:\runtime`, `C:\cache`, false)
	joined := strings.Join(env, "\n")
	for _, required := range []string{"UV_NO_MANAGED_PYTHON=true", "UV_PYTHON_DOWNLOADS=never", `UV_PYTHON=C:\runtime\python\python.exe`, "npm_config_ignore_scripts=true"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %q in %s", required, joined)
		}
	}
	if strings.Contains(joined, "OPENAI_API_KEY") || strings.Contains(joined, "secret") {
		t.Fatal("credential leaked into runtime environment")
	}
}

func TestStagedRuntimeIfConfigured(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("HITK_TEST_RUNTIME"))
	if root == "" {
		t.Skip("HITK_TEST_RUNTIME is not set")
	}
	m := NewManager(root, t.TempDir())
	statuses := m.ListRuntimes()
	if len(statuses) != 4 {
		t.Fatalf("runtime count = %d", len(statuses))
	}
	for _, status := range statuses {
		if !status.Available || !status.Verified {
			t.Errorf("runtime %s is not verified: %#v", status.Name, status)
		}
	}
	if status := m.GetSkillEnvironment("document"); status.State != "ready" && status.State != "approval_required" {
		t.Fatalf("document skill has unexpected state: %#v", status)
	} else if status.State == "approval_required" && (status.ComponentVersion == "" || status.BytesTotal <= 0) {
		t.Fatalf("document component metadata is incomplete: %#v", status)
	}
}

func testPayloadDigest(t *testing.T, root string) string {
	t.Helper()
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() != "runtime-manifest.json" {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		_, _ = io.WriteString(h, filepath.ToSlash(rel))
		data, _ := os.ReadFile(path)
		digest := sha256.Sum256(data)
		_, _ = h.Write(digest[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
