package companion

import (
	"context"
	"errors"
	"testing"
	"time"
)

// busyResolver serves a hand-built day with the block at `at` already begun. That
// is what the tick loop produces over time, and availability now turns on whether
// she has actually started something rather than on what the clock says she is
// scheduled for, so a test that wants her busy has to put her inside it.
func busyResolver(t *testing.T, plan DayPlan, at time.Time) planResolver {
	t.Helper()
	block := plan.blockAt(at)
	if block.Kind == "open" {
		t.Fatalf("no committed block at %s to be busy with", at.Format("15:04"))
	}
	if !plan.markStarted(block.ID, at) {
		t.Fatalf("could not start block %q", block.ID)
	}
	return func(time.Time) DayPlan { return plan }
}

func TestTaskAvailabilityWaitsForNextFreeBlock(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 6},
		blockSpec{kind: "rest", label: "在家待着", start: "17:00", end: "23:00", weight: 2},
	)
	available, activity, notBefore := taskAvailability(DefaultState(at.UnixMilli()), busyResolver(t, plan, at), at)
	if available {
		t.Fatal("a block she has actually started should hold her")
	}
	want := time.Date(2026, 9, 28, 17, 0, 0, 0, time.Local).UnixMilli()
	if activity == "" || notBefore != want {
		t.Fatalf("activity=%q notBefore=%d, want %d", activity, notBefore, want)
	}
}

func TestUnstartedHeavyBlockLeavesHerReachable(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	// The same heavy block, but she has not begun it. That is putting it off, which
	// is exactly when she picks up the phone, and it is the whole reason the old
	// kind table was replaced.
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 8},
	)
	resolve := func(time.Time) DayPlan { return plan }
	if available, _, _ := taskAvailability(DefaultState(at.UnixMilli()), resolve, at); !available {
		t.Fatal("a heavy block she has not started should leave her reachable")
	}
}

// TestBusyPromisePointsAtReliablyFreeBlock: while she is working, the time she is
// promised is the next block that is light enough to stay free. Pointing at the
// unstarted lunch instead promised 12:45 and delivered 17:00, because by 12:45 she
// had started the afternoon and the promise was worthless.
func TestBusyPromisePointsAtReliablyFreeBlock(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "12:00", weight: 6},
		blockSpec{kind: "meal", label: "吃午饭", start: "12:00", end: "12:45", weight: 6},
		blockSpec{kind: "work", label: "下午上班", start: "12:45", end: "17:00", weight: 6},
		blockSpec{kind: "open", label: "晚上空着", start: "17:00", end: "23:00", weight: 1},
	)
	plan.markStarted("b_0", at)
	resolve := func(time.Time) DayPlan { return plan }

	available, activity, notBefore := taskAvailability(DefaultState(at.UnixMilli()), resolve, at)
	if available {
		t.Fatal("a started work block should hold her")
	}
	want := time.Date(2026, 9, 28, 17, 0, 0, 0, time.Local).UnixMilli()
	if notBefore != want {
		t.Fatalf("promised %v, want the 17:00 open block (%v); activity=%q",
			time.UnixMilli(notBefore).Format("15:04"), time.UnixMilli(want).Format("15:04"), activity)
	}
}

// TestAvailabilityNeverEscapesToIdleWhenNothingIsFree: a day with no light block
// must keep her parked rather than falling through the bottom into "空闲".
func TestAvailabilityNeverEscapesToIdleWhenNothingIsFree(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 8},
	)
	plan.markStarted("b_0", at)
	resolve := func(time.Time) DayPlan { return plan }

	available, activity, notBefore := taskAvailability(DefaultState(at.UnixMilli()), resolve, at)
	if available {
		t.Fatalf("a heavy started block with nothing light after it should keep her parked, got activity=%q", activity)
	}
	if notBefore <= at.UnixMilli() {
		t.Fatalf("notBefore = %d, want a retry point ahead of now", notBefore)
	}
}

func TestLiveSceneOverridesBusySchedule(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 8},
	)
	plan.markStarted("b_0", at)
	resolve := func(time.Time) DayPlan { return plan }

	if available, _, _ := taskAvailability(DefaultState(at.UnixMilli()), resolve, at); available {
		t.Fatal("a started heavy block should hold her")
	}
	state := DefaultState(at.UnixMilli())
	state.SceneStage = "active"
	state.SceneStartedAt = at.Add(-10 * time.Minute).UnixMilli()
	if available, activity, _ := taskAvailability(state, resolve, at); !available {
		t.Fatalf("a live scene should beat the schedule, activity=%q", activity)
	}
}

// TestParkedMessageKeepsItsPromise walks the minute ticker forward from the
// moment she said "busy until X" and fails if it ever lets her write before X.
// The ticker re-judges availability every minute, and the live rule counts a
// heavy block she has not started yet as free time, so crossing into the next
// unstarted block pulled a parked message out well before the time she named.
func TestParkedMessageKeepsItsPromise(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	pinned := fixedBusyHour
	seedDay(t, manager, pinned,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "12:00", weight: 6},
		blockSpec{kind: "meal", label: "吃午饭", start: "12:00", end: "13:00", weight: 6},
		blockSpec{kind: "work", label: "下午上班", start: "13:00", end: "17:00", weight: 6},
		blockSpec{kind: "open", label: "晚上空着", start: "17:00", end: "23:00", weight: 1},
	)
	startBlockAt(t, manager, pinned)
	pinClock(t, manager, pinned)

	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "在吗", "", "", "", pinned)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskWaiting || task.NotBefore <= pinned.UnixMilli() {
		t.Fatalf("want a parked task, got status=%s notBefore=%s",
			task.Status, time.UnixMilli(task.NotBefore).Format("15:04"))
	}
	promise := time.UnixMilli(task.NotBefore)
	dumpDay(t, manager, pinned)

	for step := pinned.Add(time.Minute); step.Before(promise); step = step.Add(time.Minute) {
		manager.processDueTurnTasks(context.Background(), step)
		current, err := manager.store.TurnTask(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != TaskWaiting {
			t.Fatalf("ticker released her at %s (status=%s), promise was %s",
				step.Format("15:04"), current.Status, promise.Format("15:04"))
		}
		if current.NotBefore < promise.UnixMilli() {
			t.Fatalf("ticker pulled the promise in from %s to %s at %s",
				promise.Format("15:04"), time.UnixMilli(current.NotBefore).Format("15:04"), step.Format("15:04"))
		}
	}
}

// TestTickerStillReleasesForALiveScene keeps the one early release that matters:
// if a scene starts while she is parked, she is back in this conversation and the
// parked message goes out now rather than at the time named hours ago.
func TestTickerStillReleasesForALiveScene(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	pinned := fixedBusyHour
	seedDay(t, manager, pinned,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 8},
		blockSpec{kind: "open", label: "晚上空着", start: "17:00", end: "23:00", weight: 1},
	)
	startBlockAt(t, manager, pinned)
	pinClock(t, manager, pinned)

	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "在吗", "", "", "", pinned)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskWaiting || task.NotBefore <= pinned.UnixMilli() {
		t.Fatalf("want a parked task, got status=%s", task.Status)
	}

	later := pinned.Add(time.Hour)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = later.Add(-5 * time.Minute).UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}

	manager.processDueTurnTasks(context.Background(), later)
	current, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.NotBefore > later.UnixMilli() {
		t.Fatalf("live scene did not release the parked task: notBefore=%s",
			time.UnixMilli(current.NotBefore).Format("15:04"))
	}
}

// TestTickerReleasesIntoALightBlock keeps the other honest early release: drift
// moved her into an open stretch ahead of the promised time, so waiting longer
// would be pointless.
func TestTickerReleasesIntoALightBlock(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	pinned := fixedBusyHour
	seedDay(t, manager, pinned,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "12:00", weight: 8},
		blockSpec{kind: "open", label: "中午空着", start: "12:00", end: "13:00", weight: 1},
		blockSpec{kind: "work", label: "下午上班", start: "13:00", end: "18:00", weight: 8},
	)
	startBlockAt(t, manager, pinned)
	pinClock(t, manager, pinned)

	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "在吗", "", "", "", pinned)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskWaiting || task.NotBefore <= pinned.UnixMilli() {
		t.Fatalf("want a parked task, got status=%s", task.Status)
	}

	light := pinned.Add(2 * time.Hour)
	manager.processDueTurnTasks(context.Background(), light)
	current, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.NotBefore > light.UnixMilli() {
		t.Fatalf("an open block should have released her: notBefore=%s",
			time.UnixMilli(current.NotBefore).Format("15:04"))
	}
}

// dumpDay prints the live plan so a failing availability assertion can be read
// against the blocks that actually produced it.
func dumpDay(t *testing.T, manager *Manager, at time.Time) {
	t.Helper()
	profile, err := manager.store.Profile(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	plan := manager.planAt(profile, at)
	t.Logf("plan %s rev=%d", plan.Date, plan.Revision)
	for _, block := range plan.Blocks {
		started := "unstarted"
		if block.StartedAt > 0 {
			started = "started"
		}
		t.Logf("  %02d:%02d-%02d:%02d w=%-2d %-8s %s %s",
			block.StartMinute/60, block.StartMinute%60, block.EndMinute/60, block.EndMinute%60,
			block.Weight, block.Kind, block.Label, started)
	}
}

func TestAlignmentFailureUsesConservativeBrief(t *testing.T) {
	manager := openTestManager(t)
	model := &fakeModel{alignErr: errors.New("model unavailable")}
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(model, conversation)
	pinned := fixedMorning
	pinClock(t, manager, pinned)
	task, err := manager.submitUserTurnAt(
		context.Background(),
		"th_companion",
		"我们继续刚才的事",
		"", "", "",
		pinned,
	)
	if err != nil {
		t.Fatal(err)
	}
	updated := processTaskNow(t, manager, task)
	if updated.Brief == nil || len(updated.Brief.Warnings) == 0 {
		t.Fatalf("fallback brief missing warning: %+v", updated.Brief)
	}
	if updated.Resolution != nil && len(updated.Resolution.MetricDeltas) != 0 {
		t.Fatalf("fallback applied semantic deltas: %+v", updated.Resolution.MetricDeltas)
	}
}

func TestSceneAdvancesByStrictWallClock(t *testing.T) {
	now := time.Date(2026, 9, 28, 20, 0, 0, 0, time.Local)
	state := DefaultState(now.UnixMilli())
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = now.Add(-50 * time.Minute).UnixMilli()
	if !advanceSceneByWallClock(&state, now) {
		t.Fatal("active scene did not advance")
	}
	if state.SceneStage != "aftercare" {
		t.Fatalf("stage = %q, want aftercare", state.SceneStage)
	}
	state.SceneStartedAt = now.Add(-4 * time.Hour).UnixMilli()
	advanceSceneByWallClock(&state, now)
	if state.SceneStage != "none" || state.ConsentState != "not_applicable" {
		t.Fatalf("expired scene = %s/%s", state.SceneStage, state.ConsentState)
	}
}

func TestWaitingTaskCoalescesMessages(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(&fakeModel{}, conversation)
	profile, _ := manager.store.Profile(manager.id)
	profile.ScheduleTendency = "busy"
	_ = manager.store.SaveProfile(profile)

	now := fixedBusyHour
	state, _ := manager.store.State(manager.id)
	state.LastTickAt = now.UnixMilli()
	_ = manager.store.SaveState(manager.id, state)

	first, err := manager.submitUserTurnAt(context.Background(), "th_companion", "第一条", "", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != TaskWaiting {
		// If the test happens to run in an available minute, force the task back
		// into waiting so coalescing is still exercised deterministically.
		t.Fatalf("task status = %s, want waiting", first.Status)
	}
	second, err := manager.submitUserTurnAt(context.Background(), "th_companion", "第二条", "", "", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Inputs) != 2 {
		t.Fatalf("inputs = %d, want 2", len(second.Inputs))
	}
	conversation.mu.Lock()
	messages := append([]string(nil), conversation.messages...)
	conversation.mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("persisted messages = %#v", messages)
	}
}

func TestForceReplyPullsWaitingTaskOutOfSchedule(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(&fakeModel{}, conversation)
	profile, err := manager.store.Profile(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	profile.ScheduleTendency = "busy"
	if err := manager.store.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	pinned := fixedBusyHour
	pinClock(t, manager, pinned)
	// She is genuinely mid-way through the work block, not merely scheduled for it.
	startBlockAt(t, manager, pinned)

	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "在吗", "", "", "", pinned)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskWaiting || task.NotBefore <= pinned.UnixMilli() {
		t.Fatalf("want a parked task, got status=%s notBefore=%d", task.Status, task.NotBefore)
	}

	if err := manager.ForceReply(task.ID); err != nil {
		t.Fatal(err)
	}
	forced, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !forced.Forced {
		t.Fatal("forced flag not set")
	}
	if forced.NotBefore > pinned.UnixMilli() {
		t.Fatalf("notBefore = %d, want pulled forward to the pinned instant", forced.NotBefore)
	}
	if forced.AvailabilityReason != "" {
		t.Fatalf("availability reason still set: %q", forced.AvailabilityReason)
	}

	// 关键一关：同一个忙碌时刻再跑一遍，也不该被日程重新塞回等待。
	manager.processTurnTaskAt(context.Background(), task.ID, pinned)
	processed, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if processed.Status == TaskWaiting {
		t.Fatalf("forced task got re-parked by the schedule: %+v", processed)
	}
}

func TestForceReplyIgnoresNonWaitingTask(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(&fakeModel{}, conversation)
	pinned := fixedMorning
	pinClock(t, manager, pinned)

	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "在吗", "", "", "", pinned)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status == TaskWaiting {
		t.Skip("task parked at a morning instant; nothing to assert")
	}
	before, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ForceReply(task.ID); err != nil {
		t.Fatal(err)
	}
	after, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Forced {
		t.Fatalf("force leaked onto a %s task", after.Status)
	}
	if after.Status != before.Status {
		t.Fatalf("status changed %s -> %s", before.Status, after.Status)
	}
}
