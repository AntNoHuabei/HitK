package skillruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Manager struct {
	runtimeRoot string
	dataRoot    string
}

func NewManager(runtimeRoot, dataRoot string) *Manager {
	return &Manager{runtimeRoot: filepath.Clean(runtimeRoot), dataRoot: filepath.Clean(dataRoot)}
}

func DefaultRuntimeRoot() string {
	if configured := strings.TrimSpace(os.Getenv("HITK_RUNTIME_ROOT")); configured != "" {
		if abs, err := filepath.Abs(configured); err == nil {
			return abs
		}
	}
	executable, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(executable), "runtime")
	}
	return "runtime"
}

func (m *Manager) RuntimeRoot() string { return m.runtimeRoot }

func (m *Manager) Manifest() (RuntimeManifest, error) {
	data, err := os.ReadFile(filepath.Join(m.runtimeRoot, "runtime-manifest.json"))
	if err != nil {
		return RuntimeManifest{}, err
	}
	var manifest RuntimeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest invalid: %w", err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return RuntimeManifest{}, fmt.Errorf("unsupported runtime manifest schema %d", manifest.SchemaVersion)
	}
	return manifest, nil
}

func (m *Manager) ListRuntimes() []RuntimeStatus {
	manifest, err := m.Manifest()
	if err != nil {
		return expectedRuntimeStatuses(m.runtimeRoot, err)
	}
	payloadVerified, payloadErr := m.VerifyPayload(manifest)
	statuses := make([]RuntimeStatus, 0, len(manifest.Runtimes))
	for _, spec := range manifest.Runtimes {
		path := m.executable(spec.Name)
		status := RuntimeStatus{Name: spec.Name, Version: spec.Version, Path: path}
		if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
			status.Message = "内置运行时未安装"
		} else if version, versionErr := readVersion(m.versionDir(spec.Name)); versionErr != nil || version != spec.Version {
			status.Available = true
			status.Message = "版本文件不匹配"
		} else {
			status.Available, status.Verified = true, payloadVerified
			if payloadErr != nil {
				status.Message = payloadErr.Error()
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// VerifyPayload checks the immutable staged tree against the aggregate digest
// written by the build script. Development trees may omit the digest.
func (m *Manager) VerifyPayload(manifest RuntimeManifest) (bool, error) {
	if manifest.PayloadDigest == "" {
		return false, errors.New("runtime payload digest is missing")
	}
	actual, err := ComputePayloadDigest(m.runtimeRoot)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(actual, manifest.PayloadDigest) {
		return false, fmt.Errorf("runtime payload digest mismatch: %s", actual)
	}
	return true, nil
}

// ComputePayloadDigest hashes every staged payload file using stable relative
// paths. The release staging tool calls this same implementation, avoiding
// shell-specific ordering and encoding differences.
func ComputePayloadDigest(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Clean(path) == filepath.Join(root, "runtime-manifest.json") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		_, _ = io.WriteString(h, filepath.ToSlash(rel))
		file, openErr := os.Open(path)
		if openErr != nil {
			return "", openErr
		}
		fileHash := sha256.New()
		_, copyErr := io.Copy(fileHash, file)
		_ = file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		_, _ = h.Write(fileHash.Sum(nil))
	}
	actual := hex.EncodeToString(h.Sum(nil))
	return actual, nil
}

func expectedRuntimeStatuses(root string, cause error) []RuntimeStatus {
	versions := map[string]string{"python": "3.12.14", "node": "24.21.0", "uv": "0.12.13", "pnpm": "11.19.0"}
	out := make([]RuntimeStatus, 0, len(versions))
	for _, name := range []string{"python", "node", "uv", "pnpm"} {
		out = append(out, RuntimeStatus{Name: name, Version: versions[name], Path: runtimeExecutable(root, name), Message: cause.Error()})
	}
	return out
}

func (m *Manager) LoadSkill(name string) (SkillManifest, string, error) {
	name = normalizeName(name)
	if name == "" {
		return SkillManifest{}, "", errors.New("Skill 名称无效")
	}
	candidates := []string{
		filepath.Join(m.runtimeRoot, "skills", name),
		filepath.Join(m.dataRoot, "skills", name),
	}
	for _, dir := range candidates {
		data, err := os.ReadFile(filepath.Join(dir, "skill.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return SkillManifest{}, "", err
		}
		var manifest SkillManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return SkillManifest{}, "", fmt.Errorf("Skill manifest invalid: %w", err)
		}
		if err := validateSkillManifest(manifest, dir); err != nil {
			return SkillManifest{}, "", err
		}
		return manifest, dir, nil
	}
	return SkillManifest{}, "", fmt.Errorf("Skill %q 未安装", name)
}

func validateSkillManifest(manifest SkillManifest, dir string) error {
	if manifest.SchemaVersion != ManifestSchemaVersion || normalizeName(manifest.Name) == "" {
		return errors.New("Skill manifest schema 或名称无效")
	}
	if manifest.Runtime.Kind != "python" && manifest.Runtime.Kind != "node" {
		return errors.New("Skill runtime 仅支持 python 或 node")
	}
	if manifest.Entry == "" || !within(dir, filepath.Join(dir, filepath.FromSlash(manifest.Entry))) {
		return errors.New("Skill 入口路径无效")
	}
	pm := manifest.PackageManager.Name
	if pm != "" && !((manifest.Runtime.Kind == "python" && pm == "uv") || (manifest.Runtime.Kind == "node" && (pm == "pnpm" || pm == "npm"))) {
		return errors.New("Skill package manager 与 runtime 不匹配")
	}
	paths := append([]string{manifest.PackageManager.DependencyFile, manifest.PackageManager.Lockfile}, manifest.Assets...)
	for _, rel := range paths {
		if rel != "" && !within(dir, filepath.Join(dir, filepath.FromSlash(rel))) {
			return errors.New("Skill 文件路径越界")
		}
	}
	return nil
}

func (m *Manager) GetSkillEnvironment(name string) SkillEnvironmentStatus {
	manifest, skillDir, err := m.LoadSkill(name)
	if err != nil {
		return SkillEnvironmentStatus{Skill: normalizeName(name), State: "failed", Error: err.Error(), UpdatedAt: nowMillis()}
	}
	hash, err := m.planHash(manifest, skillDir)
	if err != nil {
		return SkillEnvironmentStatus{Skill: manifest.Name, State: "failed", Error: err.Error(), UpdatedAt: nowMillis()}
	}
	if manifest.Builtin {
		entry := filepath.Join(skillDir, filepath.FromSlash(manifest.Entry))
		python := m.executable(manifest.Runtime.Kind)
		complete := fileExists(entry) && fileExists(python)
		for _, rel := range manifest.Assets {
			if _, err := os.Stat(filepath.Join(skillDir, filepath.FromSlash(rel))); err != nil {
				complete = false
				break
			}
		}
		if complete {
			return SkillEnvironmentStatus{Skill: manifest.Name, State: "ready", Environment: skillDir, PlanHash: hash, RuntimeVersion: m.runtimeVersion(manifest.Runtime.Kind), UpdatedAt: nowMillis()}
		}
		return SkillEnvironmentStatus{Skill: manifest.Name, State: "failed", Environment: skillDir, PlanHash: hash, Error: "内置 Skill 运行时不完整", UpdatedAt: nowMillis()}
	}
	statePath := m.statePath(manifest.Name)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return SkillEnvironmentStatus{Skill: manifest.Name, State: "none", PlanHash: hash, RuntimeVersion: m.runtimeVersion(manifest.Runtime.Kind), UpdatedAt: nowMillis()}
	}
	var record environmentRecord
	if json.Unmarshal(data, &record) != nil || record.PlanHash != hash || !dirExists(record.Environment) {
		return SkillEnvironmentStatus{Skill: manifest.Name, State: "none", PlanHash: hash, RuntimeVersion: m.runtimeVersion(manifest.Runtime.Kind), UpdatedAt: nowMillis()}
	}
	return record.SkillEnvironmentStatus
}

func (m *Manager) PrepareSkillEnvironment(ctx context.Context, name string, allowNetwork bool) (SkillEnvironmentStatus, error) {
	manifest, skillDir, err := m.LoadSkill(name)
	if err != nil {
		return SkillEnvironmentStatus{}, err
	}
	if manifest.Builtin {
		status := m.GetSkillEnvironment(name)
		if status.State != "ready" {
			return status, errors.New(status.Error)
		}
		return status, nil
	}
	if manifest.Permissions.InstallNetwork && !allowNetwork {
		return SkillEnvironmentStatus{}, errors.New("Skill 依赖安装需要联网确认")
	}
	hash, err := m.planHash(manifest, skillDir)
	if err != nil {
		return SkillEnvironmentStatus{}, err
	}
	if current := m.GetSkillEnvironment(name); current.State == "ready" && current.PlanHash == hash {
		return current, nil
	}
	environments := filepath.Join(m.dataRoot, "skill-environments", manifest.Name)
	if err := os.MkdirAll(environments, 0o755); err != nil {
		return SkillEnvironmentStatus{}, err
	}
	target := filepath.Join(environments, hash)
	temporary := target + ".partial"
	_ = os.RemoveAll(temporary)
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		return SkillEnvironmentStatus{}, err
	}
	if err := m.install(ctx, manifest, skillDir, temporary, allowNetwork); err != nil {
		_ = os.RemoveAll(temporary)
		status := SkillEnvironmentStatus{Skill: manifest.Name, State: "failed", PlanHash: hash, RuntimeVersion: m.runtimeVersion(manifest.Runtime.Kind), Error: err.Error(), UpdatedAt: nowMillis()}
		_ = m.writeState(manifest, status)
		return status, err
	}
	_ = os.RemoveAll(target)
	if err := os.Rename(temporary, target); err != nil {
		_ = os.RemoveAll(temporary)
		return SkillEnvironmentStatus{}, err
	}
	status := SkillEnvironmentStatus{Skill: manifest.Name, State: "ready", Environment: target, PlanHash: hash, RuntimeVersion: m.runtimeVersion(manifest.Runtime.Kind), UpdatedAt: nowMillis()}
	return status, m.writeState(manifest, status)
}

func (m *Manager) RepairSkillEnvironment(ctx context.Context, name string, allowNetwork bool) (SkillEnvironmentStatus, error) {
	if err := m.RemoveSkillEnvironment(name); err != nil {
		return SkillEnvironmentStatus{}, err
	}
	return m.PrepareSkillEnvironment(ctx, name, allowNetwork)
}

func (m *Manager) RemoveSkillEnvironment(name string) error {
	name = normalizeName(name)
	root := filepath.Join(m.dataRoot, "skill-environments")
	target := filepath.Join(root, name)
	if name == "" || !within(root, target) {
		return errors.New("Skill 环境路径无效")
	}
	return os.RemoveAll(target)
}

func (m *Manager) install(ctx context.Context, manifest SkillManifest, skillDir, environment string, network bool) error {
	pm := manifest.PackageManager
	if pm.Name == "" || pm.DependencyFile == "" {
		return nil
	}
	if !fileExists(filepath.Join(skillDir, filepath.FromSlash(pm.Lockfile))) {
		return errors.New("Skill 必须提供完整锁文件")
	}
	cacheRoot := filepath.Join(m.dataRoot, "runtime-cache")
	_ = os.MkdirAll(cacheRoot, 0o755)
	if manifest.Runtime.Kind == "python" {
		python, uv := m.executable("python"), m.executable("uv")
		env := installerEnv(m.runtimeRoot, cacheRoot, network)
		if err := runCommand(ctx, uv, []string{"venv", "--python", python, environment}, skillDir, env); err != nil {
			return err
		}
		args := []string{"sync", "--project", skillDir, "--frozen", "--no-install-project", "--no-build"}
		if !network {
			args = append(args, "--offline")
		}
		env = append(env, "UV_PROJECT_ENVIRONMENT="+environment)
		return runCommand(ctx, uv, args, skillDir, env)
	}
	for _, rel := range []string{pm.DependencyFile, pm.Lockfile} {
		data, err := os.ReadFile(filepath.Join(skillDir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(environment, filepath.Base(rel)), data, 0o644); err != nil {
			return err
		}
	}
	env := installerEnv(m.runtimeRoot, cacheRoot, network)
	if pm.Name == "npm" {
		args := []string{"ci", "--ignore-scripts"}
		if !network {
			args = append(args, "--offline")
		}
		return runCommand(ctx, filepath.Join(m.runtimeRoot, "node", "npm.cmd"), args, environment, env)
	}
	args := []string{filepath.Join(m.runtimeRoot, "pnpm", "bin", "pnpm.cjs"), "install", "--frozen-lockfile", "--ignore-scripts", "--dir", environment}
	if !network {
		args = append(args, "--offline")
	}
	return runCommand(ctx, m.executable("node"), args, environment, env)
}

func installerEnv(runtimeRoot, cacheRoot string, network bool) []string {
	env := minimalHostEnv(filepath.Join(runtimeRoot, "node"), filepath.Join(runtimeRoot, "python"), filepath.Join(runtimeRoot, "uv"))
	env = append(env,
		"UV_PYTHON="+runtimeExecutable(runtimeRoot, "python"),
		"UV_NO_MANAGED_PYTHON=true", "UV_PYTHON_DOWNLOADS=never",
		"UV_CACHE_DIR="+filepath.Join(cacheRoot, "uv"),
		"PNPM_HOME="+filepath.Join(runtimeRoot, "pnpm"),
		"PNPM_STORE_DIR="+filepath.Join(cacheRoot, "pnpm"),
		"npm_config_cache="+filepath.Join(cacheRoot, "npm"),
		"npm_config_ignore_scripts=true",
	)
	if network {
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
			if value := os.Getenv(key); value != "" {
				env = append(env, key+"="+value)
			}
		}
	}
	return env
}

func runCommand(ctx context.Context, command string, args []string, dir string, env []string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir, cmd.Env = dir, env
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &limitedWriter{Writer: &output, Remaining: 1 << 20}, &limitedWriter{Writer: &output, Remaining: 1 << 20}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(command), err, strings.TrimSpace(output.String()))
	}
	return nil
}

type limitedWriter struct {
	Writer    io.Writer
	Remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	if w.Remaining > 0 {
		chunk := p
		if len(chunk) > w.Remaining {
			chunk = chunk[:w.Remaining]
		}
		_, _ = w.Writer.Write(chunk)
		w.Remaining -= len(chunk)
	}
	return original, nil
}

func (m *Manager) planHash(manifest SkillManifest, skillDir string) (string, error) {
	files := []string{"skill.json", manifest.Entry, manifest.PackageManager.DependencyFile, manifest.PackageManager.Lockfile}
	sort.Strings(files)
	h := sha256.New()
	_, _ = io.WriteString(h, m.runtimeVersion(manifest.Runtime.Kind))
	for _, rel := range files {
		if rel == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(skillDir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, filepath.ToSlash(rel))
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func (m *Manager) statePath(name string) string {
	return filepath.Join(m.dataRoot, "skill-environments", normalizeName(name), "state.json")
}
func (m *Manager) writeState(manifest SkillManifest, status SkillEnvironmentStatus) error {
	path := m.statePath(manifest.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(environmentRecord{SkillEnvironmentStatus: status, Manifest: manifest}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) executable(name string) string { return runtimeExecutable(m.runtimeRoot, name) }
func runtimeExecutable(root, name string) string {
	switch name {
	case "python":
		return filepath.Join(root, "python", "python.exe")
	case "node":
		return filepath.Join(root, "node", "node.exe")
	case "uv":
		return filepath.Join(root, "uv", "uv.exe")
	case "pnpm":
		return filepath.Join(root, "pnpm", "bin", "pnpm.cjs")
	default:
		return filepath.Join(root, name)
	}
}
func (m *Manager) versionDir(name string) string {
	if name == "pnpm" {
		return filepath.Join(m.runtimeRoot, "pnpm")
	}
	return filepath.Dir(m.executable(name))
}
func (m *Manager) runtimeVersion(name string) string {
	version, _ := readVersion(m.versionDir(name))
	return version
}
func readVersion(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	return strings.TrimSpace(string(data)), err
}
func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, r := range value {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return value
}
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }
func dirExists(path string) bool  { info, err := os.Stat(path); return err == nil && info.IsDir() }
func minimalHostEnv(paths ...string) []string {
	env := []string{"PATH=" + strings.Join(paths, string(os.PathListSeparator))}
	for _, key := range []string{"SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	if runtime.GOOS != "windows" {
		env = append(env, "LANG=C.UTF-8")
	}
	return env
}
