package service

import (
	"reflect"
	"testing"

	"github.com/hitk/hitk/internal/config"
	"github.com/hitk/hitk/internal/provider"
)

// TestProviderLifecycle drives the model settings surface the way the UI does:
// configure a built-in provider, enable a model that is not in the catalog,
// switch between models, and finally delete the provider.
func TestProviderLifecycle(t *testing.T) {
	container, _, _ := newTestContainer(t, nil)
	models := NewModelService(container)

	if len(models.Catalog()) == 0 {
		t.Fatal("the built-in catalog is empty, so the settings page would render nothing")
	}

	// A built-in provider needs the preset models the user ticked in the dialog.
	saved, err := models.SaveProvider(ProviderInput{
		CatalogID: "deepseek",
		APIKey:    "sk-test-key",
		Model:     "deepseek-flash",
		Models: []ProviderModelView{
			{Model: "deepseek-flash"},
			{Model: "deepseek-v4-pro"},
		},
	})
	if err != nil {
		t.Fatalf("save provider: %v", err)
	}
	if saved.ID != "deepseek" || !saved.Ready {
		t.Fatalf("saved provider = %+v, want a ready deepseek", saved)
	}
	if saved.Model != "deepseek-flash" {
		t.Errorf("current model = %q, want deepseek-flash", saved.Model)
	}
	// A secret must never round-trip in the clear.
	if saved.MaskedKey == "" || saved.MaskedKey == "sk-test-key" {
		t.Errorf("masked key = %q, want a masked value", saved.MaskedKey)
	}

	// Enabling a model the user typed by hand (not in the catalog) has to be
	// recorded as custom, because that is what the UI badges.
	// The id has to be one the catalog does not know and that no rename rule
	// claims, otherwise the assertion below would be testing the migration
	// instead of the custom-model path.
	if err := models.EnableModel("deepseek", "deepseek-exp-alpha", "Experimental Alpha", true); err != nil {
		t.Fatalf("enable model: %v", err)
	}
	view := findProvider(t, models.Providers(), "deepseek")
	var custom *ProviderModelView
	for i := range view.Models {
		if view.Models[i].Model == "deepseek-exp-alpha" {
			custom = &view.Models[i]
		}
	}
	if custom == nil {
		t.Fatal("the hand-added model did not appear on the provider")
	}
	if !custom.Custom {
		t.Error("a model enabled by hand should be marked custom")
	}

	// Saving again with an empty key must keep the stored credential, otherwise
	// editing any unrelated field would silently wipe the API key.
	if _, err := models.SaveProvider(ProviderInput{
		CatalogID: "deepseek",
		Model:     "deepseek-v4-pro",
		Models:    view.Models,
	}); err != nil {
		t.Fatalf("re-save provider: %v", err)
	}
	view = findProvider(t, models.Providers(), "deepseek")
	if !view.HasKey || !view.Ready {
		t.Error("editing a provider without re-typing the key dropped the credential")
	}
	if view.Model != "deepseek-v4-pro" {
		t.Errorf("current model = %q, want deepseek-v4-pro", view.Model)
	}

	// The composer's flat model list has to include every enabled model.
	options := models.ModelOptions()
	if !hasOption(options, "deepseek", "deepseek-exp-alpha") {
		t.Error("the composer's model list is missing an enabled model")
	}

	// Disabling the provider's current model must move it to a survivor rather
	// than leave it pointing at something no longer enabled.
	if err := models.DisableModel("deepseek", "deepseek-v4-pro"); err != nil {
		t.Fatalf("disable model: %v", err)
	}
	view = findProvider(t, models.Providers(), "deepseek")
	if view.Model == "deepseek-v4-pro" {
		t.Error("the provider still points at the model that was just disabled")
	}
	if !hasModelRef(view.Models, view.Model) {
		t.Errorf("current model %q is not among the enabled models", view.Model)
	}

	if err := models.DeleteProvider("deepseek"); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	for _, p := range models.Providers() {
		if p.ID == "deepseek" {
			t.Fatal("the deleted provider is still listed")
		}
	}
}

// TestModeDefaultsFollowTheConfiguredProvider covers the rule that makes the
// first provider a user adds usable in both modes, and that a deleted provider
// does not leave a mode pointing at nothing.
func TestModeDefaultsFollowTheConfiguredProvider(t *testing.T) {
	container, _, _ := newTestContainer(t, nil)
	models := NewModelService(container)

	if _, err := models.SaveProvider(ProviderInput{
		CatalogID: "deepseek",
		APIKey:    "sk-test-key",
		Model:     "deepseek-flash",
		Models:    []ProviderModelView{{Model: "deepseek-flash"}},
	}); err != nil {
		t.Fatalf("save provider: %v", err)
	}

	defaults := models.ModeDefaults()
	if defaults.Work.Provider != "deepseek" || defaults.Coding.Provider != "deepseek" {
		t.Fatalf("mode defaults = %+v, want both modes on deepseek", defaults)
	}

	// Work is pointed somewhere else on purpose; adding a second provider must
	// not steal it, because the user has already made that choice.
	if err := models.SetModeDefault("coding", "deepseek", "deepseek-flash"); err != nil {
		t.Fatalf("set mode default: %v", err)
	}
	if _, err := models.SaveProvider(ProviderInput{
		CatalogID: "openai",
		APIKey:    "sk-openai",
		Model:     "gpt-4o",
		Models:    []ProviderModelView{{Model: "gpt-4o"}},
	}); err != nil {
		t.Fatalf("save second provider: %v", err)
	}
	defaults = models.ModeDefaults()
	if defaults.Work.Provider == "openai" && defaults.Coding.Provider == "openai" {
		t.Error("adding a provider overwrote a mode the user had already set")
	}

	if err := models.DeleteProvider("deepseek"); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	defaults = models.ModeDefaults()
	if defaults.Work.Provider == "deepseek" || defaults.Coding.Provider == "deepseek" {
		t.Errorf("mode defaults still name the deleted provider: %+v", defaults)
	}
}

// TestCustomProviderIsSavedAndUsable checks the "custom endpoint" path: no
// catalog entry, so identity and models all come from the dialog.
func TestCustomProviderIsSavedAndUsable(t *testing.T) {
	container, _, _ := newTestContainer(t, nil)
	models := NewModelService(container)

	saved, err := models.SaveProvider(ProviderInput{
		Name:    "内网网关",
		Kind:    "openai",
		BaseURL: "https://gw.internal/v1",
		APIKey:  "sk-internal",
		Model:   "qwen-max",
		Models:  []ProviderModelView{{Model: "qwen-max"}, {Model: "qwen-plus", Custom: true}},
	})
	if err != nil {
		t.Fatalf("save custom provider: %v", err)
	}
	if !saved.Custom {
		t.Error("a provider with no catalog id must be marked custom")
	}
	if saved.ID == "" || saved.ID == "deepseek" {
		t.Errorf("custom provider id = %q, want a minted id", saved.ID)
	}
	if saved.Name != "内网网关" {
		t.Errorf("name = %q", saved.Name)
	}
	if len(saved.Models) != 2 {
		t.Errorf("enabled models = %d, want 2", len(saved.Models))
	}

	// Custom providers sort after the built-in ones so a new user scans the
	// known list first.
	list := models.Providers()
	if len(list) > 0 && !list[len(list)-1].Custom {
		t.Error("custom providers should sort last")
	}

	// A missing endpoint is a hard error: there is nothing to call.
	if _, err := models.SaveProvider(ProviderInput{
		Name: "无地址", Kind: "openai", APIKey: "sk-x",
		Models: []ProviderModelView{{Model: "m"}},
	}); err == nil {
		t.Error("a custom provider without a base URL was accepted")
	}

	// A cloud provider cannot be saved without a credential.
	if _, err := models.SaveProvider(ProviderInput{
		CatalogID: "openai", Models: []ProviderModelView{{Model: "gpt-4o"}},
	}); err == nil {
		t.Error("a hosted provider was saved with no API key")
	}

	// Preferences are the theme's backing store, so they must round-trip.
	if got := models.GetPreference("theme"); got != "" {
		t.Errorf("unset preference = %q, want empty", got)
	}
	if err := models.SetPreference("theme", "graphite"); err != nil {
		t.Fatalf("set preference: %v", err)
	}
	if got := models.GetPreference("theme"); got != "graphite" {
		t.Errorf("preference = %q, want graphite", got)
	}
	if models.Preferences()["theme"] != "graphite" {
		t.Error("Preferences() did not include the value just written")
	}
}

// TestProviderConfigSurvivesRestart checks the settings the model page writes
// are on disk, since losing them on every launch would be unusable.
func TestProviderConfigSurvivesRestart(t *testing.T) {
	container, _, paths := newTestContainer(t, nil)
	models := NewModelService(container)

	if _, err := models.SaveProvider(ProviderInput{
		CatalogID: "deepseek",
		APIKey:    "sk-persisted",
		Model:     "deepseek-flash",
		Models:    []ProviderModelView{{Model: "deepseek-flash"}, {Model: "deepseek-v4-pro"}},
	}); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	if err := models.SetModeDefault("coding", "deepseek", "deepseek-v4-pro"); err != nil {
		t.Fatalf("set mode default: %v", err)
	}
	if err := models.SetPreference("theme", "ink"); err != nil {
		t.Fatalf("set preference: %v", err)
	}
	container.Close()

	reopened, err := config.Load(paths)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	got := reopened.Get()

	var found bool
	for _, vc := range got.Vendors {
		if vc.ID != "deepseek" {
			continue
		}
		found = true
		if vc.APIKey != "sk-persisted" {
			t.Error("the API key did not round-trip")
		}
		if len(vc.EnabledModels) != 2 {
			t.Errorf("enabled models = %d, want 2", len(vc.EnabledModels))
		}
	}
	if !found {
		t.Fatal("the provider was not persisted")
	}
	if got.ModeDefaults["coding"].Model != "deepseek-v4-pro" {
		t.Errorf("coding default = %+v, want deepseek-v4-pro", got.ModeDefaults["coding"])
	}
	if got.Preferences["theme"] != "ink" {
		t.Errorf("preferences = %+v, want theme=ink", got.Preferences)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func findProvider(t *testing.T, list []ProviderView, id string) ProviderView {
	t.Helper()
	for _, p := range list {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("provider %s not found in %d entries", id, len(list))
	return ProviderView{}
}

func hasOption(options []ModelOptionView, providerID, model string) bool {
	for _, o := range options {
		if o.Provider == providerID && o.Model == model {
			return true
		}
	}
	return false
}

func TestVolcenginePlanExposesVerifiedReasoningEffort(t *testing.T) {
	want := []string{"low", "medium", "high"}
	meta, ok := provider.VendorByID("volcengine-plan")
	if !ok {
		t.Fatal("Agent Plan is missing from the provider catalog")
	}
	for _, preset := range meta.Models {
		got := reasoningOptions("volcengine-plan", provider.ModelRef{ID: preset.ID, Reasoning: preset.Reasoning})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Agent Plan model %s options = %#v, want %#v", preset.ID, got, want)
		}
	}
	if got := reasoningOptions("volcengine-plan", provider.ModelRef{}); got != nil {
		t.Fatalf("non-reasoning model options = %#v, want nil", got)
	}
}

func hasModelRef(models []ProviderModelView, model string) bool {
	for _, m := range models {
		if m.Model == model {
			return true
		}
	}
	return false
}
