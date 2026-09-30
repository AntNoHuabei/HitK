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

func TestLiveSceneOverridesBusySchedule(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 6},
		blockSpec{kind: "rest", label: "在家待着", start: "17:00", end: "23:00", weight: 2},
	)
	resolve := busyResolver(t, plan, at)

	// 日程上这格是工作时间，本来要推到下一个空档。
	state := DefaultState(at.UnixMilli())
	if available, _, notBefore := taskAvailability(state, resolve, at); available || notBefore <= at.UnixMilli() {
		t.Fatalf("expected a deferral, got available=%v notBefore=%d", available, notBefore)
	}

	// 但亲密场景 10 分钟前才起来，人就在这段对话里，必须立刻能回。
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = at.Add(-10 * time.Minute).UnixMilli()
	available, activity, notBefore := taskAvailability(state, resolve, at)
	if !available {
		t.Fatal("live scene should make her available regardless of the schedule")
	}
	if notBefore != 0 {
		t.Fatalf("notBefore = %d, want 0", notBefore)
	}
	if activity != "亲密中" {
		t.Fatalf("activity = %q, want 亲密中", activity)
	}

	// 过了 3 小时衰减窗口就不再算数，回到日程判断。
	state.SceneStartedAt = at.Add(-4 * time.Hour).UnixMilli()
	if available, _, _ := taskAvailability(state, resolve, at); available {
		t.Fatal("scene past its decay window should not force availability")
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
