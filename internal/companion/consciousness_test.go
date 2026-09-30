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
	resolve := func(time.Time) DayPlan { return plan }

	available, activity, notBefore := taskAvailability(DefaultState(at.UnixMilli()), resolve, at)
	if available {
		t.Fatal("asleep must not be available")
	}
	if activity != "她在睡觉" {
		t.Fatalf("activity = %q, want 她在睡觉", activity)
	}
	want := time.Date(2030, 9, 30, 24, 0, 0, 0, time.Local).UnixMilli()
	if notBefore != want {
		t.Fatalf("wake time = %v, want the block end %v",
			time.UnixMilli(notBefore).Format("15:04"), time.UnixMilli(want).Format("15:04"))
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
