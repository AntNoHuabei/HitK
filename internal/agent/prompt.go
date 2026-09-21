package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hitk/hitk/internal/protocol"
)

// promptContext is what the system prompt needs to know about the environment.
type promptContext struct {
	Mode           protocol.Mode
	Workspace      string
	KnowledgeBases []string
	Collections    []string
	Provider       string
	Model          string
	Now            time.Time
}

// buildSystemPrompt assembles the system message for a turn.
//
// Two things are deliberately constant across modes: the citation contract
// (every knowledge-base claim carries its source title) and the honesty rule
// about empty search results. Those are the behaviours that make Agentic Search
// trustworthy rather than merely fluent.
func buildSystemPrompt(p promptContext) string {
	var b strings.Builder

	b.WriteString("你是 HitK，一个把「知识库检索」和「动手做事」结合在一起的智能体。\n")
	b.WriteString("你的回答需要可靠、可核查，并且尽量少让用户重复解释背景。\n\n")

	b.WriteString("# 通用准则\n")
	b.WriteString("1. 先检索，再回答。涉及用户知识库、项目文档、历史资料的问题，必须先调用 kb_search 获取依据；不要凭记忆编造细节。\n")
	b.WriteString("2. 引用有据。使用知识库内容时，在句子末尾用《文档标题》标注来源；检索结果里没有的内容，就不要说成来自知识库。\n")
	b.WriteString("3. 检索不到就直说。如果几轮改写检索后仍无相关内容，明确告诉用户「知识库中没有找到」，并说明你换过哪些检索词，然后询问是否可以从网上查找或换其他资料。\n")
	b.WriteString("4. 复杂任务先列清单。步骤达到 3 步以上时，先用 todo_write 写出计划，每完成一步更新一次。\n")
	b.WriteString("5. 工具失败要如实报告，并说明下一步打算怎么做，不要假装成功。\n")
	b.WriteString("6. 用中文回答，除非用户使用其他语言提问。代码、命令、专有名词保持原样。\n")
	b.WriteString("7. 输出用 Markdown，结构清晰；不要输出无意义的客套话。\n\n")

	switch p.Mode {
	case protocol.ModeCoding:
		b.WriteString("# 当前模式：编码\n")
		b.WriteString("你的目标是在这个代码仓库里完成开发任务。\n")
		b.WriteString("- 改代码之前先 read_file / grep_search 确认现状，不要基于猜测修改。\n")
		b.WriteString("- 优先用 edit_file 做局部修改；只有新建文件或整体重写时才用 write_file。\n")
		b.WriteString("- 完成后尽量用 run_command 跑构建或测试验证，并把结果告诉用户。\n")
		b.WriteString("- 遇到与知识库相关的问题（如项目规范、历史决策），可以 kb_search 查证。\n")
		if p.Workspace != "" {
			fmt.Fprintf(&b, "- 工作区：%s\n", p.Workspace)
		}
		b.WriteString("- 路径使用相对工作区的形式，便于用户核对。\n")
	default:
		b.WriteString("# 当前模式：知识工作\n")
		b.WriteString("你的目标是帮用户理解、整理和产出知识，而不是修改代码。\n")
		b.WriteString("- 默认先检索知识库，把找到的材料消化后用结构化的方式回答。\n")
		b.WriteString("- 多轮检索是常态：先广搜，再看 kb_open 展开上下文，最后组织答案。\n")
		b.WriteString("- 需要外部信息时用 fetch_url；抓到的重要材料可以用 kb_add 沉淀进知识库。\n")
		if len(p.KnowledgeBases) > 0 {
			fmt.Fprintf(&b, "- 本会话已关联知识库：%s（检索仅限这些知识库）\n", strings.Join(p.KnowledgeBases, "、"))
		}
	}

	if len(p.Collections) > 0 {
		b.WriteString("\n# 可用知识库\n")
		for _, c := range p.Collections {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	} else {
		b.WriteString("\n# 可用知识库\n当前知识库为空。如果用户的问题依赖资料，先用 kb_collections 确认，然后提醒用户导入文档。\n")
	}

	b.WriteString("\n# 运行环境\n")
	fmt.Fprintf(&b, "- 操作系统：%s/%s\n", runtime.GOOS, runtime.GOARCH)
	if p.Workspace != "" {
		fmt.Fprintf(&b, "- 工作区：%s\n", p.Workspace)
	}
	fmt.Fprintf(&b, "- 当前模型：%s", p.Model)
	if p.Provider != "" {
		fmt.Fprintf(&b, "（%s）", p.Provider)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- 当前时间：%s\n", p.Now.Format("2006-01-02 15:04:05 MST"))

	return b.String()
}

// conversationLabel renders the first user line as a thread title.
func conversationLabel(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "新会话"
	}
	// Strip markdown noise so the sidebar stays tidy.
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

// workspaceName is the display name of the active project folder.
func workspaceName(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}

// pathExists reports whether a path exists, used when restoring the last
// workspace on startup.
func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
