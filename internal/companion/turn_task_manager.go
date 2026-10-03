package companion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SubmitUserTurn folds one user message into a durable turn task.
func (m *Manager) SubmitUserTurn(ctx context.Context, threadID, text, providerID, model, reasoningEffort string) (TurnTask, error) {
	return m.submitUserTurnAt(ctx, threadID, text, providerID, model, reasoningEffort, time.Now())
}

func (m *Manager) submitUserTurnAt(ctx context.Context, threadID, text, providerID, model, reasoningEffort string, now time.Time) (TurnTask, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return TurnTask{}, errors.New("消息不能为空")
	}
	m.taskMu.Lock()
	defer m.taskMu.Unlock()

	active, ok, err := m.store.ActiveTurnTask(threadID)
	if err != nil {
		return TurnTask{}, err
	}
	if ok {
		switch active.Status {
		case TaskWaiting:
			active.Inputs = append(active.Inputs, TurnTaskInput{Text: text, At: now.UnixMilli()})
			active.LastMessageAt = now.UnixMilli()
			if err := m.store.SaveTurnTask(active); err != nil {
				return TurnTask{}, err
			}
			if err := m.appendUserMessage(threadID, text); err != nil {
				return TurnTask{}, err
			}
			m.emitTask(active)
			if active.NotBefore <= now.UnixMilli() {
				m.kickTurnTask(ctx, active)
			}
			return active, nil
		default:
			return TurnTask{}, errors.New("她正在回复，请稍候")
		}
	}

	profile, err := m.store.Profile(m.id)
	if err != nil {
		return TurnTask{}, err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return TurnTask{}, err
	}
	avail := evaluate(state, m.resolver(profile), now)
	task := TurnTask{
		ID:                 "task_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
		ThreadID:           threadID,
		Trigger:            TriggerUser,
		Status:             TaskWaiting,
		Provider:           providerID,
		Model:              model,
		ReasoningEffort:    reasoningEffort,
		Inputs:             []TurnTaskInput{{Text: text, At: now.UnixMilli()}},
		RequestedAt:        now.UnixMilli(),
		LastMessageAt:      now.UnixMilli(),
		NotBefore:          avail.NextFree.UnixMilli(),
		Blocked:            avail.Blocked,
	}
	// 可用时不写活动。以前这里无条件写，于是忙线卡拿到「亲密中」就演成
	// 「她正在忙，暂时没看手机」，而她手机就在手里。活动只在真被拦时才有意义。
	if avail.Blocked {
		task.CurrentActivity = avail.Activity
		task.AvailabilityReason = avail.Activity
	} else {
		task.NotBefore = now.UnixMilli()
	}
	if err := m.appendUserMessage(threadID, text); err != nil {
		return TurnTask{}, err
	}
	if err := m.store.SaveTurnTask(task); err != nil {
		return TurnTask{}, err
	}
	m.emitTask(task)
	m.kickTurnTask(ctx, task)
	return task, nil
}

// GetTask returns one durable task.
func (m *Manager) GetTask(taskID string) (TurnTask, error) {
	return m.store.TurnTask(taskID)
}

// ListTasks returns newest tasks, optionally limited to active work.
func (m *Manager) ListTasks(activeOnly bool) ([]TurnTask, error) {
	tasks, err := m.store.TurnTasks("", 100)
	if err != nil || !activeOnly {
		return tasks, err
	}
	out := tasks[:0]
	for _, task := range tasks {
		if task.Active() {
			out = append(out, task)
		}
	}
	return out, nil
}

// CancelTask marks a waiting or resolving task as interrupted.
func (m *Manager) CancelTask(taskID string) error {
	task, err := m.store.TurnTask(taskID)
	if err != nil {
		return err
	}
	if !task.Active() {
		return nil
	}
	task.Status = TaskInterrupted
	task.Error = "用户取消了本轮任务"
	task.CompletedAt = time.Now().UnixMilli()
	if err := m.store.SaveTurnTask(task); err != nil {
		return err
	}
	m.emitTask(task)
	return nil
}

// ForceReply 让用户把她从日程里拽出来立刻回消息。
func (m *Manager) ForceReply(taskID string) error {
	return m.forceReplyAt(taskID, time.Now())
}

func (m *Manager) forceReplyAt(taskID string, at time.Time) error {
	task, err := m.store.TurnTask(taskID)
	if err != nil {
		return err
	}
	if task.Status != TaskWaiting {
		return nil
	}
	// 前端禁用按钮只是体验，后端必须自己守住，否则绕过 UI 的调用能把人从睡眠里
	// 拽出来。
	profile, err := m.store.Profile(m.id)
	if err != nil {
		return err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return err
	}
	if consciousness, wakeAt := m.consciousnessNow(profile, state, at); consciousness == ConsciousAsleep {
		return fmt.Errorf("%s正在睡觉，预计 %s 醒，叫不醒", profile.Name, wakeAt.Format("15:04"))
	}
	task.Forced = true
	// 用调用方给的时刻，不用 time.Now()。检查睡没睡读的是 at，写回却写真实时钟，
	// 两个时钟不一致时强制回复会落到错误的一刻。
	task.NotBefore = at.UnixMilli()
	task.AvailabilityReason = ""
	task.Blocked = false
	if err := m.store.SaveTurnTask(task); err != nil {
		return err
	}
	m.emitTask(task)
	m.kickTurnTask(context.Background(), task)
	return nil
}

// SubmitProactiveTurn creates the same task type used by user replies.
func (m *Manager) SubmitProactiveTurn(ctx context.Context) (TurnTask, bool, error) {
	return m.submitProactiveTurnAt(ctx, time.Now())
}

// SubmitDirective writes one director instruction into her world. It is not a
// message to her: nothing is appended to the conversation as a user turn, and the
// task it starts is a world edit rather than a request for a reply.
func (m *Manager) SubmitDirective(
	ctx context.Context, threadID, text, providerID, model, reasoningEffort string,
) (DirectorDirective, error) {
	return m.submitDirectiveAt(ctx, threadID, text, providerID, model, reasoningEffort, time.Now())
}

func (m *Manager) submitDirectiveAt(
	ctx context.Context, threadID, text, providerID, model, reasoningEffort string, now time.Time,
) (DirectorDirective, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return DirectorDirective{}, errDirectiveEmpty
	}
	if strings.TrimSpace(threadID) == "" {
		resolved, err := m.ensureThread(ctx)
		if err != nil {
			return DirectorDirective{}, err
		}
		threadID = resolved
	}
	m.taskMu.Lock()
	defer m.taskMu.Unlock()

	// One intervention at a time. Two world edits landing on the same day inside
	// one minute is how a plan stops being traceable to the instruction that made
	// it, and the ledger is the whole point of this mode.
	if _, active, err := m.store.ActiveTurnTask(threadID); err != nil {
		return DirectorDirective{}, err
	} else if active {
		return DirectorDirective{}, errors.New("她手上那件事还没处理完，稍后再写入世界")
	}

	profile, err := m.store.Profile(m.id)
	if err != nil {
		return DirectorDirective{}, err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return DirectorDirective{}, err
	}
	// The asleep answer is captured here so the ledger records what was known
	// when the words arrived. The apply step re-reads it, because a turn may sit
	// for a moment before the model gets to it.
	consciousness, _ := m.consciousnessNow(profile, state, now)

	taskID := newTurnTaskID()
	directiveID := newDirectiveID()
	directive := DirectorDirective{
		ID: directiveID, CharacterID: m.id, ThreadID: threadID, Text: text,
		Status: DirectivePending, TaskID: taskID, CreatedAt: now.UnixMilli(),
	}
	// The raw words are written before anything can fail. An instruction that
	// never made it through the model still shows up in the ledger as rejected.
	if err := m.store.SaveDirective(m.id, directive); err != nil {
		return directive, err
	}
	task := TurnTask{
		ID:              taskID,
		ThreadID:        threadID,
		Trigger:         TriggerDirector,
		Status:          TaskWaiting,
		Provider:        providerID,
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Inputs:          []TurnTaskInput{{Text: text, At: now.UnixMilli()}},
		RequestedAt:     now.UnixMilli(),
		LastMessageAt:   now.UnixMilli(),
		NotBefore:       now.UnixMilli(),
		DirectiveID:     directiveID,
		CurrentActivity: state.CurrentActivity,
	}
	if consciousness == ConsciousAsleep {
		task.AvailabilityReason = "她在睡觉"
	}
	if err := m.store.SaveTurnTask(task); err != nil {
		return directive, err
	}
	m.emitTask(task)
	m.kickTurnTask(ctx, task)
	return directive, nil
}

func newTurnTaskID() string {
	return "task_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func (m *Manager) submitProactiveTurnAt(ctx context.Context, now time.Time) (TurnTask, bool, error) {
	threadID, err := m.ensureThread(ctx)
	if err != nil || threadID == "" {
		return TurnTask{}, false, err
	}
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	_, ok, err := m.store.ActiveTurnTask(threadID)
	if err != nil || ok {
		return TurnTask{}, false, err
	}
	profile, err := m.store.Profile(m.id)
	if err != nil {
		return TurnTask{}, false, err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return TurnTask{}, false, err
	}
	// 主动消息第一关就是可达性。不可达直接不建任务，连退避都不必问。
	avail := evaluate(state, m.resolver(profile), now)
	if avail.Blocked {
		return TurnTask{}, false, nil
	}
	// Backoff runs before a task is ever created. Unanswered messages close this
	// door rather than opening it wider.
	if allowed, _ := proactiveBackoff(state.UnrepliedProactive, state.LastProactiveAt, now); !allowed {
		return TurnTask{}, false, nil
	}
	task := TurnTask{
		ID:              "task_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
		ThreadID:        threadID,
		Trigger:         TriggerProactive,
		Status:          TaskWaiting,
		RequestedAt:     now.UnixMilli(),
		LastMessageAt:   now.UnixMilli(),
		NotBefore:       now.UnixMilli(),
	}
	if err := m.store.SaveTurnTask(task); err != nil {
		return TurnTask{}, false, err
	}
	m.emitTask(task)
	m.kickTurnTask(ctx, task)
	return task, true, nil
}

func (m *Manager) appendUserMessage(threadID, text string) error {
	m.mu.Lock()
	conversation := m.conversation
	m.mu.Unlock()
	if conversation == nil {
		return errors.New("陪伴会话尚未初始化")
	}
	return conversation.AppendUserMessage(threadID, text)
}

func (m *Manager) kickTurnTask(ctx context.Context, task TurnTask) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		if task.NotBefore > time.Now().UnixMilli() {
			return
		}
		m.processTurnTask(ctx, task.ID)
	}()
}

func (m *Manager) recoverTurnTasks(ctx context.Context) {
	tasks, err := m.store.TurnTasks("", 200)
	if err != nil {
		return
	}
	now := time.Now()
	for _, task := range tasks {
		switch task.Status {
		case TaskAligning, TaskResolved, TaskGenerating:
			if replayed, ok := m.replayAfterCrash(task, now); ok {
				m.kickTurnTask(ctx, replayed)
				continue
			}
			task.Status = TaskInterrupted
			task.Error = "应用在生成过程中退出，本轮未自动重放"
			task.CompletedAt = now.UnixMilli()
			_ = m.store.SaveTurnTask(task)
			m.emitTask(task)
		case TaskWaiting:
			m.kickTurnTask(ctx, task)
		}
	}
}

const (
	// maxTurnRecoveries bounds replay so a crash loop cannot keep reviving one
	// turn and eventually emit the same answer several times.
	maxTurnRecoveries = 2
	// replayWindow is how recent a dead turn has to be to be worth answering. A
	// question from two days ago should not suddenly get its reply on a restart.
	replayWindow = 6 * time.Hour
)

// replayAfterCrash resets a turn whose answer never reached anyone back to
// waiting and reports whether it did. It persists but does not schedule: the
// caller owns kicking, so the decision stays testable without a goroutine
// racing the assertions.
//
// A user who asked something and got nothing deserves the answer, so user turns
// come back. A proactive impulse that died in the crash is stale: replaying it
// would push a "just saw this" message about a moment that already passed, so
// those stay retired. A director edit replays only while its ledger row is still
// pending, which is exactly what a crash leaves behind -- anything already
// settled must not be run a second time.
func (m *Manager) replayAfterCrash(task TurnTask, now time.Time) (TurnTask, bool) {
	switch task.Trigger {
	case TriggerUser:
	case TriggerDirector:
		directive, err := m.store.Directive(m.id, task.DirectiveID)
		if err != nil || directive.Status != DirectivePending {
			return task, false
		}
	default:
		return task, false
	}
	if task.Recoveries >= maxTurnRecoveries {
		return task, false
	}
	if task.RequestedAt < now.Add(-replayWindow).UnixMilli() {
		return task, false
	}
	task.Status = TaskWaiting
	task.Recoveries++
	task.Error = ""
	task.StartedAt = 0
	task.NotBefore = now.UnixMilli()
	if err := m.store.SaveTurnTask(task); err != nil {
		return task, false
	}
	m.emitTask(task)
	return task, true
}

func (m *Manager) processDueTurnTasks(ctx context.Context, now time.Time) {
	tasks, err := m.store.TurnTasks("", 200)
	if err != nil {
		return
	}
	profile, profileErr := m.store.Profile(m.id)
	state, stateErr := m.store.State(m.id)
	for _, task := range tasks {
		if task.Status != TaskWaiting {
			continue
		}
		if task.NotBefore > now.UnixMilli() && profileErr == nil && stateErr == nil {
			// 等待途中场景可能才起来，或者日程刚好空出来，所以每分钟重判一次，
			// 别让人守着一个早已过期的 notBefore 干等。但提前放行只认这两种：
			// 场景把她拉回了对话，或者眼下这格本来就轻。用完整的日程判断会把
			// 已经说出口的时间往回吞 —— 她讲了 17 点，结果 12 点一进那格
			// 还没开始的午饭就被放出来了。
			if released, activity := earlyRelease(state, m.resolver(profile), now); released {
				task.NotBefore = now.UnixMilli()
				task.CurrentActivity = activity
				task.AvailabilityReason = ""
				task.Blocked = false
				if err := m.store.SaveTurnTask(task); err != nil {
					continue
				}
			}
		}
		if task.NotBefore <= now.UnixMilli() {
			m.kickTurnTask(ctx, task)
		}
	}
}

func (m *Manager) processTurnTask(ctx context.Context, taskID string) {
	m.processTurnTaskAt(ctx, taskID, time.Now())
}

func (m *Manager) processTurnTaskAt(ctx context.Context, taskID string, now time.Time) {
	task, ok := m.claimTurnTask(taskID, now)
	if !ok {
		return
	}
	m.runTurnTask(ctx, task, now)
}

// claimTurnTask moves a waiting task into aligning and does nothing else. That
// transition is the only place two paths can race over one thread, so the lock
// covers the read and the write and then lets go.
//
// The slow work used to live inside this same lock: a catch-up tick over as much as
// seven days, a weather call out to the network, and a model round trip with a 60
// second timeout that retries once. Submitting a message needs this lock too, so
// pressing Enter just sat there until she happened to finish.
func (m *Manager) claimTurnTask(taskID string, now time.Time) (TurnTask, bool) {
	m.taskMu.Lock()
	defer m.taskMu.Unlock()

	task, err := m.store.TurnTask(taskID)
	if err != nil || task.Status != TaskWaiting {
		return TurnTask{}, false
	}
	if task.NotBefore > now.UnixMilli() {
		return TurnTask{}, false
	}
	task.Status = TaskAligning
	task.StartedAt = now.UnixMilli()
	task.Error = ""
	if err := m.store.SaveTurnTask(task); err != nil {
		return TurnTask{}, false
	}
	m.emitTask(task)
	return task, true
}

func (m *Manager) runTurnTask(ctx context.Context, task TurnTask, now time.Time) {
	m.tickMu.Lock()
	err := m.advance(ctx, now)
	m.tickMu.Unlock()
	if err != nil {
		m.failTurnTask(task, err)
		return
	}

	state, err := m.store.State(m.id)
	if err != nil {
		m.failTurnTask(task, err)
		return
	}
	sceneChanged := advanceSceneByWallClock(&state, now)
	if sceneChanged {
		state.UpdatedAt = now.UnixMilli()
		_ = m.store.SaveState(m.id, state)
	}

	profile, err := m.store.Profile(m.id)
	if err != nil {
		m.failTurnTask(task, err)
		return
	}
	// 复用上面已经按真实时间衰减过的那份 state。
	avail := evaluate(state, m.resolver(profile), now)
	available, activity, nextAt := avail.admitNew(), avail.Activity, avail.NextFree.UnixMilli()
	consciousness, _ := m.consciousnessNow(profile, state, now)
	asleep := consciousness == ConsciousAsleep
	// 用户按过「让她现在回」就不再拿日程把她塞回等待。导演指令也不走这一关：
	// 它是在改她的世界，不是排队等她有空回话。这里绝不能复用 Forced ——
	// Forced 的语义是「把她叫回来立刻回」，那等于用导演模式把人叫醒。
	if avail.Blocked && !task.Forced && task.Trigger != TriggerDirector {
		task.Status = TaskWaiting
		task.NotBefore = nextAt
		task.CurrentActivity = activity
		task.AvailabilityReason = activity
		task.Blocked = true
		task.StartedAt = 0
		_ = m.store.SaveTurnTask(task)
		m.emitTask(task)
		return
	}
	// 走到这里她要开口了，忙线标记必须清掉，否则卡会一直挂在流上。
	task.Blocked = false
	task.AvailabilityReason = ""
	events, _ := m.store.RecentEvents(now.Add(-72*time.Hour).UnixMilli(), 8)
	thoughts, _ := m.store.ActiveThoughts(6)
	memories, _ := m.store.Memories(12)
	// A trip that covers today moves the weather she talks about with her.
	weather, _ := m.weather.Get(ctx, m.effectiveCity(profile, now))
	recent := m.recentMessages(task.ThreadID, 8)

	m.mu.Lock()
	model := m.model
	m.mu.Unlock()
	var brief TurnBrief
	if model == nil || !model.Available() {
		err = errors.New("尚未配置可用的陪伴模型")
	} else {
		alignCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		brief, err = model.AlignTurn(alignCtx, TurnAlignmentInput{
			Profile: profile, State: state, Derived: m.engine.Derived(state),
			Clock: worldClock(now), Task: task, Recent: recent, Events: events,
			Thoughts: thoughts, Memories: memories, Weather: weather,
		})
		cancel()
		if err != nil {
			alignCtx, cancel = context.WithTimeout(ctx, 60*time.Second)
			brief, err = model.AlignTurn(alignCtx, TurnAlignmentInput{
				Profile: profile, State: state, Derived: m.engine.Derived(state),
				Clock: worldClock(now), Task: task, Recent: recent, Events: events,
				Thoughts: thoughts, Memories: memories, Weather: weather,
			})
			cancel()
		}
	}
	if err != nil {
		brief = conservativeTurnBrief(state, now, activity, activity, available, nextAt, err.Error())
		if task.Trigger == TriggerProactive {
			brief.Reply.Send = false
		}
	}
	brief = decorateTurnBrief(brief, state, now, activity, available, nextAt, sceneChanged)
	brief = validateTurnBrief(brief, state, now, avail.Shape)
	if task.Trigger == TriggerUser {
		brief.Reply.Send = true
	}
	if task.Trigger == TriggerDirector {
		// 背景安排不该打扰人。只有这事值得她此刻开口，她才开口；
		// 她睡着的时候一律不开口，导演改不了她的意识状态。
		brief.Reply.Send = brief.Resolution.World != nil &&
			brief.Resolution.World.ShouldReply != nil && *brief.Resolution.World.ShouldReply
		if asleep {
			brief.Reply.Send = false
		}
	}

	task.Brief = &brief
	resolved, err := m.applyTurnBrief(&task, brief, now)
	if err != nil {
		m.failTurnTask(task, err)
		return
	}
	task.Resolution = &resolved
	task.Status = TaskResolved
	task.ResolvedAt = now.UnixMilli()
	task.Applied = true
	if err := m.store.SaveTurnTask(task); err != nil {
		m.failTurnTask(task, err)
		return
	}
	m.emitTask(task)
	if (task.Trigger == TriggerProactive || task.Trigger == TriggerDirector) && !brief.Reply.Send {
		task.Status = TaskCompleted
		task.CompletedAt = time.Now().UnixMilli()
		_ = m.store.SaveTurnTask(task)
		m.emitTask(task)
		return
	}
	if err := m.appendAlignment(task); err != nil {
		m.failTurnTask(task, err)
		return
	}

	// The slow stretch is no longer under the lock, so a cancel that landed while we
	// were away has to be noticed before we push a reply into the conversation.
	if current, err := m.store.TurnTask(task.ID); err == nil && current.Status != TaskResolved {
		return
	}
	task.Status = TaskGenerating
	if err := m.store.SaveTurnTask(task); err != nil {
		m.failTurnTask(task, err)
		return
	}
	m.emitTask(task)
	m.mu.Lock()
	conversation := m.conversation
	m.mu.Unlock()
	if conversation == nil {
		m.failTurnTask(task, errors.New("陪伴会话尚未初始化"))
		return
	}
	if err := conversation.StartTurnTask(task, profile); err != nil {
		m.failTurnTask(task, err)
	}
}

func (m *Manager) applyTurnBrief(task *TurnTask, brief TurnBrief, now time.Time) (TurnResolution, error) {
	if task.Applied && task.Resolution != nil {
		return *task.Resolution, nil
	}
	m.conversationMu.Lock()
	defer m.conversationMu.Unlock()
	state, err := m.store.State(m.id)
	if err != nil {
		return TurnResolution{}, err
	}
	profile, err := m.store.Profile(m.id)
	if err != nil {
		return TurnResolution{}, err
	}
	// 睡着的时候不能给她立一个场景。settleConsciousness 会在下一个 tick 把它清掉，
	// 但只守那一侧不够：对齐模型完全可能在她睡着的这一轮里回一个 active 场景，
	// 于是 evaluate 读到「场景活着」就放行，而 CanForceReply 读意识说「不行」。
	// I4 和 I6 要在源头就一致，不能靠可用性那边排序兜。
	consciousness, wakeAt := m.consciousnessNow(profile, state, now)
	asleep := consciousness == ConsciousAsleep
	if asleep {
		brief.Resolution.SceneStage = "none"
		brief.Resolution.ConsentState = "not_applicable"
	}
	effects, _ := m.store.ActiveEffects()
	outcome := ConversationOutcome{
		SceneStage: brief.Resolution.SceneStage, ConsentState: brief.Resolution.ConsentState,
		Impulses: brief.Resolution.Impulses, TraitChanges: brief.Resolution.TraitChanges,
		Effects: brief.Resolution.Effects,
	}
	effects = mergeEffects(effects, m.engine.ApplyConversationOutcome(
		&state,
		outcome,
		now,
		task.Trigger == TriggerUser,
	))
	// 只有他真的出现了才算「上次联系」。导演指令不算，她自己的主动消息同样
	// 不算 —— relationship.absenceHours 就是读这个字段的，她一开口就把这个钟
	// 拨回零，等于替他答了「我在」：思念和孤独再也长不起来，下一轮对齐还会
	// 拿到「自然延续上次话题，不要说好久不见」。她主动找他而他没有回，恰恰是
	// absence 该继续涨的那种情况。
	if task.Trigger == TriggerUser {
		state.LastUserAt = task.LastMessageAt
	}
	// Only he himself clears the unanswered count. A proactive turn of her own
	// must not wipe the very number that is supposed to hold her back, and a
	// director edit is not someone showing up either.
	if task.Trigger == TriggerUser {
		state.UnrepliedProactive = 0
	}
	state.UpdatedAt = now.UnixMilli()
	resolution := TurnResolution{
		AppliedAt: now.UnixMilli(), ActionMinutes: brief.Resolution.ActionMinutes,
		MetricDeltas: brief.Resolution.Impulses, TraitChanges: brief.Resolution.TraitChanges,
	}
	var extraEvents []LifeEvent
	for _, proposed := range brief.Resolution.Events {
		content := strings.TrimSpace(proposed.Content)
		if content == "" {
			continue
		}
		importance := int(proposed.Importance + 0.5)
		if importance < 1 || importance > 10 {
			importance = 5
		}
		event := LifeEvent{
			ID:         "evt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
			Type:       firstNonEmpty(proposed.Type, "conversation"),
			Content:    content,
			Deltas:     proposed.Deltas,
			Importance: importance,
			Shareable:  proposed.Shareable,
			OccurredAt: now.UnixMilli(),
		}
		extraEvents = append(extraEvents, event)
		resolution.EventIDs = append(resolution.EventIDs, event.ID)
	}
	var memories []Memory
	for i, candidate := range brief.Resolution.Memories {
		if i >= 3 || strings.TrimSpace(candidate.Content) == "" {
			continue
		}
		kind := candidate.Kind
		switch kind {
		case "user_fact", "relationship", "life_event", "promise":
		default:
			kind = "user_fact"
		}
		importance := int(candidate.Importance + 0.5)
		if importance < 1 || importance > 10 {
			importance = 5
		}
		memory := Memory{
			ID:         "mem_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
			Kind:       kind,
			Content:    strings.TrimSpace(candidate.Content),
			Importance: importance,
			Source:     "turn_task:" + task.ID,
			Strength:   float64(importance),
			CreatedAt:  now.UnixMilli(),
			UpdatedAt:  now.UnixMilli(),
		}
		memories = append(memories, memory)
		resolution.MemoryIDs = append(resolution.MemoryIDs, memory.ID)
	}
	var thought *PendingThought
	if len(brief.Resolution.Thoughts) > 0 {
		content := strings.TrimSpace(brief.Resolution.Thoughts[0])
		if content != "" {
			thought = &PendingThought{
				ID:      "thought_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
				Content: content, Priority: 5, Status: "active", CreatedAt: now.UnixMilli(),
			}
			resolution.ThoughtIDs = append(resolution.ThoughtIDs, thought.ID)
		}
	}
	// Plans the user actually voiced become dated commitments in the same
	// transaction as the turn that produced them, so the promise and the calendar
	// cannot drift apart. Whether she keeps it is still the forces' decision.
	var commitments []Commitment
	if len(brief.Resolution.Commitments) > 0 {
		held, _ := m.store.Commitments(m.id)
		commitments = NormalizeTurnCommitments(brief.Resolution.Commitments, held, now)
		for _, commitment := range commitments {
			resolution.CommitmentIDs = append(resolution.CommitmentIDs, commitment.ID)
		}
	}
	// A director turn edits her world through the primitives the simulation
	// already owns, and its ledger entry lands in this same transaction so the
	// record of an intervention can never drift from the intervention.
	var directive *DirectorDirective
	if task.Trigger == TriggerDirector {
		draft := WorldDraft{}
		if brief.Resolution.World != nil {
			draft = *brief.Resolution.World
		}
		directiveID := task.DirectiveID
		if directiveID == "" {
			directiveID = newDirectiveID()
		}
		compiled := compileDirective(directiveInput{
			CharacterID: m.id, ThreadID: task.ThreadID, TaskID: task.ID,
			DirectiveID: directiveID, Text: task.InputText(), Draft: draft,
			Asleep:  asleep,
			WakeAt:  wakeAt,
			Now:     now,
			Metrics: m.knownMetrics(),
		})
		settled, directedCommitments, directedEffects, directedEvents := m.applyDirectivePlan(compiled, now)
		directive = &settled
		commitments = append(commitments, directedCommitments...)
		effects = mergeEffects(effects, directedEffects)
		extraEvents = append(extraEvents, directedEvents...)
		resolution.DirectiveID = settled.ID
		resolution.Narration = settled.Narration
		for _, commitment := range directedCommitments {
			resolution.CommitmentIDs = append(resolution.CommitmentIDs, commitment.ID)
		}
	}
	resolution.GenerationFacts = generationFacts(brief, resolution)
	task.Status = TaskResolved
	task.Applied = true
	task.Resolution = &resolution
	if err := m.store.CommitTick(TickCommit{ID: m.id,
		State: state, Effects: effects, Events: extraEvents, Memories: memories,
		Thought: thought, Task: task, Commitments: commitments, Directive: directive,
	}); err != nil {
		return TurnResolution{}, err
	}
	// The turn's own hours go into the day, so a long scene really does push her
	// afternoon around instead of leaving the schedule untouched.
	m.scheduleTurnDrift(now, resolution.ActionMinutes)
	return resolution, nil
}

func (m *Manager) appendAlignment(task TurnTask) error {
	m.mu.Lock()
	conversation := m.conversation
	m.mu.Unlock()
	if conversation == nil {
		return errors.New("陪伴会话尚未初始化")
	}
	return conversation.AppendTurnTask(task.ThreadID, task)
}

func (m *Manager) recentMessages(threadID string, limit int) []ConversationMessage {
	m.mu.Lock()
	conversation := m.conversation
	m.mu.Unlock()
	if conversation == nil {
		return nil
	}
	return conversation.RecentMessages(threadID, limit)
}

func (m *Manager) failTurnTask(task TurnTask, err error) {
	task.Status = TaskFailed
	task.Error = err.Error()
	task.CompletedAt = time.Now().UnixMilli()
	_ = m.store.SaveTurnTask(task)
	// Settle the ledger before announcing the failure: the UI re-lists
	// directives when it hears about a failed turn, and it should not be able to
	// read a row that is still pending.
	if task.Trigger == TriggerDirector && task.DirectiveID != "" {
		m.markDirectiveFailed(task.DirectiveID, err.Error())
	}
	m.emitTask(task)
}

func (m *Manager) emitTask(task TurnTask) {
	overview, err := m.overviewUnchecked(context.Background())
	if err != nil {
		overview = Overview{}
	}
	overview.ActiveTask = taskViewPointer(task.View())
	m.emitEvent(EventTurnTaskUpdated, overview)
}

func taskViewPointer(view TurnTaskView) *TurnTaskView {
	return &view
}

func (m *Manager) PrepareCompanionTurn(ctx context.Context, threadID, turnID, taskID, userText string) (string, error) {
	_ = ctx
	task, err := m.store.TurnTask(taskID)
	if err != nil {
		return "", err
	}
	if task.ThreadID != threadID {
		return "", errors.New("陪伴任务与会话不匹配")
	}
	if task.Brief == nil || task.Resolution == nil {
		return "", errors.New("陪伴任务尚未完成细节对齐")
	}
	profile, _ := m.store.Profile(m.id)
	state, _ := m.store.State(m.id)
	return formatTurnBriefContext(profile, state, *task.Brief, *task.Resolution, task.Trigger, userText), nil
}

func (m *Manager) CompleteCompanionTurn(threadID, turnID, taskID, userText, assistantText string) {
	_ = threadID
	_ = turnID
	_ = userText
	task, err := m.store.TurnTask(taskID)
	if err != nil {
		return
	}
	task.Status = TaskCompleted
	task.CompletedAt = time.Now().UnixMilli()
	task.MessageItemID = "assistant"
	_ = m.store.SaveTurnTask(task)
	if task.Trigger == TriggerProactive {
		// Atomic: a tick landing between the read and the write would swallow
		// the count, and the backoff rule is only as good as that count.
		_, _ = m.store.RecordProactiveSent(m.id, task.CompletedAt)
		_ = m.store.MarkThoughtsShared(task.CompletedAt)
		_, _ = m.store.AddUnread(1)
	}
	m.emitTask(task)
	m.extractMemoriesAfterTurn(task, userText, assistantText)
}

func (m *Manager) FailCompanionTurn(threadID, turnID, taskID, message string) {
	_ = threadID
	_ = turnID
	task, err := m.store.TurnTask(taskID)
	if err != nil {
		return
	}
	m.failTurnTask(task, errors.New(message))
}

func (m *Manager) extractMemoriesAfterTurn(task TurnTask, userText, assistantText string) {
	m.mu.Lock()
	model := m.model
	if m.closed || model == nil || !model.Available() || strings.TrimSpace(assistantText) == "" {
		m.mu.Unlock()
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		profile, err := m.store.Profile(m.id)
		if err != nil {
			return
		}
		existing, _ := m.store.Memories(80)
		candidates, err := model.ExtractMemories(ctx, MemoryInput{
			Profile: profile, UserText: userText, AssistantText: assistantText, Existing: existing,
		})
		if err == nil {
			m.saveMemoryCandidates(candidates, existing)
		}
	}()
}

func validateTurnBrief(brief TurnBrief, state State, now time.Time, shape ReplyShape) TurnBrief {
	if brief.Clock.Now <= 0 {
		brief.Clock.Now = now.UnixMilli()
	}
	if brief.Clock.LocalTime == "" {
		brief.Clock.LocalTime = now.Format("15:04")
	}
	if brief.Clock.Now > now.UnixMilli()+time.Minute.Milliseconds() {
		brief.Clock.Now = now.UnixMilli()
	}
	if !validSceneStages[brief.Resolution.SceneStage] {
		brief.Resolution.SceneStage = state.SceneStage
	}
	if !validConsentStates[brief.Resolution.ConsentState] {
		brief.Resolution.ConsentState = state.ConsentState
	}
	if brief.Scene.Stage == "" || !validSceneStages[brief.Scene.Stage] {
		brief.Scene.Stage = brief.Resolution.SceneStage
	}
	if brief.Scene.ConsentState == "" || !validConsentStates[brief.Scene.ConsentState] {
		brief.Scene.ConsentState = brief.Resolution.ConsentState
	}
	if brief.Reply.MaxChars <= 0 {
		brief.Reply.MaxChars = 800
	}
	if brief.Reply.MaxChars < 80 {
		brief.Reply.MaxChars = 80
	}
	if brief.Reply.MaxChars > 2000 {
		brief.Reply.MaxChars = 2000
	}
	// 身体状态给回复长度封顶。只做上界不做替代：她想更短随时可以，更长不行。
	// 场景正酣时憋出六百字长文比晚回更出戏，这里是唯一拦住它的地方。
	brief.Reply.MaxChars = clampShape(shape, brief.Reply.MaxChars)
	if brief.Confidence < 0 {
		brief.Confidence = 0
	}
	if brief.Confidence > 1 {
		brief.Confidence = 1
	}
	if strings.TrimSpace(brief.Reply.Goal) == "" {
		brief.Reply.Goal = "自然延续对话"
	}
	if strings.TrimSpace(brief.Reply.Tone) == "" {
		brief.Reply.Tone = "自然"
	}
	switch brief.Reply.Delivery {
	case "both", "action_only", "speech_only", "either":
	default:
		brief.Reply.Delivery = "either"
	}
	return brief
}

func decorateTurnBrief(
	brief TurnBrief,
	state State,
	now time.Time,
	activity string,
	available bool,
	nextAt int64,
	sceneChanged bool,
) TurnBrief {
	elapsedMinutes := 0
	absenceHours := 0
	if state.LastUserAt > 0 && now.UnixMilli() > state.LastUserAt {
		elapsed := now.Sub(time.UnixMilli(state.LastUserAt))
		elapsedMinutes = int(elapsed.Minutes())
		absenceHours = int(elapsed.Hours())
	}
	daysCrossed := 0
	if state.LastUserAt > 0 {
		last := time.UnixMilli(state.LastUserAt)
		daysCrossed = int(dateOnly(now).Sub(dateOnly(last)).Hours() / 24)
		if daysCrossed < 0 {
			daysCrossed = 0
		}
	}
	brief.Clock.Now = now.UnixMilli()
	brief.Clock.LocalTime = now.Format("15:04")
	brief.Clock.ElapsedMinutes = elapsedMinutes
	brief.Clock.AbsenceHours = absenceHours
	brief.Clock.DaysCrossed = daysCrossed
	brief.Clock.Activity = activity
	brief.Clock.Available = available
	brief.Clock.NextAvailableAt = nextAt
	brief.Scene.StartedAt = state.SceneStartedAt
	brief.Scene.DurationMinutes = sceneDurationMinutes(state, now)
	brief.Scene.AutoAdvancedByClock = sceneChanged
	return brief
}

func dateOnly(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
}

func generationFacts(brief TurnBrief, resolution TurnResolution) string {
	var b strings.Builder
	fmt.Fprintf(&b, "本轮真实耗时约%d分钟。", resolution.ActionMinutes)
	if brief.Clock.ElapsedMinutes > 0 {
		fmt.Fprintf(&b, "距上一轮真实经过%d分钟。", brief.Clock.ElapsedMinutes)
	}
	if brief.Scene.TimeConstraintNote != "" {
		b.WriteString(brief.Scene.TimeConstraintNote)
	}
	if len(resolution.EventIDs) > 0 {
		fmt.Fprintf(&b, "本轮结算了%d个事件。", len(resolution.EventIDs))
	}
	if len(resolution.CommitmentIDs) > 0 {
		fmt.Fprintf(&b, "你记下了%d个新安排，那天会真的出现在你的日程里。", len(resolution.CommitmentIDs))
	}
	return b.String()
}

func formatTurnBriefContext(
	profile Profile,
	state State,
	brief TurnBrief,
	resolution TurnResolution,
	trigger TurnTaskTrigger,
	userText string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【本轮任务】\n陪伴者：%s。当前活动：%s。真实时间：%s，%s。\n",
		profile.Name, firstNonEmpty(brief.Clock.Activity, state.CurrentActivity), brief.Clock.LocalTime, brief.Clock.AvailabilityNote)
	fmt.Fprintf(&b, "场景：%s；同意：%s；真实持续：%d分钟。\n",
		brief.Scene.Stage, brief.Scene.ConsentState, brief.Scene.DurationMinutes)
	fmt.Fprintf(&b, "用户意图：%s\n", brief.Input.Intent)
	if len(brief.Input.ExplicitRequests) > 0 {
		fmt.Fprintf(&b, "明确请求：%s\n", strings.Join(brief.Input.ExplicitRequests, "；"))
	}
	if len(brief.Input.Boundaries) > 0 {
		fmt.Fprintf(&b, "边界：%s\n", strings.Join(brief.Input.Boundaries, "；"))
	}
	fmt.Fprintf(&b, "回复目标：%s\n语气：%s\n", brief.Reply.Goal, brief.Reply.Tone)
	fmt.Fprintf(&b, "允许的交付形式：%s\n", firstNonEmpty(brief.Reply.Delivery, "either"))
	if len(brief.Reply.Beats) > 0 {
		fmt.Fprintf(&b, "回复节拍：%s\n", strings.Join(brief.Reply.Beats, "；"))
	}
	if len(brief.Reply.MustInclude) > 0 {
		fmt.Fprintf(&b, "必须包含：%s\n", strings.Join(brief.Reply.MustInclude, "；"))
	}
	if len(brief.Reply.MustNotInclude) > 0 {
		fmt.Fprintf(&b, "禁止偏离：%s\n", strings.Join(brief.Reply.MustNotInclude, "；"))
	}
	if resolution.GenerationFacts != "" {
		fmt.Fprintf(&b, "已结算事实：%s\n", resolution.GenerationFacts)
	}
	if trigger == TriggerDirector {
		// 旁白不是「对方对你说的话」。她世界里下了场雨不等于有人陪着她，
		// 走用户消息那条语义会把思念和孤独算成有人在场，那是假的。
		fmt.Fprintf(&b, "【世界旁白】\n%s\n",
			firstNonEmpty(resolution.Narration, "她的世界里刚发生了一点变化。"))
		b.WriteString("这不是对方对你说的话，而是你世界里客观发生的事。不要当成有人对你提要求，也不要说「你让我」「系统」「被安排」这类暴露幕后的说法。\n")
	} else {
		fmt.Fprintf(&b, "最新用户消息：%s\n", userText)
	}
	b.WriteString(`
输出格式必须严格使用以下标签：
<action>动作描写</action>
<speech>说出口的台词</speech>
可以有多个 action。action 只写可观察行为和旁白，不写引号台词；speech 只写真正说出口的话，不写动作。
动作和台词至少一个非空。不要输出标签之外的说明、Markdown、JSON 或内部字段。
`)
	b.WriteString("只叙述已经结算的事实。不得自行新增地点、人物、事件、承诺或身体动作。")
	return b.String()
}
