package skillruntime

import (
	"bytes"
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
	"time"

	"github.com/hitk/hitk/internal/skillmodel"
)

// Prepare, Cancel and Remove make Manager satisfy skillmodel.Executor without
// forcing tools to depend on the concrete runtime package.
func (m *Manager) Prepare(ctx context.Context, plan skillmodel.ResolvedPlan, allowNetwork bool) (skillmodel.SkillEnvironmentStatus, error) {
	return m.PrepareResolvedPlan(ctx, plan, allowNetwork)
}

func (m *Manager) Cancel(name string) error {
	return m.CancelSkillEnvironment(name)
}

func (m *Manager) Remove(name string) error {
	return m.RemoveSkillEnvironment(name)
}

// PrepareResolvedPlan locks and installs a compatibility Skill plan. It is the
// only runtime path allowed to materialize packages for external Skills.
func (m *Manager) PrepareResolvedPlan(ctx context.Context, plan skillmodel.ResolvedPlan, allowNetwork bool) (skillmodel.SkillEnvironmentStatus, error) {
	skill := normalizeName(plan.ID)
	if skill == "" {
		return skillmodel.SkillEnvironmentStatus{}, errors.New("Skill 名称无效")
	}
	if plan.Kind == skillmodel.SkillKindPrompt {
		return m.resolvedReadyStatus(plan, ""), nil
	}
	if len(plan.Unresolved) > 0 {
		status := m.resolvedStatus(plan, skillmodel.StateApprovalRequired)
		status.Error = skillmodel.ErrUnresolvedDependencies.Error()
		m.publishResolvedStatus(status)
		return status, skillmodel.ErrUnresolvedDependencies
	}
	if prepared, ok := m.loadPreparedResolvedPlan(plan); ok {
		status := m.resolvedReadyStatus(prepared, m.resolvedEnvironmentPath(prepared))
		return status, nil
	}
	if plan.Environment == "component" {
		status, err := m.PrepareSkillEnvironment(ctx, skill, allowNetwork)
		status.PlanHash = plan.PlanHash
		return status, err
	}
	if !allowNetwork && planNeedsNetwork(plan) && !m.resolvedEnvironmentReady(plan) {
		status := m.resolvedStatus(plan, skillmodel.StateApprovalRequired)
		status.Error = ""
		m.publishResolvedStatus(status)
		return status, skillmodel.ErrDependencyApprovalRequired
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	if m.planOps == nil {
		m.planOps = map[string]context.CancelFunc{}
	}
	m.planOps[skill] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.planOps, skill)
		m.mu.Unlock()
		cancel()
	}()

	status := m.resolvedStatus(plan, skillmodel.StateInstalling)
	m.publishResolvedStatus(status)
	finalPlan, environment, err := m.installResolvedPlan(runCtx, plan, allowNetwork)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status.State = skillmodel.StateCancelled
			status.Error = "Skill 环境初始化已取消"
		} else {
			status.State = skillmodel.StateFailed
			status.Error = err.Error()
		}
		status.UpdatedAt = nowMillis()
		m.publishResolvedStatus(status)
		return status, err
	}
	finalPlan.PlanHash = ComputeResolvedPlanHash(finalPlan)
	if err := writeResolvedPlanFile(m.resolvedPlanPath(finalPlan), finalPlan); err != nil {
		return status, err
	}
	status = m.resolvedReadyStatus(finalPlan, environment)
	m.publishResolvedStatus(status)
	return status, nil
}

// ExecuteResolved runs a script Skill from an already prepared ResolvedPlan.
func (m *Manager) ExecuteResolved(ctx context.Context, plan skillmodel.ResolvedPlan, request skillmodel.SkillRequest) (skillmodel.SkillExecutionResult, error) {
	if plan.Kind != skillmodel.SkillKindScript {
		return skillmodel.SkillExecutionResult{}, errors.New("Skill 不需要脚本执行")
	}
	status, err := m.PrepareResolvedPlan(ctx, plan, false)
	if err != nil {
		return skillmodel.SkillExecutionResult{}, err
	}
	if status.State != skillmodel.StateReady {
		return skillmodel.SkillExecutionResult{}, fmt.Errorf("Skill 环境未就绪: %s", status.State)
	}
	if request.Workspace != "" {
		request.Workspace, err = filepath.Abs(request.Workspace)
		if err != nil || !dirExists(request.Workspace) {
			return skillmodel.SkillExecutionResult{}, errors.New("Skill 工作区无效")
		}
	}
	var payload []byte
	if plan.Invocation != skillmodel.InvocationArgv {
		payload, err = json.Marshal(request)
		if err != nil {
			return skillmodel.SkillExecutionResult{}, err
		}
	}
	timeout := time.Duration(plan.Budget.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sourceRoot := plan.SourceRoot
	if sourceRoot == "" || !dirExists(sourceRoot) {
		return skillmodel.SkillExecutionResult{}, errors.New("Skill 源目录不可用")
	}
	entry := filepath.Join(sourceRoot, filepath.FromSlash(plan.Entry))
	if !within(sourceRoot, entry) || !fileExists(entry) {
		return skillmodel.SkillExecutionResult{}, errors.New("Skill 入口路径无效")
	}
	command, args := m.executable(plan.Runtime.Kind), []string(nil)
	environment := status.Environment
	if plan.Runtime.Kind == "python" {
		command = filepath.Join(environment, "Scripts", "python.exe")
		if runtime.GOOS != "windows" {
			command = filepath.Join(environment, "bin", "python")
		}
		args = []string{"-X", "utf8", "-s", "-B", entry}
	} else {
		args = []string{entry}
	}
	if plan.Invocation == skillmodel.InvocationArgv {
		if strings.TrimSpace(request.Command) != "" && !strings.EqualFold(request.Command, "run") {
			args = append(args, request.Command)
		}
		args = append(args, request.Args...)
	}
	stdoutLimit := plan.Budget.MaxOutputBytes
	if stdoutLimit <= 0 {
		stdoutLimit = 1 << 20
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(command, args...)
	cmd.Dir = sourceRoot
	if len(payload) > 0 {
		cmd.Stdin = bytes.NewReader(payload)
	}
	cmd.Stdout = &limitedWriter{Writer: &stdout, Remaining: stdoutLimit}
	cmd.Stderr = &limitedWriter{Writer: &stderr, Remaining: 256 << 10}
	cmd.Env = resolvedExecutionEnv(m.runtimeRoot, m.dataRoot, sourceRoot, environment, plan.Permissions.RuntimeNetwork)
	job, err := startManagedProcess(cmd, plan.Budget.MemoryBytes)
	if err != nil {
		return skillmodel.SkillExecutionResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-runCtx.Done():
		_ = job.Kill()
		err = <-done
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return skillmodel.SkillExecutionResult{}, errors.New("Skill 执行超时")
		}
		return skillmodel.SkillExecutionResult{}, errors.New("Skill 执行已取消")
	}
	_ = job.Close()
	runErr := err
	if runErr != nil && !(plan.Invocation == skillmodel.InvocationArgv && stdout.Len() > 0) {
		return skillmodel.SkillExecutionResult{}, fmt.Errorf("Skill 执行失败: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() >= stdoutLimit {
		return skillmodel.SkillExecutionResult{}, errors.New("Skill 响应超过大小限制")
	}
	var result skillmodel.SkillExecutionResult
	if plan.Invocation == skillmodel.InvocationArgv {
		result = cliExecutionResult(stdout.String())
		if result.Metadata == nil {
			result.Metadata = map[string]interface{}{}
		}
		if runErr == nil {
			result.Metadata["exitCode"] = 0
		} else if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.Metadata["exitCode"] = exitErr.ExitCode()
		}
	} else {
		result, err = decodeExecutionResult(stdout.Bytes())
		if err != nil {
			return skillmodel.SkillExecutionResult{}, err
		}
	}
	if err := validateResolvedArtifacts(result.Artifacts, request.Workspace); err != nil {
		return skillmodel.SkillExecutionResult{}, err
	}
	return result, nil
}

func cliExecutionResult(output string) skillmodel.SkillExecutionResult {
	content := strings.TrimSpace(output)
	result := skillmodel.SkillExecutionResult{Content: content, Display: content}
	if newline := strings.IndexByte(content, '\n'); newline >= 0 {
		result.Display = strings.TrimSpace(content[:newline])
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "wrote ") {
			continue
		}
		path := strings.TrimSpace(line[len("wrote "):])
		if path == "" {
			continue
		}
		result.Artifacts = append(result.Artifacts, skillmodel.SkillArtifact{Path: path, Name: filepath.Base(path), Operation: "created"})
	}
	if len([]rune(result.Display)) > 120 {
		result.Display = string([]rune(result.Display)[:120]) + "…"
	}
	return result
}

func (m *Manager) installResolvedPlan(ctx context.Context, plan skillmodel.ResolvedPlan, network bool) (skillmodel.ResolvedPlan, string, error) {
	planDir := filepath.Join(m.dataRoot, "skill-plans", normalizeName(plan.ID), shortHash(plan.SourceHash))
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return plan, "", err
	}
	cacheRoot := filepath.Join(m.dataRoot, "runtime-cache")
	env := installerEnv(m.runtimeRoot, cacheRoot, network)

	switch plan.Runtime.Kind {
	case "python":
		project, err := pythonProject(plan)
		if err != nil {
			return plan, "", err
		}
		if err := os.WriteFile(filepath.Join(planDir, "pyproject.toml"), project, 0o600); err != nil {
			return plan, "", err
		}
		uv := m.executable("uv")
		if err := runCommand(ctx, uv, []string{"lock", "--project", planDir}, planDir, env); err != nil {
			return plan, "", err
		}
		lockDigest, err := fileDigest(filepath.Join(planDir, "uv.lock"))
		if err != nil {
			return plan, "", err
		}
		plan.LockDigest = lockDigest
		plan.PlanHash = ComputeResolvedPlanHash(plan)
		environment := m.resolvedEnvironmentPath(plan)
		if err := os.MkdirAll(filepath.Dir(environment), 0o755); err != nil {
			return plan, "", err
		}
		_ = os.RemoveAll(environment)
		python := m.executable("python")
		if err := runCommand(ctx, uv, []string{"venv", "--python", python, environment}, planDir, env); err != nil {
			return plan, "", err
		}
		syncEnv := append(append([]string(nil), env...), "UV_PROJECT_ENVIRONMENT="+environment)
		args := []string{"sync", "--project", planDir, "--frozen", "--no-install-project", "--no-build"}
		if !network {
			args = append(args, "--offline")
		}
		if err := runCommand(ctx, uv, args, planDir, syncEnv); err != nil {
			return plan, "", err
		}
		return plan, environment, nil
	case "node":
		pkg, err := nodePackage(plan)
		if err != nil {
			return plan, "", err
		}
		if err := os.WriteFile(filepath.Join(planDir, "package.json"), pkg, 0o600); err != nil {
			return plan, "", err
		}
		pnpm := filepath.Join(m.runtimeRoot, "pnpm", "bin", "pnpm.cjs")
		lockArgs := []string{pnpm, "install", "--lockfile-only", "--ignore-scripts", "--dir", planDir}
		if !network {
			lockArgs = append(lockArgs, "--offline")
		}
		if err := runCommand(ctx, m.executable("node"), lockArgs, planDir, env); err != nil {
			return plan, "", err
		}
		lockDigest, err := fileDigest(filepath.Join(planDir, "pnpm-lock.yaml"))
		if err != nil {
			return plan, "", err
		}
		plan.LockDigest = lockDigest
		plan.PlanHash = ComputeResolvedPlanHash(plan)
		environment := m.resolvedEnvironmentPath(plan)
		if err := os.MkdirAll(environment, 0o755); err != nil {
			return plan, "", err
		}
		_ = os.RemoveAll(environment)
		for _, name := range []string{"package.json", "pnpm-lock.yaml"} {
			data, readErr := os.ReadFile(filepath.Join(planDir, name))
			if readErr != nil {
				return plan, "", readErr
			}
			if err := os.WriteFile(filepath.Join(environment, name), data, 0o600); err != nil {
				return plan, "", err
			}
		}
		installArgs := []string{pnpm, "install", "--frozen-lockfile", "--ignore-scripts", "--dir", environment}
		if !network {
			installArgs = append(installArgs, "--offline")
		}
		if err := runCommand(ctx, m.executable("node"), installArgs, environment, env); err != nil {
			return plan, "", err
		}
		return plan, environment, nil
	default:
		return plan, "", fmt.Errorf("不支持的 Skill runtime: %s", plan.Runtime.Kind)
	}
}

func pythonProject(plan skillmodel.ResolvedPlan) ([]byte, error) {
	require := plan.Runtime.Constraint
	if require == "" {
		require = ">=3.10"
	}
	var b strings.Builder
	b.WriteString("[project]\n")
	fmt.Fprintf(&b, "name = %q\n", "hitk-"+normalizeName(plan.ID))
	b.WriteString("version = \"0.0.0\"\n")
	fmt.Fprintf(&b, "requires-python = %q\n", require)
	b.WriteString("dependencies = [\n")
	for _, dependency := range plan.Dependencies {
		if strings.ToLower(dependency.Ecosystem) != "python" || dependency.PackageName == "" {
			continue
		}
		value := dependency.PackageName
		if dependency.Version != "" {
			value += dependency.Version
		}
		fmt.Fprintf(&b, "  %q,\n", value)
	}
	b.WriteString("]\n\n[tool.uv]\npackage = false\n")
	return []byte(b.String()), nil
}

func nodePackage(plan skillmodel.ResolvedPlan) ([]byte, error) {
	dependencies := map[string]string{}
	for _, dependency := range plan.Dependencies {
		if strings.ToLower(dependency.Ecosystem) == "node" && dependency.PackageName != "" {
			version := dependency.Version
			if version == "" {
				version = "latest"
			}
			dependencies[dependency.PackageName] = version
		}
	}
	doc := map[string]any{
		"name":         "hitk-" + normalizeName(plan.ID),
		"version":      "0.0.0",
		"private":      true,
		"dependencies": dependencies,
	}
	return json.MarshalIndent(doc, "", "  ")
}

func (m *Manager) resolvedEnvironmentPath(plan skillmodel.ResolvedPlan) string {
	return filepath.Join(m.dataRoot, "skill-environments", normalizeName(plan.ID), shortHash(plan.PlanHash))
}

func (m *Manager) resolvedPlanPath(plan skillmodel.ResolvedPlan) string {
	return filepath.Join(m.dataRoot, "skill-plans", normalizeName(plan.ID), shortHash(plan.SourceHash), "resolved-skill.json")
}

func (m *Manager) loadPreparedResolvedPlan(incoming skillmodel.ResolvedPlan) (skillmodel.ResolvedPlan, bool) {
	if incoming.ID == "" || incoming.SourceHash == "" {
		return skillmodel.ResolvedPlan{}, false
	}
	data, err := os.ReadFile(m.resolvedPlanPath(incoming))
	if err != nil {
		return skillmodel.ResolvedPlan{}, false
	}
	var stored skillmodel.ResolvedPlan
	if json.Unmarshal(data, &stored) != nil || stored.LockDigest == "" || stored.PlanHash == "" {
		return skillmodel.ResolvedPlan{}, false
	}
	stored.ID = incoming.ID
	stored.SourceHash = incoming.SourceHash
	stored.SourceRoot = incoming.SourceRoot
	if expected := ComputeResolvedPlanHash(stored); expected != stored.PlanHash {
		return skillmodel.ResolvedPlan{}, false
	}
	if !dirExists(m.resolvedEnvironmentPath(stored)) {
		return skillmodel.ResolvedPlan{}, false
	}
	return stored, true
}

// LoadResolvedPlan loads a previously locked compatibility plan. It is used by
// integration tooling and the UI when execution needs the exact locked plan
// rather than the original analysis draft.
func (m *Manager) LoadResolvedPlan(id, sourceHash, sourceRoot string) (skillmodel.ResolvedPlan, error) {
	plan, ok := m.loadPreparedResolvedPlan(skillmodel.ResolvedPlan{
		ID:         id,
		SourceHash: sourceHash,
		SourceRoot: sourceRoot,
	})
	if !ok {
		return skillmodel.ResolvedPlan{}, errors.New("Skill 锁定计划不存在或环境未就绪")
	}
	return plan, nil
}

func (m *Manager) resolvedEnvironmentReady(plan skillmodel.ResolvedPlan) bool {
	return dirExists(m.resolvedEnvironmentPath(plan))
}

func (m *Manager) resolvedStatus(plan skillmodel.ResolvedPlan, state string) skillmodel.SkillEnvironmentStatus {
	return skillmodel.SkillEnvironmentStatus{
		Skill:          plan.ID,
		State:          state,
		PlanHash:       plan.PlanHash,
		RuntimeVersion: m.runtimeVersion(plan.Runtime.Kind),
		UpdatedAt:      nowMillis(),
	}
}

func (m *Manager) resolvedReadyStatus(plan skillmodel.ResolvedPlan, environment string) skillmodel.SkillEnvironmentStatus {
	status := m.resolvedStatus(plan, skillmodel.StateReady)
	status.Environment = environment
	status.PlanHash = plan.PlanHash
	return status
}

func (m *Manager) publishResolvedStatus(status skillmodel.SkillEnvironmentStatus) {
	m.notifyComponentStatus(status.Skill, status)
}

func resolvedExecutionEnv(runtimeRoot, dataRoot, skillRoot, environment string, network bool) []string {
	paths := []string{filepath.Join(environment, "Scripts"), filepath.Join(environment, "node_modules", ".bin"), filepath.Join(runtimeRoot, "node"), filepath.Join(runtimeRoot, "python")}
	env := minimalHostEnv(paths...)
	temp := filepath.Join(dataRoot, "runtime-cache", "tmp")
	_ = os.MkdirAll(temp, 0o700)
	env = append(env, managedHomeEnv(filepath.Join(dataRoot, "runtime-cache"))...)
	env = append(env,
		"HITK_SKILL_ROOT="+skillRoot,
		"HITK_RUNTIME_ROOT="+runtimeRoot,
		"HITK_RUNTIME_CACHE="+filepath.Join(dataRoot, "runtime-cache"),
		"HITK_SKILL_ENV="+environment,
		"TEMP="+temp, "TMP="+temp,
		"PYTHONUTF8=1", "PYTHONIOENCODING=utf-8",
		"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1",
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

func validateResolvedArtifacts(artifacts []skillmodel.SkillArtifact, workspace string) error {
	if len(artifacts) == 0 {
		return nil
	}
	if workspace == "" {
		return errors.New("Skill 产物缺少有效工作区")
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return errors.New("Skill 产物缺少有效工作区")
	}
	for i := range artifacts {
		artifact := &artifacts[i]
		if !filepath.IsAbs(artifact.Path) {
			return errors.New("Skill 产物必须使用绝对路径")
		}
		abs, absErr := filepath.Abs(artifact.Path)
		if absErr != nil {
			return errors.New("Skill 产物路径无效")
		}
		info, statErr := os.Stat(abs)
		if statErr != nil || info.IsDir() {
			return errors.New("Skill 返回了不存在的产物")
		}
		resolvedArtifact, resolveErr := filepath.EvalSymlinks(abs)
		if resolveErr != nil || !within(resolvedWorkspace, resolvedArtifact) {
			return errors.New("Skill 产物路径越过工作区")
		}
		artifact.Path = abs
		artifact.Size = info.Size()
	}
	return nil
}

func ComputeResolvedPlanHash(plan skillmodel.ResolvedPlan) string {
	hash := sha256.New()
	for _, value := range []string{
		plan.SourceHash,
		plan.ResolverVersion,
		plan.Runtime.Kind,
		plan.Runtime.Constraint,
		plan.LockDigest,
		plan.Entry,
		string(plan.Invocation),
		plan.Environment,
	} {
		_, _ = io.WriteString(hash, value)
		_, _ = io.WriteString(hash, "\x00")
	}
	dependencies := append([]skillmodel.DependencyCandidate(nil), plan.Dependencies...)
	sort.Slice(dependencies, func(i, j int) bool {
		left := strings.ToLower(dependencies[i].Ecosystem + ":" + dependencies[i].PackageName + "@" + dependencies[i].Version)
		right := strings.ToLower(dependencies[j].Ecosystem + ":" + dependencies[j].PackageName + "@" + dependencies[j].Version)
		return left < right
	})
	for _, dependency := range dependencies {
		_, _ = io.WriteString(hash, strings.ToLower(dependency.Ecosystem+":"+dependency.PackageName+"@"+dependency.Version))
		_, _ = io.WriteString(hash, "\x00")
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func shortHash(value string) string {
	if len(value) > 16 {
		return value[:16]
	}
	if value == "" {
		return "unknown"
	}
	return value
}

func writeResolvedPlanFile(path string, plan skillmodel.ResolvedPlan) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func planNeedsNetwork(plan skillmodel.ResolvedPlan) bool {
	return plan.Kind == skillmodel.SkillKindScript && len(plan.Dependencies) > 0
}
