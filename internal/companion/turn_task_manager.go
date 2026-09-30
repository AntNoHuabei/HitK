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
	available, activity, notBefore := taskAvailability(profile, state, now)
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
		NotBefore:          notBefore,
		CurrentActivity:    activity,
		AvailabilityReason: activity,
	}
	if available {
		task.NotBefore = now.UnixMilli()
		task.AvailabilityReason = ""
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

// SubmitProactiveTurn creates the same task type used by user replies.
func (m *Manager) SubmitProactiveTurn(ctx context.Context) (TurnTask, bool, error) {
	return m.submitProactiveTurnAt(ctx, time.Now())
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
	available, activity, notBefore := taskAvailability(profile, state, now)
	if !available {
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
		CurrentActivity: activity,
	}
	_ = notBefore
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
		// 等待途中场景可能才起来，或者日程刚好空出来。每分钟重判一次并往前挪，
		// 别让人守着一个早已过期的 notBefore 干等。
		if task.NotBefore > now.UnixMilli() && profileErr == nil && stateErr == nil {
			if available, activity, _ := taskAvailability(profile, state, now); available {
				task.NotBefore = now.UnixMilli()
				task.CurrentActivity = activity
				task.AvailabilityReason = ""
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
	m.taskMu.Lock()
	defer m.taskMu.Unlock()

	task, err := m.store.TurnTask(taskID)
	if err != nil || task.Status != TaskWaiting {
		return
	}
	if task.NotBefore > now.UnixMilli() {
		return
	}
	task.Status = TaskAligning
	task.StartedAt = now.UnixMilli()
	task.Error = ""
	if err := m.store.SaveTurnTask(task); err != nil {
		return
	}
	m.emitTask(task)

	m.tickMu.Lock()
	err = m.advance(ctx, now)
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
	available, activity, nextAt := taskAvailability(profile, state, now)
	if !available {
		task.Status = TaskWaiting
		task.NotBefore = nextAt
		task.CurrentActivity = activity
		task.AvailabilityReason = activity
		task.StartedAt = 0
		_ = m.store.SaveTurnTask(task)
		m.emitTask(task)
		return
	}
	events, _ := m.store.RecentEvents(now.Add(-72*time.Hour).UnixMilli(), 8)
	thoughts, _ := m.store.ActiveThoughts(6)
	memories, _ := m.store.Memories(12)
	weather, _ := m.weather.Get(ctx, profile.City)
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
	brief = validateTurnBrief(brief, state, now)
	if task.Trigger == TriggerUser {
		brief.Reply.Send = true
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
	if task.Trigger == TriggerProactive && !brief.Reply.Send {
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
	state.LastUserAt = task.LastMessageAt
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
	resolution.GenerationFacts = generationFacts(brief, resolution)
	task.Status = TaskResolved
	task.Applied = true
	task.Resolution = &resolution
	if err := m.store.CommitTick(TickCommit{ID: m.id,
		State: state, Effects: effects, Events: extraEvents, Memories: memories,
		Thought: thought, Task: task,
	}); err != nil {
		return TurnResolution{}, err
	}
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
	return formatTurnBriefContext(profile, state, *task.Brief, *task.Resolution, userText), nil
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
		state, stateErr := m.store.State(m.id)
		if stateErr == nil {
			state.LastProactiveAt = task.CompletedAt
			state.UpdatedAt = task.CompletedAt
			_ = m.store.SaveState(m.id, state)
		}
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

func validateTurnBrief(brief TurnBrief, state State, now time.Time) TurnBrief {
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
	return b.String()
}

func formatTurnBriefContext(profile Profile, state State, brief TurnBrief, resolution TurnResolution, userText string) string {
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
	fmt.Fprintf(&b, "最新用户消息：%s\n", userText)
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
