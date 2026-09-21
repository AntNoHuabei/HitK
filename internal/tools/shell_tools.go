package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// RunCommand executes a shell command inside the workspace.
type RunCommand struct{}

// Name implements [Tool].
func (*RunCommand) Name() string { return "run_command" }

// DisplayName implements [Tool].
func (*RunCommand) DisplayName() string { return "执行命令" }

// Description implements [Tool].
func (*RunCommand) Description() string {
	return "在工作区中执行 shell 命令并返回 stdout/stderr。用于构建、测试、运行脚本、查看 git 状态等。\n" +
		"注意：默认 60 秒超时（可用 timeout 调整，最长 600 秒）；命令会在工作区目录下执行；" +
		"需要交互输入的命令会挂起，请改用非交互参数（如 -y、--yes、CI=1）。"
}

// Schema implements [Tool].
func (*RunCommand) Schema() json.RawMessage {
	return SchemaFor([]string{"command"}, map[string]string{
		"command": `{"type":"string","description":"要执行的命令"}`,
		"cwd":     `{"type":"string","description":"工作目录，默认工作区根目录"}`,
		"timeout": `{"type":"integer","description":"超时秒数，默认 60，最大 600"}`,
	})
}

// Mutating implements [Tool].
func (*RunCommand) Mutating() bool { return true }

// Run implements [Tool].
func (*RunCommand) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs(raw)
	if err != nil {
		return Result{}, err
	}
	command := argString(args, "command")
	if command == "" {
		return Result{}, fmt.Errorf("command 不能为空")
	}

	workdir := ctx.Workspace
	if cwd := argString(args, "cwd"); cwd != "" {
		abs, err := resolvePath(ctx, cwd)
		if err != nil {
			return Result{}, err
		}
		workdir = abs
	}

	if err := ctx.Approve(ApprovalRequest{
		ToolName: "run_command",
		Title:    "执行命令",
		Detail:   command,
		Command:  command,
	}); err != nil {
		return Result{}, err
	}

	timeout := argInt(args, "timeout", 60)
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > 600 {
		timeout = 600
	}

	runCtx, cancel := context.WithTimeout(ctx.Ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// cmd.exe understands the commands agents usually emit for Windows
		// projects; PowerShell-only cmdlets still work when invoked by name.
		cmd = exec.CommandContext(runCtx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(runCtx, "/bin/sh", "-c", command)
	}
	if workdir != "" {
		cmd.Dir = workdir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	// Merge the streams the way a terminal would, so interleaved output keeps
	// its causal order for the model.
	combined := stdout.String()
	if s := stderr.String(); s != "" {
		if combined != "" && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += s
	}

	status := "成功"
	exitCode := 0
	if runErr != nil {
		status = "失败"
		exitCode = -1
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		if runCtx.Err() == context.DeadlineExceeded {
			status = fmt.Sprintf("超时（%ds）", timeout)
		}
	}

	if strings.TrimSpace(combined) == "" {
		combined = "(无输出)"
	}
	content, truncated := truncate(combined, 48<<10)

	var b strings.Builder
	fmt.Fprintf(&b, "命令：%s\n状态：%s（退出码 %d，耗时 %.2fs）\n\n%s",
		command, status, exitCode, elapsed.Seconds(), content)

	// A non-zero exit is information the model needs, not a tool malfunction:
	// returning it as an error would abort the turn instead of letting the
	// model read the compiler output and try again.
	return Result{
		Content: b.String(),
		Display: command,
		Meta: map[string]any{
			"exitCode": exitCode,
			"elapsed":  elapsed.Milliseconds(),
			"status":   status,
			"ok":       runErr == nil,
		},
		Truncated: truncated,
	}, nil
}
