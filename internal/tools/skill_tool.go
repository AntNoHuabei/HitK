package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/skillmodel"
	"github.com/hitk/hitk/internal/skills"
)

// SkillRun is the stable, model-facing entry for on-demand capabilities.
type SkillRun struct {
	Dependencies SkillRunDependencies
}

type SkillRunDependencies struct {
	Catalog  skillmodel.Catalog
	Resolver skillmodel.Resolver
	Executor skillmodel.Executor
}

func (*SkillRun) Name() string        { return "skill_run" }
func (*SkillRun) DisplayName() string { return "运行 Skill" }
func (s *SkillRun) Description() string {
	base := "按需运行 Skill。knowledge 和 document 是内置能力；其他 Skill 由名称调用。脚本 Skill 可先不传 command/args 获取 SKILL.md 指令，再带参执行。args 必须拆成独立字符串。"
	if s.Dependencies.Catalog == nil {
		return base
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	items, err := s.Dependencies.Catalog.List(ctx)
	if err != nil || len(items) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString(" 已安装：")
	for index, item := range items {
		if index >= 30 {
			b.WriteString(" 等")
			break
		}
		if index > 0 {
			b.WriteString("；")
		}
		b.WriteString(item.ID)
		if item.Description != "" {
			description := strings.Join(strings.Fields(item.Description), " ")
			if len([]rune(description)) > 80 {
				description = string([]rune(description)[:80]) + "…"
			}
			b.WriteString("：")
			b.WriteString(description)
		}
	}
	return b.String()
}
func (*SkillRun) Schema() json.RawMessage {
	return SchemaFor([]string{"skill"}, map[string]string{
		"skill":   `{"type":"string","description":"Skill 名称"}`,
		"command": `{"type":"string","description":"可选子命令或操作"}`,
		"args":    `{"type":"array","items":{"type":"string"},"description":"参数及其值，必须拆成独立字符串"}`,
	})
}
func (*SkillRun) Mutating() bool { return false }

func (s *SkillRun) MutatingCall(raw json.RawMessage) bool {
	var req struct {
		Skill   string   `json:"skill"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(raw, &req) != nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(req.Skill), "knowledge") {
		return strings.EqualFold(strings.TrimSpace(req.Command), "add")
	}
	if s.Dependencies.Catalog == nil {
		return false
	}
	pkg, err := s.Dependencies.Catalog.Get(context.Background(), req.Skill)
	if err != nil {
		return false
	}
	if pkg.Kind == skillmodel.SkillKindScript && strings.TrimSpace(req.Command) == "" && len(req.Args) == 0 {
		return false
	}
	return pkg.Manifest.Permissions.RuntimeNetwork || pkg.Kind == skillmodel.SkillKindScript
}

func (s *SkillRun) Run(ctx *Context, raw json.RawMessage) (Result, error) {
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
		res, err = s.runCompatibilitySkill(ctx, req.Skill, req.Command, req.Args)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Content: res.Content, Display: res.Display, Refs: res.Refs, Artifacts: res.Artifacts, Truncated: res.Truncated, Meta: res.Meta}, nil
}

func (s *SkillRun) runCompatibilitySkill(ctx *Context, name, command string, args []string) (skills.Result, error) {
	if s.Dependencies.Catalog == nil {
		return skills.Result{}, errors.New("未知 Skill；当前仅支持 knowledge、document")
	}
	pkg, err := s.Dependencies.Catalog.Get(ctx.Ctx, name)
	if err != nil {
		return skills.Result{}, err
	}
	if pkg.Kind == skillmodel.SkillKindPrompt {
		return skills.Result{
			Content: pkg.Body,
			Display: firstNonEmpty(pkg.DisplayName, pkg.Name),
			Meta: map[string]any{
				"skill":  pkg.ID,
				"source": pkg.Source,
				"kind":   pkg.Kind,
			},
		}, nil
	}
	if pkg.Kind == skillmodel.SkillKindScript && strings.TrimSpace(command) == "" && len(args) == 0 {
		return skills.Result{
			Content: pkg.Body,
			Display: firstNonEmpty(pkg.DisplayName, pkg.Name) + " 使用说明",
			Meta: map[string]any{
				"skill":  pkg.ID,
				"source": pkg.Source,
				"kind":   pkg.Kind,
			},
		}, nil
	}
	if s.Dependencies.Resolver == nil || s.Dependencies.Executor == nil {
		return skills.Result{}, errors.New("Skill 运行时未初始化")
	}
	if s.Dependencies.Catalog != nil && !s.Dependencies.Catalog.Mode().AllowsScriptExecution() {
		return skills.Result{}, errors.New("当前兼容模式仅允许解析脚本 Skill，设置 HITK_SKILL_COMPAT=full 后才能执行")
	}
	analysis, err := s.Dependencies.Resolver.Analyze(ctx.Ctx, pkg)
	if err != nil {
		return skills.Result{}, err
	}
	plan, err := s.Dependencies.Resolver.Resolve(ctx.Ctx, pkg, analysis)
	if err != nil {
		return skills.Result{}, err
	}
	plan.ID = pkg.ID
	plan.SourceHash = pkg.SourceHash
	plan.SourceRoot = pkg.Root
	status, err := s.Dependencies.Executor.Prepare(ctx.Ctx, plan, false)
	if err != nil {
		return skills.Result{}, err
	}
	if status.State != skillmodel.StateReady {
		return skills.Result{}, errors.New("Skill 环境尚未就绪")
	}
	result, err := s.Dependencies.Executor.ExecuteResolved(ctx.Ctx, plan, skillmodel.SkillRequest{
		Command:   command,
		Args:      args,
		Workspace: ctx.Workspace,
	})
	if err != nil {
		return skills.Result{}, err
	}
	artifacts := make([]protocolArtifact, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		artifacts = append(artifacts, protocolArtifact{
			Path: artifact.Path, Name: artifact.Name, Mime: artifact.Mime,
			Size: artifact.Size, Operation: artifact.Operation,
		})
	}
	return skills.Result{
		Content:   result.Content,
		Display:   result.Display,
		Artifacts: artifacts,
		Truncated: result.Truncated,
		Meta:      result.Metadata,
	}, nil
}

// protocolArtifact is a local alias that keeps the conversion explicit without
// making the compatibility path depend on protocol internals.
type protocolArtifact = protocol.Artifact

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
