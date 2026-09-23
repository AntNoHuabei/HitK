package skillruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) Execute(ctx context.Context, name string, request SkillRequest) (SkillExecutionResult, error) {
	manifest, skillDir, err := m.LoadSkill(name)
	if err != nil {
		return SkillExecutionResult{}, err
	}
	if !manifest.Builtin {
		return SkillExecutionResult{}, errors.New("第三方 Skill 必须先经过完全放行确认")
	}
	status, err := m.PrepareSkillEnvironment(ctx, name, false)
	if err != nil {
		return SkillExecutionResult{}, err
	}
	if request.Workspace != "" {
		request.Workspace, err = filepath.Abs(request.Workspace)
		if err != nil || !dirExists(request.Workspace) {
			return SkillExecutionResult{}, errors.New("Skill 工作区无效")
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return SkillExecutionResult{}, err
	}
	timeout := time.Duration(manifest.Budget.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	entry := filepath.Join(skillDir, filepath.FromSlash(manifest.Entry))
	command, args := m.executable(manifest.Runtime.Kind), []string{"-I", "-B", entry}
	if manifest.Runtime.Kind == "node" {
		args = []string{entry}
	}
	if !manifest.Builtin {
		if manifest.Runtime.Kind == "python" {
			command = filepath.Join(status.Environment, "Scripts", "python.exe")
		}
	}
	stdoutLimit := manifest.Budget.MaxOutputBytes
	if stdoutLimit <= 0 {
		stdoutLimit = 1 << 20
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(command, args...)
	cmd.Dir = skillDir
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = &limitedWriter{Writer: &stdout, Remaining: stdoutLimit}
	cmd.Stderr = &limitedWriter{Writer: &stderr, Remaining: 256 << 10}
	cmd.Env = executionEnv(m.runtimeRoot, m.dataRoot, skillDir, status.Environment, manifest.Permissions.RuntimeNetwork)
	job, err := startManagedProcess(cmd, manifest.Budget.MemoryBytes)
	if err != nil {
		return SkillExecutionResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-runCtx.Done():
		_ = job.Kill()
		err = <-done
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return SkillExecutionResult{}, errors.New("Skill 执行超时")
		}
		return SkillExecutionResult{}, errors.New("Skill 执行已取消")
	}
	_ = job.Close()
	if err != nil {
		return SkillExecutionResult{}, fmt.Errorf("Skill 执行失败: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() >= stdoutLimit {
		return SkillExecutionResult{}, errors.New("Skill 响应超过大小限制")
	}
	result, err := decodeExecutionResult(stdout.Bytes())
	if err != nil {
		return SkillExecutionResult{}, err
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(request.Workspace)
	if len(result.Artifacts) > 0 && (err != nil || request.Workspace == "") {
		return SkillExecutionResult{}, errors.New("Skill 产物缺少有效工作区")
	}
	for i := range result.Artifacts {
		artifact := &result.Artifacts[i]
		if !filepath.IsAbs(artifact.Path) {
			return SkillExecutionResult{}, errors.New("Skill 产物必须使用绝对路径")
		}
		abs, absErr := filepath.Abs(artifact.Path)
		if absErr != nil {
			return SkillExecutionResult{}, errors.New("Skill 产物路径无效")
		}
		info, statErr := os.Stat(abs)
		if statErr != nil || info.IsDir() {
			return SkillExecutionResult{}, errors.New("Skill 返回了不存在的产物")
		}
		resolvedArtifact, resolveErr := filepath.EvalSymlinks(abs)
		if resolveErr != nil || !within(resolvedWorkspace, resolvedArtifact) {
			return SkillExecutionResult{}, errors.New("Skill 产物路径越过工作区")
		}
		artifact.Path = abs
		artifact.Size = info.Size()
	}
	return result, nil
}

func decodeExecutionResult(data []byte) (SkillExecutionResult, error) {
	var result SkillExecutionResult
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return SkillExecutionResult{}, fmt.Errorf("Skill 返回了无效 JSON: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return SkillExecutionResult{}, errors.New("Skill 返回了多个 JSON 值")
	}
	return result, nil
}

func executionEnv(runtimeRoot, dataRoot, skillDir, environment string, network bool) []string {
	paths := []string{filepath.Join(runtimeRoot, "node"), filepath.Join(runtimeRoot, "python")}
	if environment != "" {
		paths = append([]string{filepath.Join(environment, "Scripts"), filepath.Join(environment, "node_modules", ".bin")}, paths...)
	}
	env := minimalHostEnv(paths...)
	temp := filepath.Join(dataRoot, "runtime-cache", "tmp")
	_ = os.MkdirAll(temp, 0o700)
	env = append(env,
		"HITK_SKILL_ROOT="+skillDir,
		"HITK_RUNTIME_ROOT="+runtimeRoot,
		"HITK_RUNTIME_CACHE="+filepath.Join(dataRoot, "runtime-cache"),
		"TEMP="+temp, "TMP="+temp,
		"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1",
		"DOCLING_CACHE_DIR="+filepath.Join(skillDir, "models-cache"),
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
