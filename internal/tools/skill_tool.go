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
	return "按需运行 Skill。knowledge 用于知识库检索，command 为 collections/search/open/add；document 用于文件解析，command 为 inspect/extract/open/convert。args 必须拆成独立字符串。"
}
func (*SkillRun) Schema() json.RawMessage {
	return SchemaFor([]string{"skill", "command"}, map[string]string{
		"skill":   `{"type":"string","enum":["knowledge","document"],"description":"Skill 名称"}`,
		"command": `{"type":"string","enum":["collections","search","open","add","inspect","extract","convert"],"description":"Cobra 子命令"}`,
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
	base := &skills.Context{
		Ctx:            ctx.Ctx,
		KB:             ctx.KB,
		KnowledgeBases: append([]string(nil), ctx.KnowledgeBases...),
		Approve: func(title, detail string) error {
			return ctx.Approve(ApprovalRequest{ToolName: "skill_run", Title: title, Detail: detail})
		},
	}
	var res skills.Result
	var err error
	switch strings.ToLower(strings.TrimSpace(req.Skill)) {
	case "knowledge":
		res, err = skills.RunKnowledge(base, req.Command, req.Args)
	case "document":
		res, err = skills.RunDocument(&skills.DocumentContext{Context: base, Documents: ctx.Documents, Workspace: ctx.Workspace}, req.Command, req.Args)
	default:
		return Result{}, errors.New("未知 Skill；当前支持 knowledge、document")
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Content: res.Content, Display: res.Display, Refs: res.Refs, Artifacts: res.Artifacts, Truncated: res.Truncated, Meta: res.Meta}, nil
}
