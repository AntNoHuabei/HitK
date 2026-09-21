package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hitk/hitk/internal/config"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

// ModelService owns the model-provider surface: the built-in catalog, the
// user's configured providers, the set of models each one has switched on, and
// the per-mode defaults.
//
// Providers are the unit the user thinks in ("DeepSeek", "火山方舟"), while a
// vendor's Kind is just the wire protocol. Keeping both means a custom endpoint
// reuses the OpenAI client without pretending to be in the catalog.
type ModelService struct{ c *Container }

// NewModelService builds the service.
func NewModelService(c *Container) *ModelService { return &ModelService{c: c} }

// ServiceName implements [application.ServiceName].
func (*ModelService) ServiceName() string { return "Model" }

// ---------------------------------------------------------------------------
// Views
// ---------------------------------------------------------------------------

// CatalogModelView is one preset model from the built-in catalog.
type CatalogModelView struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Context   int    `json:"context,omitempty"`
	Reasoning bool   `json:"reasoning,omitempty"`
	Vision    bool   `json:"vision,omitempty"`
	Tools     bool   `json:"tools,omitempty"`
	Embedding bool   `json:"embedding,omitempty"`
}

// CatalogProviderView is a built-in provider the user can configure.
type CatalogProviderView struct {
	// Kind doubles as the catalog id: it is what a ProviderInput.CatalogID
	// refers back to, so built-in providers need no separate identifier.
	Kind      string             `json:"kind"`
	Name      string             `json:"name"`
	Label     string             `json:"label"`
	BaseURL   string             `json:"baseUrl"`
	Region    string             `json:"region"`
	EnvKey    string             `json:"envKey,omitempty"`
	DocsURL   string             `json:"docsUrl,omitempty"`
	APIKeyURL string             `json:"apiKeyUrl,omitempty"`
	Note      string             `json:"note,omitempty"`
	Models    []CatalogModelView `json:"models"`
}

// ProviderModelView is a model that is currently enabled on a provider.
type ProviderModelView struct {
	Model     string `json:"model"`
	Label     string `json:"label"`
	Custom    bool   `json:"custom"`
	Reasoning bool   `json:"reasoning,omitempty"`
	Vision    bool   `json:"vision,omitempty"`
	Tools     bool   `json:"tools,omitempty"`
	Context   int    `json:"context,omitempty"`
}

// ProviderView is a configured provider plus its enabled models.
type ProviderView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"baseUrl"`
	// Model is the provider's currently selected model.
	Model     string              `json:"model"`
	Region    string              `json:"region"`
	Custom    bool                `json:"custom"`
	Ready     bool                `json:"ready"`
	HasKey    bool                `json:"hasKey"`
	MaskedKey string              `json:"maskedKey,omitempty"`
	Models    []ProviderModelView `json:"models"`
}

// ProviderInput is what the settings dialog submits.
type ProviderInput struct {
	// ID is the existing provider id, empty when adding.
	ID string `json:"id"`
	// CatalogID names the built-in vendor this provider is. Empty means the
	// user is defining a custom (OpenAI-compatible) endpoint.
	CatalogID string `json:"catalogId"`
	Name      string `json:"name"`
	// Kind is the wire protocol; only consulted for custom providers.
	Kind    string              `json:"kind"`
	BaseURL string              `json:"baseUrl"`
	APIKey  string              `json:"apiKey"`
	Model   string              `json:"model"`
	Models  []ProviderModelView `json:"models"`
}

// DiscoveredModelView is one model returned by a provider's listing endpoint.
type DiscoveredModelView struct {
	ID string `json:"id"`
	// Label is the catalog label when the id matches a preset, otherwise the
	// raw id. Capability flags are best-effort: a live listing never says
	// whether a model accepts images, so the UI lets the user tick that.
	Label     string `json:"label"`
	Reasoning bool   `json:"reasoning,omitempty"`
	Vision    bool   `json:"vision,omitempty"`
	Tools     bool   `json:"tools,omitempty"`
	Context   int    `json:"context,omitempty"`
}

// ModelOptionView is one selectable model, flattened across providers.
type ModelOptionView struct {
	Provider     string   `json:"provider"`
	ProviderName string   `json:"providerName"`
	Model        string   `json:"model"`
	Label        string   `json:"label"`
	Vision       bool     `json:"vision,omitempty"`
	Reasoning    []string `json:"reasoning,omitempty"`
	Context      int      `json:"context,omitempty"`
}

// ModeDefaultView is the model one agent mode starts with.
type ModeDefaultView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// ModeDefaultsView carries the Work and Coding selections.
type ModeDefaultsView struct {
	Work   ModeDefaultView `json:"work"`
	Coding ModeDefaultView `json:"coding"`
}

// ---------------------------------------------------------------------------
// Catalog
// ---------------------------------------------------------------------------

// Catalog lists the built-in providers and their preset models.
func (*ModelService) Catalog() []CatalogProviderView {
	out := make([]CatalogProviderView, 0, len(provider.Catalog))
	for _, v := range provider.Catalog {
		view := CatalogProviderView{
			Kind:      v.ID,
			Name:      v.Label,
			Label:     v.Label,
			BaseURL:   v.BaseURL,
			Region:    v.Region,
			EnvKey:    v.EnvKey,
			DocsURL:   v.DocsURL,
			APIKeyURL: v.APIKeyURL,
			Note:      v.Note,
			Models:    make([]CatalogModelView, 0, len(v.Models)),
		}
		for _, m := range v.Models {
			view.Models = append(view.Models, CatalogModelView{
				ID: m.ID, Label: m.Label, Context: m.Context,
				Reasoning: m.Reasoning, Vision: m.Vision, Tools: m.Tools,
				Embedding: m.Embedding,
			})
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

// Providers lists every configured provider with its enabled models.
func (s *ModelService) Providers() []ProviderView {
	cfg := s.c.Config.Get()
	out := make([]ProviderView, 0, len(cfg.Vendors))
	for _, vc := range cfg.Vendors {
		out = append(out, s.providerView(vc))
	}
	// Custom providers sort last: the built-in list is what a new user scans.
	sort.SliceStable(out, func(i, j int) bool { return !out[i].Custom && out[j].Custom })
	return out
}

func (s *ModelService) providerView(vc provider.VendorConfig) ProviderView {
	meta, _ := vc.Meta()
	name := firstNonEmpty(vc.Name, meta.Label, vc.ID)
	view := ProviderView{
		ID: vc.ID, Name: name, Kind: string(vc.Kind),
		BaseURL:   firstNonEmpty(vc.BaseURL, meta.BaseURL),
		Model:     vc.DefaultModel,
		Region:    meta.Region,
		Custom:    vc.Custom,
		HasKey:    strings.TrimSpace(vc.APIKey) != "",
		MaskedKey: maskKey(vc.APIKey),
		Models:    make([]ProviderModelView, 0, len(vc.EnabledModels)),
	}
	if view.Kind == "" {
		view.Kind = string(meta.Kind)
	}
	for _, m := range vc.EnabledModels {
		view.Models = append(view.Models, ProviderModelView{
			Model: m.ID, Label: firstNonEmpty(m.Label, m.ID), Custom: m.Custom,
			Reasoning: m.Reasoning, Vision: m.Vision, Tools: m.Tools, Context: m.Context,
		})
	}
	view.Ready = providerReady(meta, vc)
	return view
}

// SaveProvider creates or updates a provider and refreshes the live registry.
func (s *ModelService) SaveProvider(in ProviderInput) (ProviderView, error) {
	catalogID := strings.TrimSpace(in.CatalogID)
	id := strings.TrimSpace(in.ID)

	var (
		meta  provider.Vendor
		isNew bool
	)
	switch {
	case catalogID != "":
		var ok bool
		meta, ok = provider.VendorByID(catalogID)
		if !ok {
			return ProviderView{}, fmt.Errorf("未知的服务商类型: %s", catalogID)
		}
		// One config per built-in vendor: re-picking a vendor already
		// configured edits it rather than creating a duplicate.
		id = meta.ID
	default:
		kind, err := parseVendorKind(in.Kind)
		if err != nil {
			return ProviderView{}, err
		}
		if id == "" {
			id = s.newCustomID()
			isNew = true
		}
		meta = provider.Vendor{
			ID: id, Label: strings.TrimSpace(in.Name), Kind: kind,
			BaseURL: strings.TrimSpace(in.BaseURL), Region: "custom", SupportsModelList: true,
		}
	}

	existing, hadExisting := s.vendor(id)
	if in.Name == "" && catalogID != "" {
		in.Name = meta.Label
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = firstNonEmpty(meta.Label, id)
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = meta.BaseURL
	}
	if baseURL == "" {
		return ProviderView{}, fmt.Errorf("请填写服务地址")
	}

	models := normaliseModelRefs(in.Models)
	if len(models) == 0 {
		return ProviderView{}, fmt.Errorf("请至少启用一个模型")
	}
	current := strings.TrimSpace(in.Model)
	if current == "" || !hasModel(models, current) {
		current = models[0].ID
	}

	cfg := provider.VendorConfig{
		ID:            id,
		Name:          name,
		Kind:          meta.Kind,
		BaseURL:       baseURL,
		APIKey:        strings.TrimSpace(in.APIKey),
		DefaultModel:  current,
		EnabledModels: models,
		Enabled:       true,
		Custom:        catalogID == "",
	}
	// An empty key means "keep the stored one", so the dialog can round-trip a
	// masked field without wiping the credential.
	if cfg.APIKey == "" && hadExisting {
		cfg.APIKey = existing.APIKey
		cfg.EmbeddingModel = existing.EmbeddingModel
	}
	if !cfg.Custom && meta.EmbeddingModel != "" && cfg.EmbeddingModel == "" {
		cfg.EmbeddingModel = meta.EmbeddingModel
	}
	// A provider with no credential is only usable when the endpoint is local.
	if cfg.APIKey == "" && meta.Region != "local" {
		return ProviderView{}, fmt.Errorf("请填写 %s 的 API Key", name)
	}

	if err := s.c.Config.SetVendorConfig(cfg); err != nil {
		return ProviderView{}, err
	}
	s.syncRegistry()
	s.seedModeDefaults(cfg)
	if isNew {
		_ = s.c.Config.Update(func(c *config.Config) { c.Onboarded = true })
	}
	return s.providerView(cfg), nil
}

// DeleteProvider removes a provider and everything derived from it.
func (s *ModelService) DeleteProvider(id string) error {
	if _, ok := s.vendor(id); !ok {
		return fmt.Errorf("服务商不存在: %s", id)
	}
	if err := s.c.Config.DeleteVendorConfig(id); err != nil {
		return err
	}
	// A mode default that still names the deleted provider would leave the
	// composer pointing at a client that can no longer be built, so clear it and
	// let the next saved provider re-seed both modes.
	s.clearModeDefaultsFor(id)
	s.syncRegistry()
	return nil
}

// SetProviderModel marks which of a provider's models is current.
func (s *ModelService) SetProviderModel(providerID, model string) error {
	vc, ok := s.vendor(providerID)
	if !ok {
		return fmt.Errorf("服务商不存在: %s", providerID)
	}
	if !hasModel(vc.EnabledModels, model) {
		return fmt.Errorf("模型 %s 尚未启用", model)
	}
	return s.c.Config.SetVendorModel(providerID, model)
}

// EnableModel switches a model on, snapshotting its capabilities.
func (s *ModelService) EnableModel(providerID, model, label string, custom bool) error {
	vc, ok := s.vendor(providerID)
	if !ok {
		return fmt.Errorf("服务商不存在: %s", providerID)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("请填写模型名")
	}
	if hasModel(vc.EnabledModels, model) {
		return nil
	}
	ref := provider.ModelRef{ID: model, Label: label, Custom: custom}
	if preset, ok := lookupPreset(providerID, model); ok {
		ref.Label = firstNonEmpty(label, preset.Label)
		ref.Custom = false
		ref.Reasoning, ref.Vision, ref.Tools, ref.Context = preset.Reasoning, preset.Vision, preset.Tools, preset.Context
	}
	if ref.Label == "" {
		ref.Label = model
	}
	refs := append(append([]provider.ModelRef(nil), vc.EnabledModels...), ref)
	return s.c.Config.SetVendorModelRefs(providerID, refs)
}

// DisableModel switches a model off. The provider's own model is moved to a
// survivor so it never points at something no longer enabled.
func (s *ModelService) DisableModel(providerID, model string) error {
	vc, ok := s.vendor(providerID)
	if !ok {
		return fmt.Errorf("服务商不存在: %s", providerID)
	}
	refs := make([]provider.ModelRef, 0, len(vc.EnabledModels))
	for _, m := range vc.EnabledModels {
		if m.ID != model {
			refs = append(refs, m)
		}
	}
	if len(refs) == len(vc.EnabledModels) {
		return nil
	}
	if err := s.c.Config.SetVendorModelRefs(providerID, refs); err != nil {
		return err
	}
	if vc.DefaultModel == model {
		next := ""
		if len(refs) > 0 {
			next = refs[0].ID
		}
		if err := s.c.Config.SetVendorModel(providerID, next); err != nil {
			return err
		}
		s.dropModeDefaultFor(providerID, model)
	}
	return nil
}

// SetModelVision records whether an enabled model accepts image input. A live
// listing cannot tell the UI this, so the user has the final word.
func (s *ModelService) SetModelVision(providerID, model string, vision bool) error {
	vc, ok := s.vendor(providerID)
	if !ok {
		return fmt.Errorf("服务商不存在: %s", providerID)
	}
	refs := make([]provider.ModelRef, 0, len(vc.EnabledModels))
	for _, m := range vc.EnabledModels {
		if m.ID == model {
			m.Vision = vision
		}
		refs = append(refs, m)
	}
	return s.c.Config.SetVendorModelRefs(providerID, refs)
}

// DiscoverModels asks a provider which models it serves. The config is not
// saved first, so the dialog can populate its picker from a URL the user is
// still typing.
func (s *ModelService) DiscoverModels(in ProviderInput) ([]DiscoveredModelView, error) {
	cfg, meta, err := s.draftConfig(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := contextWithTimeout(30 * time.Second)
	defer cancel()

	names, err := provider.ListModels(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%s", friendlyError(err))
	}
	out := make([]DiscoveredModelView, 0, len(names))
	for _, name := range names {
		view := DiscoveredModelView{ID: name, Label: name}
		if preset, ok := lookupPreset(meta.ID, name); ok {
			view.Label = preset.Label
			view.Reasoning, view.Vision, view.Tools, view.Context = preset.Reasoning, preset.Vision, preset.Tools, preset.Context
		} else {
			// An unknown id is almost always a chat model, so assume tools and
			// leave vision for the user to confirm.
			view.Tools = true
		}
		out = append(out, view)
	}
	return out, nil
}

// TestProvider sends a tiny completion to verify credentials and endpoint.
func (s *ModelService) TestProvider(in ProviderInput) TestResult {
	cfg, _, err := s.draftConfig(in)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	model := strings.TrimSpace(in.Model)
	if model == "" && len(cfg.EnabledModels) > 0 {
		model = cfg.EnabledModels[0].ID
	}
	if model == "" {
		model = cfg.DefaultModel
	}
	if model == "" {
		return TestResult{OK: false, Message: "请先启用一个模型"}
	}
	cfg.DefaultModel = model

	client, err := provider.Build(cfg)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	ctx, cancel := contextWithTimeout(45 * time.Second)
	defer cancel()

	start := time.Now()
	stream, err := client.Stream(ctx, provider.ChatRequest{
		Model:     model,
		Messages:  []provider.Message{provider.UserText("回复两个字：正常")},
		MaxTokens: 32,
	})
	if err != nil {
		return TestResult{OK: false, Message: friendlyError(err), Latency: time.Since(start).Milliseconds()}
	}

	var reply strings.Builder
	for chunk := range stream {
		switch chunk.Kind {
		case provider.ChunkText:
			reply.WriteString(chunk.Text)
		case provider.ChunkError:
			return TestResult{OK: false, Message: friendlyError(chunk.Err), Latency: time.Since(start).Milliseconds(), Model: model}
		}
		if reply.Len() > 200 {
			break
		}
	}
	latency := time.Since(start).Milliseconds()
	if strings.TrimSpace(reply.String()) == "" {
		return TestResult{OK: false, Message: "连接成功但模型没有返回内容，请确认模型名是否正确", Latency: latency, Model: model}
	}
	return TestResult{OK: true, Message: "连接正常", Latency: latency, Reply: strings.TrimSpace(reply.String()), Model: model}
}

// draftConfig turns a dialog submission into a config that can be used without
// being saved, filling in catalog defaults the user did not type.
func (s *ModelService) draftConfig(in ProviderInput) (provider.VendorConfig, provider.Vendor, error) {
	catalogID := strings.TrimSpace(in.CatalogID)
	id := strings.TrimSpace(in.ID)

	var meta provider.Vendor
	if catalogID != "" {
		found, ok := provider.VendorByID(catalogID)
		if !ok {
			return provider.VendorConfig{}, provider.Vendor{}, fmt.Errorf("未知的服务商类型: %s", catalogID)
		}
		meta = found
		id = found.ID
	} else {
		kind, err := parseVendorKind(in.Kind)
		if err != nil {
			return provider.VendorConfig{}, provider.Vendor{}, err
		}
		if id == "" {
			id = "custom_draft"
		}
		meta = provider.Vendor{
			ID: id, Label: strings.TrimSpace(in.Name), Kind: kind,
			BaseURL: strings.TrimSpace(in.BaseURL), Region: "custom", SupportsModelList: true,
		}
	}

	cfg := provider.VendorConfig{
		ID: id, Name: firstNonEmpty(strings.TrimSpace(in.Name), meta.Label),
		Kind:    meta.Kind,
		BaseURL: firstNonEmpty(strings.TrimSpace(in.BaseURL), meta.BaseURL),
		APIKey:  strings.TrimSpace(in.APIKey),
		Custom:  catalogID == "",
	}
	// A masked round-trip submits an empty key, so fall back to what is stored.
	if cfg.APIKey == "" {
		if stored, ok := s.c.Providers.Config(id); ok {
			cfg.APIKey = stored.APIKey
		}
	}
	if cfg.BaseURL == "" {
		return provider.VendorConfig{}, provider.Vendor{}, fmt.Errorf("请填写服务地址")
	}
	meta.BaseURL = cfg.BaseURL
	return cfg, meta, nil
}

// ---------------------------------------------------------------------------
// Mode defaults
// ---------------------------------------------------------------------------

// ModeDefaults reports the model each agent mode starts with.
func (s *ModelService) ModeDefaults() ModeDefaultsView {
	work := s.c.Config.ModeDefault(protocol.ModeWork)
	coding := s.c.Config.ModeDefault(protocol.ModeCoding)
	return ModeDefaultsView{
		Work:   ModeDefaultView{Provider: work.Provider, Model: work.Model},
		Coding: ModeDefaultView{Provider: coding.Provider, Model: coding.Model},
	}
}

// SetModeDefault records which model a mode starts with.
func (s *ModelService) SetModeDefault(mode, providerID, model string) error {
	parsed, err := parseMode(mode)
	if err != nil {
		return err
	}
	vc, ok := s.vendor(providerID)
	if !ok {
		return fmt.Errorf("服务商不存在: %s", providerID)
	}
	if !hasModel(vc.EnabledModels, model) {
		return fmt.Errorf("模型 %s 尚未启用", model)
	}
	return s.c.Config.SetModeDefault(parsed, config.ModelChoice{Provider: providerID, Model: model})
}

// ModelOptions flattens every enabled model so the composer can offer one list.
func (s *ModelService) ModelOptions() []ModelOptionView {
	cfg := s.c.Config.Get()
	out := []ModelOptionView{}
	for _, vc := range cfg.Vendors {
		meta, _ := vc.Meta()
		name := firstNonEmpty(vc.Name, meta.Label, vc.ID)
		for _, m := range vc.EnabledModels {
			out = append(out, ModelOptionView{
				Provider: vc.ID, ProviderName: name,
				Model: m.ID, Label: firstNonEmpty(m.Label, m.ID),
				Vision: m.Vision, Context: m.Context, Reasoning: reasoningOptions(vc.ID, m),
			})
		}
	}
	return out
}

// reasoningOptions is deliberately adapter-gated. A catalog badge only says a
// model can emit thinking; the selector must not expose a control unless this
// application can send a documented request field for it.
func reasoningOptions(providerID string, model provider.ModelRef) []string {
	if !model.Reasoning {
		return nil
	}
	switch providerID {
	case "anthropic":
		return []string{"off", "low", "medium", "high"}
	case "volcengine-plan":
		// Agent Plan uses thinking.type=disabled to turn thinking off. GLM 5.3
		// and Kimi K3 are explicitly documented as always-thinking models.
		switch model.ID {
		case "glm-5.3", "glm-latest", "kimi-k3":
			return []string{"low", "medium", "high"}
		default:
			return []string{"off", "low", "medium", "high"}
		}
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Preferences
// ---------------------------------------------------------------------------

// GetPreference reads a small UI preference (theme, density, …).
func (s *ModelService) GetPreference(key string) string {
	return s.c.Config.Preference(key)
}

// SetPreference writes a small UI preference. An empty value clears it.
func (s *ModelService) SetPreference(key, value string) error {
	return s.c.Config.SetPreference(key, value)
}

// Preferences returns every stored preference in one round trip.
func (s *ModelService) Preferences() map[string]string {
	return s.c.Config.Get().Preferences
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// vendor reads a provider from the config store, which is the source of truth.
// The registry only caches the constructed clients, so reading through it would
// serve a stale enabled-model set right after a toggle.
func (s *ModelService) vendor(id string) (provider.VendorConfig, bool) {
	for _, vc := range s.c.Config.Get().Vendors {
		if vc.ID == id {
			return vc, true
		}
	}
	return provider.VendorConfig{}, false
}

// syncRegistry republishes the saved configs so the next turn builds its client
// from what the user just changed.
func (s *ModelService) syncRegistry() {
	s.c.Providers.Set(s.c.Config.VendorConfigs())
}

// seedModeDefaults points any mode that references an unconfigured provider at
// the one just saved, so the first provider a user adds is immediately usable
// in both Work and Coding.
func (s *ModelService) seedModeDefaults(cfg provider.VendorConfig) {
	choice := config.ModelChoice{Provider: cfg.ID, Model: cfg.DefaultModel}
	for _, mode := range []protocol.Mode{protocol.ModeWork, protocol.ModeCoding} {
		current := s.c.Config.ModeDefault(mode)
		if _, configured := s.vendor(current.Provider); configured {
			continue
		}
		_ = s.c.Config.SetModeDefault(mode, choice)
	}
	// The global pair mirrors the last explicit choice; older call sites and
	// the context-window lookup still read it.
	_ = s.c.Config.Update(func(c *config.Config) {
		c.Provider, c.Model = cfg.ID, cfg.DefaultModel
	})
}

// dropModeDefaultFor clears mode selections that point at a model the user has
// just switched off.
func (s *ModelService) dropModeDefaultFor(providerID, model string) {
	for _, mode := range []protocol.Mode{protocol.ModeWork, protocol.ModeCoding} {
		if current := s.c.Config.ModeDefault(mode); current.Provider == providerID && current.Model == model {
			_ = s.c.Config.Update(func(c *config.Config) { delete(c.ModeDefaults, string(mode)) })
		}
	}
}

// clearModeDefaultsFor drops every mode selection that names a provider, used
// when that provider is deleted outright.
func (s *ModelService) clearModeDefaultsFor(providerID string) {
	for _, mode := range []protocol.Mode{protocol.ModeWork, protocol.ModeCoding} {
		if current := s.c.Config.ModeDefault(mode); current.Provider == providerID {
			_ = s.c.Config.Update(func(c *config.Config) { delete(c.ModeDefaults, string(mode)) })
		}
	}
}

// newCustomID mints a stable id for a user-defined provider.
func (s *ModelService) newCustomID() string {
	for i := 1; ; i++ {
		id := fmt.Sprintf("custom-%d", i)
		if _, taken := s.vendor(id); !taken {
			return id
		}
	}
}

// firstNonEmpty returns the first non-blank candidate, so display names can
// fall back through user text, catalog label, and finally the raw id.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseVendorKind(kind string) (provider.VendorKind, error) {
	switch provider.VendorKind(strings.TrimSpace(kind)) {
	case "", provider.KindOpenAI:
		return provider.KindOpenAI, nil
	case provider.KindAnthropic:
		return provider.KindAnthropic, nil
	case provider.KindGemini:
		return provider.KindGemini, nil
	case provider.KindOllama:
		return provider.KindOllama, nil
	default:
		return "", fmt.Errorf("不支持的接口类型: %s", kind)
	}
}

func parseMode(mode string) (protocol.Mode, error) {
	switch protocol.Mode(strings.TrimSpace(mode)) {
	case protocol.ModeWork:
		return protocol.ModeWork, nil
	case protocol.ModeCoding:
		return protocol.ModeCoding, nil
	default:
		return "", fmt.Errorf("未知的模式: %s", mode)
	}
}

// normaliseModelRefs de-duplicates a submitted model list and backfills the
// capability flags from the catalog where the frontend did not send them.
func normaliseModelRefs(in []ProviderModelView) []provider.ModelRef {
	out := make([]provider.ModelRef, 0, len(in))
	seen := map[string]bool{}
	for _, m := range in {
		id := strings.TrimSpace(m.Model)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, provider.ModelRef{
			ID: id, Label: firstNonEmpty(m.Label, id), Custom: m.Custom,
			Reasoning: m.Reasoning, Vision: m.Vision, Tools: m.Tools, Context: m.Context,
		})
	}
	return out
}

func hasModel(refs []provider.ModelRef, model string) bool {
	for _, m := range refs {
		if m.ID == model {
			return true
		}
	}
	return false
}

// lookupPreset finds a catalog preset for a provider/model pair.
func lookupPreset(vendorID, model string) (provider.ModelPreset, bool) {
	meta, ok := provider.VendorByID(vendorID)
	if !ok {
		return provider.ModelPreset{}, false
	}
	for _, preset := range meta.Models {
		if preset.ID == model {
			return preset, true
		}
	}
	return provider.ModelPreset{}, false
}

// providerReady reports whether a config has everything its protocol needs.
func providerReady(meta provider.Vendor, cfg provider.VendorConfig) bool {
	if meta.Region == "local" || meta.Kind == provider.KindOllama {
		return true
	}
	return strings.TrimSpace(cfg.APIKey) != ""
}
