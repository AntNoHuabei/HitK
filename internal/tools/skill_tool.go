package tools

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/hitk/hitk/internal/skills"
)

// SkillRun is the stable, model-facing entry for on-demand capabilities.
type SkillRun struct{}

func (*SkillRun) Name() string        { return "skill_run" }
func (*SkillRun) DisplayName() string { return "运行 Skill" }
func (*SkillRun) Description() string {
	return "按需运行 Skill。知识库使用 skill=knowledge；command 可选 collections、search、open、add。args 是分开的 Cobra 参数，例如 [\"--query\",\"关键词\"]。search 返回短证据，需阅读全文时再调用 open。"
}
func (*SkillRun) Schema() json.RawMessage {
	return SchemaFor([]string{"skill", "command"}, map[string]string{
		"skill":   `{"type":"string","enum":["knowledge"],"description":"Skill 名称"}`,
		"command": `{"type":"string","enum":["collections","search","open","add"],"description":"Cobra 子命令"}`,
		"args":    `{"type":"array","items":{"type":"string"},"description":"参数及其值，必须拆成独立字符串"}`,
	})
}
func (*SkillRun) Mutating() bool { return false }

func (*SkillRun) MutatingCall(raw json.RawMessage) bool {
	var req struct {
		Command string `json:"command"`
	}
	return json.Unmarshal(raw, &req) == nil && strings.EqualFold(strings.TrimSpace(req.Command), "add")
}

func (*SkillRun) Run(ctx *Context, raw json.RawMessage) (Result, error) {
	var req struct {
		Skill   string   `json:"skill"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return Result{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(req.Skill), "knowledge") {
		return Result{}, errors.New("未知 Skill；当前仅支持 knowledge")
	}
	res, err := skills.RunKnowledge(&skills.Context{
		Ctx:            ctx.Ctx,
		KB:             ctx.KB,
		KnowledgeBases: append([]string(nil), ctx.KnowledgeBases...),
		Approve: func(title, detail string) error {
			return ctx.Approve(ApprovalRequest{ToolName: "skill_run", Title: title, Detail: detail})
		},
	}, req.Command, req.Args)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: res.Content, Display: res.Display, Refs: res.Refs, Truncated: res.Truncated, Meta: res.Meta}, nil
}
