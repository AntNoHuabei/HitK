package service

import (
	"path/filepath"
	"testing"

	"github.com/hitk/hitk/internal/companion"
	"github.com/hitk/hitk/internal/config"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

// newTestModelBridge builds a bridge whose mode default is deepseek and whose
// registry has deepseek and anthropic available.
func newTestModelBridge(t *testing.T) *companionModelBridge {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Load(config.Paths{
		DataDir:    dir,
		ConfigFile: filepath.Join(dir, "config.json"),
		ThreadsDir: filepath.Join(dir, "threads"),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	err = store.SetModeDefault(protocol.ModeCompanion, config.ModelChoice{
		Provider: "deepseek", Model: "deepseek-flash",
	})
	if err != nil {
		t.Fatalf("set mode default: %v", err)
	}
	providers := provider.NewRegistry()
	providers.Set([]provider.VendorConfig{
		{ID: "deepseek", APIKey: "test-deepseek"},
		{ID: "anthropic", APIKey: "test-anthropic"},
	})
	return &companionModelBridge{providers: providers, config: store}
}

func TestResolveModelPrefersCharacterPin(t *testing.T) {
	bridge := newTestModelBridge(t)
	choice := bridge.resolveModel(companion.Profile{Provider: "anthropic", Model: "claude-sonnet"})
	if choice.provider != "anthropic" || choice.model != "claude-sonnet" {
		t.Fatalf("resolved = %s, want anthropic/claude-sonnet", choice)
	}
}

func TestResolveModelFallsBackToModeDefault(t *testing.T) {
	bridge := newTestModelBridge(t)
	empty := bridge.resolveModel(companion.Profile{})
	if empty.provider != "deepseek" || empty.model != "deepseek-flash" {
		t.Fatalf("empty profile resolved = %s, want the mode default", empty)
	}
	// A pin pointing at a provider that is not usable must fall back too, which
	// is what lets a deleted or disabled provider self-heal.
	stale := bridge.resolveModel(companion.Profile{Provider: "gone", Model: "gone-model"})
	if stale.provider != "deepseek" || stale.model != "deepseek-flash" {
		t.Fatalf("stale pin resolved = %s, want the mode default", stale)
	}
}

func TestDecodeModelJSONHandlesFences(t *testing.T) {
	var decision companion.ProactiveDecision
	if err := decodeModelJSON("```json\n{\"send\":true,\"message\":\"在忙吗？\"}\n```", &decision); err != nil {
		t.Fatal(err)
	}
	if !decision.Send || decision.Message != "在忙吗？" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecodeModelJSONRejectsPlainText(t *testing.T) {
	var decision companion.ProactiveDecision
	if err := decodeModelJSON("现在就发", &decision); err == nil {
		t.Fatal("plain text was accepted as JSON")
	}
}

func TestCompanionReasoningMetadataIsAdapterGated(t *testing.T) {
	if got := companionReasoningMetadata("openai", "high"); got != nil {
		t.Fatalf("openai metadata = %#v, want nil", got)
	}
	if got := companionReasoningMetadata("anthropic", "high")["thinking_budget"]; got != "8192" {
		t.Fatalf("anthropic budget = %q, want 8192", got)
	}
}
