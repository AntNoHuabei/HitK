package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// VendorConfig is the user's saved settings for one catalog entry.
type VendorConfig struct {
	ID      string `json:"id"`
	APIKey  string `json:"apiKey,omitempty"`
	BaseURL string `json:"baseUrl,omitempty"` // overrides the catalog default
	// Name is the user-facing label. Built-in vendors fall back to the catalog
	// label, so it only has to be set for custom providers.
	Name string `json:"name,omitempty"`
	// Kind is the wire protocol family. Empty means "look it up in the
	// catalog"; custom providers must set it, because there is no catalog
	// entry to inherit from.
	Kind VendorKind `json:"kind,omitempty"`
	// DefaultModel is the provider's currently selected model.
	DefaultModel string `json:"defaultModel,omitempty"`
	// EnabledModels is the set the user has switched on. The UI renders the
	// catalog as a list of toggles, so this is the authoritative subset.
	EnabledModels  []ModelRef        `json:"enabledModels,omitempty"`
	EmbeddingModel string            `json:"embeddingModel,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Enabled        bool              `json:"enabled"`
	// Custom marks a user-defined provider that has no catalog entry.
	Custom bool `json:"custom,omitempty"`
}

// ModelRef is one model a user has switched on for a vendor.
//
// The capability flags are a snapshot taken when the model was enabled, which
// is what lets the UI keep rendering badges for a custom provider whose live
// model list is unavailable offline.
type ModelRef struct {
	ID        string `json:"id"`
	Label     string `json:"label,omitempty"`
	Custom    bool   `json:"custom,omitempty"`
	Reasoning bool   `json:"reasoning,omitempty"`
	Vision    bool   `json:"vision,omitempty"`
	Tools     bool   `json:"tools,omitempty"`
	Context   int    `json:"context,omitempty"`
}

// Meta resolves the catalog entry for a saved config. Custom providers have no
// catalog entry, so their identity comes from the config itself; synthesising a
// Vendor keeps every downstream code path (client construction, model listing)
// working against one shape.
func (c VendorConfig) Meta() (Vendor, bool) {
	if meta, ok := VendorByID(c.ID); ok {
		return meta, true
	}
	if !c.Custom {
		return Vendor{}, false
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = c.ID
	}
	kind := c.Kind
	if kind == "" {
		kind = KindOpenAI
	}
	return Vendor{
		ID:      c.ID,
		Label:   name,
		Kind:    kind,
		BaseURL: c.BaseURL,
		Region:  "custom",
		// A custom endpoint is assumed capable: the user typed the URL, so the
		// only honest answer to "can it list models" is "try it".
		SupportsModelList: true,
	}, true
}

// Registry resolves vendor ids to live [Provider] instances using saved
// credentials, rebuilding clients whenever settings change.
type Registry struct {
	mu      sync.RWMutex
	configs map[string]VendorConfig
	cache   map[string]Provider
	// custom holds caller-supplied providers that bypass the catalog, used by
	// tests and by embedders that need a provider the catalog cannot express.
	custom map[string]Provider
}

// NewRegistry builds an empty registry. Configs are seeded from the saved
// settings before first use; providers are constructed lazily.
func NewRegistry() *Registry {
	return &Registry{
		configs: map[string]VendorConfig{},
		cache:   map[string]Provider{},
		custom:  map[string]Provider{},
	}
}

// Register installs a caller-supplied provider under its own [Provider.ID].
// A registered provider takes precedence over the catalog and is never rebuilt
// from saved credentials.
func (r *Registry) Register(p Provider) {
	if p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.custom[p.ID()] = p
	delete(r.cache, p.ID())
}

// Set replaces the whole configuration set and drops cached clients.
func (r *Registry) Set(configs []VendorConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configs = make(map[string]VendorConfig, len(configs))
	for _, c := range configs {
		r.configs[c.ID] = c
	}
	r.cache = map[string]Provider{}
}

// SetOne updates a single vendor configuration.
func (r *Registry) SetOne(c VendorConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configs[c.ID] = c
	delete(r.cache, c.ID)
}

// Config returns the saved configuration for a vendor.
func (r *Registry) Config(id string) (VendorConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.configs[id]
	return c, ok
}

// Configs returns a copy of every saved configuration.
func (r *Registry) Configs() []VendorConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]VendorConfig, 0, len(r.configs))
	for _, c := range r.configs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Provider resolves a vendor id to a client, building it on first use.
func (r *Registry) Provider(id string) (Provider, error) {
	r.mu.RLock()
	if p, ok := r.custom[id]; ok {
		r.mu.RUnlock()
		return p, nil
	}
	if p, ok := r.cache[id]; ok {
		r.mu.RUnlock()
		return p, nil
	}
	cfg, ok := r.configs[id]
	meta, known := cfg.Meta()
	r.mu.RUnlock()

	if !known {
		return nil, fmt.Errorf("provider: unknown vendor %q", id)
	}
	// A vendor with no saved config still resolves when the catalog default
	// needs no key (Ollama), so local models work out of the box.
	if !ok {
		cfg = VendorConfig{ID: id, Enabled: false}
	}
	p, err := build(meta, cfg)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.cache[id] = p
	r.mu.Unlock()
	return p, nil
}

// Ready reports whether a vendor can actually be used right now.
func (r *Registry) Ready(id string) bool {
	p, err := r.Provider(id)
	if err != nil {
		return false
	}
	if oc, ok := p.(*OpenAICompat); ok {
		return oc.apiKey != "" || oc.local()
	}
	if a, ok := p.(*Anthropic); ok {
		return a.apiKey != ""
	}
	if g, ok := p.(*Gemini); ok {
		return g.apiKey != ""
	}
	return true
}

// build constructs the concrete client for a catalog entry.
func build(meta Vendor, cfg VendorConfig) (Provider, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = meta.BaseURL
	}
	key := strings.TrimSpace(cfg.APIKey)

	switch meta.Kind {
	case KindAnthropic:
		return NewAnthropic(AnthropicConfig{BaseURL: base, APIKey: key}), nil
	case KindGemini:
		return NewGemini(GeminiConfig{BaseURL: base, APIKey: key}), nil
	default: // KindOpenAI, KindOllama
		return NewOpenAICompat(OpenAICompatConfig{
			ID:        meta.ID,
			Label:     meta.Label,
			BaseURL:   base,
			APIKey:    key,
			Headers:   cfg.Headers,
			Embedding: cfg.EmbeddingModel,
		}), nil
	}
}

// ---------------------------------------------------------------------------
// Model enumeration
// ---------------------------------------------------------------------------

// ListModels enumerates a provider that may not be saved yet. The settings
// dialog needs the live list to populate its picker before the user commits,
// and it must work for a custom endpoint that has no catalog entry.
func ListModels(ctx context.Context, cfg VendorConfig) ([]string, error) {
	meta, ok := cfg.Meta()
	if !ok {
		return nil, fmt.Errorf("provider: unknown vendor %q", cfg.ID)
	}
	if !meta.SupportsModelList {
		return catalogModelIDs(meta), nil
	}
	names, err := listRemote(ctx, meta, cfg)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// Build constructs a client from an unsaved config, which is what lets the
// settings dialog test a connection before writing anything to disk.
func Build(cfg VendorConfig) (Provider, error) {
	meta, ok := cfg.Meta()
	if !ok {
		return nil, fmt.Errorf("provider: unknown vendor %q", cfg.ID)
	}
	return build(meta, cfg)
}

// Models fetches the live model list for a vendor. Vendors without a listing
// endpoint fall back to the embedded catalog presets.
func (r *Registry) Models(ctx context.Context, id string) ([]string, error) {
	cfg, _ := r.Config(id)
	meta, ok := cfg.Meta()
	if !ok {
		return nil, fmt.Errorf("provider: unknown vendor %q", id)
	}

	var (
		names []string
		err   error
	)
	if meta.SupportsModelList {
		names, err = listRemote(ctx, meta, cfg)
	}
	if err != nil || len(names) == 0 {
		names = catalogModelIDs(meta)
		// A custom endpoint has no presets to fall back on, so a failed listing
		// is the only signal the user gets that the URL or key is wrong.
		if err != nil && len(names) == 0 {
			return nil, err
		}
	}
	sort.Strings(names)
	return names, nil
}

// catalogModelIDs returns the chat-capable presets in display-neutral order.
// It is shared by saved and unsaved provider paths so vendors such as Agent
// Plan never accidentally probe an unsupported model-list endpoint.
func catalogModelIDs(meta Vendor) []string {
	names := make([]string, 0, len(meta.Models))
	for _, m := range meta.Models {
		if !m.Embedding {
			names = append(names, m.ID)
		}
	}
	sort.Strings(names)
	return names
}

func listRemote(ctx context.Context, meta Vendor, cfg VendorConfig) ([]string, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = meta.BaseURL
	}
	if base == "" {
		return nil, fmt.Errorf("provider %s: no base URL", meta.ID)
	}
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" && meta.Kind != KindOllama {
		return nil, ErrNoCredentials
	}

	var (
		endpoint string
		headers  = map[string]string{"Accept": "application/json"}
	)
	switch meta.Kind {
	case KindAnthropic:
		endpoint = strings.TrimRight(base, "/") + "/v1/models?limit=100"
		headers["x-api-key"] = key
		headers["anthropic-version"] = "2023-06-01"
	case KindGemini:
		endpoint = strings.TrimRight(base, "/") + "/models?pageSize=200&key=" + urlQueryEscape(key)
	default:
		endpoint = strings.TrimRight(base, "/") + "/models"
		if key != "" {
			headers["Authorization"] = "Bearer " + key
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%s: HTTP %d: %s", meta.ID, resp.StatusCode, summarizeError(body))
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name        string   `json:"name"`
			DisplayName string   `json:"displayName"`
			Methods     []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var names []string
	for _, d := range payload.Data {
		if d.ID != "" {
			names = append(names, d.ID)
		}
	}
	for _, m := range payload.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if name == "" {
			continue
		}
		// Gemini lists embedding and TTS models alongside chat models; keep
		// only the ones that can actually generate content.
		if len(m.Methods) > 0 {
			ok := false
			for _, method := range m.Methods {
				if method == "generateContent" || method == "streamGenerateContent" {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		names = append(names, name)
	}
	return names, nil
}

func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '~':
			b.WriteRune(r)
		default:
			for _, by := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", by)
			}
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Embeddings
// ---------------------------------------------------------------------------

// EmbedderFor returns an embedding client for a vendor, or nil when the vendor
// exposes no embedding model.
func (r *Registry) EmbedderFor(id string) Embedder {
	meta, ok := VendorByID(id)
	if !ok {
		return nil
	}
	cfg, _ := r.Config(id)
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = meta.BaseURL
	}
	model := cfg.EmbeddingModel
	if model == "" {
		model = meta.EmbeddingModel
	}
	if model == "" || base == "" {
		return nil
	}
	return NewRemoteEmbedder(id, base, cfg.APIKey, model, meta)
}

// RemoteEmbedder calls an OpenAI- or Gemini-shaped embeddings endpoint.
type RemoteEmbedder struct {
	vendorID string
	baseURL  string
	apiKey   string
	model    string
	kind     VendorKind
}

// NewRemoteEmbedder builds an embeddings client.
func NewRemoteEmbedder(vendorID, baseURL, apiKey, model string, meta Vendor) *RemoteEmbedder {
	return &RemoteEmbedder{
		vendorID: vendorID,
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiKey:   apiKey,
		model:    model,
		kind:     meta.Kind,
	}
}

// ID implements [Embedder].
func (e *RemoteEmbedder) ID() string { return e.vendorID + "/" + e.model }

// Dimensions reports 0 because the size is vendor-specific and discovered on
// first use.
func (e *RemoteEmbedder) Dimensions() int { return 0 }

// Embed implements [Embedder].
func (e *RemoteEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if e.kind == KindGemini {
		return e.embedGemini(ctx, texts)
	}
	return e.embedOpenAI(ctx, texts)
}

func (e *RemoteEmbedder) embedOpenAI(ctx context.Context, texts []string) ([][]float32, error) {
	if e.apiKey == "" && !strings.Contains(e.baseURL, "127.0.0.1") && !strings.Contains(e.baseURL, "localhost") {
		return nil, ErrNoCredentials
	}
	body := map[string]any{"model": e.model, "input": texts}
	raw, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%s embeddings: HTTP %d: %s", e.vendorID, resp.StatusCode, summarizeError(msg))
	}

	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	return vecs, nil
}

func (e *RemoteEmbedder) embedGemini(ctx context.Context, texts []string) ([][]float32, error) {
	if e.apiKey == "" {
		return nil, ErrNoCredentials
	}
	vecs := make([][]float32, 0, len(texts))
	for _, t := range texts {
		payload := map[string]any{
			"model":   "models/" + e.model,
			"content": map[string]any{"parts": []map[string]any{{"text": t}}},
		}
		raw, _ := json.Marshal(payload)

		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("%s/models/%s:embedContent", e.baseURL, e.model), strings.NewReader(string(raw)))
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", e.apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			return nil, err
		}
		var out struct {
			Embedding struct {
				Values []float32 `json:"values"`
			} `json:"embedding"`
		}
		derr := json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		cancel()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("gemini embeddings: HTTP %d", resp.StatusCode)
		}
		if derr != nil {
			return nil, derr
		}
		vecs = append(vecs, out.Embedding.Values)
	}
	return vecs, nil
}
