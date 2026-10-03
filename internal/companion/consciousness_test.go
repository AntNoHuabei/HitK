package companion

import (
	"context"
	"testing"
	"time"
)

// nightPlan is a tail bedtime, the shape a real day ends with: sleep from 22:00 to
// midnight. StartedAt is set by the caller so the test controls how long she has
// been lying there.
func nightPlan(t *testing.T, startedAt time.Time) DayPlan {
	t.Helper()
	plan := mustPlan(t, blockSpec{kind: "sleep", label: "睡觉", start: "22:00", end: "24:00", weight: 10})
	plan.Blocks[0].StartedAt = startedAt.UnixMilli()
	return plan
}

func TestNotAsleepTheInstantSheGetsIntoBed(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-5*time.Minute))
	if got := consciousnessAt(DefaultState(at.UnixMilli()), plan, at); got != ConsciousAwake {
		t.Fatalf("five minutes in bed should still be awake, got %s", got)
	}
}

func TestGoesUnderOnceOnsetIsPaid(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-40*time.Minute))
	if got := consciousnessAt(DefaultState(at.UnixMilli()), plan, at); got != ConsciousAsleep {
		t.Fatalf("forty minutes in bed should be asleep, got %s", got)
	}
}

// TestWiredTakesLongerToGoUnder is where "lay down and could not sleep" comes from:
// flow stretches the onset out, and because flow only decays in bed she still goes
// under eventually, just much later.
func TestWiredTakesLongerToGoUnder(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	state := DefaultState(at.UnixMilli())
	state.Metrics[MetricFlow] = 50
	plan := nightPlan(t, at.Add(-60*time.Minute))
	if got := consciousnessAt(state, plan, at); got != ConsciousAwake {
		t.Fatalf("flow 50 should stretch onset past an hour, got %s", got)
	}

	later := at.Add(20 * time.Minute)
	laterPlan := nightPlan(t, later.Add(-80*time.Minute))
	if got := consciousnessAt(state, laterPlan, later); got != ConsciousAsleep {
		t.Fatalf("eighty minutes in should be asleep even wired, got %s", got)
	}
}

func TestWakesWhenTheSleepBlockEnds(t *testing.T) {
	at := time.Date(2030, 10, 1, 8, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "sleep", label: "睡觉", start: "00:00", end: "07:30", weight: 10},
		blockSpec{kind: "open", label: "早上空着", start: "07:30", end: "12:00", weight: 1},
	)
	plan.markStarted("b_0", at.Add(-9*time.Hour))
	state := DefaultState(at.UnixMilli())
	state.AsleepSince = at.Add(-8 * time.Hour).UnixMilli()
	state.AsleepBlockID = "b_0"
	if got := consciousnessAt(state, plan, at); got != ConsciousAwake {
		t.Fatalf("a finished sleep block must not leave her asleep, got %s", got)
	}
}

// TestSleepSurvivesTheDayBoundary guards the worst stale-state failure: she went to
// sleep at 23:40 and the day rolled over. Without the carry she would surface at
// 00:00 and be reachable in the middle of the night.
func TestSleepSurvivesTheDayBoundary(t *testing.T) {
	prev := mustPlan(t,
		blockSpec{kind: "open", label: "晚上空着", start: "19:00", end: "23:00", weight: 1},
		blockSpec{kind: "sleep", label: "该睡了", start: "23:00", end: "24:00", weight: 10},
	)
	prev.markStarted("b_1", time.Date(2030, 9, 30, 23, 40, 0, 0, time.Local))

	today := mustPlan(t,
		blockSpec{kind: "sleep", label: "还在睡", start: "00:00", end: "07:30", weight: 10},
		blockSpec{kind: "open", label: "早上空着", start: "07:30", end: "12:00", weight: 1},
	)
	today.carrySleepFrom(prev)

	at := time.Date(2030, 10, 1, 0, 30, 0, 0, time.Local)
	if got := consciousnessAt(DefaultState(at.UnixMilli()), today, at); got != ConsciousAsleep {
		t.Fatalf("sleep carried over midnight should still hold her, got %s", got)
	}
}

func TestFallingAsleepEndsTheScene(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-40*time.Minute))
	state := DefaultState(at.UnixMilli())
	state.SceneStage = "active"
	state.ConsentState = "affirmed"

	settleConsciousness(&state, plan, at)

	if state.Consciousness != ConsciousAsleep {
		t.Fatalf("want asleep, got %s", state.Consciousness)
	}
	if state.SceneStage != "none" || state.ConsentState != "not_applicable" {
		t.Fatalf("falling asleep should close the scene, got stage=%q consent=%q", state.SceneStage, state.ConsentState)
	}
	if state.AsleepSince == 0 || state.AsleepBlockID == "" {
		t.Fatalf("latch not set: since=%d block=%q", state.AsleepSince, state.AsleepBlockID)
	}
}

func TestWakingClearsTheLatch(t *testing.T) {
	at := time.Date(2030, 10, 1, 8, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "open", label: "早上空着", start: "07:30", end: "12:00", weight: 1},
	)
	state := DefaultState(at.UnixMilli())
	state.AsleepSince = at.Add(-8 * time.Hour).UnixMilli()
	state.AsleepBlockID = "sleep_0000"

	settleConsciousness(&state, plan, at)

	if state.Consciousness != ConsciousAwake {
		t.Fatalf("want awake, got %s", state.Consciousness)
	}
	if state.AsleepSince != 0 || state.AsleepBlockID != "" {
		t.Fatalf("latch not cleared: since=%d block=%q", state.AsleepSince, state.AsleepBlockID)
	}
}

func TestAsleepBlocksReplyAndNamesTheWakeTime(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-40*time.Minute))
	// Her bedtime runs to the end of the day, so midnight is not her alarm. The
	// night continues into tomorrow's plan and the alarm lives in that opening
	// sleep block. Printing 00:00 would park a message on a promise that is false
	// the moment it is shown.
	tomorrow := mustPlan(t,
		blockSpec{kind: "sleep", label: "还在睡", start: "00:00", end: "07:30", weight: 10},
		blockSpec{kind: "open", label: "早上空着", start: "07:30", end: "12:00", weight: 1},
	)
	resolve := func(day time.Time) DayPlan {
		if day.Day() == at.Day() {
			return plan
		}
		return tomorrow
	}

	available, activity, notBefore := taskAvailability(DefaultState(at.UnixMilli()), resolve, at)
	if available {
		t.Fatal("asleep must not be available")
	}
	if activity != "她在睡觉" {
		t.Fatalf("activity = %q, want 她在睡觉", activity)
	}
	want := time.Date(2030, 10, 1, 7, 30, 0, 0, time.Local).UnixMilli()
	if notBefore != want {
		t.Fatalf("wake time = %v, want tomorrow's alarm %v",
			time.UnixMilli(notBefore).Format("01-02 15:04"), time.UnixMilli(want).Format("01-02 15:04"))
	}
}

// TestWakesInsideTheDayFromTheOpeningBlock covers the other half: when the sleep
// block ends before midnight the alarm is same-day, no lookup into tomorrow.
func TestWakesInsideTheDayFromTheOpeningBlock(t *testing.T) {
	at := time.Date(2030, 10, 1, 2, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "sleep", label: "还在睡", start: "00:00", end: "07:30", weight: 10},
		blockSpec{kind: "open", label: "早上空着", start: "07:30", end: "12:00", weight: 1},
	)
	plan.markStarted("b_0", at.Add(-2*time.Hour))

	_, _, notBefore := taskAvailability(DefaultState(at.UnixMilli()), func(time.Time) DayPlan { return plan }, at)
	want := time.Date(2030, 10, 1, 7, 30, 0, 0, time.Local).UnixMilli()
	if notBefore != want {
		t.Fatalf("wake time = %v, want %v",
			time.UnixMilli(notBefore).Format("01-02 15:04"), time.UnixMilli(want).Format("01-02 15:04"))
	}
}

// TestOpeningSleepStartsUnclaimed is the root of the midnight bug: a freshly built
// day used to come with its opening sleep already started, so every new day began
// with her "asleep" from 00:00 whether she was or not. Only carrySleepFrom, which
// reads how the day before actually ended, gets to start it.
func TestOpeningSleepStartsUnclaimed(t *testing.T) {
	now := time.Date(2030, 9, 30, 21, 0, 0, 0, time.Local)
	profile := DefaultProfile(now.UnixMilli())
	plan := buildDayPlan(profile, now, defaultCommitments(profile))
	if len(plan.Blocks) == 0 {
		t.Fatal("empty plan")
	}
	head := plan.Blocks[0]
	if head.Kind != "sleep" {
		t.Fatalf("head block kind = %q, want sleep", head.Kind)
	}
	if head.StartedAt != 0 {
		t.Fatalf("opening sleep came pre-started at %d; only carrySleepFrom may start it", head.StartedAt)
	}
}

// TestMidnightAloneDoesNotKnockHerOut is the failure a user actually sees: she is
// still awake chatting when the day rolls over, and crossing 00:00 must not turn
// into "她在睡觉" half an hour later with the phone hard-blocked until morning.
func TestMidnightAloneDoesNotKnockHerOut(t *testing.T) {
	manager := openTestManager(t)
	profile, err := manager.store.Profile(manager.ID())
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	prev := mustPlan(t,
		blockSpec{kind: "open", label: "晚上空着", start: "18:00", end: "23:00", weight: 1},
		blockSpec{kind: "sleep", label: "该睡了", start: "23:00", end: "24:00", weight: 10},
	)
	prev.Date = "2030-09-30"
	if err := manager.store.SaveDayPlan(manager.ID(), prev); err != nil {
		t.Fatalf("save prev: %v", err)
	}
	today := mustPlan(t,
		blockSpec{kind: "sleep", label: "还在睡", start: "00:00", end: "07:30", weight: 10},
		blockSpec{kind: "open", label: "早上空着", start: "07:30", end: "12:00", weight: 1},
	)
	today.Date = "2030-10-01"
	if err := manager.store.SaveDayPlan(manager.ID(), today); err != nil {
		t.Fatalf("save today: %v", err)
	}

	at := time.Date(2030, 10, 1, 0, 40, 0, 0, time.Local)
	state := DefaultState(at.UnixMilli())
	// She is not sleepy yet and she is busy talking, which is exactly the case the
	// pre-started block used to steamroll.
	state.Metrics[MetricSleepPressure] = 30
	state.Metrics[MetricFlow] = 60

	if got, _ := manager.consciousnessNow(profile, state, at); got != ConsciousAwake {
		t.Fatalf("consciousness = %s; crossing midnight alone must not put her to sleep", got)
	}
	if available, activity, _ := taskAvailability(state, manager.resolver(profile), at); !available {
		t.Fatalf("blocked at %q after midnight without ever starting bedtime", activity)
	}
}

// TestFreshPlanAfterMidnightKeepsHerReachable runs the whole path rather than a
// hand-built plan: nothing is stored, so planAt builds today from the template, and
// a low-flow, low-pressure state at 00:40 must leave her awake and reachable. The
// pre-started opening block made this exact state read as asleep twenty minutes
// after midnight, and worse, updateSleepLedger saw a started sleep and paid debt
// down instead of charging it, so staying up cured her of being tired.
func TestFreshPlanAfterMidnightKeepsHerReachable(t *testing.T) {
	manager := openTestManager(t)
	profile, err := manager.store.Profile(manager.ID())
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	at := time.Date(2030, 10, 1, 0, 40, 0, 0, time.Local)
	state := DefaultState(at.UnixMilli())
	state.Metrics[MetricSleepPressure] = 20
	state.Metrics[MetricFatigue] = 15
	state.Metrics[MetricFlow] = 5
	state.LastTickAt = at.Add(-15 * time.Minute).UnixMilli()
	if err := manager.store.SaveState(manager.ID(), state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	if got, _ := manager.consciousnessNow(profile, state, at); got != ConsciousAwake {
		t.Fatalf("consciousness = %s; she never started bedtime", got)
	}
	if available, activity, _ := taskAvailability(state, manager.resolver(profile), at); !available {
		t.Fatalf("blocked at %q while still awake after midnight", activity)
	}

	if err := manager.TickNow(context.Background(), at); err != nil {
		t.Fatalf("tick: %v", err)
	}
	after, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.SleepDebtMinutes <= 0 {
		t.Fatalf("sleep debt = %d; being awake past midnight should charge it, not repay it", after.SleepDebtMinutes)
	}
}

// TestSleepCarriedIntoTheNewDayWakesTomorrow is the mirror case: she really did
// start bedtime before midnight, so the new day inherits it and the alarm is read
// from the carried block rather than from 00:00.
func TestSleepCarriedIntoTheNewDayWakesTomorrow(t *testing.T) {
	manager := openTestManager(t)
	profile, err := manager.store.Profile(manager.ID())
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	prev := mustPlan(t,
		blockSpec{kind: "open", label: "晚上空着", start: "18:00", end: "23:00", weight: 1},
		blockSpec{kind: "sleep", label: "该睡了", start: "23:00", end: "24:00", weight: 10},
	)
	prev.Date = "2030-09-30"
	prev.markStarted("b_1", time.Date(2030, 9, 30, 23, 40, 0, 0, time.Local))
	if err := manager.store.SaveDayPlan(manager.ID(), prev); err != nil {
		t.Fatalf("save prev: %v", err)
	}
	today := mustPlan(t,
		blockSpec{kind: "sleep", label: "还在睡", start: "00:00", end: "07:30", weight: 10},
		blockSpec{kind: "open", label: "早上空着", start: "07:30", end: "12:00", weight: 1},
	)
	today.Date = "2030-10-01"
	if err := manager.store.SaveDayPlan(manager.ID(), today); err != nil {
		t.Fatalf("save today: %v", err)
	}

	at := time.Date(2030, 10, 1, 0, 40, 0, 0, time.Local)
	got, wakeAt := manager.consciousnessNow(profile, DefaultState(at.UnixMilli()), at)
	if got != ConsciousAsleep {
		t.Fatalf("consciousness = %s, want asleep", got)
	}
	if wakeAt.Format("01-02 15:04") != "10-01 07:30" {
		t.Fatalf("wake = %s, want the carried block end 10-01 07:30", wakeAt.Format("01-02 15:04"))
	}

	// The carry has to land on disk, otherwise the next read forgets it again.
	stored, ok, err := manager.store.DayPlan(manager.ID(), "2030-10-01")
	if err != nil || !ok {
		t.Fatalf("reload today: ok=%v err=%v", ok, err)
	}
	if stored.Blocks[0].StartedAt == 0 {
		t.Fatal("carried sleep was not persisted")
	}
}

// TestLyingInBedStillReplies keeps the reachable half of bedtime: she is in the
// sleep block, but she has not gone under, so the phone still works.
func TestLyingInBedStillReplies(t *testing.T) {
	at := time.Date(2030, 9, 30, 22, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-3*time.Minute))
	resolve := func(time.Time) DayPlan { return plan }

	available, activity, _ := taskAvailability(DefaultState(at.UnixMilli()), resolve, at)
	if !available {
		t.Fatalf("awake in bed should still reply, activity=%q", activity)
	}
	if activity != "躺床上还没睡着" {
		t.Fatalf("activity = %q, want 躺床上还没睡着", activity)
	}
}

func TestAsleepNeverReleasedEarly(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-40*time.Minute))
	resolve := func(time.Time) DayPlan { return plan }

	if released, _ := earlyRelease(DefaultState(at.UnixMilli()), resolve, at); released {
		t.Fatal("a parked message must not escape a sleeping character")
	}
}

// TestSleepBeatsALiveScene closes the gap I6 leaves open. settleConsciousness clears
// the scene when a tick puts her under, but a scene can still be staged while she is
// asleep, and if availability read scene-first the screen would show her asleep, no
// busy card, a greyed force button -- and still answer him. Unconscious beats
// narrative, every time.
func TestSleepBeatsALiveScene(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-40*time.Minute))
	resolve := func(time.Time) DayPlan { return plan }

	state := DefaultState(at.UnixMilli())
	state.SceneStage = "active"
	state.SceneStartedAt = at.Add(-10 * time.Minute).UnixMilli()
	if !sceneLive(state, at) {
		t.Fatal("setup: the scene should read as live")
	}

	a := evaluate(state, resolve, at)
	if a.Reason != ReasonAsleep {
		t.Fatalf("reason = %s; a live scene must not outrank being unconscious", a.Reason)
	}
	if a.Reachable || !a.Blocked {
		t.Fatalf("reachable=%v blocked=%v while she is asleep", a.Reachable, a.Blocked)
	}
}

// TestTurnCannotOpenASceneWhileSheIsAsleep guards the same invariant at its source:
// the alignment model must not get to stage a scene into an unconscious character.
func TestTurnCannotOpenASceneWhileSheIsAsleep(t *testing.T) {
	manager := openTestManager(t)
	at := time.Date(2030, 10, 1, 1, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "sleep", label: "还在睡", start: "00:00", end: "08:00", weight: 10},
	)
	plan.Date = "2030-10-01"
	plan.markStarted("b_0", at.Add(-time.Hour))
	if err := manager.store.SaveDayPlan(manager.ID(), plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}

	task := &TurnTask{
		ID: "task_asleep_scene", ThreadID: "thread_test", Trigger: TriggerDirector,
		LastMessageAt: at.UnixMilli(), Inputs: []TurnTaskInput{{Text: "突然下起大雨", At: at.UnixMilli()}},
	}
	brief := TurnBrief{Resolution: ResolutionDraft{SceneStage: "active", ConsentState: "enthusiastic"}}
	if _, err := manager.applyTurnBrief(task, brief, at); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.SceneStage != "" && after.SceneStage != "none" {
		t.Fatalf("scene stage = %q; nothing gets staged while she is unconscious", after.SceneStage)
	}
}

func TestForceReplyRefusedWhileAsleep(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	seedDay(t, manager, at,
		blockSpec{kind: "sleep", label: "睡觉", start: "22:00", end: "24:00", weight: 10},
	)
	startBlockAt(t, manager, at.Add(-40*time.Minute))
	pinClock(t, manager, at)

	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "睡了吗", "", "", "", at)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskWaiting || task.CurrentActivity != "她在睡觉" {
		t.Fatalf("want a parked sleeping task, got status=%s activity=%q", task.Status, task.CurrentActivity)
	}

	if err := manager.forceReplyAt(task.ID, at); err == nil {
		t.Fatal("forcing a sleeping character must be refused")
	}
	after, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Forced {
		t.Fatal("refused force still set the forced flag")
	}
}
