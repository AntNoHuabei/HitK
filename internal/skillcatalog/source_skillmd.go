package skillcatalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
	"gopkg.in/yaml.v3"
)

type skillFrontmatter struct {
	Slug        string `yaml:"slug"`
	Name        string `yaml:"name"`
	DisplayName string `yaml:"display_name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
	Entry       string `yaml:"entry"`
	Runtime     struct {
		Kind       string `yaml:"kind"`
		Constraint string `yaml:"constraint"`
	} `yaml:"runtime"`
}

func loadSkillMDPackage(ctx context.Context, root string, spec rootSpec, plugin *skillmodel.PluginPackage) (skillmodel.Package, error) {
	path := filepath.Join(root, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return skillmodel.Package{}, err
	}
	meta, body, err := parseSkillMarkdown(data)
	if err != nil {
		return skillmodel.Package{}, fmt.Errorf("SKILL.md 无效: %w", err)
	}
	files, sourceHash, err := collectPackageFiles(ctx, root)
	if err != nil {
		return skillmodel.Package{}, err
	}
	baseName := filepath.Base(root)
	name := firstNonEmpty(meta.Name, meta.Slug, baseName)
	id := normalizeID(firstNonEmpty(meta.Slug, meta.Name))
	if id == "" {
		id = normalizeID(baseName)
	}
	if plugin != nil {
		id = normalizeID(plugin.Name + ":" + firstNonEmpty(meta.Slug, name))
	}
	entry := filepath.ToSlash(strings.TrimSpace(meta.Entry))
	if entry == "" {
		entry = discoverEntryFromBody(string(body))
	}
	if entry == "" {
		entry = discoverEntry(root)
	}
	kind := skillmodel.SkillKindPrompt
	runtimeSpec := skillmodel.RuntimeConstraint{Kind: strings.ToLower(strings.TrimSpace(meta.Runtime.Kind)), Constraint: strings.TrimSpace(meta.Runtime.Constraint)}
	if entry != "" || runtimeSpec.Kind != "" {
		kind = skillmodel.SkillKindScript
	}
	if runtimeSpec.Kind == "" && entry != "" {
		switch strings.ToLower(filepath.Ext(entry)) {
		case ".py":
			runtimeSpec.Kind = "python"
		case ".js", ".mjs", ".cjs":
			runtimeSpec.Kind = "node"
		}
	}
	if kind == skillmodel.SkillKindScript && runtimeSpec.Kind == "" {
		return skillmodel.Package{}, errors.New("脚本 Skill 缺少 runtime.kind")
	}
	source := spec.source
	if plugin != nil && spec.source != skillmodel.SourceSkillHub {
		source = sourceKindForPluginDir(plugin.Root)
	}
	sourceMetadata := readSourceMetadata(root)
	version := firstNonEmpty(meta.Version, sourceMetadata.Version)
	manifest := skillmodel.SkillManifest{
		SchemaVersion: skillmodel.ManifestSchemaVersion,
		Name:          name,
		Version:       version,
		Builtin:       spec.trust == skillmodel.TrustBuiltin,
		Kind:          kind,
		Invocation:    invocationForSkillMD(kind),
		Runtime:       runtimeSpec,
		Entry:         entry,
		Source:        source,
		SourceID:      id,
		SourceHash:    sourceHash,
	}
	projectDir := ""
	if spec.trust == skillmodel.TrustProject {
		projectDir = projectRootForSkill(root)
	}
	rendered := renderSkillText(string(body), root, pluginRoot(plugin), pluginData(plugin), projectDir)
	return skillmodel.Package{
		ID:           id,
		Name:         name,
		DisplayName:  firstNonEmpty(meta.DisplayName, name),
		Description:  strings.TrimSpace(meta.Description),
		Version:      version,
		Publisher:    sourceMetadata.Publisher,
		SourceURL:    sourceMetadata.URL,
		Ref:          sourceMetadata.Ref,
		SourceSHA256: sourceMetadata.SHA256,
		Kind:         kind,
		Source:       source,
		Trust:        spec.trust,
		Entry:        entry,
		Manifest:     manifest,
		Body:         rendered,
		Files:        files,
		Plugin:       plugin,
		SourceHash:   sourceHash,
		Root:         root,
		SkillFile:    path,
	}, nil
}

func parseSkillMarkdown(data []byte) (skillFrontmatter, string, error) {
	var meta skillFrontmatter
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return meta, text, nil
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return meta, "", errors.New("frontmatter 未闭合")
	}
	if err := yaml.Unmarshal([]byte(strings.TrimSpace(parts[1])), &meta); err != nil {
		return meta, "", err
	}
	return meta, strings.TrimLeft(parts[2], "\r\n"), nil
}

func discoverEntry(root string) string {
	candidates := []string{"main.py", "index.js", "index.mjs", "index.cjs", "scripts/main.py", "scripts/index.js", "scripts/watermark.py"}
	for _, candidate := range candidates {
		if fileExists(filepath.Join(root, filepath.FromSlash(candidate))) {
			return candidate
		}
	}
	entries, err := os.ReadDir(root)
	if err == nil {
		var rootScripts []string
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".py", ".js", ".mjs", ".cjs":
				rootScripts = append(rootScripts, entry.Name())
			}
		}
		if len(rootScripts) == 1 {
			return rootScripts[0]
		}
	}
	scriptsDir := filepath.Join(root, "scripts")
	if info, err := os.Stat(scriptsDir); err == nil && info.IsDir() {
		var scripts []string
		_ = filepath.WalkDir(scriptsDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".py", ".js", ".mjs", ".cjs":
				rel, relErr := filepath.Rel(root, path)
				if relErr == nil {
					scripts = append(scripts, filepath.ToSlash(rel))
				}
			}
			return nil
		})
		if len(scripts) == 1 {
			return scripts[0]
		}
	}
	return ""
}

var markdownEntryRE = regexp.MustCompile(`(?:^|[\s"'` + "`" + `])((?:scripts|src)/[A-Za-z0-9_./-]+\.(?:py|js|mjs|cjs|ts))`)

func discoverEntryFromBody(body string) string {
	match := markdownEntryRE.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return filepath.ToSlash(strings.TrimSpace(match[1]))
}

func invocationForSkillMD(kind skillmodel.SkillKind) skillmodel.InvocationMode {
	if kind == skillmodel.SkillKindScript {
		return skillmodel.InvocationArgv
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func projectRootForSkill(skillRoot string) string {
	path := filepath.Clean(skillRoot)
	for {
		if dirExists(filepath.Join(path, ".hitk")) || dirExists(filepath.Join(path, ".git")) {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func pluginRoot(plugin *skillmodel.PluginPackage) string {
	if plugin == nil {
		return ""
	}
	return plugin.Root
}

func pluginData(plugin *skillmodel.PluginPackage) string {
	if plugin == nil {
		return ""
	}
	return plugin.DataDir
}

func normalizeFrontmatterList(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		parts := strings.Split(typed, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		sort.Strings(out)
		return out
	case []any:
		var out []string
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		sort.Strings(out)
		return out
	default:
		return nil
	}
}

func trimBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
}
