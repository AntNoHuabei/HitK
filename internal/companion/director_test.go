package companion

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func directorTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager(filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func compileDraft(
	t *testing.T, manager *Manager, draft WorldDraft, asleep bool, now time.Time,
) directivePlan {
	t.Helper()
	return compileDirective(directiveInput{
		CharacterID: manager.ID(),
		ThreadID:    "thread_test",
		TaskID:      "task_test",
		DirectiveID: newDirectiveID(),
		Text:        "测试指令",
		Draft:       draft,
		Asleep:      asleep,
		WakeAt:      now.Add(4 * time.Hour),
		Now:         now,
		Metrics:     manager.knownMetrics(),
	})
}

func boolPtr(value bool) *bool { return &value }

func TestDirectorArrangementLandsOnTheDayItWasPromisedFor(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	plan := compileDraft(t, manager, WorldDraft{
		Arrangements: []DirectorArrangement{{CommitmentDraft: CommitmentDraft{
			Kind: "social", Label: "妈妈来访", Date: tomorrow, Start: "15:00", Duration: 120, Weight: 7,
		}}},
		Narration: "她妈说明天要过来坐坐",
	}, false, now)
	settled, commitments, _, _ := manager.applyDirectivePlan(plan, now)

	if settled.Status != DirectiveApplied {
		t.Fatalf("status = %s (%s), want applied", settled.Status, settled.Reason)
	}
	if len(commitments) != 1 || commitments[0].Date != tomorrow {
		t.Fatalf("commitments = %+v, want one dated %s", commitments, tomorrow)
	}
	if len(settled.Changes) != 1 || settled.Changes[0].Kind != ChangeArrangement {
		t.Fatalf("changes = %+v, want one arrangement", settled.Changes)
	}
	if !settled.Changes[0].Revertible {
		t.Fatal("an arrangement must be revertible")
	}

	if err := manager.store.SaveCommitment(manager.ID(), commitments[0]); err != nil {
		t.Fatalf("save commitment: %v", err)
	}
	profile, err := manager.Profile()
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	dayPlan := manager.dayPlanFor(profile, now.AddDate(0, 0, 1))
	if !planHasBlockLabel(dayPlan, "妈妈来访") {
		t.Fatalf("tomorrow's plan has no 妈妈来访 block: %+v", dayPlan.Blocks)
	}
}

func TestDirectorDisruptionMovesTodayAndIsNeverRevertible(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	profile, err := manager.Profile()
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	before := manager.planAt(profile, now)

	plan := compileDraft(t, manager, WorldDraft{
		Disruptions: []DisruptionDraft{{Minutes: 180, Severity: 8, Reason: "路上堵了两个多小时"}},
		Narration:   "路上堵了两个多小时",
	}, false, now)
	settled, _, _, _ := manager.applyDirectivePlan(plan, now)

	if settled.Status != DirectiveApplied {
		t.Fatalf("status = %s (%s), want applied", settled.Status, settled.Reason)
	}
	if len(settled.Changes) != 1 || settled.Changes[0].Kind != ChangeDisruption {
		t.Fatalf("changes = %+v, want one disruption", settled.Changes)
	}
	if settled.Changes[0].Revertible {
		t.Fatal("a disruption must never be revertible; time does not run backwards")
	}
	after := manager.planAt(profile, now)
	if after.Revision == before.Revision {
		t.Fatal("the day's plan was not touched by the disruption")
	}
}

func TestDirectorMoodEffectCarriesPrefixAndDoesNotStack(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)

	plan := compileDraft(t, manager, WorldDraft{
		MoodEffects: []DirectorMoodDraft{{
			Target: "moodValence", BaselineDelta: 18, DurationMinutes: 240, Label: "心情特别好",
		}},
		Narration: "她今天心情特别好",
	}, false, now)
	settled, _, effects, _ := manager.applyDirectivePlan(plan, now)

	if len(effects) != 1 {
		t.Fatalf("effects = %+v, want one", effects)
	}
	wantSource := directiveEffectPrefix + settled.ID + ":" + string(MetricMoodValence)
	if effects[0].SourceRuleID != wantSource {
		t.Fatalf("source = %q, want %q", effects[0].SourceRuleID, wantSource)
	}
	// The whole reason the source id carries the directive id: the tick merges by
	// (source, target), so re-merging refreshes the same effect instead of
	// piling another +18 on every fifteen minutes.
	if merged := mergeEffects(effects, effects); len(merged) != 1 {
		t.Fatalf("merged %d effects, want the same one refreshed", len(merged))
	}
}

func TestDirectorPlaceMovesWhereSheStands(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	profile, err := manager.Profile()
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	profile.City = "杭州"
	if err := manager.store.SaveProfile(profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}

	plan := compileDraft(t, manager, WorldDraft{
		Place:     &DirectorPlaceDraft{City: "成都", Date: now.Format("2006-01-02"), Days: 3},
		Narration: "她人在成都",
	}, false, now)
	_, commitments, _, _ := manager.applyDirectivePlan(plan, now)
	if len(commitments) != 3 {
		t.Fatalf("commitments = %d, want one per day for 3 days", len(commitments))
	}
	for _, commitment := range commitments {
		if err := manager.store.SaveCommitment(manager.ID(), commitment); err != nil {
			t.Fatalf("save commitment: %v", err)
		}
	}
	if got := manager.effectiveCity(profile, now.Add(4*time.Hour)); got != "成都" {
		t.Fatalf("effectiveCity = %q, want 成都", got)
	}
	// The trip ends, so home is home again the day after.
	if got := manager.effectiveCity(profile, now.AddDate(0, 0, 4)); got != "杭州" {
		t.Fatalf("effectiveCity after the trip = %q, want 杭州", got)
	}
}

func TestDirectorCannotWakeHerButCanStillPlanAhead(t *testing.T) {
	now := time.Date(2026, 10, 2, 3, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	plan := compileDraft(t, manager, WorldDraft{
		Disruptions:  []DisruptionDraft{{Minutes: 60, Reason: "突然下起大雨"}},
		Events:       []DirectorEventDraft{{Content: "楼下着火了"}},
		Arrangements: []DirectorArrangement{{CommitmentDraft: CommitmentDraft{Label: "体检", Date: tomorrow, Start: "09:00"}}},
	}, true, now)

	if len(plan.Changes) != 1 || plan.Changes[0].Kind != ChangeArrangement {
		t.Fatalf("changes = %+v, want only the future arrangement to survive", plan.Changes)
	}
	if len(plan.Reasons) != 2 {
		t.Fatalf("reasons = %v, want the disruption and the event both refused", plan.Reasons)
	}
	settled, _, _, _ := manager.applyDirectivePlan(plan, now)
	if settled.Status != DirectivePartial {
		t.Fatalf("status = %s, want partial", settled.Status)
	}
	if !strings.Contains(settled.Reason, "睡觉") {
		t.Fatalf("reason = %q, want it to say she is asleep", settled.Reason)
	}
}

func TestDirectorCannotSetAMetricOutright(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	pinned := 95.0

	plan := compileDraft(t, manager, WorldDraft{
		MoodEffects: []DirectorMoodDraft{{Target: "moodValence", SetTo: &pinned, DurationMinutes: 120}},
	}, false, now)
	if len(plan.Changes) != 0 {
		t.Fatalf("changes = %+v, want the pinned value refused", plan.Changes)
	}
	if len(plan.Reasons) == 0 || !strings.Contains(plan.Reasons[0], "按到某个数") {
		t.Fatalf("reasons = %v, want it to name the pinning problem", plan.Reasons)
	}
}

func TestDirectorEffectNeedsADuration(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)

	plan := compileDraft(t, manager, WorldDraft{
		MoodEffects: []DirectorMoodDraft{{Target: "stress", BaselineDelta: -30}},
	}, false, now)
	if len(plan.Changes) != 0 {
		t.Fatalf("changes = %+v, want a duration-less effect refused", plan.Changes)
	}
}

func TestDirectorCannotForceABlockToStartOrFinish(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)

	plan := compileDraft(t, manager, WorldDraft{
		Disruptions: []DisruptionDraft{{Minutes: 60, Reason: "立刻开始工作", StartBlock: "work_0540"}},
	}, false, now)
	if len(plan.Changes) != 0 {
		t.Fatalf("changes = %+v, want the forced block start refused", plan.Changes)
	}
	if len(plan.Reasons) == 0 || !strings.Contains(plan.Reasons[0], "不能替她") {
		t.Fatalf("reasons = %v, want it to say she cannot be driven", plan.Reasons)
	}
}

func TestDirectorRejectsMustAttend(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	must := true

	plan := compileDraft(t, manager, WorldDraft{
		Arrangements: []DirectorArrangement{{
			CommitmentDraft: CommitmentDraft{Label: "考试", Date: now.AddDate(0, 0, 2).Format("2006-01-02")},
			MustAttend:      &must,
		}},
	}, false, now)
	if len(plan.Changes) != 0 {
		t.Fatalf("changes = %+v, want mustAttend refused", plan.Changes)
	}
	if len(plan.Reasons) == 0 || !strings.Contains(plan.Reasons[0], "非去不可") {
		t.Fatalf("reasons = %v, want the nothing-is-unmissable line", plan.Reasons)
	}
}

func TestDirectorRejectsBadDates(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)

	plan := compileDraft(t, manager, WorldDraft{
		Arrangements: []DirectorArrangement{
			{CommitmentDraft: CommitmentDraft{Label: "昨天", Date: now.AddDate(0, 0, -1).Format("2006-01-02")}},
			{CommitmentDraft: CommitmentDraft{Label: "太远", Date: now.AddDate(0, 0, 400).Format("2006-01-02")}},
			{CommitmentDraft: CommitmentDraft{Label: "没日期", Date: ""}},
		},
	}, false, now)
	if len(plan.Changes) != 0 {
		t.Fatalf("changes = %+v, want all three refused", plan.Changes)
	}
	if len(plan.Reasons) != 3 {
		t.Fatalf("reasons = %v, want one per refused arrangement", plan.Reasons)
	}
}

func TestDirectorWithNothingToSayIsRejectedNotSilentlyChat(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)

	plan := compileDraft(t, manager, WorldDraft{}, false, now)
	settled, commitments, effects, events := manager.applyDirectivePlan(plan, now)
	if settled.Status != DirectiveRejected {
		t.Fatalf("status = %s, want rejected", settled.Status)
	}
	if settled.Reason == "" {
		t.Fatal("a rejected directive must say why")
	}
	if len(commitments)+len(effects)+len(events) != 0 {
		t.Fatal("a rejected directive must not write anything into her world")
	}
}

func TestDirectorCapsAtThreeChanges(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	date := now.AddDate(0, 0, 1).Format("2006-01-02")

	plan := compileDraft(t, manager, WorldDraft{
		Arrangements: []DirectorArrangement{
			{CommitmentDraft: CommitmentDraft{Label: "甲", Date: date}},
			{CommitmentDraft: CommitmentDraft{Label: "乙", Date: date}},
			{CommitmentDraft: CommitmentDraft{Label: "丙", Date: date}},
			{CommitmentDraft: CommitmentDraft{Label: "丁", Date: date}},
		},
	}, false, now)
	if len(plan.Changes) != maxDirectiveChanges {
		t.Fatalf("changes = %d, want %d", len(plan.Changes), maxDirectiveChanges)
	}
	if len(plan.Reasons) == 0 {
		t.Fatal("the overflow should be reported, not dropped quietly")
	}
}

func TestRevertDirectiveTakesBackWhatItCan(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	today := now.Format("2006-01-02")

	plan := compileDraft(t, manager, WorldDraft{
		Arrangements: []DirectorArrangement{{CommitmentDraft: CommitmentDraft{
			Label: "看牙", Date: today, Start: "16:00", Duration: 60, Weight: 6,
		}}},
		MoodEffects: []DirectorMoodDraft{{Target: "moodValence", BaselineDelta: 20, DurationMinutes: 240}},
		Events:      []DirectorEventDraft{{Content: "路边看到一只很少见的鸟"}},
		Disruptions: []DisruptionDraft{{Minutes: 90, Severity: 7, Reason: "电梯坏了走楼梯"}},
	}, false, now)
	settled, commitments, effects, events := manager.applyDirectivePlan(plan, now)
	state, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if err := manager.store.CommitTick(TickCommit{
		ID: manager.ID(), State: state, Events: events, Commitments: commitments,
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := manager.store.SaveEffects(effects); err != nil {
		t.Fatalf("save effects: %v", err)
	}
	if err := manager.store.SaveDirective(manager.ID(), settled); err != nil {
		t.Fatalf("save directive: %v", err)
	}

	if err := manager.RevertDirective(settled.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}

	reverted, err := manager.store.Directive(manager.ID(), settled.ID)
	if err != nil {
		t.Fatalf("reload directive: %v", err)
	}
	if !reverted.Reverted() {
		t.Fatal("the ledger should record that this was taken back")
	}

	held, _ := manager.store.Commitments(manager.ID())
	if len(held) != 0 {
		t.Fatalf("commitments after revert = %+v, want none", held)
	}
	live, _ := manager.store.ActiveEffects()
	for _, effect := range live {
		if strings.HasPrefix(effect.SourceRuleID, directiveEffectPrefix+settled.ID) {
			t.Fatalf("director effect survived the revert: %+v", effect)
		}
	}
	remaining, _ := manager.store.RecentEvents(0, 50)
	if len(remaining) != 0 {
		t.Fatalf("events after revert = %+v, want none", remaining)
	}

	// The disruption stays on the record and stays un-revertible, so the ledger
	// keeps saying out loud which part cannot be undone.
	var sawDisruption bool
	for _, change := range reverted.Changes {
		if change.Kind == ChangeDisruption {
			sawDisruption = true
			if change.Revertible {
				t.Fatal("a disruption must never become revertible")
			}
		}
	}
	if !sawDisruption {
		t.Fatal("the disruption vanished from the ledger")
	}
}

func TestRevertArrangementDropsUnstartedBlockFromToday(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	today := now.Format("2006-01-02")
	profile, err := manager.Profile()
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	plan := compileDraft(t, manager, WorldDraft{
		Arrangements: []DirectorArrangement{{CommitmentDraft: CommitmentDraft{
			Label: "看牙", Date: today, Start: "16:00", Duration: 60, Weight: 6,
		}}},
	}, false, now)
	_, commitments, _, _ := manager.applyDirectivePlan(plan, now)
	state, _ := manager.store.State(manager.ID())
	if err := manager.store.CommitTick(TickCommit{
		ID: manager.ID(), State: state, Commitments: commitments,
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := manager.store.SaveDayPlan(manager.ID(), manager.dayPlanFor(profile, now)); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if !planHasBlockLabel(mustDayPlan(t, manager, today), "看牙") {
		t.Fatal("the arrangement's block never showed up in today's plan")
	}

	if err := manager.revertCommittedRow(commitments[0].ID); err != nil {
		t.Fatalf("revert row: %v", err)
	}
	if planHasBlockLabel(mustDayPlan(t, manager, today), "看牙") {
		t.Fatal("the unstarted block should be gone from the saved day")
	}
}

func TestDirectorNarrationIsWhatSheGetsNotTheCommand(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	plan := compileDraft(t, manager, WorldDraft{
		Arrangements: []DirectorArrangement{{CommitmentDraft: CommitmentDraft{
			Label: "妈妈来访", Date: tomorrow, Start: "15:00",
		}}},
		Narration: "她妈说明天要过来坐坐",
	}, false, now)
	settled, _, _, _ := manager.applyDirectivePlan(plan, now)

	profile, _ := manager.Profile()
	resolution := TurnResolution{DirectiveID: settled.ID, Narration: settled.Narration}
	context := formatTurnBriefContext(profile, DefaultState(now.UnixMilli()), TurnBrief{}, resolution, TriggerDirector, "让她妈明天来看她")
	if !strings.Contains(context, "【世界旁白】") {
		t.Fatal("director context should carry the narration tag")
	}
	if strings.Contains(context, "最新用户消息：让她妈明天来看她") {
		t.Fatal("the raw command must never reach her as a user message")
	}

	chat := formatTurnBriefContext(profile, DefaultState(now.UnixMilli()), TurnBrief{}, resolution, TriggerUser, "今天怎么样")
	if !strings.Contains(chat, "最新用户消息：今天怎么样") {
		t.Fatal("ordinary turns must still read the user message")
	}
}

// TestFailedDirectorTurnDoesNotLeaveTheLedgerPending guards I33: an instruction
// whose turn dies before settling must still close out as rejected, otherwise the
// ledger keeps a row that never says what happened to it.
func TestFailedDirectorTurnDoesNotLeaveTheLedgerPending(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	directiveID := newDirectiveID()

	if err := manager.store.SaveDirective(manager.ID(), DirectorDirective{
		ID: directiveID, CharacterID: manager.ID(), ThreadID: "thread_test",
		Text: "突然下起大雨", Status: DirectivePending, CreatedAt: now.UnixMilli(),
	}); err != nil {
		t.Fatalf("save directive: %v", err)
	}

	manager.failTurnTask(TurnTask{
		ID: "task_fail", ThreadID: "thread_test", Trigger: TriggerDirector,
		DirectiveID: directiveID, Status: TaskAligning,
	}, errors.New("尚未配置可用的陪伴模型"))

	stored, err := manager.store.Directive(manager.ID(), directiveID)
	if err != nil {
		t.Fatalf("directive: %v", err)
	}
	if stored.Status != DirectiveRejected {
		t.Fatalf("status = %s, want rejected", stored.Status)
	}
	if !strings.Contains(stored.Reason, "陪伴模型") {
		t.Fatalf("reason = %q, want the failure reason carried through", stored.Reason)
	}
}

// TestFailedTurnDoesNotClobberASettledDirective keeps the close-out honest: a
// directive that already landed and then tripped over a later step keeps the
// status it actually earned.
func TestFailedTurnDoesNotClobberASettledDirective(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	directiveID := newDirectiveID()

	if err := manager.store.SaveDirective(manager.ID(), DirectorDirective{
		ID: directiveID, CharacterID: manager.ID(), ThreadID: "thread_test",
		Text: "她妈明天来看她", Status: DirectiveApplied, CreatedAt: now.UnixMilli(),
		Changes: []DirectiveChange{{
			Kind: ChangeArrangement, Summary: "明天 15:00 妈妈来访", Revertible: true,
		}},
	}); err != nil {
		t.Fatalf("save directive: %v", err)
	}

	manager.failTurnTask(TurnTask{
		ID: "task_late_fail", ThreadID: "thread_test", Trigger: TriggerDirector,
		DirectiveID: directiveID, Status: TaskResolved,
	}, errors.New("生成回复失败"))

	stored, err := manager.store.Directive(manager.ID(), directiveID)
	if err != nil {
		t.Fatalf("directive: %v", err)
	}
	if stored.Status != DirectiveApplied {
		t.Fatalf("status = %s, want the earned applied status preserved", stored.Status)
	}
}

// TestDirectorTurnIsNotSomeoneShowingUp guards I32 from the other side: the
// social bookkeeping must not read a world edit as company. Rain falling because
// the user willed it does not make her less lonely, and it is not an interaction
// that resets how long it has been since anyone spoke to her.
func TestDirectorTurnIsNotSomeoneShowingUp(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	state, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	state.Metrics[MetricMissing] = 80
	state.Metrics[MetricLoneliness] = 70
	lastUser := now.Add(-48 * time.Hour).UnixMilli()
	state.LastUserAt = lastUser
	if err := manager.store.SaveState(manager.ID(), state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	task := &TurnTask{
		ID: "task_director_presence", ThreadID: "thread_test", Trigger: TriggerDirector,
		LastMessageAt: now.UnixMilli(),
		Inputs:        []TurnTaskInput{{Text: "突然下起大雨", At: now.UnixMilli()}},
	}
	brief := TurnBrief{Resolution: ResolutionDraft{
		World: &WorldDraft{Events: []DirectorEventDraft{{Content: "突然下起大雨"}}},
	}}
	if _, err := manager.applyTurnBrief(task, brief, now); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if after.LastUserAt != lastUser {
		t.Fatalf("LastUserAt moved to %d; a directive is not an interaction", after.LastUserAt)
	}
	if after.Metric(MetricLoneliness) >= 70 {
		return
	}
	t.Fatalf("loneliness dropped to %.1f; a directive must not buy down being alone", after.Metric(MetricLoneliness))
}

func TestUserTurnStillCountsAsSomeoneShowingUp(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.Local)
	manager := directorTestManager(t)
	state, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	state.LastUserAt = now.Add(-48 * time.Hour).UnixMilli()
	if err := manager.store.SaveState(manager.ID(), state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	task := &TurnTask{
		ID: "task_user_presence", ThreadID: "thread_test", Trigger: TriggerUser,
		LastMessageAt: now.UnixMilli(), Inputs: []TurnTaskInput{{Text: "在吗", At: now.UnixMilli()}},
	}
	if _, err := manager.applyTurnBrief(task, TurnBrief{}, now); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, _ := manager.store.State(manager.ID())
	if after.LastUserAt != now.UnixMilli() {
		t.Fatalf("LastUserAt = %d, want the user turn to land", after.LastUserAt)
	}
}
func planHasBlockLabel(plan DayPlan, label string) bool {
	for _, block := range plan.Blocks {
		if block.Label == label {
			return true
		}
	}
	return false
}

func mustDayPlan(t *testing.T, manager *Manager, date string) DayPlan {
	t.Helper()
	plan, ok, err := manager.store.DayPlan(manager.ID(), date)
	if err != nil {
		t.Fatalf("day plan %s: %v", date, err)
	}
	if !ok {
		t.Fatalf("no saved plan for %s", date)
	}
	return plan
}
