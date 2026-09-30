package companion

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeModel struct {
	mu         sync.Mutex
	decision   ProactiveDecision
	candidates []MemoryCandidate
	analysis   ConversationOutcome
	brief      TurnBrief
	alignErr   error
	decisions  int
	extracts   int
	analyses   int
}

func (f *fakeModel) Available() bool { return true }

func (f *fakeModel) Decide(context.Context, DecisionInput) (ProactiveDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions++
	return f.decision, nil
}

func (f *fakeModel) ExtractMemories(context.Context, MemoryInput) ([]MemoryCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extracts++
	return append([]MemoryCandidate(nil), f.candidates...), nil
}

func (f *fakeModel) AlignTurn(context.Context, TurnAlignmentInput) (TurnBrief, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.analyses++
	if f.alignErr != nil {
		return TurnBrief{}, f.alignErr
	}
	if f.brief.Clock.Now != 0 || f.brief.Reply.Goal != "" {
		return f.brief, nil
	}
	return TurnBrief{
		Resolution: ResolutionDraft{SceneStage: "none", ConsentState: "not_applicable"},
		Reply:      ReplyBrief{Send: true, Goal: "自然延续对话", Tone: "平静", MaxChars: 400},
		Confidence: 0.8,
	}, nil
}

type fakeConversation struct {
	mu       sync.Mutex
	threadID string
	messages []string
	cleared  int
	started  int
	aligned  int
	running  bool
}

func (f *fakeConversation) EnsureThread(context.Context, string, Profile) (string, error) {
	if f.threadID == "" {
		f.threadID = "th_companion"
	}
	return f.threadID, nil
}

func (f *fakeConversation) Running(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fakeConversation) AppendMessage(_ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	return nil
}

func (f *fakeConversation) AppendUserMessage(_ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	return nil
}

func (f *fakeConversation) AppendTurnTask(string, TurnTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aligned++
	return nil
}

func (f *fakeConversation) StartTurnTask(TurnTask, Profile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	return nil
}

func (f *fakeConversation) ClearThread(string, Profile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared++
	return nil
}

func (f *fakeConversation) RecentMessages(string, int) []ConversationMessage {
	return nil
}

func openTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager(filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

// fixedMorning and fixedBusyHour are fixed instants far enough ahead of the real
// clock that a task submitted for them is never picked up by the background kick
// goroutine. Deterministic tests need that: once the wall clock passes a
// hardcoded date, the kick goroutine starts racing the test's own processing.
// 2030-09-30 is a Monday, matching the weekday shape these tests were written
// against.
var (
	fixedMorning  = time.Date(2030, 9, 30, 8, 0, 0, 0, time.Local)
	fixedBusyHour = time.Date(2030, 9, 30, 10, 0, 0, 0, time.Local)
)

func processTaskNow(t *testing.T, manager *Manager, task TurnTask) TurnTask {
	t.Helper()
	task.NotBefore = 0
	if err := manager.store.SaveTurnTask(task); err != nil {
		t.Fatal(err)
	}
	manager.processTurnTaskAt(context.Background(), task.ID, fixedMorning)
	updated, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

// pinClock pulls the world's LastTickAt back to `at` so a task processed at that
// same fixed instant sees a non-negative delta. Without this, advance() no-ops as
// soon as the real wall clock passes the fixed dates these tests use.
func pinClock(t *testing.T, manager *Manager, at time.Time) {
	t.Helper()
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.LastTickAt = at.Add(-15 * time.Minute).UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCleansLegacyKeywordEffectsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "companion.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEffects([]ActiveEffect{
		{ID: "legacy-boundary", SourceRuleID: "conversation_boundary", Target: MetricLibido},
		{ID: "legacy-explicit", SourceRuleID: "conversation_explicit", Target: MetricLibido},
		{ID: "semantic", SourceRuleID: "conversation", Target: MetricLibido},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := manager.store.ActiveEffects()
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].ID != "semantic" {
		t.Fatalf("legacy effects were not cleaned: %#v", effects)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEffects(append(effects, ActiveEffect{
		ID: "legacy-after-migration", SourceRuleID: "conversation_humor", Target: MetricMoodValence,
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err = NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	effects, err = manager.store.ActiveEffects()
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 2 {
		t.Fatalf("cleanup ran more than once: %#v", effects)
	}
}

func TestManagerMigratesLegacyCompanionPersona(t *testing.T) {
	path := filepath.Join(t.TempDir(), "companion.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	profile := DefaultProfile(now)
	profile.Summary = legacySummary
	profile.Traits = []string{"温柔", "清醒", "有边界感"}
	if err := store.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	migrated, err := manager.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Summary != DefaultSummary {
		t.Fatalf("summary = %q", migrated.Summary)
	}
	for _, trait := range migrated.Traits {
		if trait == "有边界感" {
			t.Fatalf("legacy boundary trait survived: %#v", migrated.Traits)
		}
	}
}

func TestTickCreatesProactiveMessageAndUnread(t *testing.T) {
	manager := openTestManager(t)
	model := &fakeModel{brief: TurnBrief{
		Resolution: ResolutionDraft{SceneStage: "none", ConsentState: "not_applicable"},
		Reply:      ReplyBrief{Send: true, Goal: "分享刚发生的小事", Tone: "自然", MaxChars: 300},
		Confidence: 0.8,
	}}
	conversation := &fakeConversation{}
	manager.SetPorts(model, conversation)

	now := fixedMorning
	pinClock(t, manager, now)
	task, created, err := manager.submitProactiveTurnAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("proactive task was not created")
	}
	processTaskNow(t, manager, task)
	manager.CompleteCompanionTurn(task.ThreadID, "turn", task.ID, "", "刚忙完，忽然想和你说句话。")
	conversation.mu.Lock()
	started := conversation.started
	conversation.mu.Unlock()
	if started != 1 {
		t.Fatalf("proactive tasks started = %d, want 1", started)
	}
	overview, err := manager.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.Unread != 1 {
		t.Fatalf("unread = %d, want 1", overview.Unread)
	}
	if err := manager.MarkRead(context.Background()); err != nil {
		t.Fatal(err)
	}
	overview, _ = manager.Overview(context.Background())
	if overview.Unread != 0 {
		t.Fatalf("unread after mark read = %d, want 0", overview.Unread)
	}
}

func TestCatchupIsCappedAtSevenDays(t *testing.T) {
	manager := openTestManager(t)
	now := time.Now().Truncate(time.Minute)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.LastTickAt = now.Add(-10 * 24 * time.Hour).UnixMilli()
	state.LastUserAt = now.Add(-10 * 24 * time.Hour).UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}
	if err := manager.advance(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	next, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if got := now.Sub(time.UnixMilli(next.LastTickAt)); got > tickInterval {
		t.Fatalf("catch-up ended %s before now", got)
	}
	if _, err := manager.ListEvents(200); err != nil {
		t.Fatal(err)
	}
}

func TestSceneDecaysOnAdvanceEvenWithoutTickSteps(t *testing.T) {
	manager := openTestManager(t)
	now := time.Now().Truncate(time.Second)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	// 场景两小时前开的，模拟时钟已经追平，这一步本来一步都不用跑。
	// 衰减不能挂在「跑了若干 step」上，否则这种路径下旧场景会一直挂着。
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = now.Add(-2 * time.Hour).UnixMilli()
	state.LastTickAt = now.UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}
	if err := manager.advance(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	next, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if next.SceneStage != "aftercare" {
		t.Fatalf("scene did not decay when no tick step was needed: %q", next.SceneStage)
	}
}

func TestOverviewNeverServesStaleScene(t *testing.T) {
	manager := openTestManager(t)
	now := time.Now().Truncate(time.Second)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = now.Add(-5 * time.Hour).UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}
	overview, err := manager.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.State.SceneStage != "none" {
		t.Fatalf("overview served a 5h-old scene as current: %q", overview.State.SceneStage)
	}
}

func TestOverviewActivityFollowsLiveScene(t *testing.T) {
	manager := openTestManager(t)
	now := time.Now().Truncate(time.Second)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.CurrentActivity = "吃午饭，短暂休息"
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = now.Add(-10 * time.Minute).UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}
	overview, err := manager.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.State.CurrentActivity != "亲密中" {
		t.Fatalf("activity still came off the schedule: %q", overview.State.CurrentActivity)
	}
	// 落库的仍是日程活动，displayActivity 只改读视图。
	stored, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentActivity != "吃午饭，短暂休息" {
		t.Fatalf("display activity leaked into storage: %q", stored.CurrentActivity)
	}
}

func TestSimulationClockConvergesAfterMissedTick(t *testing.T) {
	manager := openTestManager(t)
	now := time.Now().Truncate(time.Second)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	// 机器打盹漏掉一次 ticker：距上次 tick 28 分钟。floor 只跑一步，
	// 余下 13 分钟永久丢掉，模拟时钟就再也追不回来。
	state.LastTickAt = now.Add(-28 * time.Minute).UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}
	if err := manager.advance(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	next, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if lag := now.Sub(time.UnixMilli(next.LastTickAt)); lag > tickInterval/2 {
		t.Fatalf("sim clock still %s behind real time after a missed tick", lag)
	}
}

func TestCompanionStateSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "companion.db")
	first, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := first.store.Profile(first.id)
	profile.City = "杭州"
	if _, err := first.SaveProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	state, _ := first.store.State(first.id)
	state.Metrics[MetricVitality] = 42
	if err := first.store.SaveState(first.id, state); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	reloadedProfile, _ := second.store.Profile(second.id)
	reloadedState, _ := second.store.State(second.id)
	if reloadedProfile.City != "杭州" || reloadedState.Metric(MetricVitality) != 42 {
		t.Fatalf("state did not survive reopen: profile=%+v state=%+v", reloadedProfile, reloadedState)
	}
}

func TestRecordTurnExtractsMemories(t *testing.T) {
	manager := openTestManager(t)
	model := &fakeModel{candidates: []MemoryCandidate{
		{Kind: "user_fact", Content: "用户喜欢手冲咖啡", Importance: 7},
	}}
	conversation := &fakeConversation{}
	manager.SetPorts(model, conversation)
	task := TurnTask{ID: "task_memory", ThreadID: "th_companion", Status: TaskGenerating}
	if err := manager.store.SaveTurnTask(task); err != nil {
		t.Fatal(err)
	}
	manager.CompleteCompanionTurn("th_companion", "turn", task.ID, "我喜欢手冲咖啡", "记住啦。")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		memories, err := manager.ListMemories(10)
		if err == nil && len(memories) == 1 {
			if memories[0].Content != "用户喜欢手冲咖啡" {
				t.Fatalf("memory = %+v", memories[0])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("memory extraction did not finish")
}

func TestResetClearsWorldAndDedicatedThread(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(&fakeModel{decision: ProactiveDecision{Send: true, Message: "在忙吗？"}}, conversation)
	if _, err := manager.Overview(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ := manager.store.State(manager.id)
	if err := manager.TickNow(context.Background(), time.UnixMilli(state.LastTickAt).Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SaveMemory(Memory{Kind: "user_fact", Content: "临时记忆", Importance: 5}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	memories, _ := manager.ListMemories(10)
	events, _ := manager.ListEvents(10)
	overview, _ := manager.Overview(context.Background())
	if len(memories) != 0 || len(events) != 0 || overview.Unread != 0 {
		t.Fatalf("reset left world data: memories=%d events=%d unread=%d", len(memories), len(events), overview.Unread)
	}
	conversation.mu.Lock()
	cleared := conversation.cleared
	conversation.mu.Unlock()
	if cleared != 1 {
		t.Fatalf("dedicated thread cleared %d times, want 1", cleared)
	}
}

func TestEventIdsDoNotAffectDeterministicChoice(t *testing.T) {
	profile := DefaultProfile(1)
	state := DefaultState(1)
	at := time.Date(2026, 9, 28, 15, 0, 0, 0, time.Local)
	block := scheduleBlockFor(profile, at)
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	first, firstOK := engine.ChooseEvent(state, profile, block, at, nil)
	second, secondOK := engine.ChooseEvent(state, profile, block, at, nil)
	if firstOK != secondOK {
		t.Fatal("event choice was not deterministic")
	}
	if firstOK && first.ID != second.ID {
		t.Fatalf("event changed: %+v vs %+v", first, second)
	}
}

func TestStateClampsAndDerivesMood(t *testing.T) {
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricVitality] = -20
	state.Metrics[MetricStress] = 120
	state.Metrics[MetricVitality] = clampFloat(state.Metric(MetricVitality), 0, 100)
	state.Metrics[MetricStress] = clampFloat(state.Metric(MetricStress), 0, 100)
	if state.Metric(MetricVitality) != 0 || state.Metric(MetricStress) != 100 {
		t.Fatalf("clamp failed: %+v", state)
	}
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	if mood := engine.Derived(state).Mood; mood != "紧绷" {
		t.Fatalf("mood = %q, want 紧绷", mood)
	}
}

func TestWeatherWithoutCityIsNoop(t *testing.T) {
	client := NewWeatherClient()
	weather, err := client.Get(context.Background(), "")
	if err != nil || weather != nil {
		t.Fatalf("empty city weather = %#v, %v", weather, err)
	}
}

func TestWorldClockKnowsWeekdaySeasonAndDayPart(t *testing.T) {
	at := time.Date(2026, time.September, 28, 20, 30, 0, 0, time.Local)
	clock := worldClock(at)
	if clock.Weekday != "周一" {
		t.Fatalf("weekday = %q, want 周一", clock.Weekday)
	}
	if clock.Season != "秋" || clock.TimeOfDay != "晚上" || clock.IsWeekend {
		t.Fatalf("clock = %+v", clock)
	}
}

func TestScheduleChangesAcrossTheDayAndWeek(t *testing.T) {
	profile := DefaultProfile(1)
	weekdayMorning := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.Local)
	weekdayNight := time.Date(2026, time.September, 28, 23, 0, 0, 0, time.Local)
	weekend := time.Date(2026, time.October, 3, 14, 0, 0, 0, time.Local)
	if kind := scheduleBlockFor(profile, weekdayMorning).Kind; kind == "sleep" {
		t.Fatalf("weekday morning should not be sleep")
	}
	if kind := scheduleBlockFor(profile, weekdayNight).Kind; kind != "sleep" {
		t.Fatalf("weekday night kind = %q, want sleep", kind)
	}
	if kind := scheduleBlockFor(profile, weekend).Kind; kind == "work" {
		t.Fatalf("weekend unexpectedly uses work block")
	}
}

func TestUserPresenceImprovesRelationshipAndMood(t *testing.T) {
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricMissing] = 80
	state.Metrics[MetricLoneliness] = 70
	state.Metrics[MetricSocialEnergy] = 20
	state.Metrics[MetricTrust] = 50
	state.Metrics[MetricIntimacy] = 30
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	beforeMissing := state.Metric(MetricMissing)
	engine.ApplyInteraction(&state, "user_presence")
	engine.ApplyInteraction(&state, "meaningful_conversation")
	if state.Metric(MetricMissing) >= beforeMissing {
		t.Fatalf("missing did not fall: %.1f -> %.1f", beforeMissing, state.Metric(MetricMissing))
	}
	if state.Metric(MetricLoneliness) >= 70 || state.Metric(MetricSocialEnergy) <= 20 {
		t.Fatalf("social state did not improve: %+v", state)
	}
	if state.Metric(MetricTrust) <= 50 || state.Metric(MetricIntimacy) <= 30 {
		t.Fatalf("relationship did not strengthen: trust=%.1f intimacy=%.1f", state.Metric(MetricTrust), state.Metric(MetricIntimacy))
	}
}

func TestCausalWorkEventEmergesFromHighFatigue(t *testing.T) {
	profile := DefaultProfile(1)
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricFatigue] = 75
	state.Metrics[MetricStress] = 70
	state.Metrics[MetricAnxiety] = 60
	state.Metrics[MetricMissing] = 45
	block := scheduleBlock{Kind: "work", Label: "工作上正忙", StartMinute: 540, EndMinute: 720}
	start := time.Date(2026, time.September, 28, 9, 0, 0, 0, time.Local)
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, raw := engine.inputSignals(state, profile, block, nil, start.UnixMilli())
	if !engine.rules.Events["work_error"].When.Evaluate(raw, map[string]string{"context.block": block.Kind}) {
		t.Fatalf("high fatigue did not satisfy the work-error causal condition: when=%+v raw=%+v", engine.rules.Events["work_error"].When, raw)
	}
}

func TestMemoryDecayAndRecallReinforcement(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "companion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UnixMilli()
	memory := Memory{
		ID:         "mem_test",
		Kind:       "user_fact",
		Content:    "用户喜欢雨天",
		Importance: 2,
		Strength:   2,
		CreatedAt:  now - 10*24*time.Hour.Milliseconds(),
		UpdatedAt:  now - 10*24*time.Hour.Milliseconds(),
	}
	if err := store.SaveMemory(memory); err != nil {
		t.Fatal(err)
	}
	if err := store.DecayMemories(now); err != nil {
		t.Fatal(err)
	}
	memories, err := store.Memories(10)
	if err != nil || len(memories) != 1 {
		t.Fatalf("memories after decay = %#v, %v", memories, err)
	}
	if memories[0].Strength >= 2 {
		t.Fatalf("low-value memory did not decay: %+v", memories[0])
	}
	if err := store.TouchMemories(memories, now); err != nil {
		t.Fatal(err)
	}
	reinforced, _ := store.Memories(10)
	if reinforced[0].Strength <= memories[0].Strength || reinforced[0].AccessCount != 1 {
		t.Fatalf("recall did not reinforce memory: %+v", reinforced[0])
	}
}

func TestLibidoFollowsWellbeingAndIntimacy(t *testing.T) {
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := DefaultProfile(time.Now().UnixMilli())
	block := scheduleBlock{Kind: "rest", Label: "休息", StartMinute: 1200, EndMinute: 1260}
	low := DefaultState(time.Now().UnixMilli())
	low.Metrics[MetricLibido] = 50
	low.Metrics[MetricVitality] = 15
	low.Metrics[MetricFatigue] = 85
	low.Metrics[MetricStress] = 80
	low.Metrics[MetricAnxiety] = 75
	low.Metrics[MetricIntimacy] = 10
	lowStart := low.Metric(MetricLibido)
	for i := 0; i < 20; i++ {
		engine.ApplyTick(&low, profile, block, nil, time.Now().Add(time.Duration(i)*tickInterval).UnixMilli())
	}
	if low.Metric(MetricLibido) >= lowStart {
		t.Fatalf("libido did not fall under strain: %.1f", low.Metric(MetricLibido))
	}

	high := DefaultState(time.Now().UnixMilli())
	high.Metrics[MetricLibido] = 20
	high.Metrics[MetricVitality] = 90
	high.Metrics[MetricFatigue] = 10
	high.Metrics[MetricStress] = 10
	high.Metrics[MetricAnxiety] = 10
	high.Metrics[MetricMoodValence] = 85
	high.Metrics[MetricIntimacy] = 90
	highStart := high.Metric(MetricLibido)
	for i := 0; i < 20; i++ {
		engine.ApplyTick(&high, profile, block, nil, time.Now().Add(time.Duration(i)*tickInterval).UnixMilli())
	}
	if high.Metric(MetricLibido) <= highStart {
		t.Fatalf("libido did not recover with wellbeing and intimacy: %.1f", high.Metric(MetricLibido))
	}
}
