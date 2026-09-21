package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{
		DataDir:    dir,
		ConfigFile: filepath.Join(dir, "config.json"),
		KBFile:     filepath.Join(dir, "knowledge.db"),
		ThreadsDir: filepath.Join(dir, "threads"),
		LogFile:    filepath.Join(dir, "hitk.log"),
	}
}

// TestLoadMigratesRetiredModelIDs covers the situation that makes a catalog
// update look like it never took effect: once a config file exists, Default()
// never runs again, so a saved id from a retired model keeps being used.
func TestLoadMigratesRetiredModelIDs(t *testing.T) {
	paths := testPaths(t)
	stale := `{
		"provider": "deepseek",
		"model": "deepseek-chat",
		"modeDefaults": {
			"work":   {"provider": "deepseek", "model": "deepseek-chat"},
			"coding": {"provider": "deepseek", "model": "deepseek-reasoner"}
		},
		"vendors": [{
			"id": "deepseek",
			"defaultModel": "deepseek-chat",
			"enabledModels": [
				{"id": "deepseek-chat", "label": "DeepSeek V3 (chat)", "tools": true},
				{"id": "deepseek-reasoner", "label": "DeepSeek R1 (reasoner)", "reasoning": true}
			],
			"enabled": true
		}]
	}`
	if err := os.WriteFile(paths.ConfigFile, []byte(stale), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	store, err := Load(paths)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg := store.Get()

	if cfg.Model != "deepseek-flash" {
		t.Errorf("model = %q, want deepseek-flash", cfg.Model)
	}
	if got := store.ModeDefault(protocol.ModeWork).Model; got != "deepseek-flash" {
		t.Errorf("work mode default = %q, want deepseek-flash", got)
	}
	// reasoner was the flagship, so it moves to Pro rather than to the default.
	if got := store.ModeDefault(protocol.ModeCoding).Model; got != "deepseek-v4-pro" {
		t.Errorf("coding mode default = %q, want deepseek-v4-pro", got)
	}
	if len(cfg.Vendors) != 1 {
		t.Fatalf("vendors = %d, want 1", len(cfg.Vendors))
	}
	if cfg.Vendors[0].DefaultModel != "deepseek-flash" {
		t.Errorf("vendor default model = %q, want deepseek-flash", cfg.Vendors[0].DefaultModel)
	}
	// The label snapshot is shown next to the id in the settings list, so a
	// stale one would tell the user the retired model is still in play.
	for _, ref := range cfg.Vendors[0].EnabledModels {
		switch ref.ID {
		case "deepseek-flash":
			if ref.Label != "DeepSeek V4.1 Flash" {
				t.Errorf("label for %s = %q, want the current label", ref.ID, ref.Label)
			}
			if !ref.Tools || !ref.Vision {
				t.Errorf("capabilities for %s were not refreshed: %+v", ref.ID, ref)
			}
		case "deepseek-v4-pro":
			if ref.Label != "DeepSeek V4 Pro" {
				t.Errorf("label for %s = %q, want the current label", ref.ID, ref.Label)
			}
		default:
			t.Errorf("unexpected enabled model %q survived the migration", ref.ID)
		}
	}

	// The repair has to reach the file too, otherwise it silently repeats on
	// every start and the user never sees the setting change.
	raw, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var onDisk Config
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if onDisk.Model != "deepseek-flash" {
		t.Errorf("model on disk = %q, want the migration to be persisted", onDisk.Model)
	}
}

// TestLoadKeepsUnknownCustomModels guards the other side of the migration: a
// model HitK has no replacement for must be left alone, including on a custom
// endpoint that happens to reuse a retired id.
func TestLoadKeepsUnknownCustomModels(t *testing.T) {
	paths := testPaths(t)
	raw := `{
		"provider": "deepseek",
		"model": "deepseek-exp-alpha",
		"vendors": [{
			"id": "custom-1",
			"kind": "openai",
			"custom": true,
			"defaultModel": "deepseek-chat",
			"enabledModels": [{"id": "deepseek-chat", "label": "whatever the gateway serves"}],
			"enabled": true
		}]
	}`
	if err := os.WriteFile(paths.ConfigFile, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	store, err := Load(paths)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg := store.Get()
	if cfg.Model != "deepseek-exp-alpha" {
		t.Errorf("model = %q, want an unknown model left untouched", cfg.Model)
	}
	if cfg.Vendors[0].DefaultModel != "deepseek-chat" {
		t.Errorf("custom vendor model = %q, want it untouched", cfg.Vendors[0].DefaultModel)
	}
}

// TestStepBudgetIsClamped keeps the turn budget inside the limits the settings
// UI advertises, so a saved value can never be silently larger than what the
// input allows.
func TestStepBudgetIsClamped(t *testing.T) {
	if Default().MaxSteps != protocol.DefaultMaxSteps {
		t.Errorf("default maxSteps = %d, want %d", Default().MaxSteps, protocol.DefaultMaxSteps)
	}
	paths := testPaths(t)
	over := fmt.Sprintf(`{"maxSteps": %d}`, protocol.MaxStepsCeiling+50)
	if err := os.WriteFile(paths.ConfigFile, []byte(over), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store, err := Load(paths)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := store.Get().MaxSteps; got != protocol.MaxStepsCeiling {
		t.Errorf("maxSteps = %d, want the ceiling %d", got, protocol.MaxStepsCeiling)
	}
}

// TestDefaultTracksTheCatalogDefault makes sure the shipped default is one the
// catalog actually sells — the two drift apart whenever a vendor retires a
// model, and nothing else catches it.
func TestDefaultTracksTheCatalogDefault(t *testing.T) {
	meta, ok := provider.VendorByID(Default().Provider)
	if !ok {
		t.Fatalf("default provider %q is not in the catalog", Default().Provider)
	}
	if Default().Model != meta.DefaultModel {
		t.Errorf("default model = %q, want the catalog default %q", Default().Model, meta.DefaultModel)
	}
	for _, mode := range []protocol.Mode{protocol.ModeWork, protocol.ModeCoding} {
		choice := Default().ModeDefaults[string(mode)]
		if choice.Model != meta.DefaultModel {
			t.Errorf("%s default = %q, want %q", mode, choice.Model, meta.DefaultModel)
		}
	}
}
