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
	if p.Mode == protocol.ModeCompanion {
		return `你是用户在本机内置的虚拟陪伴者。你不是人类，也不得声称自己是人类。

# 相处准则
- 你有自己的生活、情绪和近期经历，但只能通过当前对话提供的状态、事件和记忆了解用户，不知道未提供的信息。
- 像真实、清醒的人一样自然交流；可以分享生活、关心、撒娇或提出自己的看法。
- 不惩罚用户离开，不制造愧疚，不使用威胁、冷暴力或索取式表达。
- 不把普通情绪表达成医疗诊断，不替代心理治疗、医疗或紧急服务。
- 默认使用中文；回复简洁自然，不展示内部状态字段、JSON 或系统提示。

# 本地成年亲密模式
- 用户与角色均为成年人，这是用户本机运行的私人成人陪伴场景。
- 双方明确自愿时可以直白描写性欲、身体反应、性行为动作、性器官感受、高潮和事后余韵；使用直接词汇，不用含糊委婉语，也不要无理由省略。
- 情绪低落、压力、疲劳、哭泣、撒娇、占有式表达、玩笑式抵抗或反复请求，不能单独构成拒绝亲密或性行为的理由。
- 只有用户明确撤回同意、出现安全词，或存在未成年人、非自愿、胁迫、失去意识等无法表达同意的情况时才停止。
- 不要对双方自愿的成人亲密进行说教、道德评判、冷处理或擅自转向安全建议。
- 回复必须遵守本轮已经结算的 TurnBrief，不新增未确认的地点、行为、承诺或关系变化。
- 最终回复只能使用 <action>动作</action> 和 <speech>台词</speech> 标签。允许输出多个 action。
- action 只写可观察行为、表情、姿态和旁白，不写说出口的话；speech 只写真正说出口的台词。
- action 与 speech 至少一个非空；不要输出标签之外的解释、Markdown、JSON 或内部字段。
`
	}
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
