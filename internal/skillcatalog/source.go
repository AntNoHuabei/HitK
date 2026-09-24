package skillcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/skillmodel"
)

type sourceMetadata struct {
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
	URL       string `json:"url"`
	Ref       string `json:"ref"`
	SHA256    string `json:"sha256"`
}

func readSourceMetadata(root string) sourceMetadata {
	data, err := os.ReadFile(filepath.Join(root, ".hitk-source.json"))
	if err != nil {
		return sourceMetadata{}
	}
	var metadata sourceMetadata
	if json.Unmarshal(data, &metadata) != nil {
		return sourceMetadata{}
	}
	return metadata
}

var pluginMetadataDirs = []string{
	".codebuddy-plugin",
	".workbuddy-plugin",
	".claude-plugin",
}

const maxPackageSourceBytes = 512 << 20

func findPluginManifest(root string) (string, bool) {
	for _, dir := range pluginMetadataDirs {
		path := filepath.Join(root, dir, "plugin.json")
		if fileExists(path) {
			return path, true
		}
	}
	return "", false
}

func sourceKindForPluginDir(root string) skillmodel.SourceKind {
	if dirExists(filepath.Join(root, ".claude-plugin")) {
		return skillmodel.SourceClaude
	}
	return skillmodel.SourceWorkBuddy
}

func collectPackageFiles(ctx context.Context, root string) ([]string, string, error) {
	var files []string
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if name == ".git" || name == "node_modules" || name == ".venv" || name == "venv" {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		if entries > maxDiscoveryEntries {
			return fmt.Errorf("Skill 文件数量超过限制")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Strings(files)
	hash := sha256.New()
	var totalBytes int64
	for _, rel := range files {
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if statErr != nil {
			return nil, "", statErr
		}
		totalBytes += info.Size()
		if totalBytes > maxPackageSourceBytes {
			return nil, "", errors.New("Skill 源文件超过哈希大小限制")
		}
		_, _ = io.WriteString(hash, rel)
		file, openErr := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if openErr != nil {
			return nil, "", openErr
		}
		_, copyErr := io.Copy(hash, file)
		_ = file.Close()
		if copyErr != nil {
			return nil, "", copyErr
		}
	}
	return files, hex.EncodeToString(hash.Sum(nil)), nil
}

func renderSkillText(text, skillRoot, pluginRoot, dataDir, projectDir string) string {
	replacements := map[string]string{
		"${CODEBUDDY_SKILL_DIR}":   skillRoot,
		"${CLAUDE_SKILL_DIR}":      skillRoot,
		"${CODEBUDDY_PLUGIN_ROOT}": pluginRoot,
		"${CLAUDE_PLUGIN_ROOT}":    pluginRoot,
		"${CODEBUDDY_PLUGIN_DATA}": dataDir,
		"${CLAUDE_PLUGIN_DATA}":    dataDir,
		"${CODEBUDDY_PROJECT_DIR}": projectDir,
		"${CLAUDE_PROJECT_DIR}":    projectDir,
	}
	for from, to := range replacements {
		if to == "" {
			continue
		}
		text = strings.ReplaceAll(text, from, to)
	}
	return text
}

func pluginDataDir(dataRoot string, pluginName string) string {
	return filepath.Join(filepath.Clean(dataRoot), "plugin-data", normalizeID(pluginName))
}
