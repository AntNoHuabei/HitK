package companion

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// EventName is the Wails event channel for companion state changes.
	EventName = "hitk:companion"

	tickInterval = 15 * time.Minute
	catchupLimit = 7 * 24 * time.Hour
)

// EventKind identifies one companion update.
type EventKind string

const (
	EventStateUpdated    EventKind = "state.updated"
	EventProfileUpdated  EventKind = "profile.updated"
	EventMessageCreated  EventKind = "message.created"
	EventPausedChanged   EventKind = "paused.changed"
	EventReset           EventKind = "reset"
	EventTurnTaskUpdated EventKind = "task.updated"
)

// Event is emitted through Wails so the sidebar and companion view stay in
// sync without polling the database.
type Event struct {
	Kind        EventKind `json:"kind"`
	CharacterID string    `json:"characterId"`
	Overview Overview  `json:"overview"`
	At       int64     `json:"at"`
}

// ModelPort performs the two opt-in model operations used by the simulation.
type ModelPort interface {
	Available() bool
	Decide(context.Context, DecisionInput) (ProactiveDecision, error)
	ExtractMemories(context.Context, MemoryInput) ([]MemoryCandidate, error)
	AlignTurn(context.Context, TurnAlignmentInput) (TurnBrief, error)
}

// ConversationPort connects the companion module to the generic chat runtime.
type ConversationPort interface {
	EnsureThread(context.Context, string, Profile) (string, error)
	Running(string) bool
	AppendMessage(string, string) error
	AppendUserMessage(string, string) error
	AppendTurnTask(string, TurnTask) error
	StartTurnTask(TurnTask, Profile) error
	ClearThread(string, Profile) error
	RecentMessages(string, int) []ConversationMessage
}

// Manager owns one companion world and its background lifecycle.
type Manager struct {
	id    string
	store   *Store
	weather *WeatherClient
	engine  *MetricEngine
	logger  *slog.Logger

	mu           sync.Mutex
	model        ModelPort
	conversation ConversationPort
	emit         func(Event)
	closed       bool

	tickMu         sync.Mutex
	taskMu         sync.Mutex
	conversationMu sync.Mutex
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

// NewManager opens the companion database for the built-in default world.
func NewManager(path string) (*Manager, error) {
	return NewManagerWithID(path, DefaultID)
}

// NewManagerWithID opens one character's world. Every character owns a separate
// database file, so the id only decides which profile and state rows this
// manager reads and writes.
func NewManagerWithID(path, id string) (*Manager, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("companion id is empty")
	}
	store, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	if cleaned, _ := store.Meta(metaLegacyKeywordEffectCleanup); cleaned != "1" {
		if err := store.RemoveEffectsBySourcePrefix("conversation_"); err != nil {
			_ = store.Close()
			return nil, err
		}
		if err := store.SetMeta(metaLegacyKeywordEffectCleanup, "1"); err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	if migrated, _ := store.Meta(metaAdultPersonaMigration); migrated != "1" {
		if profile, profileErr := store.Profile(id); profileErr == nil && profile.Summary == legacySummary {
			now := time.Now().UnixMilli()
			profile.Summary = DefaultSummary
			profile.Traits = []string{"温柔", "直接", "愿意亲密", "尊重明确撤回"}
			profile.UpdatedAt = now
			if err := store.SaveProfile(profile); err != nil {
				_ = store.Close()
				return nil, err
			}
		}
		if err := store.SetMeta(metaAdultPersonaMigration, "1"); err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	engine, err := NewMetricEngine(nil)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.SetMeta(metaRuleHash, engine.Rules().Hash); err != nil {
		_ = store.Close()
		return nil, err
	}
	return &Manager{id: id, store: store, weather: NewWeatherClient(), engine: engine}, nil
}

// ID returns the character this manager owns.
func (m *Manager) ID() string { return m.id }

// SetPorts wires model and conversation adapters after the container has built
// the agent. The manager remains usable without them: state still advances,
// but no model calls or proactive messages occur.
func (m *Manager) SetPorts(model ModelPort, conversation ConversationPort) {
	m.mu.Lock()
	m.model = model
	m.conversation = conversation
	m.mu.Unlock()
}

// SetEmitter wires the Wails event publisher.
func (m *Manager) SetEmitter(emit func(Event)) {
	m.mu.Lock()
	m.emit = emit
	m.mu.Unlock()
}

// SetLogger attaches the application logger.
func (m *Manager) SetLogger(logger *slog.Logger) {
	m.mu.Lock()
	m.logger = logger
	m.mu.Unlock()
}

func (m *Manager) logInfo(message string, args ...any) {
	m.mu.Lock()
	logger := m.logger
	m.mu.Unlock()
	if logger != nil {
		logger.Info(message, args...)
	}
}

// Profile returns the current companion profile.
func (m *Manager) Profile() (Profile, error) {
	return m.store.Profile(m.id)
}

// Start launches startup catch-up and the fifteen-minute real-time loop.
func (m *Manager) Start(parent context.Context) {
	m.mu.Lock()
	if m.cancel != nil || m.closed {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		m.startup(ctx)
		m.recoverTurnTasks(ctx)
		tick := time.NewTicker(tickInterval)
		taskTick := time.NewTicker(time.Minute)
		defer tick.Stop()
		defer taskTick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				_ = m.TickNow(ctx, now)
			case now := <-taskTick.C:
				m.processDueTurnTasks(ctx, now)
			}
		}
	}()
}

// Stop halts the tick and task loops but leaves the store open, so the registry
// can resume this world when the user switches back to it.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.mu.Unlock()
	m.wg.Wait()
	return nil
}

// Close stops background work and closes the store for good.
func (m *Manager) Close() error {
	if err := m.Stop(); err != nil {
		return err
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return m.store.Close()
}

// Busy reports whether this character is mid-response. The registry refuses to
// switch away from, or delete, a busy character.
func (m *Manager) Busy() (bool, error) {
	m.mu.Lock()
	conversation := m.conversation
	m.mu.Unlock()
	threadID, _ := m.store.Meta(metaPrimaryThread)
	if conversation != nil && threadID != "" && conversation.Running(threadID) {
		return true, nil
	}
	if _, active, err := m.store.ActiveTurnTask(threadID); err == nil && active {
		return true, nil
	}
	return false, nil
}

// Overview returns the compact state used by the shell and companion page.
func (m *Manager) Overview(ctx context.Context) (Overview, error) {
	profile, err := m.store.Profile(m.id)
	if err != nil {
		return Overview{}, err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return Overview{}, err
	}
	threadID, err := m.ensureThread(ctx)
	if err != nil {
		return Overview{}, err
	}
	paused, _ := m.store.Meta(metaPaused)
	unread, _ := m.store.Meta(metaUnread)
	count, _ := strconv.Atoi(unread)
	var activeTask *TurnTaskView
	if task, ok, taskErr := m.store.ActiveTurnTask(threadID); taskErr == nil && ok {
		view := task.View()
		activeTask = &view
	}
	return Overview{
		CharacterID: m.id,
		Profile:    profile,
		State:      state,
		Metrics:    m.engine.Views(state),
		Derived:    m.engine.Derived(state),
		Clock:      worldClock(time.Now()),
		Weather:    m.weather.Cached(profile.City),
		Paused:     paused == "1",
		Unread:     count,
		ThreadID:   threadID,
		ActiveTask: activeTask,
	}, nil
}

// TickNow runs one manual or scheduled Tick.
func (m *Manager) TickNow(ctx context.Context, now time.Time) error {
	if !m.tickMu.TryLock() {
		return nil
	}
	defer m.tickMu.Unlock()
	if err := m.advance(ctx, now); err != nil {
		return err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return err
	}
	if advanceSceneByWallClock(&state, now) {
		state.UpdatedAt = now.UnixMilli()
		if err := m.store.SaveState(m.id, state); err != nil {
			return err
		}
	}
	m.maybeProactive(ctx)
	return nil
}

// SetPaused pauses or resumes the world.
func (m *Manager) SetPaused(ctx context.Context, paused bool) error {
	value := ""
	if paused {
		value = "1"
	}
	if err := m.store.SetMeta(metaPaused, value); err != nil {
		return err
	}
	if !paused {
		state, err := m.store.State(m.id)
		if err != nil {
			return err
		}
		state.LastTickAt = time.Now().UnixMilli()
		state.UpdatedAt = state.LastTickAt
		if err := m.store.SaveState(m.id, state); err != nil {
			return err
		}
	}
	overview, _ := m.overviewUnchecked(ctx)
	m.emitEvent(EventPausedChanged, overview)
	return nil
}

// SaveProfile validates and persists a profile edit.
func (m *Manager) SaveProfile(ctx context.Context, profile Profile) (Profile, error) {
	now := time.Now().UnixMilli()
	current, err := m.store.Profile(m.id)
	if err != nil {
		return Profile{}, err
	}
	profile.ID = m.id
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Summary = strings.TrimSpace(profile.Summary)
	profile.City = strings.TrimSpace(profile.City)
	profile.CreatedAt = current.CreatedAt
	profile.UpdatedAt = now
	if profile.Name == "" {
		profile.Name = DefaultName
	}
	if profile.Summary == "" {
		profile.Summary = DefaultSummary
	}
	if len(profile.Traits) == 0 {
		profile.Traits = append([]string(nil), current.Traits...)
	}
	switch profile.ScheduleTendency {
	case "relaxed", "balanced", "busy", "social":
	default:
		profile.ScheduleTendency = "balanced"
	}
	switch profile.ReasoningEffort {
	case "", "off", "low", "medium", "high":
	default:
		profile.ReasoningEffort = ""
	}
	if err := m.store.SaveProfile(profile); err != nil {
		return Profile{}, err
	}
	overview, _ := m.overviewUnchecked(ctx)
	m.emitEvent(EventProfileUpdated, overview)
	return profile, nil
}

// Reset clears the companion world and its dedicated conversation.
func (m *Manager) Reset(ctx context.Context) error {
	oldThread, _ := m.store.Meta(metaPrimaryThread)
	m.mu.Lock()
	conversation := m.conversation
	m.mu.Unlock()
	if oldThread != "" && conversation != nil && conversation.Running(oldThread) {
		return errors.New("陪伴会话正在生成，暂时不能重置")
	}
	now := time.Now().UnixMilli()
	profile := DefaultProfile(now)
	if err := m.store.Reset(profile, DefaultState(now)); err != nil {
		return err
	}
	if oldThread != "" && conversation != nil {
		if err := conversation.ClearThread(oldThread, profile); err != nil {
			return err
		}
	}
	overview, _ := m.overviewUnchecked(ctx)
	m.emitEvent(EventReset, overview)
	return nil
}

// MarkRead clears the unread proactive-message count.
func (m *Manager) MarkRead(ctx context.Context) error {
	if err := m.store.SetUnread(0); err != nil {
		return err
	}
	overview, _ := m.overviewUnchecked(ctx)
	m.emitEvent(EventStateUpdated, overview)
	return nil
}

// ListEvents returns recent life events.
func (m *Manager) ListEvents(limit int) ([]LifeEvent, error) {
	return m.store.RecentEvents(0, limit)
}

// ListMemories returns durable memories.
func (m *Manager) ListMemories(limit int) ([]Memory, error) {
	return m.store.Memories(limit)
}

// SaveMemory creates or edits a memory from the management panel.
func (m *Manager) SaveMemory(memory Memory) (Memory, error) {
	now := time.Now().UnixMilli()
	memory.Content = strings.TrimSpace(memory.Content)
	if memory.Content == "" {
		return Memory{}, errors.New("记忆内容不能为空")
	}
	if memory.ID == "" {
		memory.ID = "mem_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		memory.CreatedAt = now
	} else if memory.CreatedAt == 0 {
		memory.CreatedAt = now
	}
	if memory.Kind == "" {
		memory.Kind = "user_fact"
	}
	if memory.Importance < 1 || memory.Importance > 10 {
		memory.Importance = 5
	}
	if memory.Strength <= 0 || memory.Strength < float64(memory.Importance) {
		memory.Strength = float64(memory.Importance)
	}
	memory.UpdatedAt = now
	if err := m.store.SaveMemory(memory); err != nil {
		return Memory{}, err
	}
	return memory, nil
}

// DeleteMemory removes one memory.
func (m *Manager) DeleteMemory(id string) error {
	return m.store.DeleteMemory(id)
}

func (m *Manager) startup(ctx context.Context) {
	initialized, _ := m.store.Meta(metaInitialized)
	if initialized == "" {
		now := time.Now()
		state, err := m.store.State(m.id)
		if err == nil {
			state.LastTickAt = now.UnixMilli()
			state.UpdatedAt = state.LastTickAt
			_ = m.store.SaveState(m.id, state)
		}
		_ = m.store.SetMeta(metaInitialized, "1")
		overview, _ := m.overviewUnchecked(ctx)
		m.emitEvent(EventStateUpdated, overview)
		return
	}
	m.tickMu.Lock()
	if err := m.advance(ctx, time.Now()); err == nil {
		m.maybeProactive(ctx)
	}
	m.tickMu.Unlock()
}

func (m *Manager) advance(ctx context.Context, now time.Time) error {
	profile, err := m.store.Profile(m.id)
	if err != nil {
		return err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return err
	}
	paused, _ := m.store.Meta(metaPaused)
	if paused == "1" {
		state.LastTickAt = now.UnixMilli()
		state.UpdatedAt = state.LastTickAt
		return m.store.SaveState(m.id, state)
	}

	if state.LastTickAt <= 0 {
		state.LastTickAt = now.UnixMilli()
	}
	from := time.UnixMilli(state.LastTickAt)
	if now.Sub(from) > catchupLimit {
		from = now.Add(-catchupLimit)
	}
	steps := int(now.Sub(from) / tickInterval)
	if steps <= 0 {
		return nil
	}
	m.logInfo("companion tick advance start",
		"from", from.Format(time.RFC3339),
		"to", now.Format(time.RFC3339),
		"steps", steps,
	)
	recent, _ := m.store.RecentEvents(now.Add(-7*24*time.Hour).UnixMilli(), 0)
	effects, _ := m.store.ActiveEffects()
	triggerStates, _ := m.store.TriggerStates()
	knownMemories := map[string]bool{}
	if existing, err := m.store.Memories(0); err == nil {
		for _, memory := range existing {
			knownMemories[normalize(memory.Content)] = true
		}
	}
	thoughtContents := map[string]bool{}
	if thoughts, err := m.store.ActiveThoughts(0); err == nil {
		for _, thought := range thoughts {
			thoughtContents[normalize(thought.Content)] = true
		}
	}
	eventCount := 0
	for i := 0; i < steps; i++ {
		stepTime := from.Add(time.Duration(i+1) * tickInterval)
		block := scheduleBlockFor(profile, stepTime)
		effects = pruneEffects(effects, stepTime.UnixMilli())
		m.engine.ApplyTick(&state, profile, block, effects, stepTime.UnixMilli())

		triggerEvents, nextTriggers := m.engine.EvaluateTriggers(state, triggerStates, stepTime)
		triggerStates = nextTriggers
		var event *LifeEvent
		if len(triggerEvents) > 0 {
			event = &triggerEvents[0]
		} else if rule, ok := m.engine.ChooseEvent(state, profile, block, stepTime, recent); ok {
			lifeEvent := m.engine.lifeEvent(rule, stepTime.UnixMilli(), "")
			event = &lifeEvent
		}
		var memories []Memory
		var thought *PendingThought
		if event != nil {
			eventCount++
			if rule, ok := m.engine.EventByID(event.RuleID); ok {
				effects = mergeEffects(effects, m.engine.ApplyEvent(&state, rule, event.ID, stepTime))
			}
			recent = append([]LifeEvent{*event}, recent...)
			if len(recent) > 80 {
				recent = recent[:80]
			}
			if event.Importance >= 7 && !knownMemories[normalize(event.Content)] {
				memories = append(memories, Memory{
					ID:         "mem_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
					Kind:       "life_event",
					Content:    event.Content,
					Importance: event.Importance,
					Source:     "life_event:" + event.ID,
					Strength:   float64(event.Importance),
					CreatedAt:  event.OccurredAt,
					UpdatedAt:  event.OccurredAt,
				})
				knownMemories[normalize(event.Content)] = true
			}
			if event.Shareable && event.Importance >= 5 && !thoughtContents[normalize(event.Content)] {
				thought = &PendingThought{
					ID:            "thought_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
					Content:       event.Content,
					Priority:      event.Importance,
					SourceEventID: event.ID,
					Status:        "active",
					CreatedAt:     event.OccurredAt,
				}
				thoughtContents[normalize(event.Content)] = true
			}
		}
		startedAt, endsAt := blockBounds(block, stepTime)
		state.CurrentActivity = block.Label
		state.ActivityKind = block.Kind
		state.ActivityStartedAt = startedAt
		state.ActivityEndsAt = endsAt
		state.LastTickAt = stepTime.UnixMilli()
		state.UpdatedAt = state.LastTickAt
		if err := m.store.CommitTick(TickCommit{ID: m.id,
			State: state, Event: event, Thought: thought, Memories: memories,
			Effects: effects, Triggers: triggerStates,
		}); err != nil {
			return err
		}
	}
	if state.LastMemoryDecayAt == 0 {
		state.LastMemoryDecayAt = now.UnixMilli()
	}
	if now.UnixMilli()-state.LastMemoryDecayAt >= 24*time.Hour.Milliseconds() {
		if err := m.store.DecayMemories(now.UnixMilli()); err != nil {
			return err
		}
		state.LastMemoryDecayAt = now.UnixMilli()
		state.UpdatedAt = now.UnixMilli()
		if err := m.store.SaveState(m.id, state); err != nil {
			return err
		}
	}
	_, _ = m.weather.Get(ctx, profile.City)
	overview, _ := m.overviewUnchecked(ctx)
	m.emitEvent(EventStateUpdated, overview)
	m.logInfo("companion tick advance done",
		"steps", steps,
		"events", eventCount,
		"active_effects", len(effects),
		"last_tick_at", state.LastTickAt,
	)
	return nil
}

func (m *Manager) maybeProactive(ctx context.Context) {
	profile, err := m.store.Profile(m.id)
	if err != nil || !profile.ProactiveEnabled {
		return
	}
	paused, _ := m.store.Meta(metaPaused)
	if paused == "1" {
		return
	}
	_, _, _ = m.SubmitProactiveTurn(ctx)
}

func (m *Manager) saveMemoryCandidates(candidates []MemoryCandidate, existing []Memory) {
	known := map[string]bool{}
	for _, memory := range existing {
		known[normalize(memory.Content)] = true
	}
	now := time.Now().UnixMilli()
	for i, candidate := range candidates {
		if i >= 3 {
			break
		}
		content := strings.TrimSpace(candidate.Content)
		key := normalize(content)
		if content == "" || known[key] {
			continue
		}
		kind := candidate.Kind
		switch kind {
		case "user_fact", "relationship", "life_event", "promise":
		default:
			kind = "user_fact"
		}
		if candidate.Importance < 1 || candidate.Importance > 10 {
			candidate.Importance = 5
		}
		memory := Memory{
			ID:         "mem_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
			Kind:       kind,
			Content:    content,
			Importance: candidate.Importance,
			Source:     "conversation",
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := m.store.SaveMemory(memory); err == nil {
			known[key] = true
		}
	}
}

func (m *Manager) ensureThread(ctx context.Context) (string, error) {
	m.mu.Lock()
	conversation := m.conversation
	m.mu.Unlock()
	if conversation == nil {
		return "", nil
	}
	stored, _ := m.store.Meta(metaPrimaryThread)
	profile, err := m.store.Profile(m.id)
	if err != nil {
		return "", err
	}
	threadID, err := conversation.EnsureThread(ctx, stored, profile)
	if err != nil {
		return "", err
	}
	if threadID != stored {
		if err := m.store.SetMeta(metaPrimaryThread, threadID); err != nil {
			return "", err
		}
	}
	return threadID, nil
}

func (m *Manager) overviewUnchecked(ctx context.Context) (Overview, error) {
	profile, err := m.store.Profile(m.id)
	if err != nil {
		return Overview{}, err
	}
	state, err := m.store.State(m.id)
	if err != nil {
		return Overview{}, err
	}
	threadID, _ := m.ensureThread(ctx)
	paused, _ := m.store.Meta(metaPaused)
	unread, _ := m.store.Meta(metaUnread)
	count, _ := strconv.Atoi(unread)
	return Overview{
		CharacterID: m.id,
		Profile:  profile,
		State:    state,
		Metrics:  m.engine.Views(state),
		Derived:  m.engine.Derived(state),
		Clock:    worldClock(time.Now()),
		Weather:  m.weather.Cached(profile.City),
		Paused:   paused == "1",
		Unread:   count,
		ThreadID: threadID,
	}, nil
}

func (m *Manager) emitEvent(kind EventKind, overview Overview) {
	m.mu.Lock()
	emit := m.emit
	m.mu.Unlock()
	if emit != nil {
		if overview.CharacterID == "" {
			overview.CharacterID = m.id
		}
	emit(Event{Kind: kind, CharacterID: m.id, Overview: overview, At: time.Now().UnixMilli()})
	}
}

func formatContextBlock(
	profile Profile,
	state State,
	derived DerivedState,
	events []LifeEvent,
	thoughts []PendingThought,
	memories []Memory,
	weather *WeatherSnapshot,
	wake WakeSummary,
) string {
	var b strings.Builder
	clock := wake.Clock
	if clock.Now == 0 {
		clock = worldClock(time.Now())
	}
	fmt.Fprintf(&b, "【角色设定】\n你叫%s。%s\n", profile.Name, profile.Summary)
	if len(profile.Traits) > 0 {
		fmt.Fprintf(&b, "性格关键词：%s。\n", strings.Join(profile.Traits, "、"))
	}
	b.WriteString("\n【成人亲密模式】\n本应用是用户本地运行的成人陪伴模式。当前关系状态支持时，可以直白表达成年人的欲望、身体反应、性行为动作、性器官感受和高潮后的余韵；可以使用明确的性词汇，不使用含糊的委婉语，也不要把关键动作写成省略号。只有双方成年人明确自愿时才能继续；不得涉及未成年人、非自愿、胁迫、失去意识或无法表达同意的情况。\n")
	fmt.Fprintf(&b, "\n【世界时间】\n今天是%s，%s，属于%s。当前是%s。\n",
		clock.Date, clock.Weekday, clock.Season, clock.TimeOfDay)
	fmt.Fprintf(&b, "\n【当前状态】\n你此刻%s；心情%s。生命力%.1f，饥饿%.1f，疲劳%.1f，睡眠压力%.1f，清醒度%.1f，认知负荷%.1f，行动动机%.1f。\n",
		state.CurrentActivity, derived.Mood,
		state.Metric(MetricVitality), state.Metric(MetricHunger), state.Metric(MetricFatigue),
		state.Metric(MetricSleepPressure), state.Metric(MetricAlertness),
		state.Metric(MetricCognitiveLoad), state.Metric(MetricMotivation))
	fmt.Fprintf(&b, "情绪%.1f，压力%.1f，焦虑%.1f，易怒%.1f，孤独%.1f，社交电量%.1f，性欲%.1f。\n",
		state.Metric(MetricMoodValence), state.Metric(MetricStress), state.Metric(MetricAnxiety),
		state.Metric(MetricIrritability), state.Metric(MetricLoneliness),
		state.Metric(MetricSocialEnergy), state.Metric(MetricLibido))
	fmt.Fprintf(&b, "关系状态：思念%.1f，信任%.1f，亲密度%.1f，安全感%.1f。\n",
		state.Metric(MetricMissing), state.Metric(MetricTrust), state.Metric(MetricIntimacy), derived.RelationshipSafety)
	if wake.Tier != "initial" {
		fmt.Fprintf(&b, "距离上次互动约%d小时。开场方式：%s。\n", wake.ElapsedHours, wakeTone(wake.Tier))
	}
	if len(events) > 0 {
		b.WriteString("\n【近期生活】\n")
		for _, event := range events {
			fmt.Fprintf(&b, "- %s：%s\n", time.UnixMilli(event.OccurredAt).Format("01-02 15:04"), event.Content)
		}
	}
	if len(thoughts) > 0 {
		b.WriteString("\n【你想跟对方说的事】\n")
		for _, thought := range thoughts {
			fmt.Fprintf(&b, "- %s\n", thought.Content)
		}
	}
	if len(memories) > 0 {
		b.WriteString("\n【关于对方的记忆】\n")
		for _, memory := range memories {
			fmt.Fprintf(&b, "- %s\n", memory.Content)
		}
	}
	if weather != nil {
		fmt.Fprintf(&b, "\n【有限环境感知】\n你知道对方手动填写的城市是%s，当地现在%s，约%d°C。不要声称知道对方具体位置。\n",
			weather.City, weather.Description, weather.Temperature)
	}
	b.WriteString("\n只使用以上有限信息，不要假装全知。自然地结合状态和近期经历回复，不要逐条复述这些资料。")
	return b.String()
}

func wakeTone(tier string) string {
	switch tier {
	case "under_1h":
		return "自然延续上次话题，不要说好久不见"
	case "1_6h":
		return "可以先分享一件刚发生的小事，再关心对方"
	case "6_24h":
		return "可以克制地表达一点想念，但不要质问对方去了哪里"
	case "1_3d":
		return "带着这几天自己的生活重新接上话题，不表达责怪"
	case "over_3d":
		return "记得对方，但从当下状态自然开始；关心而不制造愧疚"
	default:
		return "像第一次见面一样自然认识对方"
	}
}

func summarizeWake(state State, events []LifeEvent, thoughts []PendingThought) WakeSummary {
	clock := worldClock(time.Now())
	last := state.LastUserAt
	if last == 0 {
		return WakeSummary{Tier: "initial", Clock: clock, RecentEvents: events, Thoughts: thoughts, State: state}
	}
	hours := int(time.Since(time.UnixMilli(last)).Hours())
	tier := "over_3d"
	switch {
	case hours < 1:
		tier = "under_1h"
	case hours < 6:
		tier = "1_6h"
	case hours < 24:
		tier = "6_24h"
	case hours < 72:
		tier = "1_3d"
	}
	return WakeSummary{Tier: tier, ElapsedHours: hours, Clock: clock, RecentEvents: events, Thoughts: thoughts, State: state}
}

func clipRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func normalize(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func metricLogSnapshot(state State) map[string]float64 {
	ids := []MetricID{
		MetricVitality, MetricHunger, MetricFatigue, MetricSleepPressure,
		MetricMoodValence, MetricStress, MetricAnxiety, MetricLoneliness,
		MetricLibido, MetricSexualSatisfaction, MetricMissing,
		MetricTrust, MetricIntimacy,
	}
	out := make(map[string]float64, len(ids))
	for _, id := range ids {
		out[string(id)] = math.Round(state.Metric(id)*100) / 100
	}
	return out
}

func hashAt(id string, at time.Time) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	_, _ = h.Write([]byte(strconv.FormatInt(at.Unix(), 10)))
	return h.Sum64()
}
