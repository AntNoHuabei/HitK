package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
	"github.com/hitk/hitk/internal/tools"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// scriptedProvider replays a fixed list of chunk sequences, one per Stream call,
// so a test can describe an exact multi-step turn: first the model asks for a
// tool, then it answers.
type scriptedProvider struct {
	mu    sync.Mutex
	steps [][]provider.Chunk
	calls int
	// gotRequests records each request so a test can assert what the loop sent.
	gotRequests []provider.ChatRequest
}

func (p *scriptedProvider) ID() string { return "scripted" }

func (p *scriptedProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.gotRequests = append(p.gotRequests, req)
	idx := p.calls
	p.calls++
	var script []provider.Chunk
	if idx < len(p.steps) {
		script = p.steps[idx]
	} else {
		script = []provider.Chunk{{Kind: provider.ChunkText, Text: "（脚本已用尽）"}, {Kind: provider.ChunkDone}}
	}
	p.mu.Unlock()

	out := make(chan provider.Chunk, len(script))
	go func() {
		defer close(out)
		for _, ch := range script {
			select {
			case out <- ch:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *scriptedProvider) requests() []provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ChatRequest(nil), p.gotRequests...)
}

// echoTool is a read-only tool that returns whatever it is told to.
type echoTool struct {
	name     string
	result   string
	mutating bool
	runs     int
	mu       sync.Mutex
	lastArgs string
}

func (e *echoTool) Name() string        { return e.name }
func (e *echoTool) DisplayName() string { return "回声工具" }
func (e *echoTool) Description() string { return "测试用工具" }
func (e *echoTool) Schema() json.RawMessage {
	return tools.SchemaFor(nil, map[string]string{"value": `{"type":"string"}`})
}
func (e *echoTool) Mutating() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mutating
}

func (e *echoTool) Run(tctx *tools.Context, args json.RawMessage) (tools.Result, error) {
	// Real HitK tools request consent themselves; mirroring that here keeps the
	// double honest and exercises the agent's consent-once path.
	if e.Mutating() {
		if err := tctx.Approve(tools.ApprovalRequest{
			ToolName: e.name,
			Title:    "回声工具",
			Detail:   "测试用写操作",
		}); err != nil {
			return tools.Result{}, err
		}
	}
	e.mu.Lock()
	e.runs++
	e.lastArgs = string(args)
	e.mu.Unlock()
	return tools.Result{Content: e.result, Display: "回声完成"}, nil
}

func (e *echoTool) runCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs
}

// collectEvents records everything the agent publishes.
type collector struct {
	mu     sync.Mutex
	events []*protocol.Event
}

func (c *collector) emit(ev *protocol.Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *collector) all() []*protocol.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*protocol.Event(nil), c.events...)
}

// kindsOf returns the event kinds in arrival order.
func (c *collector) kindsOf() []protocol.EventKind {
	out := make([]protocol.EventKind, 0)
	for _, ev := range c.all() {
		out = append(out, ev.Kind)
	}
	return out
}

// waitFor blocks until the predicate holds or the deadline passes.
func (c *collector) waitFor(t *testing.T, what string, pred func([]*protocol.Event) bool) []*protocol.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		events := c.all()
		if pred(events) {
			return events
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; events seen: %v", what, c.kindsOf())
	return nil
}

// hasKind reports whether the event list contains a kind.
func hasKind(events []*protocol.Event, kind protocol.EventKind) bool {
	for _, ev := range events {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type harness struct {
	agent    *Agent
	events   *collector
	sessions *SessionStore
	kb       *kb.Store
	echo     *echoTool
	scripted *scriptedProvider
	thread   *protocol.Thread
}

func newHarness(t *testing.T, steps [][]provider.Chunk) *harness {
	t.Helper()
	dir := t.TempDir()

	kbStore, err := kb.Open(filepath.Join(dir, "kb.db"))
	if err != nil {
		t.Fatalf("open kb: %v", err)
	}
	t.Cleanup(func() { kbStore.Close() })

	col, err := kbStore.CreateCollection("测试库", "")
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if _, err := kbStore.AddParsed(context.Background(), col.ID,
		kb.ParseText("协议说明", "HitK 的 agent 协议采用提交与事件分离的模型，每条工具调用都是一个可流式渲染的条目。"),
		"test://protocol", "text", kb.IngestOptions{Title: "协议说明"}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	sessions, err := NewSessionStore(filepath.Join(dir, "threads"))
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	scripted := &scriptedProvider{steps: steps}
	providers := provider.NewRegistry()
	providers.Register(scripted)

	echo := &echoTool{name: "echo", result: "回声内容"}
	registry := tools.NewDefaultRegistry()
	registry.Register(echo)

	events := &collector{}
	workspace := filepath.Join(dir, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	a := New(Options{
		KB:           kbStore,
		Tools:        registry,
		Providers:    providers,
		Sessions:     sessions,
		Emit:         events.emit,
		Workspace:    workspace,
		ContextLimit: 32000,
		MaxSteps:     6,
	})

	thread, err := sessions.Create(protocol.ModeWork, "scripted", "test-model")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	return &harness{
		agent: a, events: events, sessions: sessions, kb: kbStore,
		echo: echo, scripted: scripted, thread: thread,
	}
}

// start begins a turn and waits for it to be fully over, including the agent
// releasing the thread, so a test never races the turn goroutine's tail.
func (h *harness) start(t *testing.T, text string, policy protocol.ApprovalPolicy) []*protocol.Event {
	t.Helper()
	err := h.agent.StartTurn(protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: h.thread.ID,
		Input:    []protocol.InputPart{{Type: "text", Text: text}},
		Mode:     protocol.ModeWork,
		Provider: "scripted",
		Model:    "test-model",
		Policy:   policy,
	})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	events := h.events.waitFor(t, "turn to finish", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnCompleted) || hasKind(events, protocol.EvTurnAborted)
	})
	h.awaitIdle(t)
	return events
}

// awaitIdle blocks until the agent no longer reports the thread as running.
func (h *harness) awaitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !h.agent.Running(h.thread.ID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("agent still reports the thread as running")
}

// interruptAndWait cancels the running turn and blocks until the agent has
// released the thread.
//
// Interrupt is deliberately asynchronous — it only signals cancellation — and
// the turn goroutine keeps writing the transcript after the terminal event is
// published. A caller that tears down the session store right after an
// interrupt therefore races the goroutine's tail, so tests must wait for the
// thread to go idle rather than for the event alone.
func (h *harness) interruptAndWait(t *testing.T) []*protocol.Event {
	t.Helper()
	if err := h.agent.Interrupt(h.thread.ID); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	events := h.events.waitFor(t, "turn to abort", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnAborted) || hasKind(events, protocol.EvTurnCompleted)
	})
	h.awaitIdle(t)
	return events
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestTurnWithoutToolsIsCompleted checks the simplest turn: the model answers
// directly and the loop closes with a completion event carrying usage.
func TestTurnWithoutToolsIsCompleted(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{{
		{Kind: provider.ChunkText, Text: "这是"},
		{Kind: provider.ChunkText, Text: "回答。"},
		{Kind: provider.ChunkUsage, Usage: &provider.Usage{InputTokens: 120, OutputTokens: 8}},
		{Kind: provider.ChunkDone},
	}})

	events := h.start(t, "你好", protocol.ApprovalAuto)

	if h.scripted.callCount() != 1 {
		t.Errorf("expected exactly one model call, got %d", h.scripted.callCount())
	}
	if !hasKind(events, protocol.EvTurnStarted) {
		t.Error("missing turn.started")
	}
	if !hasKind(events, protocol.EvTurnCompleted) {
		t.Error("missing turn.completed")
	}
	for _, ev := range events {
		if ev.Item != nil && ev.Item.Type == protocol.ItemToolCall {
			t.Errorf("unexpected tool call item: %+v", ev.Item.Tool)
		}
	}

	// The streamed text must be assembled into one completed agent message.
	var final *protocol.Item
	for _, ev := range events {
		if ev.Kind == protocol.EvItemCompleted && ev.Item != nil && ev.Item.Type == protocol.ItemAgentMessage {
			final = ev.Item
		}
	}
	if final == nil {
		t.Fatal("no completed agent message")
	}
	if final.Text != "这是回答。" {
		t.Errorf("assembled text = %q, want %q", final.Text, "这是回答。")
	}
	if final.Status != protocol.StatusCompleted {
		t.Errorf("status = %q, want completed", final.Status)
	}

	// Deltas must be emitted per fragment so the UI can stream.
	deltas := 0
	for _, ev := range events {
		if ev.Kind == protocol.EvItemDelta {
			deltas++
		}
	}
	if deltas != 2 {
		t.Errorf("expected 2 delta events, got %d", deltas)
	}
}

// TestToolCallRoundTrip is the core loop test: the model asks for a tool, the
// loop runs it, feeds the result back, and the model answers.
func TestToolCallRoundTrip(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{
		{
			{Kind: provider.ChunkText, Text: "我先取一下数据。"},
			{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
				ID: "call_1", Name: "echo", Arguments: `{"value":"ping"}`,
			}},
			{Kind: provider.ChunkDone},
		},
		{
			{Kind: provider.ChunkText, Text: "拿到结果是「回声内容」。"},
			{Kind: provider.ChunkDone},
		},
	})

	events := h.start(t, "用工具取数据", protocol.ApprovalAuto)

	if h.scripted.callCount() != 2 {
		t.Fatalf("expected 2 model calls, got %d", h.scripted.callCount())
	}
	if h.echo.runCount() != 1 {
		t.Fatalf("expected the tool to run once, got %d", h.echo.runCount())
	}
	if h.echo.lastArgs != `{"value":"ping"}` {
		t.Errorf("tool received args %q", h.echo.lastArgs)
	}

	// The tool call must be published as an item that starts and then completes.
	var toolItem *protocol.Item
	for _, ev := range events {
		if ev.Item != nil && ev.Item.Type == protocol.ItemToolCall && ev.Kind == protocol.EvItemCompleted {
			toolItem = ev.Item
		}
	}
	if toolItem == nil {
		t.Fatal("no completed tool_call item")
	}
	if toolItem.Status != protocol.StatusCompleted || toolItem.Tool == nil {
		t.Fatalf("tool item not completed: %+v", toolItem)
	}
	if !toolItem.Tool.Success {
		t.Error("tool marked unsuccessful despite a clean run")
	}
	if toolItem.Tool.Result != "回声内容" {
		t.Errorf("tool result = %q", toolItem.Tool.Result)
	}
	if toolItem.Tool.Name != "echo" {
		t.Errorf("tool name = %q", toolItem.Tool.Name)
	}

	// The second request must contain the assistant tool call and the tool
	// result, otherwise the provider cannot accept the conversation.
	reqs := h.scripted.requests()
	second := reqs[1]
	var sawAssistantCall, sawToolResult bool
	for _, m := range second.Messages {
		if m.Role == provider.RoleAssistant && len(m.ToolCalls) == 1 && m.ToolCalls[0].ID == "call_1" {
			sawAssistantCall = true
		}
		if m.Role == provider.RoleTool && m.ToolCallID == "call_1" && m.Text() == "回声内容" {
			sawToolResult = true
		}
	}
	if !sawAssistantCall {
		t.Error("follow-up request is missing the assistant tool call")
	}
	if !sawToolResult {
		t.Error("follow-up request is missing the tool result")
	}

	// The turn must still end with a normal completion.
	if !hasKind(events, protocol.EvTurnCompleted) {
		t.Error("missing turn.completed after a tool round trip")
	}
}

// TestKnowledgeSearchProducesCitations verifies that the built-in retrieval tool
// works through the loop and that its hits reach the UI as references.
func TestKnowledgeSearchProducesCitations(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{
		{
			{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
				ID:        "call_kb",
				Name:      "kb_search",
				Arguments: `{"queries":["agent 协议 提交 事件"],"limit":5}`,
			}},
			{Kind: provider.ChunkDone},
		},
		{
			{Kind: provider.ChunkText, Text: "根据《协议说明》，HitK 采用提交与事件分离的模型。"},
			{Kind: provider.ChunkDone},
		},
	})

	events := h.start(t, "协议是怎么设计的", protocol.ApprovalAuto)

	var kbItem *protocol.Item
	for _, ev := range events {
		if ev.Item != nil && ev.Item.Type == protocol.ItemToolCall &&
			ev.Item.Tool != nil && ev.Item.Tool.Name == "kb_search" &&
			ev.Kind == protocol.EvItemCompleted {
			kbItem = ev.Item
		}
	}
	if kbItem == nil {
		t.Fatal("kb_search item not found")
	}
	if !kbItem.Tool.Success {
		t.Fatalf("kb_search failed: %s", kbItem.Tool.Result)
	}
	if !strings.Contains(kbItem.Tool.Result, "协议") {
		t.Errorf("search result does not mention the ingested document:\n%s", kbItem.Tool.Result)
	}
	if len(kbItem.Refs) == 0 {
		t.Fatal("kb_search returned no references")
	}
	if kbItem.Refs[0].DocTitle != "协议说明" {
		t.Errorf("reference doc title = %q, want 协议说明", kbItem.Refs[0].DocTitle)
	}
	if kbItem.Refs[0].ChunkID == "" {
		t.Error("reference is missing a chunk id, so kb_open cannot expand it")
	}
}

// TestReadOnlyPolicyDeniesMutatingTool checks that a mutating tool is refused
// without ever reaching the tool body.
func TestReadOnlyPolicyDeniesMutatingTool(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{
		{
			{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
				ID: "call_w", Name: "echo", Arguments: `{}`,
			}},
			{Kind: provider.ChunkDone},
		},
		{
			{Kind: provider.ChunkText, Text: "写操作被拒绝，我改用只读方式。"},
			{Kind: provider.ChunkDone},
		},
	})
	// The harness's read-only check keys off tool.Mutating(), so flip it.
	h.echo.mutating = true

	events := h.start(t, "写点东西", protocol.ApprovalReadOnly)

	if h.echo.runCount() != 0 {
		t.Errorf("mutating tool ran %d times under a read-only policy", h.echo.runCount())
	}
	var denied *protocol.Item
	for _, ev := range events {
		if ev.Item != nil && ev.Item.Type == protocol.ItemToolCall && ev.Kind == protocol.EvItemCompleted {
			denied = ev.Item
		}
	}
	if denied == nil {
		t.Fatal("no tool item recorded")
	}
	if denied.Status != protocol.StatusDenied {
		t.Errorf("status = %q, want denied", denied.Status)
	}
	if denied.Tool == nil || !denied.Tool.Denied {
		t.Error("tool call was not flagged as denied")
	}
}

// TestAutoPolicyRunsMutatingToolWithoutApproval locks down the full-access
// behavior: mutating tools execute immediately and no approval event is sent.
func TestAutoPolicyRunsMutatingToolWithoutApproval(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{
		{
			{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
				ID: "call_auto", Name: "echo", Arguments: `{}`,
			}},
			{Kind: provider.ChunkDone},
		},
		{
			{Kind: provider.ChunkText, Text: "已完成。"},
			{Kind: provider.ChunkDone},
		},
	})
	h.echo.mutating = true

	events := h.start(t, "直接执行写操作", protocol.ApprovalAuto)
	if h.echo.runCount() != 1 {
		t.Fatalf("mutating tool ran %d times under auto policy, want 1", h.echo.runCount())
	}
	if hasKind(events, protocol.EvApprovalNeeded) {
		t.Fatal("auto policy emitted an approval request")
	}
}

// TestApprovalFlowBlocksUntilAnswered drives the consent path: the tool must not
// run until the UI answers, then it must run when approved.
func TestApprovalFlowBlocksUntilAnswered(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{
		{
			{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
				ID: "call_a", Name: "echo", Arguments: `{}`,
			}},
			{Kind: provider.ChunkDone},
		},
		{
			{Kind: provider.ChunkText, Text: "已完成。"},
			{Kind: provider.ChunkDone},
		},
	})
	h.echo.mutating = true

	if err := h.agent.StartTurn(protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: h.thread.ID,
		Input:    []protocol.InputPart{{Type: "text", Text: "执行写操作"}},
		Mode:     protocol.ModeCoding,
		Provider: "scripted",
		Model:    "test-model",
		Policy:   protocol.ApprovalManual,
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	events := h.events.waitFor(t, "approval request", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvApprovalNeeded)
	})

	var approval *protocol.ApprovalRequest
	for _, ev := range events {
		if ev.Approval != nil {
			approval = ev.Approval
		}
	}
	if approval == nil {
		t.Fatal("approval request payload missing")
	}
	if approval.ToolName != "echo" {
		t.Errorf("approval tool = %q", approval.ToolName)
	}

	// The agent gates the call and the tool asks again inside its body; the
	// user must still only be asked once per operation.
	prompts := 0
	for _, ev := range h.events.all() {
		if ev.Kind == protocol.EvApprovalNeeded {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("approval prompted %d times for one tool call, want 1", prompts)
	}

	// The tool must still be blocked at this point.
	if h.echo.runCount() != 0 {
		t.Fatal("tool ran before the user answered")
	}

	if err := h.agent.RespondApproval(h.thread.ID, approval.ID, true, false); err != nil {
		t.Fatalf("respond approval: %v", err)
	}

	h.events.waitFor(t, "turn completion after approval", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnCompleted)
	})
	if h.echo.runCount() != 1 {
		t.Errorf("tool ran %d times after approval, want 1", h.echo.runCount())
	}
}

// TestDeclinedApprovalSurfacesAsDenied checks the "no" path reports the denial
// to the model rather than killing the turn.
func TestDeclinedApprovalSurfacesAsDenied(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{
		{
			{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
				ID: "call_d", Name: "echo", Arguments: `{}`,
			}},
			{Kind: provider.ChunkDone},
		},
		{
			{Kind: provider.ChunkText, Text: "好的，我不执行该操作。"},
			{Kind: provider.ChunkDone},
		},
	})
	h.echo.mutating = true

	if err := h.agent.StartTurn(protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: h.thread.ID,
		Input:    []protocol.InputPart{{Type: "text", Text: "写"}},
		Mode:     protocol.ModeCoding,
		Provider: "scripted",
		Model:    "test-model",
		Policy:   protocol.ApprovalManual,
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	events := h.events.waitFor(t, "approval request", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvApprovalNeeded)
	})
	var approval *protocol.ApprovalRequest
	for _, ev := range events {
		if ev.Approval != nil {
			approval = ev.Approval
		}
	}
	if err := h.agent.RespondApproval(h.thread.ID, approval.ID, false, false); err != nil {
		t.Fatalf("respond approval: %v", err)
	}

	h.events.waitFor(t, "turn completion after denial", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnCompleted)
	})
	if h.echo.runCount() != 0 {
		t.Error("tool ran despite the user declining")
	}

	// The model must have been told the call was denied so it can adapt.
	reqs := h.scripted.requests()
	if len(reqs) < 2 {
		t.Fatal("model was not called again after the denial")
	}
	var told bool
	for _, m := range reqs[1].Messages {
		if m.Role == provider.RoleTool && strings.Contains(m.Text(), "拒绝") {
			told = true
		}
	}
	if !told {
		t.Error("the denial was not reported back to the model")
	}
}

// TestInterruptStopsTurn verifies that cancelling mid-turn ends cleanly with an
// abort event rather than a stuck UI.
func TestInterruptStopsTurn(t *testing.T) {
	h := newHarness(t, nil)

	// A provider that never finishes on its own: the channel stays open until
	// the context is cancelled.
	blocking := &blockingProvider{}
	h.agent.providers.Register(blocking)

	if err := h.agent.StartTurn(protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: h.thread.ID,
		Input:    []protocol.InputPart{{Type: "text", Text: "开始一个长任务"}},
		Mode:     protocol.ModeWork,
		Provider: "blocking",
		Model:    "slow",
		Policy:   protocol.ApprovalAuto,
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	h.events.waitFor(t, "turn to start", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnStarted)
	})

	events := h.interruptAndWait(t)
	if !hasKind(events, protocol.EvTurnAborted) {
		t.Fatal("interrupted turn did not emit turn.aborted")
	}
	// The thread must be free again so the user can send another message; the
	// helper's awaitIdle already guarantees this, but assert it explicitly so
	// the contract the UI depends on is visible here.
	if h.agent.Running(h.thread.ID) {
		t.Fatal("agent still reports the thread as running after an interrupt")
	}
}

// TestShutdownWaitsForRunningTurn guards the invariant application exit relies
// on: once Shutdown returns, no turn goroutine is still touching the session
// store.
func TestShutdownWaitsForRunningTurn(t *testing.T) {
	h := newHarness(t, nil)
	h.agent.providers.Register(&blockingProvider{})

	if err := h.agent.StartTurn(protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: h.thread.ID,
		Input:    []protocol.InputPart{{Type: "text", Text: "长任务"}},
		Provider: "blocking",
		Model:    "slow",
		Policy:   protocol.ApprovalAuto,
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	h.events.waitFor(t, "turn to start", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnStarted)
	})

	h.agent.Shutdown()

	// Shutdown is only meaningful if it blocked until the goroutine was done,
	// so the thread must already be released when it returns.
	if h.agent.Running(h.thread.ID) {
		t.Fatal("Shutdown returned while a turn was still in flight")
	}
	// A submission racing application exit must be refused rather than started
	// against state that is about to be torn down.
	if err := h.agent.StartTurn(protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: h.thread.ID,
		Input:    []protocol.InputPart{{Type: "text", Text: "又一条"}},
		Provider: "blocking",
		Model:    "slow",
		Policy:   protocol.ApprovalAuto,
	}); err == nil {
		t.Fatal("a turn was accepted after Shutdown")
	}
}

// blockingProvider streams one text chunk then holds the stream open.
type blockingProvider struct{}

func (b *blockingProvider) ID() string { return "blocking" }

func (b *blockingProvider) Stream(ctx context.Context, _ provider.ChatRequest) (<-chan provider.Chunk, error) {
	out := make(chan provider.Chunk, 1)
	out <- provider.Chunk{Kind: provider.ChunkText, Text: "开始处理…"}
	go func() {
		defer close(out)
		<-ctx.Done()
	}()
	return out, nil
}

// TestSecondTurnIsRejectedWhileRunning guards against two concurrent turns on
// one thread, which would interleave items in the transcript.
func TestSecondTurnIsRejectedWhileRunning(t *testing.T) {
	h := newHarness(t, nil)
	h.agent.providers.Register(&blockingProvider{})

	op := protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: h.thread.ID,
		Input:    []protocol.InputPart{{Type: "text", Text: "第一个任务"}},
		Provider: "blocking",
		Model:    "slow",
		Policy:   protocol.ApprovalAuto,
	}
	if err := h.agent.StartTurn(op); err != nil {
		t.Fatalf("first start: %v", err)
	}
	h.events.waitFor(t, "first turn to start", func(events []*protocol.Event) bool {
		return hasKind(events, protocol.EvTurnStarted)
	})

	if err := h.agent.StartTurn(op); err == nil {
		t.Fatal("a second concurrent turn was accepted on the same thread")
	}
	// Tear the turn down before the harness temp dir goes away, otherwise the
	// goroutine's final save lands in a directory the test already deleted.
	h.interruptAndWait(t)
}

// TestStepBudgetEmitsNotice checks that exhausting maxSteps is reported instead
// of silently truncating the answer.
func TestStepBudgetEmitsNotice(t *testing.T) {
	// Every scripted step requests another tool call, so the loop can only end
	// by running out of budget.
	step := []provider.Chunk{
		{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
			ID: "call_loop", Name: "echo", Arguments: `{}`,
		}},
		{Kind: provider.ChunkDone},
	}
	h := newHarness(t, [][]provider.Chunk{step, step, step, step, step, step, step, step})
	h.agent.SetMaxSteps(3)

	events := h.start(t, "一直调用工具", protocol.ApprovalAuto)

	if h.scripted.callCount() != 3 {
		t.Errorf("expected the loop to stop at 3 steps, got %d calls", h.scripted.callCount())
	}
	var notice *protocol.Item
	for _, ev := range events {
		if ev.Item != nil && ev.Item.Type == protocol.ItemNotice {
			notice = ev.Item
		}
	}
	if notice == nil {
		t.Fatal("no notice item explaining the step budget was exhausted")
	}
	if !strings.Contains(notice.Text, "上限") {
		t.Errorf("notice text does not mention the limit: %q", notice.Text)
	}
}

// TestProviderErrorIsReported checks that a vendor failure surfaces as an error
// item plus a terminal event, so the composer never stays disabled.
func TestProviderErrorIsReported(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{{
		{Kind: provider.ChunkError, Err: fmt.Errorf("deepseek: HTTP 401: invalid api key")},
	}})

	events := h.start(t, "你好", protocol.ApprovalAuto)

	var errItem *protocol.Item
	for _, ev := range events {
		if ev.Item != nil && ev.Item.Type == protocol.ItemError {
			errItem = ev.Item
		}
	}
	if errItem == nil {
		t.Fatal("no error item emitted")
	}
	if !strings.Contains(errItem.Error, "鉴权") {
		t.Errorf("error was not translated into actionable advice: %q", errItem.Error)
	}
	if !hasKind(events, protocol.EvTurnAborted) {
		t.Error("missing terminal event after a provider error")
	}
}

// TestCoordinationBudgetNotice ensures the persisted thread keeps the
// conversation, so reopening a session shows the same transcript.
func TestThreadPersistsItems(t *testing.T) {
	h := newHarness(t, [][]provider.Chunk{
		{
			{Kind: provider.ChunkText, Text: "持久化回复。"},
			{Kind: provider.ChunkDone},
		},
	})

	h.start(t, "请记住这句话", protocol.ApprovalAuto)

	// Reopening from disk must reproduce the transcript.
	h.sessions.mu.Lock()
	delete(h.sessions.cache, h.thread.ID)
	h.sessions.mu.Unlock()

	reloaded, err := h.sessions.Get(h.thread.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	var userMsg, agentMsg string
	for _, it := range reloaded.Items {
		switch it.Type {
		case protocol.ItemUserMessage:
			userMsg = it.Text
		case protocol.ItemAgentMessage:
			agentMsg = it.Text
		}
	}
	if userMsg != "请记住这句话" {
		t.Errorf("user message not persisted: %q", userMsg)
	}
	if agentMsg != "持久化回复。" {
		t.Errorf("agent message not persisted: %q", agentMsg)
	}
	if reloaded.Title == "" || reloaded.Title == "新会话" {
		t.Errorf("thread title was not derived from the first message: %q", reloaded.Title)
	}
}
