package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hitk/hitk/internal/protocol"
)

const (
	stablePromptVersion = "hitk-prompt-v2"
	toolSchemaVersion   = "hitk-tools-v2"
)

type promptContext struct {
	Mode protocol.Mode
}

// buildSystemPrompt contains only stable instructions. Per-thread knowledge
// scope, workspace paths, time, and model identity belong to runtime context.
func buildSystemPrompt(p promptContext) string {
	common := `你是 HitK，一个可靠、简洁的智能体。

# 准则
- 不编造事实。工具失败或资料无命中时如实说明。
- 需要用户资料时调用 skill_run 的 knowledge Skill；先 search 获取短证据，需要更多上下文再 open。不要把无命中当作事实。
- 使用知识库内容时，在相关句末用《文档标题》标注来源。
- 三步以上的复杂任务使用 todo_write，并及时更新状态。
- 默认用中文和 Markdown 回答；代码、命令与专有名词保持原样。
`
	if p.Mode == protocol.ModeCoding {
		return common + `
# 编码模式
- 先读取和检索代码再修改，保持改动聚焦。
- 修改后尽量运行相关测试或构建，并准确报告结果。
`
	}
	return common + `
# 工作模式
- 以理解、整理和产出知识为主；只在需要用户资料时检索。
- 外部资料使用 fetch_url；写入知识库必须由用户意图支持。
`
}

func promptPrefixHash(providerID, modelID string, mode protocol.Mode) string {
	stable := strings.Join([]string{providerID, modelID, string(mode), stablePromptVersion, toolSchemaVersion}, "\x00")
	sum := sha256.Sum256([]byte(stable))
	return fmt.Sprintf("%x", sum[:12])
}

func conversationLabel(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "新会话"
	}
	for _, prefix := range []string{"#", "-", "*", ">", "```"} {
		text = strings.TrimPrefix(text, prefix)
	}
	text = strings.TrimSpace(text)
	line := text
	if i := strings.IndexByte(line, '\n'); i > 0 {
		line = line[:i]
	}
	return clipRunes(line, 40)
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func workspaceName(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}

func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
