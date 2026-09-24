package skillcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
)

type pluginJSON struct {
	Name           string                 `json:"name"`
	Version        string                 `json:"version"`
	Description    string                 `json:"description"`
	Skills         json.RawMessage        `json:"skills"`
	Commands       json.RawMessage        `json:"commands"`
	Hooks          json.RawMessage        `json:"hooks"`
	MCPServers     json.RawMessage        `json:"mcpServers"`
	DefaultEnabled *bool                  `json:"defaultEnabled"`
	Dependencies   []pluginDependencyJSON `json:"dependencies"`
}

type pluginDependencyJSON struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Marketplace string `json:"marketplace"`
}

func loadPluginPackages(ctx context.Context, root, manifestPath string, spec rootSpec, dataRoot string) ([]skillmodel.Package, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var raw pluginJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("plugin.json 无效: %w", err)
	}
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = filepath.Base(root)
	}
	plugin := &skillmodel.PluginPackage{
		ID:         normalizeID(name),
		Name:       name,
		Version:    strings.TrimSpace(raw.Version),
		Root:       root,
		DataDir:    pluginDataDir(dataRoot, name),
		Commands:   decodeSkillPaths(raw.Commands),
		Hooks:      string(raw.Hooks),
		MCPServers: string(raw.MCPServers),
	}
	if raw.DefaultEnabled != nil {
		plugin.DefaultEnabled = *raw.DefaultEnabled
	}
	for _, dependency := range raw.Dependencies {
		if strings.TrimSpace(dependency.Name) == "" {
			continue
		}
		plugin.Dependencies = append(plugin.Dependencies, skillmodel.PluginDependency{
			Name:        strings.TrimSpace(dependency.Name),
			Version:     strings.TrimSpace(dependency.Version),
			Marketplace: strings.TrimSpace(dependency.Marketplace),
		})
	}

	skillPaths := decodeSkillPaths(raw.Skills)
	if len(skillPaths) == 0 {
		skillPaths = []string{"skills"}
	}
	var packages []skillmodel.Package
	for _, rel := range skillPaths {
		base := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(rel, "./")))
		if fileExists(filepath.Join(base, "SKILL.md")) {
			pkg, loadErr := loadSkillMDPackage(ctx, base, spec, plugin)
			if loadErr == nil {
				packages = append(packages, pkg)
			}
			continue
		}
		entries, readErr := os.ReadDir(base)
		if readErr != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillRoot := filepath.Join(base, entry.Name())
			if !fileExists(filepath.Join(skillRoot, "SKILL.md")) {
				continue
			}
			pkg, loadErr := loadSkillMDPackage(ctx, skillRoot, spec, plugin)
			if loadErr == nil {
				packages = append(packages, pkg)
			}
		}
	}
	return packages, nil
}

func decodeSkillPaths(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if strings.TrimSpace(single) != "" {
			return []string{single}
		}
		return nil
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		out := make([]string, 0, len(many))
		for _, value := range many {
			if strings.TrimSpace(value) != "" {
				out = append(out, value)
			}
		}
		return out
	}
	return nil
}
