// Package tools holds everything the agent can actually do.
//
// A tool is a small, self-describing unit: it declares a JSON Schema for its
// arguments (shipped verbatim to the model), a human label for the UI, whether
// it is safe to run without asking, and a Run method. The registry selects a
// subset per [protocol.Mode], which is how one agent stays useful for both
// knowledge work and coding.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hitk/hitk/internal/document"
	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

// Result is what a tool hands back to the loop.
type Result struct {
	// Content is the text placed in the conversation as the tool result.
	Content string
	// Display is a short label shown on the tool card while collapsed.
	Display string
	// Refs are knowledge-base citations surfaced by this call.
	Refs []protocol.Reference
	// Artifacts are files created by the capability and are validated before
	// they reach the UI.
	Artifacts []protocol.Artifact
	// Plan is set by plan-management tools to update the UI checklist.
	Plan []protocol.TodoEntry
	// Truncated records that Content was clipped to fit the context budget.
	Truncated bool
	// Meta carries tool-specific structured data for the UI (diffs, counts…).
	Meta map[string]any
}

// Approver decides whether a mutating tool call may proceed. It blocks until
// the user answers, and returns an error to deny.
type Approver func(ctx context.Context, req ApprovalRequest) error

// ApprovalRequest describes what the user is being asked to allow.
type ApprovalRequest struct {
	ToolName string
	Title    string
	Detail   string
	Diff     string
	Command  string
}

// Context is the per-call environment handed to a tool.
type Context struct {
	Ctx       context.Context
	KB        *kb.Store
	Workspace string
	Mode      protocol.Mode
	Approver  Approver
	// Policy is the active approval policy; tools consult it before asking.
	Policy protocol.ApprovalPolicy
	// ThreadID and TurnID are used for logging and for tools that write to the
	// conversation.
	ThreadID string
	TurnID   string
	// Plan holds the current checklist so the plan tool can replace it.
	Plan []protocol.TodoEntry
	// KnowledgeBases scopes a work-mode turn to one or more collections.
	KnowledgeBases []string
	// Documents is the shared file parsing/conversion boundary.
	Documents *document.Engine
}

// Approve runs the approver when one is configured, otherwise it denies
// mutating operations so an unconfigured agent cannot damage anything.
func (c *Context) Approve(req ApprovalRequest) error {
	if c.Policy == protocol.ApprovalAuto {
		return nil
	}
	if c.Approver == nil {
		return fmt.Errorf("操作被拒绝：当前审批策略不允许执行 %s", req.ToolName)
	}
	return c.Approver(c.Ctx, req)
}

// Tool is one callable capability.
type Tool interface {
	// Name is the identifier the model uses in tool calls.
	Name() string
	// DisplayName is the short human label shown on the tool card.
	DisplayName() string
	// Description tells the model when to reach for this tool.
	Description() string
	// Schema is the JSON Schema of the arguments object.
	Schema() json.RawMessage
	// Mutating reports whether the tool can change state outside the
	// conversation. Mutating tools go through the approval policy.
	Mutating() bool
	// Run executes the call.
	Run(ctx *Context, args json.RawMessage) (Result, error)
}

type callMutationDetector interface {
	MutatingCall(args json.RawMessage) bool
}

// MutatingCall reports whether one invocation can change external state.
func MutatingCall(tool Tool, args json.RawMessage) bool {
	if detector, ok := tool.(callMutationDetector); ok {
		return detector.MutatingCall(args)
	}
	return tool.Mutating()
}

// Registry holds every known tool and resolves the surface for a mode.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds tools, replacing any existing entry with the same name.
func (r *Registry) Register(list ...Tool) {
	for _, t := range list {
		r.tools[t.Name()] = t
	}
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names lists every registered tool.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ForMode returns the schemas of the tools available in a mode.
//
// The split is the product decision behind "work and coding in one app": work
// mode gets retrieval and writing, coding mode gets the filesystem and a shell.
// Plan management is shared because both need to show progress.
func (r *Registry) ForMode(mode protocol.Mode) []provider.ToolSchema {
	names, ok := toolSets[mode]
	if !ok {
		names = toolSets[protocol.ModeWork]
	}

	var out []provider.ToolSchema
	for _, n := range names {
		t, ok := r.tools[n]
		if !ok {
			continue
		}
		out = append(out, provider.ToolSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
	}
	return out
}

// toolSets maps a mode to its tool names. Names absent from the registry are
// skipped, so a partially wired build still starts.
var toolSets = map[protocol.Mode][]string{
	protocol.ModeWork: {
		"skill_run",
		"fetch_url",
		"todo_write",
	},
	protocol.ModeCoding: {
		"skill_run",
		"read_file",
		"write_file",
		"edit_file",
		"list_dir",
		"glob_find",
		"grep_search",
		"run_command",
		"todo_write",
	},
	// Companion conversations must never expose general agent tools. The
	// companion's state is supplied as read-only model context by its runtime.
	protocol.ModeCompanion: {},
	protocol.ModeAuto: {
		"skill_run",
		"fetch_url",
		"read_file",
		"write_file",
		"edit_file",
		"list_dir",
		"glob_find",
		"grep_search",
		"run_command",
		"todo_write",
	},
}

// NewDefaultRegistry builds the registry HitK ships with.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(
		&SkillRun{},
		&FetchURL{},
		&TodoWrite{},
		&ReadFile{},
		&WriteFile{},
		&EditFile{},
		&ListDir{},
		&GlobFind{},
		&GrepSearch{},
		&RunCommand{},
	)
	return r
}

// NewDefaultRegistryWithSkills builds the standard registry with the
// compatibility Skill dependencies injected. Keeping the no-argument
// constructor preserves existing tests and embedded callers.
func NewDefaultRegistryWithSkills(deps SkillRunDependencies) *Registry {
	r := NewDefaultRegistry()
	r.Register(&SkillRun{Dependencies: deps})
	return r
}

// SchemaFor builds a JSON Schema object from property definitions. Every tool
// schema is an object with named properties, so this removes the repetition of
// writing the same JSON by hand.
func SchemaFor(required []string, props map[string]string) json.RawMessage {
	properties := map[string]any{}
	for name, raw := range props {
		var spec any
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			// A malformed property should degrade to a plain string rather
			// than producing a schema the vendor will reject.
			spec = map[string]any{"type": "string"}
		}
		properties[name] = spec
	}
	obj := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		obj["required"] = required
	}
	raw, _ := json.Marshal(obj)
	return raw
}

// argString reads a string argument.
func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// argInt reads an integer argument, accepting the float64 that JSON decoding
// produces.
func argInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return def
}

// argBool reads a boolean argument.
func argBool(args map[string]any, key string, def bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return def
}

// argStrings reads a string-array argument, tolerating a single string.
func argStrings(args map[string]any, key string) []string {
	switch v := args[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{strings.TrimSpace(v)}
	}
	return nil
}

// decodeArgs parses a tool call's argument object.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("参数不是合法 JSON: %w", err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// truncate clips tool output to a byte budget, appending a marker the model can
// react to. Tool output is the single biggest cause of context blow-ups.
func truncate(s string, limit int) (string, bool) {
	if limit <= 0 || len(s) <= limit {
		return s, false
	}
	head := limit * 3 / 4
	tail := limit - head
	return s[:head] +
		fmt.Sprintf("\n\n… 已省略 %d 字节（原始 %d 字节）…\n\n", len(s)-limit, len(s)) +
		s[len(s)-tail:], true
}
