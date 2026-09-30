package companion

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskAvailabilityWaitsForNextFreeBlock(t *testing.T) {
	profile := DefaultProfile(time.Now().UnixMilli())
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	available, activity, notBefore := taskAvailability(profile, at)
	if available {
		t.Fatal("balanced schedule should be unavailable at 10:00")
	}
	if activity == "" || notBefore <= at.UnixMilli() {
		t.Fatalf("activity=%q notBefore=%d", activity, notBefore)
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
