// Package config holds HitK's on-disk settings and data locations.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

// Config is the persisted application state.
type Config struct {
	// Workspace is the project root used by coding tools.
	Workspace string `json:"workspace"`
	// Mode is the last used agent mode.
	Mode protocol.Mode `json:"mode"`
	// Policy is the approval policy for side-effecting tools.
	Policy protocol.ApprovalPolicy `json:"policy"`
	// ModeDefaults picks the model each agent mode starts with, so Work and
	// Coding can run on different models. Provider/Model below remain as the
	// global fallback for configs written before this existed.
	ModeDefaults map[string]ModelChoice `json:"modeDefaults"`
	Provider     string                 `json:"provider"`
	Model        string                 `json:"model"`
	// Vendors holds per-vendor credentials, enabled models and endpoint
	// overrides. Built-in vendors are keyed by catalog id; custom providers
	// carry Custom=true and their own name/kind.
	Vendors []provider.VendorConfig `json:"vendors"`
	// EmbeddingVendor and EmbeddingModel select the retrieval embedder. Empty
	// means the offline hashed model is used.
	EmbeddingVendor string `json:"embeddingVendor"`
	EmbeddingModel  string `json:"embeddingModel"`
	// MaxSteps caps tool-calling rounds per turn.
	MaxSteps int `json:"maxSteps"`
	// ContextLimit overrides the model's context window when set.
	ContextLimit int `json:"contextLimit"`
	// LastCollection is the knowledge base the work view was last scoped to.
	LastCollection string `json:"lastCollection"`
	// RecentWorkspaces powers the workspace switcher.
	RecentWorkspaces []string `json:"recentWorkspaces"`
	// Preferences stores small UI settings verbatim (theme id, density, …).
	// Keeping them opaque means a new UI preference never needs a schema
	// change here.
	Preferences map[string]string `json:"preferences,omitempty"`
	// Onboarded flips once the user has configured a first model.
	Onboarded bool `json:"onboarded"`
}

// ModelChoice names the provider and model an agent mode runs with.
type ModelChoice struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// IsZero reports whether no model has been chosen.
func (m ModelChoice) IsZero() bool {
	return m.Provider == "" && m.Model == ""
}

// Default returns the out-of-the-box configuration.
func Default() Config {
	return Config{
		Mode:     protocol.ModeWork,
		Policy:   protocol.ApprovalAuto,
		Provider: "deepseek",
		Model:    "deepseek-flash",
		MaxSteps: protocol.DefaultMaxSteps,
		ModeDefaults: map[string]ModelChoice{
			string(protocol.ModeWork):   {Provider: "deepseek", Model: "deepseek-flash"},
			string(protocol.ModeCoding): {Provider: "deepseek", Model: "deepseek-flash"},
			string(protocol.ModeCompanion): {
				Provider: "deepseek",
				Model:    "deepseek-flash",
			},
		},
		Preferences: map[string]string{},
		Onboarded:   false,
	}
}

// Store loads and saves the configuration file.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
	// dataDir is the root for the knowledge base and thread files.
	dataDir string
}

// Paths resolves the platform data directory.
type Paths struct {
	// DataDir holds the knowledge base and threads.
	DataDir string
	// ConfigFile is the settings JSON path.
	ConfigFile string
	// KBFile is the bbolt knowledge-base file.
	KBFile string
	// CompanionFile is the bbolt database owned by the companion module. It is
	// deliberately separate from the knowledge base so either can be backed up
	// or reset independently.
	CompanionFile string
	// CompanionRegistryFile indexes every companion character and records which
	// one is active.
	CompanionRegistryFile string
	// CompanionDir holds one bbolt database per extra companion character. The
	// default character keeps living in CompanionFile so no migration is needed.
	CompanionDir string
	// ThreadsDir holds one JSON file per conversation.
	ThreadsDir string
	// LogFile receives the application log.
	LogFile string
}

// ResolvePaths computes the on-disk layout for the current user.
func ResolvePaths() (Paths, error) {
	base := os.Getenv("HITK_DATA_DIR")
	if base == "" {
		var err error
		switch runtime.GOOS {
		case "windows":
			base = os.Getenv("APPDATA")
			if base == "" {
				base, err = os.UserConfigDir()
				if err != nil {
					return Paths{}, err
				}
			}
		case "darwin":
			home, err := os.UserHomeDir()
			if err != nil {
				return Paths{}, err
			}
			base = filepath.Join(home, "Library", "Application Support")
		default:
			base = os.Getenv("XDG_CONFIG_HOME")
			if base == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return Paths{}, err
				}
				base = filepath.Join(home, ".config")
			}
		}
		base = filepath.Join(base, "HitK")
	}

	paths := Paths{
		DataDir:       base,
		ConfigFile:    filepath.Join(base, "config.json"),
		KBFile:        filepath.Join(base, "knowledge.db"),
		CompanionFile: filepath.Join(base, "companion.db"),
		CompanionRegistryFile: filepath.Join(base, "companions.db"),
		CompanionDir:          filepath.Join(base, "companions"),
		ThreadsDir:    filepath.Join(base, "threads"),
		LogFile:       filepath.Join(base, "hitk.log"),
	}
	for _, dir := range []string{paths.DataDir, paths.ThreadsDir, paths.CompanionDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Paths{}, err
		}
	}
	return paths, nil
}

// Load reads the configuration, falling back to defaults when absent or
// unreadable. A corrupt file is preserved as .bak rather than deleted, so a
// user's vendor keys are never silently thrown away.
func Load(paths Paths) (*Store, error) {
	s := &Store{path: paths.ConfigFile, cfg: Default(), dataDir: paths.DataDir}

	raw, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.cfg); err != nil {
		_ = os.Rename(paths.ConfigFile, paths.ConfigFile+".bak")
		s.cfg = Default()
		return s, nil
	}
	// normalize rewrites retired model ids. Persisting the result makes the
	// repair happen once, instead of being silently re-derived on every start
	// while the file on disk keeps the stale value.
	if s.normalize() {
		_ = s.Save()
	}
	return s, nil
}

// normalize repairs combinations that would leave the app in a broken state.
// It reports whether the configuration changed, so the caller can persist it.
func (s *Store) normalize() bool {
	changed := false
	if s.cfg.MaxSteps <= 0 {
		s.cfg.MaxSteps = protocol.DefaultMaxSteps
		changed = true
	}
	if s.cfg.MaxSteps > protocol.MaxStepsCeiling {
		s.cfg.MaxSteps = protocol.MaxStepsCeiling
		changed = true
	}
	if !s.cfg.Mode.Valid() {
		s.cfg.Mode = protocol.ModeWork
		changed = true
	}
	switch s.cfg.Policy {
	case protocol.ApprovalReadOnly, protocol.ApprovalAuto, protocol.ApprovalManual:
	default:
		s.cfg.Policy = protocol.ApprovalAuto
		changed = true
	}
	if s.cfg.Provider == "" {
		s.cfg.Provider = Default().Provider
		changed = true
	}
	if s.cfg.Model == "" {
		if meta, ok := provider.VendorByID(s.cfg.Provider); ok {
			s.cfg.Model = meta.DefaultModel
			changed = true
		}
	}
	// Older configs only knew the single Provider/Model pair. Promoting it into
	// both mode slots keeps Work and Coding working without a migration step.
	if s.cfg.ModeDefaults == nil {
		s.cfg.ModeDefaults = map[string]ModelChoice{}
		changed = true
	}
	fallback := ModelChoice{Provider: s.cfg.Provider, Model: s.cfg.Model}
	for _, mode := range []protocol.Mode{protocol.ModeWork, protocol.ModeCoding} {
		if choice, ok := s.cfg.ModeDefaults[string(mode)]; ok && !choice.IsZero() {
			continue
		}
		s.cfg.ModeDefaults[string(mode)] = fallback
		changed = true
	}
	if choice, ok := s.cfg.ModeDefaults[string(protocol.ModeCompanion)]; !ok || choice.IsZero() {
		work := s.cfg.ModeDefaults[string(protocol.ModeWork)]
		if work.IsZero() {
			work = fallback
		}
		s.cfg.ModeDefaults[string(protocol.ModeCompanion)] = work
		changed = true
	}
	if s.cfg.Preferences == nil {
		s.cfg.Preferences = map[string]string{}
		changed = true
	}
	// Vendors retire model ids between HitK releases. A saved default still
	// pointing at one would fail every turn until the user reopened settings,
	// so the known renames are applied here.
	if next := provider.ReplacementModel(s.cfg.Provider, s.cfg.Model); next != "" {
		s.cfg.Model = next
		changed = true
	}
	for mode, choice := range s.cfg.ModeDefaults {
		next := provider.ReplacementModel(choice.Provider, choice.Model)
		if next == "" {
			continue
		}
		choice.Model = next
		s.cfg.ModeDefaults[mode] = choice
		changed = true
	}
	for i := range s.cfg.Vendors {
		if provider.NormalizeVendorModels(&s.cfg.Vendors[i]) {
			changed = true
		}
	}
	return changed
}

// Get returns a copy of the configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.cfg
	out.Vendors = append([]provider.VendorConfig(nil), s.cfg.Vendors...)
	out.RecentWorkspaces = append([]string(nil), s.cfg.RecentWorkspaces...)
	out.ModeDefaults = make(map[string]ModelChoice, len(s.cfg.ModeDefaults))
	for k, v := range s.cfg.ModeDefaults {
		out.ModeDefaults[k] = v
	}
	out.Preferences = make(map[string]string, len(s.cfg.Preferences))
	for k, v := range s.cfg.Preferences {
		out.Preferences[k] = v
	}
	return out
}

// ModeDefault returns the saved model choice for a mode, falling back to the
// global selection.
func (s *Store) ModeDefault(mode protocol.Mode) ModelChoice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if choice, ok := s.cfg.ModeDefaults[string(mode)]; ok && !choice.IsZero() {
		return choice
	}
	for _, choice := range s.cfg.ModeDefaults {
		if !choice.IsZero() {
			return choice
		}
	}
	return ModelChoice{Provider: s.cfg.Provider, Model: s.cfg.Model}
}

// SetModeDefault records which model a mode should start on.
func (s *Store) SetModeDefault(mode protocol.Mode, choice ModelChoice) error {
	return s.Update(func(c *Config) {
		if c.ModeDefaults == nil {
			c.ModeDefaults = map[string]ModelChoice{}
		}
		c.ModeDefaults[string(mode)] = choice
	})
}

// Preference reads a UI preference, returning "" when unset.
func (s *Store) Preference(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Preferences[key]
}

// SetPreference writes a UI preference.
func (s *Store) SetPreference(key, value string) error {
	return s.Update(func(c *Config) {
		if c.Preferences == nil {
			c.Preferences = map[string]string{}
		}
		if value == "" {
			delete(c.Preferences, key)
			return
		}
		c.Preferences[key] = value
	})
}

// SetVendorModel records which model a provider currently runs.
func (s *Store) SetVendorModel(id, model string) error {
	return s.Update(func(c *Config) {
		for i := range c.Vendors {
			if c.Vendors[i].ID == id {
				c.Vendors[i].DefaultModel = model
				return
			}
		}
	})
}

// SetVendorModelRefs replaces the enabled-model set for one provider.
func (s *Store) SetVendorModelRefs(id string, models []provider.ModelRef) error {
	return s.Update(func(c *Config) {
		for i := range c.Vendors {
			if c.Vendors[i].ID == id {
				c.Vendors[i].EnabledModels = append([]provider.ModelRef(nil), models...)
				return
			}
		}
	})
}

// DeleteVendorConfig removes a vendor's saved settings.
func (s *Store) DeleteVendorConfig(id string) error {
	return s.Update(func(c *Config) {
		kept := c.Vendors[:0]
		for _, vc := range c.Vendors {
			if vc.ID != id {
				kept = append(kept, vc)
			}
		}
		c.Vendors = kept
		// Leaving a mode pointing at a deleted provider would make the next
		// turn fail with a confusing "unknown vendor" instead of falling back.
		for mode, choice := range c.ModeDefaults {
			if choice.Provider == id {
				delete(c.ModeDefaults, mode)
			}
		}
	})
}

// Update mutates the configuration through a callback and persists it.
func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	fn(&s.cfg)
	_ = s.normalize()
	s.mu.Unlock()
	return s.Save()
}

// Save writes the current configuration.
func (s *Store) Save() error {
	s.mu.RLock()
	raw, err := json.MarshalIndent(s.cfg, "", "  ")
	path := s.path
	s.mu.RUnlock()
	if err != nil {
		return err
	}

	// Secrets live in this file, so keep it owner-only where the OS supports it.
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	return nil
}

// VendorConfigs returns the saved vendor settings.
func (s *Store) VendorConfigs() []provider.VendorConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]provider.VendorConfig(nil), s.cfg.Vendors...)
}

// SetVendorConfig inserts or replaces one vendor's settings.
func (s *Store) SetVendorConfig(cfg provider.VendorConfig) error {
	return s.Update(func(c *Config) {
		for i := range c.Vendors {
			if c.Vendors[i].ID == cfg.ID {
				// An empty API key means "leave the stored one alone", so the
				// settings UI can round-trip a masked field without wiping it.
				if cfg.APIKey == "" {
					cfg.APIKey = c.Vendors[i].APIKey
				}
				c.Vendors[i] = cfg
				return
			}
		}
		c.Vendors = append(c.Vendors, cfg)
	})
}

// AddRecentWorkspace records a workspace at the head of the recents list.
func (s *Store) AddRecentWorkspace(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	return s.Update(func(c *Config) {
		out := []string{dir}
		for _, d := range c.RecentWorkspaces {
			if !strings.EqualFold(d, dir) {
				out = append(out, d)
			}
		}
		if len(out) > 10 {
			out = out[:10]
		}
		c.RecentWorkspaces = out
		c.Workspace = dir
	})
}

// DataDir reports the application data root.
func (s *Store) DataDir() string { return s.dataDir }
