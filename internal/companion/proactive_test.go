package companion

import (
	"context"
	"testing"
	"time"
)

func TestProactiveBackoffCurve(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.Local)

	// Nothing outstanding: no extra wait.
	if allowed, _ := proactiveBackoff(0, 0, now); !allowed {
		t.Fatal("an even conversation should not be held back")
	}

	// One unanswered, sent ten minutes ago: still too soon.
	allowed, wait := proactiveBackoff(1, now.Add(-10*time.Minute).UnixMilli(), now)
	if allowed {
		t.Fatal("one unanswered message should hold her back")
	}
	if wait < 110*time.Minute || wait > 2*time.Hour {
		t.Fatalf("wait = %v, want about two hours", wait)
	}

	// One unanswered, but long enough has passed: she may speak again.
	if allowed, _ := proactiveBackoff(1, now.Add(-3*time.Hour).UnixMilli(), now); !allowed {
		t.Fatal("after the gap she should be allowed again")
	}

	// At the ceiling, no passage of time lets her through.
	if allowed, _ := proactiveBackoff(maxUnrepliedProactive, now.Add(-30*24*time.Hour).UnixMilli(), now); allowed {
		t.Fatal("at the ceiling she must stay quiet")
	}
}

func TestSentProactiveCountsAsUnanswered(t *testing.T) {
	manager := openTestManager(t)
	model := &fakeModel{brief: TurnBrief{
		Resolution: ResolutionDraft{SceneStage: "none", ConsentState: "not_applicable"},
		Reply:      ReplyBrief{Send: true, Goal: "分享刚发生的小事", Tone: "自然", MaxChars: 300},
		Confidence: 0.8,
	}}
	manager.SetPorts(model, &fakeConversation{})

	pinClock(t, manager, fixedMorning)
	task, created, err := manager.submitProactiveTurnAt(context.Background(), fixedMorning)
	if err != nil || !created {
		t.Fatalf("created = %v err = %v", created, err)
	}
	processTaskNow(t, manager, task)
	manager.CompleteCompanionTurn(task.ThreadID, "turn", task.ID, "", "忽然想和你说句话。")

	state, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.UnrepliedProactive != 1 {
		t.Fatalf("unreplied = %d, want 1", state.UnrepliedProactive)
	}
}

func TestBackoffBlocksTheNextProactiveMessage(t *testing.T) {
	manager := openTestManager(t)
	model := &fakeModel{brief: TurnBrief{
		Resolution: ResolutionDraft{SceneStage: "none", ConsentState: "not_applicable"},
		Reply:      ReplyBrief{Send: true, Goal: "分享刚发生的小事", Tone: "自然", MaxChars: 300},
		Confidence: 0.8,
	}}
	manager.SetPorts(model, &fakeConversation{})

	pinClock(t, manager, fixedMorning)
	// Seed the state a sent-and-unanswered message would have left behind, so the
	// gate is tested against the pinned clock instead of the wall clock.
	state, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	state.UnrepliedProactive = 1
	state.LastProactiveAt = fixedMorning.UnixMilli()
	if err := manager.store.SaveState(manager.ID(), state); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// One tick later he still has not answered. She should not try again yet.
	_, created, err := manager.submitProactiveTurnAt(context.Background(), fixedMorning.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if created {
		t.Fatal("the next proactive message should be held back while one is unanswered")
	}

	// Past the gap she gets her voice back.
	_, created, err = manager.submitProactiveTurnAt(context.Background(), fixedMorning.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("submit later: %v", err)
	}
	if !created {
		t.Fatal("after the gap she should be able to reach out again")
	}
}

// TestRecordProactiveSentKeepsTheRestOfTheState guards the hazard that
// putState rewrites the trait bucket from the struct: the atomic bump must not
// drop metrics or traits on its way through.
func TestRecordProactiveSentKeepsTheRestOfTheState(t *testing.T) {
	manager := openTestManager(t)
	pinClock(t, manager, fixedMorning)

	before, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state before: %v", err)
	}
	// Seed something distinctive in both buckets so a lost rewrite is visible.
	before.Metrics[MetricMissing] = 77
	before.Traits[TraitID("resilience")] = 0.42
	if err := manager.store.SaveState(manager.ID(), before); err != nil {
		t.Fatalf("seed: %v", err)
	}

	total, err := manager.store.RecordProactiveSent(manager.ID(), fixedMorning.UnixMilli())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}

	after, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state after: %v", err)
	}
	if after.UnrepliedProactive != 1 {
		t.Fatalf("unreplied = %d, want 1", after.UnrepliedProactive)
	}
	if after.LastProactiveAt != fixedMorning.UnixMilli() {
		t.Fatalf("lastProactiveAt = %d, want the sent time", after.LastProactiveAt)
	}
	if got := after.Metric(MetricMissing); got != 77 {
		t.Fatalf("metric lost through the rewrite: missing = %v, want 77", got)
	}
	if got := after.Traits[TraitID("resilience")]; got != 0.42 {
		t.Fatalf("trait lost through the rewrite: resilience = %v, want 0.42", got)
	}
}

func TestUserReplyClearsTheUnansweredCount(t *testing.T) {
	manager := openTestManager(t)
	model := &fakeModel{brief: TurnBrief{
		Resolution: ResolutionDraft{SceneStage: "none", ConsentState: "not_applicable"},
		Reply:      ReplyBrief{Send: true, Goal: "回应他", Tone: "自然", MaxChars: 300},
		Confidence: 0.8,
	}}
	manager.SetPorts(model, &fakeConversation{})

	pinClock(t, manager, fixedMorning)
	state, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	state.UnrepliedProactive = 3
	if err := manager.store.SaveState(manager.ID(), state); err != nil {
		t.Fatalf("seed unreplied: %v", err)
	}

	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "在吗", "", "", "", fixedMorning)
	if err != nil {
		t.Fatalf("submit user turn: %v", err)
	}
	processTaskNow(t, manager, task)

	state, err = manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state after: %v", err)
	}
	if state.UnrepliedProactive != 0 {
		t.Fatalf("unreplied = %d, want 0 once he answers", state.UnrepliedProactive)
	}
}

func TestDirectorDoesNotClearTheUnansweredCount(t *testing.T) {
	// A world edit is not someone showing up. Editing her weather should not make
	// it feel like he answered her messages.
	manager := openTestManager(t)
	pinClock(t, manager, fixedMorning)

	state, err := manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	state.UnrepliedProactive = 2
	if err := manager.store.SaveState(manager.ID(), state); err != nil {
		t.Fatalf("seed unreplied: %v", err)
	}

	brief := TurnBrief{
		Resolution: ResolutionDraft{
			SceneStage: "none", ConsentState: "not_applicable",
			World: &WorldDraft{
				Events:    []DirectorEventDraft{{Content: "下起大雨"}},
				Narration: "外面下起大雨",
			},
		},
	}
	task := TurnTask{
		ID: "task_dir_backoff", ThreadID: "th_companion", Trigger: TriggerDirector,
		RequestedAt: fixedMorning.UnixMilli(), LastMessageAt: fixedMorning.UnixMilli(),
	}
	if _, err := manager.applyTurnBrief(&task, brief, fixedMorning); err != nil {
		t.Fatalf("apply: %v", err)
	}

	state, err = manager.store.State(manager.ID())
	if err != nil {
		t.Fatalf("state after: %v", err)
	}
	if state.UnrepliedProactive != 2 {
		t.Fatalf("unreplied = %d, want it untouched by a director turn", state.UnrepliedProactive)
	}
}
