package skillcatalog

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxPackageDownloadBytes = 512 << 20
	maxPackageExtractBytes  = 2 << 30
)

type InstallRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	Source    string `json:"source"` // local | zip | git | skillhub
	Path      string `json:"path,omitempty"`
	URL       string `json:"url,omitempty"`
	Ref       string `json:"ref,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type SkillHubFeed struct {
	Version int             `json:"version"`
	Skills  []SkillHubEntry `json:"skills"`
}

type SkillHubEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type Installer struct {
	dataRoot string
	client   *http.Client
}

func NewInstaller(dataRoot string) *Installer {
	return &Installer{
		dataRoot: filepath.Clean(dataRoot),
		client:   &http.Client{Timeout: 10 * time.Minute},
	}
}

func (i *Installer) ResolveFeed(ctx context.Context, url string, allowNetwork bool) (SkillHubFeed, error) {
	if !allowNetwork {
		return SkillHubFeed{}, errors.New("读取 SkillHub feed 需要联网确认")
	}
	if strings.TrimSpace(url) == "" {
		return SkillHubFeed{}, errors.New("SkillHub feed URL 为空")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return SkillHubFeed{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := i.client.Do(request)
	if err != nil {
		return SkillHubFeed{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SkillHubFeed{}, fmt.Errorf("SkillHub feed 返回 HTTP %d", response.StatusCode)
	}
	var feed SkillHubFeed
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&feed); err != nil {
		return SkillHubFeed{}, fmt.Errorf("SkillHub feed 无效: %w", err)
	}
	return feed, nil
}

// Install copies a source into the immutable Skill catalog root. The final
// directory replaces an existing version only after download and extraction
// have completed successfully.
func (i *Installer) Install(ctx context.Context, request InstallRequest) (string, error) {
	id := normalizeID(request.ID)
	if id == "" {
		id = normalizeID(request.Name)
	}
	if id == "" {
		return "", errors.New("Skill 缺少 id 或 name")
	}
	root := filepath.Join(i.dataRoot, "skill-catalog")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	temporary := filepath.Join(root, "."+id+"-install")
	_ = os.RemoveAll(temporary)
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)

	var sourceDir string
	switch strings.ToLower(strings.TrimSpace(request.Source)) {
	case "", "local", "directory":
		sourceDir = strings.TrimSpace(request.Path)
		if sourceDir == "" {
			return "", errors.New("本地 Skill 缺少 path")
		}
		if err := copyDirectory(ctx, sourceDir, temporary); err != nil {
			return "", err
		}
	case "zip":
		archive := strings.TrimSpace(request.Path)
		if archive == "" && strings.TrimSpace(request.URL) != "" {
			archive = filepath.Join(temporary, "download.zip")
			if err := i.download(ctx, request.URL, archive); err != nil {
				return "", err
			}
		}
		if archive == "" {
			return "", errors.New("ZIP Skill 缺少 url 或 path")
		}
		if request.SHA256 != "" {
			if err := verifyFileSHA256(archive, request.SHA256); err != nil {
				return "", err
			}
		}
		extractRoot := filepath.Join(temporary, "extract")
		if err := extractZip(archive, extractRoot); err != nil {
			return "", err
		}
		packageRoot, err := locatePackageRoot(extractRoot)
		if err != nil {
			return "", err
		}
		sourceDir = packageRoot
	case "git":
		if strings.TrimSpace(request.URL) == "" {
			return "", errors.New("Git Skill 缺少 url")
		}
		sourceDir = filepath.Join(temporary, "repo")
		if err := cloneGit(ctx, request.URL, request.Ref, sourceDir); err != nil {
			return "", err
		}
	case "skillhub":
		return "", errors.New("SkillHub feed 条目必须先解析为 zip 或 git URL")
	default:
		return "", fmt.Errorf("不支持的 Skill 来源: %s", request.Source)
	}
	if sourceDir != "" && filepath.Clean(sourceDir) != filepath.Clean(temporary) {
		staged := filepath.Join(temporary, "package")
		if err := copyDirectory(ctx, sourceDir, staged); err != nil {
			return "", err
		}
		sourceDir = staged
	} else {
		sourceDir = temporary
	}
	if _, err := locatePackageRoot(sourceDir); err != nil {
		return "", err
	}
	metadata := map[string]string{
		"id":        id,
		"name":      strings.TrimSpace(request.Name),
		"version":   strings.TrimSpace(request.Version),
		"publisher": strings.TrimSpace(request.Publisher),
		"source":    strings.TrimSpace(request.Source),
		"url":       strings.TrimSpace(request.URL),
		"ref":       strings.TrimSpace(request.Ref),
		"sha256":    strings.TrimSpace(request.SHA256),
	}
	metaData, _ := json.MarshalIndent(metadata, "", "  ")
	if err := os.WriteFile(filepath.Join(sourceDir, ".hitk-source.json"), metaData, 0o600); err != nil {
		return "", err
	}

	target := filepath.Join(root, id)
	backup := filepath.Join(root, "."+id+".previous")
	_ = os.RemoveAll(backup)
	if dirExists(target) {
		if err := os.Rename(target, backup); err != nil {
			return "", err
		}
	}
	if err := os.Rename(sourceDir, target); err != nil {
		if dirExists(backup) {
			_ = os.Rename(backup, target)
		}
		return "", err
	}
	return target, nil
}

// Rollback swaps the current Skill with the previous imported version.
func (i *Installer) Rollback(id string) error {
	id = normalizeID(id)
	if id == "" {
		return errors.New("Skill ID 无效")
	}
	root := filepath.Join(i.dataRoot, "skill-catalog")
	target := filepath.Join(root, id)
	backup := filepath.Join(root, "."+id+".previous")
	if !dirExists(backup) {
		return errors.New("没有可回滚的上一版本")
	}
	failed := filepath.Join(root, "."+id+".rollback")
	_ = os.RemoveAll(failed)
	if dirExists(target) {
		if err := os.Rename(target, failed); err != nil {
			return err
		}
	}
	if err := os.Rename(backup, target); err != nil {
		if dirExists(failed) {
			_ = os.Rename(failed, target)
		}
		return err
	}
	_ = os.RemoveAll(failed)
	return nil
}

// Prune removes the retained previous version.
func (i *Installer) Prune(id string) error {
	id = normalizeID(id)
	if id == "" {
		return errors.New("Skill ID 无效")
	}
	return os.RemoveAll(filepath.Join(i.dataRoot, "skill-catalog", "."+id+".previous"))
}

func (i *Installer) download(ctx context.Context, url, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := i.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("下载 Skill 失败: HTTP %d", response.StatusCode)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	written, err := io.Copy(output, io.LimitReader(response.Body, maxPackageDownloadBytes+1))
	if err != nil {
		return err
	}
	if written > maxPackageDownloadBytes {
		_ = os.Remove(destination)
		return errors.New("Skill 包超过大小限制")
	}
	return output.Sync()
}

func copyDirectory(ctx context.Context, source, destination string) error {
	source = filepath.Clean(source)
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("Skill 源目录不可用: %s", source)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if name == ".git" || name == "node_modules" || name == ".venv" || name == "venv" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Skill 源包含不支持的符号链接: %s", rel)
		}
		return copyFile(path, target)
	})
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func extractZip(archive, destination string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	var extracted int64
	for _, entry := range reader.File {
		rel := filepath.Clean(filepath.FromSlash(entry.Name))
		if rel == "." {
			continue
		}
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("Skill ZIP 包含越界路径: %s", entry.Name)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Skill ZIP 包含符号链接: %s", entry.Name)
		}
		target := filepath.Join(destination, rel)
		if !within(destination, target) {
			return fmt.Errorf("Skill ZIP 包含越界路径: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = source.Close()
			return err
		}
		written, copyErr := io.Copy(output, source)
		closeErr := output.Close()
		_ = source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		extracted += written
		if extracted > maxPackageExtractBytes {
			return errors.New("Skill 解压后超过大小限制")
		}
	}
	return nil
}

func locatePackageRoot(root string) (string, error) {
	if isPackageRoot(root) {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() && isPackageRoot(filepath.Join(root, entry.Name())) {
			candidates = append(candidates, filepath.Join(root, entry.Name()))
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", errors.New("压缩包中未找到 Skill 或插件根目录")
}

func isPackageRoot(root string) bool {
	if fileExists(filepath.Join(root, "skill.json")) || fileExists(filepath.Join(root, "SKILL.md")) {
		return true
	}
	_, ok := findPluginManifest(root)
	return ok
}

func cloneGit(ctx context.Context, url, ref, destination string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return errors.New("Git Skill 需要系统 git 命令")
	}
	args := []string{"clone", "--depth", "1"}
	if strings.TrimSpace(ref) != "" {
		args = append(args, "--branch", strings.TrimSpace(ref))
	}
	args = append(args, url, destination)
	cmd := exec.CommandContext(ctx, git, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone 失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func verifyFileSHA256(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("Skill 包 SHA256 不匹配")
	}
	return nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
