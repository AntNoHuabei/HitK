// Package protocol defines the HitK agent ⇄ UI interaction protocol.
//
// The design follows the submission/event queue split used by OpenAI's Codex
// (codex-rs `protocol.rs`), reduced to what a desktop knowledge-base app needs:
//
//	client ── Submission(Op) ──▶ agent runtime
//	client ◀── Event(EventMsg) ── agent runtime
//
// Work is organized in three nested layers:
//
//	Thread  — a persistent conversation, owns settings and history
//	Turn    — one user request and everything the agent does to satisfy it
//	Item    — an atomic unit of content inside a turn (message, tool call, …)
//
// Items are the only thing the UI renders. A turn emits `item.started` once,
// any number of `item.delta` updates, then exactly one `item.completed` —
// which mirrors Codex's `ItemStartedEvent` / `ItemCompletedEvent` pair and lets
// the frontend treat every item as a single mutable row.
package protocol

import "time"

// EventName is the Wails event channel every protocol event is published on.
const EventName = "hitk:event"

// Mode selects the agent's system prompt and tool surface.
type Mode string

const (
	// ModeWork is the knowledge-worker surface: search, read, summarize, write.
	ModeWork Mode = "work"
	// ModeCoding is the software-engineering surface: files, shell, git, tests.
	ModeCoding Mode = "coding"
	// ModeAuto lets the runtime pick a surface from the request.
	ModeAuto Mode = "auto"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeWork, ModeCoding, ModeAuto:
		return true
	}
	return false
}

// ApprovalPolicy controls when a side-effecting tool blocks on user consent.
type ApprovalPolicy string

const (
	// ApprovalReadOnly refuses every mutating tool call.
	ApprovalReadOnly ApprovalPolicy = "read-only"
	// ApprovalAuto runs mutating tools without asking.
	ApprovalAuto ApprovalPolicy = "auto"
	// ApprovalManual asks before every mutating tool call.
	ApprovalManual ApprovalPolicy = "manual"
)

// ---------------------------------------------------------------------------
// Items
// ---------------------------------------------------------------------------

// ItemType discriminates the payload carried by an Item.
type ItemType string

const (
	ItemUserMessage  ItemType = "user_message"
	ItemAgentMessage ItemType = "agent_message"
	ItemReasoning    ItemType = "reasoning"
	ItemToolCall     ItemType = "tool_call"
	ItemPlan         ItemType = "plan"
	ItemNotice       ItemType = "notice"
	ItemError        ItemType = "error"
)

// ItemStatus is the lifecycle state of an item.
type ItemStatus string

const (
	StatusInProgress ItemStatus = "in_progress"
	StatusCompleted  ItemStatus = "completed"
	StatusFailed     ItemStatus = "failed"
	StatusDenied     ItemStatus = "denied"
)

// Reference is a citation back into a knowledge base. Every grounded claim the
// model makes is expected to carry at least one of these so the UI can render
// a source chip next to the text.
type Reference struct {
	Collection string  `json:"collection"`
	DocID      string  `json:"docId"`
	DocTitle   string  `json:"docTitle"`
	ChunkID    string  `json:"chunkId,omitempty"`
	Path       string  `json:"path,omitempty"`
	URL        string  `json:"url,omitempty"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet,omitempty"`
}

// Attachment records a local input selected for one user turn. Attachments are
// retained in the transcript for display, but their contents are not replayed
// into later model requests.
type Attachment struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Mime string `json:"mime,omitempty"`
	Kind string `json:"kind"` // image | file
	Size int64  `json:"size,omitempty"`
}

// Artifact is a file created or changed by an agent tool.
type Artifact struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Mime      string `json:"mime,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Operation string `json:"operation"` // created | modified
}

// ToolCall is the recorded execution of one tool invocation.
type ToolCall struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Arguments   string `json:"arguments"`
	Result      string `json:"result,omitempty"`
	Success     bool   `json:"success"`
	Denied      bool   `json:"denied,omitempty"`
	DurationMS  int64  `json:"durationMs"`
	Truncated   bool   `json:"truncated,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// TodoEntry is one row of the agent's working plan.
type TodoEntry struct {
	Text   string `json:"text"`
	Status string `json:"status"` // pending | in_progress | completed
}

// Item is the atomic renderable unit inside a turn.
type Item struct {
	ID          string         `json:"id"`
	TurnID      string         `json:"turnId"`
	ThreadID    string         `json:"threadId"`
	Type        ItemType       `json:"type"`
	Status      ItemStatus     `json:"status"`
	CreatedAt   int64          `json:"createdAt"`
	Text        string         `json:"text,omitempty"`
	Reasoning   string         `json:"reasoning,omitempty"`
	Tool        *ToolCall      `json:"tool,omitempty"`
	Refs        []Reference    `json:"refs,omitempty"`
	Plan        []TodoEntry    `json:"plan,omitempty"`
	Error       string         `json:"error,omitempty"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	Artifacts   []Artifact     `json:"artifacts,omitempty"`
	Usage       *Usage         `json:"usage,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// ---------------------------------------------------------------------------
// Events (agent ➜ client)
// ---------------------------------------------------------------------------

// EventKind names the event variants. The set is deliberately small; anything
// finer-grained is expressed through Item status transitions.
type EventKind string

const (
	EvThreadReady    EventKind = "thread.ready"
	EvTurnStarted    EventKind = "turn.started"
	EvItemStarted    EventKind = "item.started"
	EvItemDelta      EventKind = "item.delta"
	EvItemCompleted  EventKind = "item.completed"
	EvTurnCompleted  EventKind = "turn.completed"
	EvTurnAborted    EventKind = "turn.aborted"
	EvApprovalNeeded EventKind = "approval.requested"
	EvTokenCount     EventKind = "token.count"
	EvStreamError    EventKind = "stream.error"
	EvNotice         EventKind = "notice"
)

// Delta carries an incremental text update for an in-progress item.
type Delta struct {
	ItemID  string `json:"itemId"`
	Channel string `json:"channel"` // text | reasoning
	Text    string `json:"text"`
}

// Usage reports token accounting for a turn or a single model call.
type Usage struct {
	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CachedTokens     int     `json:"cachedTokens,omitempty"`
	TotalTokens      int     `json:"totalTokens"`
	ContextLimit     int     `json:"contextLimit,omitempty"`
	Steps            int     `json:"steps,omitempty"`
	CacheHitRatio    float64 `json:"cacheHitRatio,omitempty"`
	PromptPrefixHash string  `json:"promptPrefixHash,omitempty"`
	DynamicTokens    int     `json:"dynamicTokens,omitempty"`
}

// ApprovalRequest blocks a tool call until the client answers with an
// OpApproval. Codex models this as `ExecApprovalRequest`; HitK merges exec and
// patch approval into one variant because a single dialog can describe both.
type ApprovalRequest struct {
	ID        string `json:"id"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	ToolName  string `json:"toolName"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	Diff      string `json:"diff,omitempty"`
	Command   string `json:"command,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// Event is the envelope published on EventName.
type Event struct {
	Seq       int64            `json:"seq"`
	Kind      EventKind        `json:"kind"`
	ThreadID  string           `json:"threadId,omitempty"`
	TurnID    string           `json:"turnId,omitempty"`
	At        int64            `json:"at"`
	Item      *Item            `json:"item,omitempty"`
	Delta     *Delta           `json:"delta,omitempty"`
	Usage     *Usage           `json:"usage,omitempty"`
	Approval  *ApprovalRequest `json:"approval,omitempty"`
	Error     string           `json:"error,omitempty"`
	Notice    string           `json:"notice,omitempty"`
	Thread    *ThreadSummary   `json:"thread,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	StepIndex int              `json:"stepIndex,omitempty"`
}

// NewEvent stamps an event with the current time.
func NewEvent(kind EventKind) *Event {
	return &Event{Kind: kind, At: time.Now().UnixMilli()}
}

// ---------------------------------------------------------------------------
// Ops (client ➜ agent)
// ---------------------------------------------------------------------------

// OpKind names the client-side submission variants.
type OpKind string

const (
	OpTurnStart    OpKind = "turn.start"
	OpInterrupt    OpKind = "turn.interrupt"
	OpApproval     OpKind = "approval.respond"
	OpCompaction   OpKind = "thread.compact"
	OpShellCommand OpKind = "shell.command"
)

// InputPart is one element of a user submission: text, or an attached file
// that the runtime ingests into a knowledge base before the turn starts.
type InputPart struct {
	Type string `json:"type"` // text | file
	Text string `json:"text,omitempty"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

// Op is a client submission. Unused fields depend on Kind.
type Op struct {
	Kind     OpKind `json:"kind"`
	ThreadID string `json:"threadId,omitempty"`
	// TurnStart
	Input           []InputPart    `json:"input,omitempty"`
	Mode            Mode           `json:"mode,omitempty"`
	Provider        string         `json:"provider,omitempty"`
	Model           string         `json:"model,omitempty"`
	Collection      string         `json:"collection,omitempty"` // deprecated singular compatibility
	Collections     []string       `json:"collections,omitempty"`
	Attached        []string       `json:"attached,omitempty"`
	Attachments     []Attachment   `json:"attachments,omitempty"`
	ReasoningEffort string         `json:"reasoningEffort,omitempty"` // off | low | medium | high
	Policy          ApprovalPolicy `json:"policy,omitempty"`
	// Approval
	ApprovalID string `json:"approvalId,omitempty"`
	Approve    bool   `json:"approve,omitempty"`
	Remember   bool   `json:"remember,omitempty"`
	// ShellCommand
	Command string `json:"command,omitempty"`
}

// ApprovalPolicy resolves the effective policy for a submission, defaulting to
// automatic execution so an unset field does not silently block every tool.
func (o Op) ApprovalPolicy() ApprovalPolicy {
	switch o.Policy {
	case ApprovalReadOnly, ApprovalAuto, ApprovalManual:
		return o.Policy
	}
	return ApprovalAuto
}

// ---------------------------------------------------------------------------
// Threads
// ---------------------------------------------------------------------------

// ThreadSummary is the list-view projection of a thread.
type ThreadSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Mode      Mode   `json:"mode"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	ItemCount int    `json:"itemCount"`
	Preview   string `json:"preview,omitempty"`
	Pinned    bool   `json:"pinned,omitempty"`
}

// Thread is the persisted conversation record.
type Thread struct {
	ThreadSummary
	Items           []*Item  `json:"items"`
	Collections     []string `json:"collections,omitempty"`
	ReasoningEffort string   `json:"reasoningEffort,omitempty"`
}

// ---------------------------------------------------------------------------
// Turn budget
// ---------------------------------------------------------------------------

// Step budget for a single turn. Both numbers live in the protocol package
// because the agent, the config store and the settings UI all need the same
// values; a budget that disagrees between them silently truncates long
// agentic searches, which is hard to notice from the outside.
const (
	// DefaultMaxSteps is the tool-call budget a fresh install starts with.
	// One agentic-search turn typically spends 8-15 steps (list collections,
	// several rephrased searches, expand a couple of chunks), so the default
	// leaves room to explore rather than cutting the search short.
	DefaultMaxSteps = 64
	// MaxStepsCeiling is the largest budget the settings UI will accept. It is
	// a runaway guard rather than a recommended setting: without it a turn
	// whose tool calls keep returning nothing would burn tokens until the
	// provider cuts it off.
	MaxStepsCeiling = 500
)
