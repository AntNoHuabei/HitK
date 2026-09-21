// Package agent implements the HitK turn loop.
//
// The loop is the classic ReAct shape — call the model, run whatever tools it
// asked for, feed the results back, repeat — with three additions that the
// Codex protocol design makes explicit:
//
//  1. Every model output and tool execution becomes an Item that is streamed to
//     the UI with started / delta / completed events, so the frontend never has
//     to guess what is happening.
//  2. Side-effecting tools block on an approval request that the UI answers
//     out of band, which is what allows a desktop app to hand a shell to a model
//     without giving up control.
//  3. A turn is cancellable at any await point; cancellation unwinds cleanly and
//     still emits a terminal event.
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
	"github.com/hitk/hitk/internal/tools"
)

// maxTurnRefs caps how many citations ride along with a final answer. A search
// returns the most relevant chunks first, so the tail adds noise rather than
// traceability.
const maxTurnRefs = 12

// Options configures an Agent.
type Options struct {
	KB        *kb.Store
	Tools     *tools.Registry
	Providers *provider.Registry
	Sessions  *SessionStore
	// Emit publishes protocol events to the UI. It must not block.
	Emit func(*protocol.Event)
	// Workspace is the project root used by coding tools.
	Workspace string
	// ContextLimit is the model's context window in tokens, used to decide
	// when to compact the conversation.
	ContextLimit int
	// MaxSteps caps tool-calling rounds per turn.
	MaxSteps int
}

// Agent runs turns.
type Agent struct {
	kb        *kb.Store
	registry  *tools.Registry
	providers *provider.Registry
	sessions  *SessionStore
	emit      func(*protocol.Event)
	workspace string
	ctxLimit  int
	maxSteps  int

	seq atomic.Int64

	mu      sync.Mutex
	running map[string]*turnHandle
	// turns tracks in-flight turn goroutines so [Agent.Shutdown] can wait for
	// them. A turn keeps writing the transcript on its way out, so anything
	// that tears down the session store has to quiesce the agent first.
	turns  sync.WaitGroup
	closed bool
}

// turnHandle tracks one in-flight turn so it can be interrupted and so approval
// replies can be routed back to the tool that is blocked.
type turnHandle struct {
	threadID string
	turnID   string
	cancel   context.CancelFunc

	mu       sync.Mutex
	approval map[string]chan bool
	// consented records tool invocations the user already allowed in this turn.
	consented map[string]bool
	remember  bool
}

// New builds an Agent.
func New(opts Options) *Agent {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = protocol.DefaultMaxSteps
	}
	ctxLimit := opts.ContextLimit
	if ctxLimit <= 0 {
		ctxLimit = 128000
	}
	return &Agent{
		kb:        opts.KB,
		registry:  opts.Tools,
		providers: opts.Providers,
		sessions:  opts.Sessions,
		emit:      opts.Emit,
		workspace: opts.Workspace,
		ctxLimit:  ctxLimit,
		maxSteps:  maxSteps,
		running:   map[string]*turnHandle{},
	}
}

// SetWorkspace changes the active project root.
func (a *Agent) SetWorkspace(dir string) {
	a.mu.Lock()
	a.workspace = dir
	a.mu.Unlock()
}

// Workspace returns the active project root.
func (a *Agent) Workspace() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workspace
}

// SetContextLimit updates the context budget after a model change.
func (a *Agent) SetContextLimit(n int) {
	if n > 0 {
		a.mu.Lock()
		a.ctxLimit = n
		a.mu.Unlock()
	}
}

// SetMaxSteps updates the per-turn tool-call budget.
func (a *Agent) SetMaxSteps(n int) {
	if n > 0 {
		a.mu.Lock()
		a.maxSteps = n
		a.mu.Unlock()
	}
}

// Running reports whether a thread has a turn in flight.
func (a *Agent) Running(threadID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.running[threadID]
	return ok
}

// ---------------------------------------------------------------------------
// Turn lifecycle
// ---------------------------------------------------------------------------

// StartTurn validates a submission, registers a cancellable turn and runs it in
// the background. It returns as soon as the turn is registered so the UI call
// does not block for the whole model interaction.
func (a *Agent) StartTurn(op protocol.Op) error {
	if op.ThreadID == "" {
		return errors.New("缺少 threadId")
	}
	thread, err := a.sessions.Get(op.ThreadID)
	if err != nil {
		return err
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("应用正在退出，无法开始新任务")
	}
	if _, busy := a.running[op.ThreadID]; busy {
		a.mu.Unlock()
		return errors.New("当前会话已有任务在执行，请先停止或等待完成")
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &turnHandle{
		threadID:  op.ThreadID,
		turnID:    "turn_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
		cancel:    cancel,
		approval:  map[string]chan bool{},
		consented: map[string]bool{},
	}
	a.running[op.ThreadID] = h
	workspace := a.workspace
	// Registered inside the lock so Shutdown cannot observe an empty set while
	// a turn is about to start.
	a.turns.Add(1)
	a.mu.Unlock()

	go func() {
		defer a.turns.Done()
		defer func() {
			cancel()
			a.mu.Lock()
			delete(a.running, op.ThreadID)
			a.mu.Unlock()
			// Any tool still waiting on consent must be released, otherwise its
			// goroutine leaks until process exit.
			h.releaseWaiting()
		}()
		a.runTurn(ctx, h, thread, op, workspace)
	}()
	return nil
}

// Interrupt cancels the turn running on a thread.
//
// It is deliberately asynchronous: it only signals cancellation and returns, so
// the caller observes the outcome through the turn.aborted event. Anything that
// then destroys state the turn writes to must use [Agent.Shutdown] instead.
func (a *Agent) Interrupt(threadID string) error {
	a.mu.Lock()
	h, ok := a.running[threadID]
	a.mu.Unlock()
	if !ok {
		return errors.New("当前会话没有正在执行的任务")
	}
	h.cancel()
	h.releaseWaiting()
	return nil
}

// Shutdown stops every in-flight turn and waits for the goroutines to unwind.
//
// A turn keeps writing the transcript while it finishes, so application exit
// must quiesce the agent before closing the session store, otherwise the tail
// of a turn lands on a closed store.
func (a *Agent) Shutdown() {
	a.mu.Lock()
	a.closed = true
	handles := make([]*turnHandle, 0, len(a.running))
	for _, h := range a.running {
		handles = append(handles, h)
	}
	a.mu.Unlock()

	for _, h := range handles {
		h.cancel()
		// Tools blocked on consent would otherwise hold their goroutine open
		// and Wait would never return.
		h.releaseWaiting()
	}
	a.turns.Wait()
}

// RespondApproval answers a pending approval request.
func (a *Agent) RespondApproval(threadID, approvalID string, approve, remember bool) error {
	a.mu.Lock()
	h, ok := a.running[threadID]
	a.mu.Unlock()
	if !ok {
		return errors.New("当前会话没有正在执行的任务")
	}
	h.mu.Lock()
	ch, exists := h.approval[approvalID]
	if exists && remember && approve {
		h.remember = true
	}
	h.mu.Unlock()
	if !exists {
		return errors.New("审批请求不存在或已过期")
	}
	select {
	case ch <- approve:
	default:
	}
	return nil
}

// releaseWaiting unblocks every approval wait with a denial.
func (h *turnHandle) releaseWaiting() {
	h.mu.Lock()
	waiters := h.approval
	h.approval = map[string]chan bool{}
	h.mu.Unlock()
	for _, ch := range waiters {
		select {
		case ch <- false:
		default:
		}
	}
}

// setWorkspaceForTurn applies a per-thread workspace when the thread carries
// one, so reopening an old coding conversation restores its directory.
func (a *Agent) setWorkspaceForTurn(dir string) (restore func()) {
	if dir == "" {
		return func() {}
	}
	a.mu.Lock()
	prev := a.workspace
	a.workspace = dir
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		a.workspace = prev
		a.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// The loop
// ---------------------------------------------------------------------------

// runTurn is the whole turn: assemble input, stream the model, execute tools,
// repeat, then persist and report.
func (a *Agent) runTurn(ctx context.Context, h *turnHandle, thread *protocol.Thread, op protocol.Op, workspace string) {
	restore := a.setWorkspaceForTurn(workspace)
	defer restore()

	mode := op.Mode
	if !mode.Valid() {
		mode = thread.Mode
		if !mode.Valid() {
			mode = protocol.ModeWork
		}
	}
	provID := firstNonEmpty(op.Provider, thread.Provider, "deepseek")
	modelID := firstNonEmpty(op.Model, thread.Model)
	if modelID == "" {
		if meta, ok := provider.VendorByID(provID); ok {
			modelID = meta.DefaultModel
		}
	}

	providerClient, err := a.providers.Provider(provID)
	if err != nil {
		a.emitError(thread.ID, h.turnID, fmt.Sprintf("模型厂商配置错误：%v", err))
		return
	}
	if !a.providers.Ready(provID) {
		a.emitError(thread.ID, h.turnID, fmt.Sprintf("%s 尚未配置 API Key，请在「设置 → 模型」中填写。", provID))
		return
	}

	// Record user input in the thread before anything can fail, so the message
	// is never lost even if the provider errors out immediately.
	userText := joinInput(op.Input)
	if userText != "" {
		item := &protocol.Item{
			ID:          newItemID(),
			TurnID:      h.turnID,
			ThreadID:    thread.ID,
			Type:        protocol.ItemUserMessage,
			Status:      protocol.StatusCompleted,
			CreatedAt:   time.Now().UnixMilli(),
			Text:        userText,
			Attachments: op.Attachments,
		}
		thread.Items = append(thread.Items, item)
		a.emitItem(protocol.EvItemCompleted, item)
	}
	thread.Mode = mode
	thread.Provider = provID
	thread.Model = modelID
	thread.Collections = opCollections(op)
	thread.ReasoningEffort = op.ReasoningEffort
	if err := a.sessions.Save(thread); err != nil {
		a.emitError(thread.ID, h.turnID, fmt.Sprintf("会话保存失败：%v", err))
	}

	start := protocol.NewEvent(protocol.EvTurnStarted)
	start.ThreadID = thread.ID
	start.TurnID = h.turnID
	a.publish(start)

	usage := &protocol.Usage{}
	step := 0
	aborted := false
	// hitLimit records that the loop stopped because the step budget ran out
	// rather than because the model produced a final answer.
	hitLimit := true
	// turnRefs accumulates the sources every tool result pointed at, so the
	// final answer can carry citations the user can follow back to the original
	// document. Without this a grounded answer looks identical to a guess.
	var turnRefs []protocol.Reference

	for step < a.maxSteps {
		step++
		if ctx.Err() != nil {
			aborted = true
			break
		}

		messages, err := a.buildMessages(thread, mode, provID, modelID, op)
		if err != nil {
			a.emitError(thread.ID, h.turnID, err.Error())
			return
		}

		schemas := a.registry.ForMode(mode)

		// --- stream the model -------------------------------------------------
		msgItem := &protocol.Item{
			ID:        newItemID(),
			TurnID:    h.turnID,
			ThreadID:  thread.ID,
			Type:      protocol.ItemAgentMessage,
			Status:    protocol.StatusInProgress,
			CreatedAt: time.Now().UnixMilli(),
		}
		reasonItem := &protocol.Item{
			ID:        newItemID(),
			TurnID:    h.turnID,
			ThreadID:  thread.ID,
			Type:      protocol.ItemReasoning,
			Status:    protocol.StatusInProgress,
			CreatedAt: time.Now().UnixMilli(),
		}
		msgStarted, reasonStarted := false, false

		stream, err := providerClient.Stream(ctx, provider.ChatRequest{
			Model:    modelID,
			System:   a.systemPrompt(thread, mode, provID, modelID, op),
			Messages: messages,
			Tools:    schemas,
			Metadata: reasoningMetadata(provID, modelID, op.ReasoningEffort),
		})
		if err != nil {
			msgItem.Status = protocol.StatusFailed
			msgItem.Error = err.Error()
			reasonItem.Status = protocol.StatusCompleted
			a.emitError(thread.ID, h.turnID, friendlyProviderError(provID, err))
			return
		}

		var (
			calls    []provider.ToolCallReq
			textBuf  strings.Builder
			reasonBu strings.Builder
			stepUse  provider.Usage
		)

	streamLoop:
		for chunk := range stream {
			switch chunk.Kind {
			case provider.ChunkText:
				if !msgStarted {
					a.emitItem(protocol.EvItemStarted, msgItem)
					msgStarted = true
				}
				textBuf.WriteString(chunk.Text)
				a.emitDelta(thread.ID, h.turnID, msgItem.ID, "text", chunk.Text)
			case provider.ChunkReasoning:
				if !reasonStarted {
					a.emitItem(protocol.EvItemStarted, reasonItem)
					reasonStarted = true
				}
				reasonBu.WriteString(chunk.Text)
				a.emitDelta(thread.ID, h.turnID, reasonItem.ID, "reasoning", chunk.Text)
			case provider.ChunkToolCall:
				if chunk.Tool != nil {
					calls = append(calls, *chunk.Tool)
				}
			case provider.ChunkUsage:
				if chunk.Usage != nil {
					stepUse = *chunk.Usage
				}
			case provider.ChunkError:
				aborted = true
				if !msgStarted {
					a.emitItem(protocol.EvItemStarted, msgItem)
					msgStarted = true
				}
				msgItem.Status = protocol.StatusFailed
				msgItem.Error = friendlyProviderError(provID, chunk.Err)
				msgItem.Text = textBuf.String()
				a.emitItem(protocol.EvItemCompleted, msgItem)
				thread.Items = append(thread.Items, msgItem)
				a.emitError(thread.ID, h.turnID, msgItem.Error)
				a.sessions.Save(thread)
				break streamLoop
			}
		}

		// A cancelled stream closes its channel without a done chunk, so the
		// context is the only reliable signal that the turn was interrupted.
		// Without this the loop would fall through and report a clean
		// completion for a turn the user stopped.
		if ctx.Err() != nil {
			aborted = true
		}

		// Token accounting from the vendor is authoritative when present; the
		// local estimate only fills the gap so the UI meter is never blank.
		if stepUse.InputTokens > 0 {
			usage.InputTokens = stepUse.InputTokens
		} else {
			usage.InputTokens = estimateMessages(messages) + estimateTokens(a.systemPrompt(thread, mode, provID, modelID, op))
		}
		if stepUse.OutputTokens > 0 {
			usage.OutputTokens += stepUse.OutputTokens
		} else {
			usage.OutputTokens += estimateTokens(textBuf.String()) + estimateTokens(reasonBu.String())
		}
		if stepUse.CachedTokens > 0 {
			usage.CachedTokens = stepUse.CachedTokens
		}
		usage.Steps = step
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		usage.ContextLimit = a.ctxLimit

		if reasonStarted {
			reasonItem.Reasoning = reasonBu.String()
			reasonItem.Status = protocol.StatusCompleted
			a.emitItem(protocol.EvItemCompleted, reasonItem)
			thread.Items = append(thread.Items, reasonItem)
		}

		if aborted {
			if msgStarted && msgItem.Status == protocol.StatusInProgress {
				msgItem.Text = textBuf.String()
				msgItem.Status = protocol.StatusCompleted
				msgItem.Refs = mergeRefs(turnRefs)
				a.emitItem(protocol.EvItemCompleted, msgItem)
				thread.Items = append(thread.Items, msgItem)
			}
			break
		}

		text := textBuf.String()
		if msgStarted || text != "" || len(calls) > 0 {
			msgItem.Text = text
			msgItem.Status = protocol.StatusCompleted
			msgItem.Refs = mergeRefs(turnRefs)
			a.emitItem(protocol.EvItemCompleted, msgItem)
			thread.Items = append(thread.Items, msgItem)
		}

		if len(calls) == 0 {
			// No tools requested: the model is answering, so the turn ends here.
			hitLimit = false
			break
		}

		// --- execute tools ----------------------------------------------------
		a.recordAssistantTurn(thread, h, text, calls)
		for _, call := range calls {
			if ctx.Err() != nil {
				aborted = true
				break
			}
			result := a.runTool(ctx, h, thread, mode, provID, modelID, op, call)
			thread.Items = append(thread.Items, result.item)
			if len(result.item.Refs) > 0 {
				turnRefs = append(turnRefs, result.item.Refs...)
			}
			a.recordToolResult(thread, h, call, result)
		}
		if aborted {
			break
		}

		a.publishUsage(thread.ID, h.turnID, usage)
		if err := a.sessions.Save(thread); err != nil {
			a.emitError(thread.ID, h.turnID, fmt.Sprintf("会话保存失败：%v", err))
		}
	}

	if hitLimit && !aborted {
		// Falling out of the loop on the step budget is a real outcome the user
		// needs to know about, otherwise the UI looks like it silently stopped.
		notice := &protocol.Item{
			ID:        newItemID(),
			TurnID:    h.turnID,
			ThreadID:  thread.ID,
			Type:      protocol.ItemNotice,
			Status:    protocol.StatusCompleted,
			CreatedAt: time.Now().UnixMilli(),
			Text:      fmt.Sprintf("已达到单轮工具调用上限（%d 步），任务暂停。可以继续追问以接着处理。", a.maxSteps),
		}
		thread.Items = append(thread.Items, notice)
		a.emitItem(protocol.EvItemCompleted, notice)
	}

	a.publishUsage(thread.ID, h.turnID, usage)
	_ = a.sessions.Save(thread)

	if aborted {
		end := protocol.NewEvent(protocol.EvTurnAborted)
		end.ThreadID = thread.ID
		end.TurnID = h.turnID
		end.Reason = abortReason(ctx)
		a.publish(end)
		return
	}

	end := protocol.NewEvent(protocol.EvTurnCompleted)
	end.ThreadID = thread.ID
	end.TurnID = h.turnID
	end.Usage = usage
	a.publish(end)
}

// modelMessage is one entry of the conversation reconstructed for the vendor.
type modelMessage struct {
	provider.Message
	// toolCalls / toolCallID round-trip through the provider message type.
}

// buildMessages reconstructs the vendor conversation from stored items,
// applying compaction when the estimated size approaches the context window.
func (a *Agent) buildMessages(thread *protocol.Thread, mode protocol.Mode, provID, modelID string, op protocol.Op) ([]provider.Message, error) {
	items := a.compactIfNeeded(thread)

	var msgs []provider.Message
	// The latest user turn is sent as an explicit message even though it is
	// also stored as an item, because the item list may have been compacted.
	// Emitting it once here keeps the model's view consistent with the UI.
	for i, it := range items {
		switch it.Type {
		case protocol.ItemUserMessage:
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			parts := []provider.Part{provider.TextPart(it.Text)}
			// Only the newest user message is the active turn. Older attachments
			// remain visible in history but must never be re-sent to a model.
			if i == lastUserItem(items) {
				parts = append(parts, attachmentParts(it.Attachments)...)
			}
			msgs = append(msgs, provider.Message{Role: provider.RoleUser, Parts: parts})
		case protocol.ItemAgentMessage:
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			// An assistant turn that requested tools must carry the calls, or
			// the following tool results reference a call the model never made.
			if calls := toolCallsOf(it); len(calls) > 0 {
				msgs = append(msgs, provider.AssistantTools(calls, it.Text))
			} else {
				msgs = append(msgs, provider.AssistantText(it.Text))
			}
		case protocol.ItemToolCall:
			if it.Tool == nil {
				continue
			}
			content := it.Tool.Result
			if content == "" {
				content = "(无输出)"
			}
			msgs = append(msgs, provider.ToolResult(it.Tool.ID, it.Tool.Name, content))
		case protocol.ItemNotice:
			msgs = append(msgs, provider.UserText("[系统提示] "+it.Text))
		}
	}

	// Some vendors reject a conversation that does not end with a user turn,
	// which happens when the previous turn finished on a tool result.
	if len(msgs) == 0 {
		msgs = append(msgs, provider.UserText(joinInput(op.Input)))
	}
	return msgs, nil
}

// toolCallsOf recovers the tool calls recorded alongside an assistant message.
func toolCallsOf(it *protocol.Item) []provider.ToolCallReq {
	if it.Meta == nil {
		return nil
	}
	raw, ok := it.Meta["toolCalls"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var calls []provider.ToolCallReq
	if json.Unmarshal(data, &calls) != nil {
		return nil
	}
	return calls
}

// compactIfNeeded trims the oldest items when the conversation outgrows the
// context budget, dropping whole turns and leaving a marker so the model knows
// the history is incomplete rather than thinking the conversation started
// mid-sentence.
func (a *Agent) compactIfNeeded(thread *protocol.Thread) []*protocol.Item {
	limit := a.ctxLimit
	if limit <= 0 {
		return thread.Items
	}
	budget := limit * 3 / 4

	if estimateItems(thread.Items) <= budget {
		return thread.Items
	}

	// Keep the tail, growing it until the budget is met, then round forward to
	// a user-message boundary so no tool result is orphaned from its call.
	kept := make([]*protocol.Item, 0, len(thread.Items))
	total := 0
	for i := len(thread.Items) - 1; i >= 0; i-- {
		cost := estimateItem(thread.Items[i])
		if total+cost > budget*2/3 && len(kept) > 0 {
			break
		}
		kept = append(kept, thread.Items[i])
		total += cost
	}
	// Reverse into chronological order.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	for len(kept) > 0 && kept[0].Type != protocol.ItemUserMessage {
		kept = kept[1:]
	}

	dropped := len(thread.Items) - len(kept)
	if dropped <= 0 {
		return thread.Items
	}
	marker := &protocol.Item{
		ID:        newItemID(),
		Type:      protocol.ItemNotice,
		Status:    protocol.StatusCompleted,
		CreatedAt: time.Now().UnixMilli(),
		Text:      fmt.Sprintf("为控制上下文长度，已省略较早的 %d 条消息，仅保留最近的对话。", dropped),
	}
	return append([]*protocol.Item{marker}, kept...)
}

// runTool executes one tool call and returns the item plus the payload the
// conversation needs.
func (a *Agent) runTool(ctx context.Context, h *turnHandle, thread *protocol.Thread,
	mode protocol.Mode, provID, modelID string, op protocol.Op, call provider.ToolCallReq) toolOutcome {

	item := &protocol.Item{
		ID:        newItemID(),
		TurnID:    h.turnID,
		ThreadID:  thread.ID,
		Type:      protocol.ItemToolCall,
		Status:    protocol.StatusInProgress,
		CreatedAt: time.Now().UnixMilli(),
		Tool: &protocol.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		},
	}
	a.emitItem(protocol.EvItemStarted, item)

	tool, ok := a.registry.Get(call.Name)
	if !ok {
		item.Status = protocol.StatusFailed
		item.Tool.Success = false
		item.Tool.Result = fmt.Sprintf("工具 %s 不存在。可用工具：%s", call.Name, strings.Join(a.registry.Names(), ", "))
		item.Tool.DisplayName = call.Name
		a.emitItem(protocol.EvItemCompleted, item)
		return toolOutcome{item: item}
	}
	item.Tool.DisplayName = tool.DisplayName()

	tctx := &tools.Context{
		Ctx:            ctx,
		KB:             a.kb,
		Workspace:      a.Workspace(),
		Mode:           mode,
		Policy:         protocol.ApprovalAuto,
		ThreadID:       thread.ID,
		TurnID:         h.turnID,
		KnowledgeBases: opCollections(op),
	}
	if tool.Mutating() {
		// Read-only mode refuses mutating tools outright; otherwise consent is
		// requested before the tool body runs.
		if op.ApprovalPolicy() == protocol.ApprovalReadOnly {
			item.Status = protocol.StatusDenied
			item.Tool.Success = false
			item.Tool.Denied = true
			item.Tool.Result = "当前为只读模式，已拒绝该写操作。"
			a.emitItem(protocol.EvItemCompleted, item)
			return toolOutcome{item: item}
		}
		// The agent owns the consent gate rather than the tool, so a mutating
		// tool can never change state before the user answers even if its own
		// approval code is missing or wrong. Tools may still call Approve to
		// show richer detail (a diff, a resolved path); that call reuses this
		// answer instead of interrupting the user a second time.
		tctx.Policy = protocol.ApprovalManual
		tctx.Approver = a.approver(h, thread.ID, h.turnID, item.ID)
		if err := a.requestConsent(ctx, h, thread.ID, h.turnID, item.ID, consentFor(tool, call)); err != nil {
			// A refusal is a conversational outcome, not a crash: the model is
			// told so it can propose something else.
			item.Status = protocol.StatusDenied
			item.Tool.Success = false
			item.Tool.Denied = true
			item.Tool.Result = err.Error()
			item.Error = err.Error()
			a.emitItem(protocol.EvItemCompleted, item)
			return toolOutcome{item: item}
		}
	} else {
		tctx.Policy = protocol.ApprovalAuto
	}

	begin := time.Now()
	res, err := tool.Run(tctx, json.RawMessage(call.Arguments))
	item.Tool.DurationMS = time.Since(begin).Milliseconds()

	if err != nil {
		// An error from a tool is a normal conversational event: the model
		// should see it and decide what to do, not have the turn collapse.
		item.Status = protocol.StatusFailed
		item.Tool.Success = false
		item.Tool.Result = err.Error()
		item.Error = err.Error()
		if strings.Contains(err.Error(), "被拒绝") || strings.Contains(err.Error(), "拒绝") {
			item.Status = protocol.StatusDenied
			item.Tool.Denied = true
		}
		a.emitItem(protocol.EvItemCompleted, item)
		return toolOutcome{item: item}
	}

	item.Status = protocol.StatusCompleted
	item.Tool.Success = true
	item.Tool.Result = res.Content
	item.Tool.Truncated = res.Truncated
	item.Refs = res.Refs
	if res.Display != "" {
		item.Tool.DisplayName = res.Display
	}
	if res.Plan != nil {
		item.Plan = res.Plan
	}
	if res.Meta != nil {
		item.Meta = res.Meta
		item.Artifacts = artifactsFromMeta(res.Meta)
	}
	a.emitItem(protocol.EvItemCompleted, item)
	return toolOutcome{item: item}
}

// toolOutcome carries what the conversation needs from a tool run.
type toolOutcome struct {
	item *protocol.Item
}

// mergeRefs folds every citation gathered during a turn into one list for the
// final answer. Duplicates by chunk are collapsed to their best score so the
// same passage surfacing from several queries is cited once.
func mergeRefs(groups []protocol.Reference) []protocol.Reference {
	if len(groups) == 0 {
		return nil
	}
	best := make(map[string]protocol.Reference, len(groups))
	order := make([]string, 0, len(groups))
	for _, r := range groups {
		key := r.ChunkID
		if key == "" {
			key = r.DocID + "|" + r.Path
		}
		prev, seen := best[key]
		if !seen {
			order = append(order, key)
			best[key] = r
			continue
		}
		if r.Score > prev.Score {
			best[key] = r
		}
	}
	out := make([]protocol.Reference, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > maxTurnRefs {
		out = out[:maxTurnRefs]
	}
	return out
}

// approver builds the callback a tool can use to ask for consent itself.
func (a *Agent) approver(h *turnHandle, threadID, turnID, itemID string) tools.Approver {
	return func(ctx context.Context, req tools.ApprovalRequest) error {
		return a.requestConsent(ctx, h, threadID, turnID, itemID, req)
	}
}

// requestConsent publishes an approval request and blocks until the UI answers.
// The first request for a tool invocation prompts the user; later requests from
// the same invocation reuse the answer, so a tool that asks again with richer
// detail does not interrupt twice for one operation.
func (a *Agent) requestConsent(ctx context.Context, h *turnHandle, threadID, turnID, itemID string, req tools.ApprovalRequest) error {
	h.mu.Lock()
	if h.remember || h.consented[itemID] {
		h.mu.Unlock()
		return nil
	}
	ch := make(chan bool, 1)
	approvalID := "apv_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	h.approval[approvalID] = ch
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.approval, approvalID)
		h.mu.Unlock()
	}()

	ev := protocol.NewEvent(protocol.EvApprovalNeeded)
	ev.ThreadID = threadID
	ev.TurnID = turnID
	ev.Approval = &protocol.ApprovalRequest{
		ID:        approvalID,
		TurnID:    turnID,
		ItemID:    itemID,
		ToolName:  req.ToolName,
		Title:     req.Title,
		Detail:    req.Detail,
		Diff:      req.Diff,
		Command:   req.Command,
		CreatedAt: time.Now().UnixMilli(),
	}
	a.publish(ev)

	select {
	case ok := <-ch:
		if !ok {
			return fmt.Errorf("用户拒绝执行 %s", req.ToolName)
		}
		h.mu.Lock()
		h.consented[itemID] = true
		h.mu.Unlock()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("任务已中断")
	}
}

// consentFor describes a call for the prompt without help from the tool, so the
// agent can gate a mutating call before the tool body runs. Commands and paths
// are pulled out of the arguments because those are what a user needs to judge.
func consentFor(tool tools.Tool, call provider.ToolCallReq) tools.ApprovalRequest {
	req := tools.ApprovalRequest{ToolName: tool.Name(), Title: tool.DisplayName()}

	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		req.Detail = clipRunes(call.Arguments, 400)
		return req
	}
	if cmd, ok := args["command"].(string); ok {
		req.Command = cmd
	}
	for _, key := range []string{"path", "file", "target", "url", "dest", "name"} {
		if v, ok := args[key].(string); ok && v != "" {
			req.Detail = v
			break
		}
	}
	if req.Detail == "" && req.Command == "" {
		if raw, err := json.Marshal(args); err == nil {
			req.Detail = clipRunes(string(raw), 400)
		}
	}
	return req
}

// ---------------------------------------------------------------------------
// Conversation bookkeeping
// ---------------------------------------------------------------------------

// recordAssistantTurn stores an assistant message carrying tool calls.
func (a *Agent) recordAssistantTurn(thread *protocol.Thread, h *turnHandle, text string, calls []provider.ToolCallReq) {
	// The message item was already appended by the caller; attach the calls so
	// buildMessages can reconstruct the vendor conversation verbatim.
	for i := len(thread.Items) - 1; i >= 0; i-- {
		it := thread.Items[i]
		if it.Type != protocol.ItemAgentMessage || it.TurnID != h.turnID {
			continue
		}
		if it.Meta == nil {
			it.Meta = map[string]any{}
		}
		it.Meta["toolCalls"] = calls
		return
	}
	// No assistant text was emitted (tools only): create a carrier item so the
	// call list survives a reload.
	item := &protocol.Item{
		ID:        newItemID(),
		TurnID:    h.turnID,
		ThreadID:  thread.ID,
		Type:      protocol.ItemAgentMessage,
		Status:    protocol.StatusCompleted,
		CreatedAt: time.Now().UnixMilli(),
		Text:      text,
		Meta:      map[string]any{"toolCalls": calls, "toolOnly": true},
	}
	thread.Items = append(thread.Items, item)
	a.emitItem(protocol.EvItemCompleted, item)
}

// recordToolResult is a hook for future use; tool results are stored as items.
func (a *Agent) recordToolResult(thread *protocol.Thread, h *turnHandle, call provider.ToolCallReq, out toolOutcome) {
	_ = thread
	_ = h
	_ = call
	_ = out
}

// ---------------------------------------------------------------------------
// System prompt assembly
// ---------------------------------------------------------------------------

func (a *Agent) systemPrompt(thread *protocol.Thread, mode protocol.Mode, provID, modelID string, op protocol.Op) string {
	var names []string
	if a.kb != nil {
		if cols, err := a.kb.Collections(); err == nil {
			for _, c := range cols {
				names = append(names, fmt.Sprintf("%s（%d 文档 / %d 分块）", c.Name, c.DocCount, c.ChunkCount))
			}
		}
	}
	return buildSystemPrompt(promptContext{
		Mode:           mode,
		Workspace:      a.Workspace(),
		KnowledgeBases: opCollections(op),
		Collections:    names,
		Provider:       provID,
		Model:          modelID,
		Now:            time.Now(),
	})
}

// ---------------------------------------------------------------------------
// Event plumbing
// ---------------------------------------------------------------------------

func (a *Agent) publish(ev *protocol.Event) {
	if a.emit == nil || ev == nil {
		return
	}
	ev.Seq = a.seq.Add(1)
	a.emit(ev)
}

func (a *Agent) emitItem(kind protocol.EventKind, item *protocol.Item) {
	ev := protocol.NewEvent(kind)
	ev.ThreadID = item.ThreadID
	ev.TurnID = item.TurnID
	ev.Item = item
	a.publish(ev)
}

func (a *Agent) emitDelta(threadID, turnID, itemID, channel, text string) {
	ev := protocol.NewEvent(protocol.EvItemDelta)
	ev.ThreadID = threadID
	ev.TurnID = turnID
	ev.Delta = &protocol.Delta{ItemID: itemID, Channel: channel, Text: text}
	a.publish(ev)
}

func (a *Agent) emitError(threadID, turnID, msg string) {
	item := &protocol.Item{
		ID:        newItemID(),
		TurnID:    turnID,
		ThreadID:  threadID,
		Type:      protocol.ItemError,
		Status:    protocol.StatusFailed,
		CreatedAt: time.Now().UnixMilli(),
		Error:     msg,
		Text:      msg,
	}
	ev := protocol.NewEvent(protocol.EvStreamError)
	ev.ThreadID = threadID
	ev.TurnID = turnID
	ev.Error = msg
	ev.Item = item
	a.publish(ev)

	// An error is terminal for the turn: emit the closing event so the UI can
	// re-enable the composer.
	end := protocol.NewEvent(protocol.EvTurnAborted)
	end.ThreadID = threadID
	end.TurnID = turnID
	end.Reason = msg
	a.publish(end)
}

func (a *Agent) publishUsage(threadID, turnID string, u *protocol.Usage) {
	ev := protocol.NewEvent(protocol.EvTokenCount)
	ev.ThreadID = threadID
	ev.TurnID = turnID
	ev.Usage = u
	a.publish(ev)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newItemID() string {
	return "it_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// joinInput flattens the text parts of a submission.
func joinInput(parts []protocol.InputPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(strings.TrimSpace(p.Text))
		}
	}
	return b.String()
}

func opCollections(op protocol.Op) []string {
	if len(op.Collections) > 0 {
		return append([]string(nil), op.Collections...)
	}
	if op.Collection != "" {
		return []string{op.Collection}
	}
	return nil
}

func reasoningMetadata(providerID, modelID, effort string) map[string]string {
	if providerID == "volcengine-plan" && effort == "off" {
		switch modelID {
		case "glm-5.3", "glm-latest", "kimi-k3":
			return nil
		default:
			return map[string]string{"thinking_type": "disabled"}
		}
	}
	budgets := map[string]string{"low": "1024", "medium": "4096", "high": "8192"}
	if budget := budgets[effort]; budget != "" {
		return map[string]string{
			"thinking_budget":  budget,
			"reasoning_effort": effort,
		}
	}
	return nil
}

func lastUserItem(items []*protocol.Item) int {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == protocol.ItemUserMessage {
			return i
		}
	}
	return -1
}

func attachmentParts(attachments []protocol.Attachment) []provider.Part {
	parts := make([]provider.Part, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Path == "" {
			continue
		}
		if attachment.Kind == "image" {
			data, err := os.ReadFile(attachment.Path)
			if err != nil {
				continue
			}
			parts = append(parts, provider.Part{Type: "image", MimeType: attachment.Mime, Data: base64.StdEncoding.EncodeToString(data)})
			continue
		}
		doc, err := kb.ParseFile(attachment.Path)
		if err != nil || strings.TrimSpace(doc.Text) == "" {
			continue
		}
		parts = append(parts, provider.TextPart(fmt.Sprintf("\n\n[附件：%s]\n%s", filepath.Base(attachment.Path), doc.Text)))
	}
	return parts
}

func artifactsFromMeta(meta map[string]any) []protocol.Artifact {
	path, _ := meta["path"].(string)
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil
	}
	op := "modified"
	if created, ok := meta["created"].(bool); ok && created {
		op = "created"
	}
	return []protocol.Artifact{{
		Path: path, Name: filepath.Base(path), Size: info.Size(),
		Mime: mimeTypeForPath(path), Operation: op,
	}}
}

func mimeTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	default:
		return "text/plain"
	}
}

// friendlyProviderError turns a raw vendor error into something a user can act
// on, because "HTTP 401" alone does not tell anyone what to fix.
func friendlyProviderError(provID string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, provider.ErrNoCredentials) || strings.Contains(msg, "401") || strings.Contains(msg, "invalid_api_key"):
		return fmt.Sprintf("%s 鉴权失败：请检查「设置 → 模型」中的 API Key 是否正确、是否有余额。原始信息：%s", provID, msg)
	case strings.Contains(msg, "404"):
		return fmt.Sprintf("%s 返回 404：通常是模型名不存在或 Base URL 不对。请确认模型 id 与端点。原始信息：%s", provID, msg)
	case strings.Contains(msg, "429"):
		return fmt.Sprintf("%s 触发限流或额度不足，请稍后重试或更换模型。原始信息：%s", provID, msg)
	case strings.Contains(msg, "context length") || strings.Contains(msg, "too long"):
		return fmt.Sprintf("上下文超出模型上限：请开启新会话或减少引用内容。原始信息：%s", msg)
	}
	return msg
}

// abortReason distinguishes a user stop from a failure, which the UI shows
// differently.
func abortReason(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "用户已停止"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "执行超时"
	}
	return "任务已中止"
}

// estimateTokens approximates a token count for context budgeting.
func estimateTokens(s string) int { return kb.EstimateTokens(s) }

// estimateItem budgets one item.
func estimateItem(it *protocol.Item) int {
	n := estimateTokens(it.Text) + estimateTokens(it.Reasoning)
	if it.Tool != nil {
		n += estimateTokens(it.Tool.Result) + estimateTokens(it.Tool.Arguments)/2
	}
	if n == 0 {
		n = 8
	}
	return n
}

// estimateItems budgets a whole conversation.
func estimateItems(items []*protocol.Item) int {
	total := 0
	for _, it := range items {
		total += estimateItem(it)
	}
	return total
}

// estimateMessages budgets a reconstructed vendor conversation.
func estimateMessages(msgs []provider.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m.Text()) + 4
	}
	return total
}
