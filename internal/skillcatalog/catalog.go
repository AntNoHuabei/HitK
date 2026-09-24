// Package skillcatalog discovers Skill packages from HitK, WorkBuddy,
// CodeBuddy, Claude and imported SkillHub sources.
//
// The package is deliberately read-only with respect to source packages. It
// never installs dependencies or executes Skill code.
package skillcatalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hitk/hitk/internal/skillmodel"
)

const (
	maxDiscoveryEntries = 5000
	maxDiscoveryDepth   = 8
)

type rootSpec struct {
	path      string
	source    skillmodel.SourceKind
	trust     skillmodel.TrustLevel
	priority  int
	pluginDir bool
}

// Catalog owns a snapshot of every currently discoverable Skill package.
type Catalog struct {
	mu sync.RWMutex

	runtimeRoot string
	dataRoot    string
	workspace   string
	mode        skillmodel.CompatibilityMode

	loaded    bool
	packages  map[string]skillmodel.Package
	aliases   map[string]string
	summaries []skillmodel.SkillSummary
}

func NewCatalog(runtimeRoot, dataRoot string) *Catalog {
	mode := skillmodel.ParseCompatibilityMode(strings.TrimSpace(os.Getenv("HITK_SKILL_COMPAT")))
	return &Catalog{
		runtimeRoot: filepath.Clean(runtimeRoot),
		dataRoot:    filepath.Clean(dataRoot),
		mode:        mode,
		packages:    map[string]skillmodel.Package{},
		aliases:     map[string]string{},
	}
}

func (c *Catalog) SetWorkspace(workspace string) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = filepath.Clean(workspace)
	} else if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	c.mu.Lock()
	if c.workspace != workspace {
		c.loaded = false
	}
	c.workspace = workspace
	c.mu.Unlock()
}

func (c *Catalog) SetMode(mode skillmodel.CompatibilityMode) {
	c.mu.Lock()
	c.mode = mode
	c.loaded = false
	c.mu.Unlock()
}

func (c *Catalog) Mode() skillmodel.CompatibilityMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode
}

func (c *Catalog) List(ctx context.Context) ([]skillmodel.SkillSummary, error) {
	if err := c.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]skillmodel.SkillSummary(nil), c.summaries...), nil
}

func (c *Catalog) Get(ctx context.Context, id string) (skillmodel.Package, error) {
	if err := c.ensureLoaded(ctx); err != nil {
		return skillmodel.Package{}, err
	}
	key := normalizeID(id)
	c.mu.RLock()
	resolved := key
	if alias, ok := c.aliases[key]; ok {
		resolved = alias
	}
	pkg, ok := c.packages[resolved]
	c.mu.RUnlock()
	if !ok {
		return skillmodel.Package{}, fmt.Errorf("Skill %q 未安装", id)
	}
	return pkg, nil
}

func (c *Catalog) Reload(ctx context.Context, _ string) error {
	c.mu.Lock()
	c.loaded = false
	c.mu.Unlock()
	return c.ensureLoaded(ctx)
}

func (c *Catalog) ReloadAll(ctx context.Context) error {
	return c.Reload(ctx, "")
}

func (c *Catalog) ensureLoaded(ctx context.Context) error {
	c.mu.RLock()
	loaded := c.loaded
	c.mu.RUnlock()
	if loaded {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return nil
	}

	started := time.Now()
	roots := c.discoveryRootsLocked()
	found := map[string]skillmodel.Package{}
	priorities := map[string]int{}
	aliases := map[string]string{}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if root.path == "" {
			continue
		}
		info, err := os.Stat(root.path)
		if err != nil || !info.IsDir() {
			continue
		}
		packages, err := discoverRoot(ctx, root, c.dataRoot)
		if err != nil {
			// One broken optional source must not hide all other skills.
			continue
		}
		for _, pkg := range packages {
			if !c.mode.AllowsPrompt() {
				continue
			}
			if !c.mode.AllowsResolve() && pkg.Kind == skillmodel.SkillKindScript {
				continue
			}
			if pkg.Kind == skillmodel.SkillKindPluginHook || pkg.Kind == skillmodel.SkillKindPluginMCP {
				continue
			}
			id := normalizeID(pkg.ID)
			if id == "" {
				continue
			}
			if previous, ok := priorities[id]; ok && previous <= root.priority {
				continue
			}
			pkg.ID = id
			found[id] = pkg
			priorities[id] = root.priority
			aliases[normalizeID(pkg.Name)] = id
			if pkg.Plugin != nil {
				aliases[normalizeID(pkg.Plugin.Name+":"+pkg.Name)] = id
			}
		}
	}

	summaries := make([]skillmodel.SkillSummary, 0, len(found))
	for _, pkg := range found {
		summaries = append(summaries, summaryForPackage(pkg))
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].ID < summaries[j].ID
	})

	c.packages = found
	c.aliases = aliases
	c.summaries = summaries
	c.loaded = true
	slog.Info("skill catalog loaded", "skills", len(summaries), "roots", len(roots), "durationMs", time.Since(started).Milliseconds())
	return nil
}

func (c *Catalog) discoveryRootsLocked() []rootSpec {
	roots := []rootSpec{
		{path: filepath.Join(c.runtimeRoot, "skills"), source: skillmodel.SourceNative, trust: skillmodel.TrustBuiltin, priority: 100},
		{path: filepath.Join(c.runtimeRoot, "resources", "plugins"), source: skillmodel.SourceWorkBuddy, trust: skillmodel.TrustBuiltin, priority: 95, pluginDir: true},
		{path: filepath.Join(c.dataRoot, "skills"), source: skillmodel.SourceNative, trust: skillmodel.TrustUser, priority: 60},
		{path: filepath.Join(c.dataRoot, "skill-catalog"), source: skillmodel.SourceSkillHub, trust: skillmodel.TrustMarketplace, priority: 30, pluginDir: true},
		{path: filepath.Join(c.dataRoot, "plugins"), source: skillmodel.SourceWorkBuddy, trust: skillmodel.TrustPlugin, priority: 40, pluginDir: true},
	}
	if c.workspace != "" {
		roots = append(roots,
			rootSpec{path: filepath.Join(c.workspace, ".hitk", "skills"), source: skillmodel.SourceNative, trust: skillmodel.TrustProject, priority: 80},
			rootSpec{path: filepath.Join(c.workspace, ".workbuddy", "skills"), source: skillmodel.SourceWorkBuddy, trust: skillmodel.TrustProject, priority: 76},
			rootSpec{path: filepath.Join(c.workspace, ".codebuddy", "skills"), source: skillmodel.SourceWorkBuddy, trust: skillmodel.TrustProject, priority: 74},
			rootSpec{path: filepath.Join(c.workspace, ".claude", "skills"), source: skillmodel.SourceClaude, trust: skillmodel.TrustProject, priority: 72},
		)
	}
	return roots
}

func summaryForPackage(pkg skillmodel.Package) skillmodel.SkillSummary {
	summary := skillmodel.SkillSummary{
		ID:           pkg.ID,
		Name:         pkg.Name,
		DisplayName:  pkg.DisplayName,
		Description:  pkg.Description,
		Kind:         pkg.Kind,
		Source:       pkg.Source,
		Version:      pkg.Version,
		Publisher:    pkg.Publisher,
		SourceURL:    pkg.SourceURL,
		Ref:          pkg.Ref,
		SourceSHA256: pkg.SourceSHA256,
		Trust:        pkg.Trust,
		State:        skillmodel.StateDiscovered,
		Mutating:     pkg.Manifest.Permissions.RuntimeNetwork,
	}
	if summary.DisplayName == "" {
		summary.DisplayName = summary.Name
	}
	return summary
}

func discoverRoot(ctx context.Context, root rootSpec, dataRoot string) ([]skillmodel.Package, error) {
	rootPath := filepath.Clean(root.path)
	if pluginManifest, ok := findPluginManifest(rootPath); ok {
		return loadPluginPackages(ctx, rootPath, pluginManifest, root, dataRoot)
	}
	if fileExists(filepath.Join(rootPath, "skill.json")) {
		pkg, err := loadNativePackage(rootPath, root)
		if err != nil {
			return nil, err
		}
		return []skillmodel.Package{pkg}, nil
	}
	if fileExists(filepath.Join(rootPath, "SKILL.md")) {
		pkg, err := loadSkillMDPackage(ctx, rootPath, root, nil)
		if err != nil {
			return nil, err
		}
		return []skillmodel.Package{pkg}, nil
	}

	var packages []skillmodel.Package
	entries := 0
	walkErr := filepath.WalkDir(rootPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries++
		if entries > maxDiscoveryEntries {
			return errors.New("Skill 发现数量超过限制")
		}
		if entry.IsDir() {
			if path != rootPath && (strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".previous")) {
				return filepath.SkipDir
			}
			if path != rootPath {
				if manifest, ok := findPluginManifest(path); ok {
					pluginPackages, loadErr := loadPluginPackages(ctx, path, manifest, root, dataRoot)
					if loadErr == nil {
						packages = append(packages, pluginPackages...)
					}
					return filepath.SkipDir
				}
			}
			depth, _ := filepath.Rel(rootPath, path)
			if depth != "." && strings.Count(depth, string(os.PathSeparator)) >= maxDiscoveryDepth {
				return filepath.SkipDir
			}
			return nil
		}
		switch entry.Name() {
		case "SKILL.md":
			pkg, loadErr := loadSkillMDPackage(ctx, filepath.Dir(path), root, nil)
			if loadErr == nil {
				packages = append(packages, pkg)
			}
		case "skill.json":
			dir := filepath.Dir(path)
			if fileExists(filepath.Join(dir, "SKILL.md")) {
				return nil
			}
			pkg, loadErr := loadNativePackage(dir, root)
			if loadErr == nil {
				packages = append(packages, pkg)
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return packages, nil
}

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "\\", "/")
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == ':' || r == '/'
		if valid {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
