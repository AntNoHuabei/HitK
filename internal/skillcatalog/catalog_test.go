package skillcatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hitk/hitk/internal/skillmodel"
)

func TestCatalogDiscoversSkillMDAndPluginNamespaces(t *testing.T) {
	t.Setenv("HITK_SKILL_COMPAT", string(skillmodel.CompatFull))
	runtimeRoot := t.TempDir()
	dataRoot := t.TempDir()

	writeTestFile(t, filepath.Join(runtimeRoot, "skills", "prompt", "SKILL.md"), `---
name: prompt-demo
description: A prompt-only skill.
---
Use ${CODEBUDDY_SKILL_DIR} for files.
`)
	pluginRoot := filepath.Join(dataRoot, "plugins", "demo")
	writeTestFile(t, filepath.Join(pluginRoot, ".codebuddy-plugin", "plugin.json"), `{
  "name":"demo",
  "version":"1.0.0",
  "skills":"./skills"
}`)
	writeTestFile(t, filepath.Join(pluginRoot, "skills", "hello", "SKILL.md"), `---
name: hello
description: Plugin hello.
---
Hello from ${CODEBUDDY_PLUGIN_ROOT}.
`)
	writeTestFile(t, filepath.Join(pluginRoot, "skills", "hello", "main.py"), "print('{}')\n")

	catalog := NewCatalog(runtimeRoot, dataRoot)
	items, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 skills, got %#v", items)
	}
	prompt, err := catalog.Get(context.Background(), "prompt-demo")
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Kind != skillmodel.SkillKindPrompt || prompt.Body == "" {
		t.Fatalf("prompt skill was not loaded: %#v", prompt)
	}
	plugin, err := catalog.Get(context.Background(), "demo:hello")
	if err != nil {
		t.Fatal(err)
	}
	if plugin.Plugin == nil || plugin.Plugin.Name != "demo" {
		t.Fatalf("plugin metadata missing: %#v", plugin.Plugin)
	}
}

func TestCatalogModePromptFiltersScripts(t *testing.T) {
	t.Setenv("HITK_SKILL_COMPAT", string(skillmodel.CompatPrompt))
	runtimeRoot := t.TempDir()
	writeTestFile(t, filepath.Join(runtimeRoot, "skills", "script", "skill.json"), `{
  "schemaVersion":1,
  "name":"script-demo",
  "runtime":{"kind":"python","constraint":">=3.10"},
  "entry":"main.py"
}`)
	writeTestFile(t, filepath.Join(runtimeRoot, "skills", "script", "main.py"), "print('{}')\n")
	catalog := NewCatalog(runtimeRoot, t.TempDir())
	items, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("prompt mode should hide script skills: %#v", items)
	}
}

func TestSkillMDFindsEntryFromCommandExample(t *testing.T) {
	t.Setenv("HITK_SKILL_COMPAT", string(skillmodel.CompatFull))
	runtimeRoot := t.TempDir()
	root := filepath.Join(runtimeRoot, "skills", "watermark")
	writeTestFile(t, filepath.Join(root, "SKILL.md"), `---
name: watermark
description: Add watermarks.
---
Run: python scripts/watermark.py --input photo.jpg --text demo
`)
	writeTestFile(t, filepath.Join(root, "scripts", "watermark.py"), "print('ok')\n")
	catalog := NewCatalog(runtimeRoot, t.TempDir())
	pkg, err := catalog.Get(context.Background(), "watermark")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Kind != skillmodel.SkillKindScript || pkg.Entry != "scripts/watermark.py" {
		t.Fatalf("entry inference failed: %#v", pkg)
	}
	if pkg.Manifest.Invocation != skillmodel.InvocationArgv {
		t.Fatalf("Skill.md script should default to argv: %#v", pkg.Manifest)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
