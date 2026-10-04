package companion

import (
	"context"
	"testing"
	"time"
)

// The scene is held up by contact, not by the clock. These tests hold the two
// halves apart: wall-clock progression means "they are still talking", idle
// collapse means "nobody came back". Confusing them is what left a ghost scene
// running for hours with her answering in short breathless fragments long after
// he put the phone down.

func windDownScene(stage string, touchedAt time.Time) State {
	state := DefaultState(touchedAt.UnixMilli())
	state.SceneStage = stage
	state.ConsentState = "affirmed"
	state.SceneStartedAt = touchedAt.Add(-30 * time.Minute).UnixMilli()
	state.SceneUpdatedAt = touchedAt.UnixMilli()
	state.SceneTouchedAt = touchedAt.UnixMilli()
	return state
}

func TestIdleCollapseWalksEachRung(t *testing.T) {
	touched := time.Date(2030, 10, 1, 21, 3, 0, 0, time.Local)

	t.Run("active to aftercare after 18 minutes of silence", func(t *testing.T) {
		state := windDownScene("active", touched)
		changed, wind := collapseIdleScene(&state, touched.Add(17*time.Minute))
		if changed || wind != nil {
			t.Fatal("collapsed before the threshold")
		}
		changed, wind = collapseIdleScene(&state, touched.Add(18*time.Minute))
		if !changed || wind == nil {
			t.Fatal("did not collapse at the threshold")
		}
		if wind.FromStage != "active" || wind.ToStage != "aftercare" {
			t.Fatalf("transition = %s -> %s, want active -> aftercare", wind.FromStage, wind.ToStage)
		}
		if state.SceneStage != "aftercare" {
			t.Fatalf("stage = %q, want aftercare", state.SceneStage)
		}
	})

	t.Run("climax collapses the same way", func(t *testing.T) {
		state := windDownScene("climax", touched)
		_, wind := collapseIdleScene(&state, touched.Add(20*time.Minute))
		if wind == nil || wind.ToStage != "aftercare" {
			t.Fatalf("wind = %+v, want -> aftercare", wind)
		}
	})

	t.Run("aftercare ends after its own 25 minutes", func(t *testing.T) {
		state := windDownScene("aftercare", touched)
		if _, wind := collapseIdleScene(&state, touched.Add(24*time.Minute)); wind != nil {
			t.Fatal("ended before its own threshold")
		}
		_, wind := collapseIdleScene(&state, touched.Add(25*time.Minute))
		if wind == nil || wind.ToStage != "none" {
			t.Fatalf("wind = %+v, want -> none", wind)
		}
		if state.ConsentState != "not_applicable" || state.SceneStartedAt != 0 || state.SceneCheckInAt != 0 {
			t.Fatalf("scene leftovers not cleared: consent=%q started=%d checkIn=%d",
				state.ConsentState, state.SceneStartedAt, state.SceneCheckInAt)
		}
	})

	t.Run("flirt ends after 30 minutes", func(t *testing.T) {
		state := windDownScene("flirt", touched)
		if _, wind := collapseIdleScene(&state, touched.Add(29*time.Minute)); wind != nil {
			t.Fatal("ended before its own threshold")
		}
		_, wind := collapseIdleScene(&state, touched.Add(30*time.Minute))
		if wind == nil || wind.ToStage != "none" {
			t.Fatalf("wind = %+v, want -> none", wind)
		}
	})
}

// TestIdleCollapseNeverStopsConsent is the hard rule. stopped means she withdrew
// consent. Silence means nobody is in the room. Reading the second as the first
// would turn "he did not reply" into "she rejected him".
func TestIdleCollapseNeverStopsConsent(t *testing.T) {
	touched := time.Date(2030, 10, 1, 21, 3, 0, 0, time.Local)
	for _, stage := range []string{"flirt", "active", "climax", "aftercare"} {
		state := windDownScene(stage, touched)
		for minute := 0; minute <= 24*60; minute += 7 {
			collapseIdleScene(&state, touched.Add(time.Duration(minute)*time.Minute))
			if state.SceneStage == "stopped" {
				t.Fatalf("idle collapse produced stopped from %q at minute %d", stage, minute)
			}
		}
	}
}

// A scene that decayed by wall clock while he was away must not be reported as a
// wind-down from the stage the clock left it in. Collapse runs first so FromStage
// is the stage he actually left.
func TestCollapseRunsAheadOfWallClockProgression(t *testing.T) {
	touched := time.Date(2030, 10, 1, 21, 3, 0, 0, time.Local)
	state := windDownScene("active", touched)

	// Two hours on the wall clock would have slid active to aftercare on its own.
	// Collapse gets there first and reports the truth about where he was.
	_, wind := collapseIdleScene(&state, touched.Add(19*time.Minute))
	if wind == nil || wind.FromStage != "active" {
		t.Fatalf("wind = %+v, want FromStage active", wind)
	}
}

// The silence clock reads SceneTouchedAt, not SceneUpdatedAt. This is the whole
// reason the field exists: decay stamps SceneUpdatedAt, so a scene that decayed
// while he was gone would come back reading as "just touched" and the wind-down
// would go missing on exactly the longest absences -- the ones where the record
// matters most.
func TestSilenceClockIgnoresDecayStamps(t *testing.T) {
	touched := time.Date(2030, 10, 1, 21, 3, 0, 0, time.Local)
	state := windDownScene("active", touched)
	// A decay pass just ran and stamped SceneUpdatedAt as if something happened.
	state.SceneUpdatedAt = touched.Add(30 * time.Minute).UnixMilli()

	_, wind := collapseIdleScene(&state, touched.Add(19*time.Minute))
	if wind == nil {
		t.Fatal("decay stamp reset the silence clock; no wind-down fired")
	}
	if wind.FromStage != "active" {
		t.Fatalf("FromStage = %q, want active", wind.FromStage)
	}
}

// The turn path must never collapse. He has been gone twenty minutes and just
// typed something: that is a man walking back in, not one who left.
func TestTurnPathDoesNotCollapseTheSceneHeJustReturnedTo(t *testing.T) {
	touched := time.Date(2030, 10, 1, 21, 3, 0, 0, time.Local)
	returned := touched.Add(20 * time.Minute)
	state := windDownScene("active", touched)
	// Started only twenty-five minutes before he came back, so the wall clock has
	// no business moving it either.
	state.SceneStartedAt = touched.Add(-5 * time.Minute).UnixMilli()

	// What the turn path does: wall-clock progression only.
	if advanceSceneByWallClock(&state, returned) {
		t.Fatalf("wall clock moved the stage on his return: %q", state.SceneStage)
	}
	if state.SceneStage != "active" {
		t.Fatalf("stage = %q, want active still", state.SceneStage)
	}

	// And what it must not do: the same clock reading would have ended things.
	// This is why collapse has exactly one call site and it is not here.
	probe := windDownScene("active", touched)
	if _, wind := collapseIdleScene(&probe, returned); wind == nil {
		t.Fatal("probe did not collapse, so this test no longer proves anything")
	}
}

func TestSceneWindDownEventIsQuietAndUnshareable(t *testing.T) {
	event := sceneWindDownEvent(SceneWindDown{FromStage: "active", ToStage: "aftercare"}, time.Now())
	if event.Type != "scene" {
		t.Fatalf("type = %q, want scene", event.Type)
	}
	if event.Shareable {
		t.Fatal("a wind-down is not something she broadcasts")
	}
	if event.Content == "" {
		t.Fatal("empty wind-down content")
	}
}

// The wind-down must outrank the random rule so the record never has a hole in it.
func TestWindDownEventLandsInTimeline(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{})
	now := fixedMorning
	pinClock(t, manager, now)

	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	// Started fifty minutes ago, so the wall clock would have slid active to
	// aftercare on its own. He last spoke twenty minutes ago. Collapse has to run
	// first or the wind-down never fires at all and the timeline has a hole.
	state.SceneStartedAt = now.Add(-50 * time.Minute).UnixMilli()
	state.SceneUpdatedAt = now.Add(-50 * time.Minute).UnixMilli()
	state.SceneTouchedAt = now.Add(-20 * time.Minute).UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}

	if err := manager.advance(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	events, err := manager.store.RecentEvents(now.Add(-time.Hour).UnixMilli(), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "scene" && event.Importance == 6 && !event.Shareable {
			return
		}
	}
	t.Fatalf("no scene wind-down event in timeline: %+v", events)
}

// Collapse cuts the immersion supply. This is the only thing standing between a
// ghost scene and her staying up straight through her bedtime.
func TestCollapseCutsTheFlowSupply(t *testing.T) {
	touched := time.Date(2030, 10, 1, 21, 3, 0, 0, time.Local)
	plan := drivePlan(driveBlock("open", 1))

	// Same starting state, one tick, scene on versus scene off. The scene has to
	// be the difference; otherwise "collapse cuts the supply" proves nothing.
	without := DefaultState(touched.UnixMilli())
	without.Metrics[MetricFlow] = 70
	UpdateDrives(&without, plan, plan.blockAt(touched), touched)

	state := windDownScene("active", touched)
	state.Metrics[MetricFlow] = 70
	UpdateDrives(&state, plan, plan.blockAt(touched), touched)
	if state.Metric(MetricFlow) <= without.Metric(MetricFlow) {
		t.Fatalf("live scene did not feed flow: with=%v without=%v",
			state.Metric(MetricFlow), without.Metric(MetricFlow))
	}

	// Walk the whole way down. aftercare still feeds a little immersion, which is
	// right; only "none" cuts it off entirely.
	if _, wind := collapseIdleScene(&state, touched.Add(18*time.Minute)); wind == nil || wind.ToStage != "aftercare" {
		t.Fatalf("scene did not reach aftercare: %+v", wind)
	}
	if sceneFlow(state, touched.Add(18*time.Minute)) == 0 {
		t.Fatal("aftercare should still feed some immersion")
	}
	endedAt := touched.Add(43 * time.Minute)
	if _, wind := collapseIdleScene(&state, endedAt); wind == nil || wind.ToStage != "none" {
		t.Fatalf("scene did not reach none: %+v", wind)
	}
	if sceneFlow(state, endedAt) != 0 {
		t.Fatal("ended scene still feeds flow")
	}

	previous := state.Metric(MetricFlow)
	for i := 1; i <= 6; i++ {
		at := endedAt.Add(time.Duration(i*15) * time.Minute)
		UpdateDrives(&state, plan, plan.blockAt(at), at)
		current := state.Metric(MetricFlow)
		if current >= previous {
			t.Fatalf("flow did not fall after collapse: step %d %v -> %v", i, previous, current)
		}
		previous = current
	}
}

func TestSceneCheckInArmsOnContactAndFiresOnce(t *testing.T) {
	now := time.Date(2030, 10, 1, 21, 3, 0, 0, time.Local)

	t.Run("a turn inside a live scene arms it", func(t *testing.T) {
		state := DefaultState(now.UnixMilli())
		applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, now, true)
		if state.SceneTouchedAt != now.UnixMilli() {
			t.Fatalf("touched = %d, want %d", state.SceneTouchedAt, now.UnixMilli())
		}
		want := now.Add(sceneCheckInDelay).UnixMilli()
		if state.SceneCheckInAt != want {
			t.Fatalf("check-in = %d, want %d", state.SceneCheckInAt, want)
		}
	})

	t.Run("warm exchange keeps pushing it out so it never fires", func(t *testing.T) {
		state := DefaultState(now.UnixMilli())
		for i := 0; i < 10; i++ {
			at := now.Add(time.Duration(i) * 5 * time.Minute)
			applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, at, true)
			if sceneCheckInDue(state, at) {
				t.Fatalf("check-in came due during a warm exchange at round %d", i)
			}
		}
	})

	t.Run("it comes due once the silence runs long enough", func(t *testing.T) {
		state := DefaultState(now.UnixMilli())
		applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, now, true)
		if sceneCheckInDue(state, now.Add(sceneCheckInDelay-time.Minute)) {
			t.Fatal("due early")
		}
		if !sceneCheckInDue(state, now.Add(sceneCheckInDelay)) {
			t.Fatal("not due on time")
		}
	})

	t.Run("no scene means nothing to check in on", func(t *testing.T) {
		state := DefaultState(now.UnixMilli())
		state.SceneCheckInAt = now.UnixMilli()
		state.SceneStage = "none"
		if sceneCheckInDue(state, now.Add(time.Hour)) {
			t.Fatal("check-in due with no scene")
		}
	})

	t.Run("ending the scene clears the pending check", func(t *testing.T) {
		state := DefaultState(now.UnixMilli())
		applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, now, true)
		applySceneState(&state, ConversationOutcome{SceneStage: "none", ConsentState: "not_applicable"}, now.Add(time.Hour), true)
		if state.SceneCheckInAt != 0 || state.SceneTouchedAt != 0 {
			t.Fatalf("leftovers: checkIn=%d touched=%d", state.SceneCheckInAt, state.SceneTouchedAt)
		}
	})
}

// She does not get an exception on the backoff. If she already has messages
// sitting unanswered, not asking is the right answer.
func TestSceneCheckInRespectsBackoff(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(&fakeModel{}, conversation)
	now := fixedMorning
	pinClock(t, manager, now)

	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = now.Add(-30 * time.Minute).UnixMilli()
	state.SceneTouchedAt = now.Add(sceneCheckInDelay).UnixMilli()
	state.SceneCheckInAt = now.UnixMilli()
	state.UnrepliedProactive = maxUnrepliedProactive
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}

	_, created, err := manager.submitProactiveTurnAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("check-in went out past the backoff gate")
	}
}

func TestSceneCheckInIsConsumedOnce(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(&fakeModel{}, conversation)
	now := fixedMorning
	pinClock(t, manager, now)

	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = now.Add(-30 * time.Minute).UnixMilli()
	state.SceneTouchedAt = now.Add(sceneCheckInDelay).UnixMilli()
	state.SceneCheckInAt = now.UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}

	task, created, err := manager.submitProactiveTurnAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("check-in task was not created")
	}
	if !task.SceneCheckIn {
		t.Fatal("task did not carry the check-in flag")
	}
	after, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if after.SceneCheckInAt != 0 {
		t.Fatalf("check-in not consumed: %d", after.SceneCheckInAt)
	}
}

// A scene is a thing that happens between two people. These tests hold that line:
// when he did not speak, her own turn must not be able to move the scene at all.
// Every one of these was observed happening in live data -- the alignment model,
// handed last night's memories on a quiet morning, typed "active" and Go built a
// brand new scene out of it with nobody in the room.
func TestTurnsWhereHeDidNotSpeakCannotMoveTheScene(t *testing.T) {
	now := time.Date(2030, 10, 4, 10, 20, 0, 0, time.Local)

	t.Run("cannot open a scene that is not running", func(t *testing.T) {
		state := DefaultState(now.UnixMilli())
		state.SceneStage = "none"
		applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, now, false)
		if state.SceneStage != "none" {
			t.Fatalf("a proactive turn opened a scene: %q", state.SceneStage)
		}
		if state.SceneStartedAt != 0 {
			t.Fatalf("a scene start was minted from nothing: %d", state.SceneStartedAt)
		}
	})

	t.Run("cannot escalate flirt into active", func(t *testing.T) {
		state := windDownScene("flirt", now.Add(-time.Hour))
		applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, now, false)
		if state.SceneStage != "flirt" {
			t.Fatalf("a proactive turn escalated the scene: %q", state.SceneStage)
		}
	})

	t.Run("cannot withdraw consent on his behalf", func(t *testing.T) {
		state := windDownScene("active", now.Add(-time.Hour))
		applySceneState(&state, ConversationOutcome{SceneStage: "stopped", ConsentState: "withdrawn"}, now, false)
		if state.SceneStage == "stopped" || state.ConsentState == "withdrawn" {
			t.Fatalf("a turn with nobody there recorded a withdrawal: %s/%s", state.SceneStage, state.ConsentState)
		}
	})

	t.Run("cannot end a live scene either", func(t *testing.T) {
		state := windDownScene("active", now.Add(-time.Hour))
		applySceneState(&state, ConversationOutcome{SceneStage: "none", ConsentState: "not_applicable"}, now, false)
		if state.SceneStage != "active" {
			t.Fatalf("a proactive turn ended the scene; collapse owns that, not this: %q", state.SceneStage)
		}
	})

	t.Run("does not move the silence clock or re-arm the check-in", func(t *testing.T) {
		touched := now.Add(-40 * time.Minute)
		state := windDownScene("active", touched)
		state.SceneCheckInAt = now.Add(3 * time.Minute).UnixMilli()
		applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, now, false)
		if state.SceneTouchedAt != touched.UnixMilli() {
			t.Fatalf("her own turn reset the silence clock: %d", state.SceneTouchedAt)
		}
		if state.SceneCheckInAt != now.Add(3*time.Minute).UnixMilli() {
			t.Fatalf("her own turn re-armed the check-in: %d", state.SceneCheckInAt)
		}
	})

	t.Run("a user turn still moves all of it", func(t *testing.T) {
		state := DefaultState(now.UnixMilli())
		applySceneState(&state, ConversationOutcome{SceneStage: "active", ConsentState: "affirmed"}, now, true)
		if state.SceneStage != "active" || state.SceneTouchedAt != now.UnixMilli() {
			t.Fatalf("user turn did not open and time the scene: %q touched=%d", state.SceneStage, state.SceneTouchedAt)
		}
	})
}

// End to end through the turn settlement: a proactive turn must leave a live scene
// exactly as it found it, so the check-in still has its scene and the collapse
// clock still reads the last time he was actually here.
func TestProactiveTurnLeavesALiveSceneUntouched(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	var aligned TurnBrief
	aligned.Scene = SceneAlignment{Stage: "active", ConsentState: "affirmed"}
	aligned.Resolution = ResolutionDraft{SceneStage: "active", ConsentState: "affirmed"}
	aligned.Reply = ReplyBrief{Send: true, Goal: "确认他是不是还在"}
	manager.SetPorts(&fakeModel{brief: aligned}, conversation)
	now := fixedMorning
	pinClock(t, manager, now)

	touched := now.Add(-40 * time.Minute)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.SceneStage = "active"
	state.ConsentState = "affirmed"
	state.SceneStartedAt = touched.Add(-20 * time.Minute).UnixMilli()
	state.SceneTouchedAt = touched.UnixMilli()
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}

	task := &TurnTask{
		ID:          "task_proactive_scene",
		ThreadID:    "th_companion",
		Trigger:     TriggerProactive,
		Status:      TaskGenerating,
		RequestedAt: now.UnixMilli(),
	}
	if _, err := manager.applyTurnBrief(task, aligned, now); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if after.SceneTouchedAt != touched.UnixMilli() {
		t.Fatalf("proactive turn moved the silence clock to %d, want %d", after.SceneTouchedAt, touched.UnixMilli())
	}
	if after.SceneStage != "active" {
		t.Fatalf("the live scene should have been left alone for the check-in, got %q", after.SceneStage)
	}
}

// The mirror case: when he does speak, the scene and its clock both follow.
func TestUserTurnStillDrivesTheSceneAndItsClock(t *testing.T) {
	manager := openTestManager(t)
	conversation := &fakeConversation{threadID: "th_companion"}
	var aligned TurnBrief
	aligned.Scene = SceneAlignment{Stage: "active", ConsentState: "affirmed"}
	aligned.Resolution = ResolutionDraft{SceneStage: "active", ConsentState: "affirmed"}
	aligned.Reply = ReplyBrief{Send: true, Goal: "回应他"}
	manager.SetPorts(&fakeModel{brief: aligned}, conversation)
	now := fixedMorning
	pinClock(t, manager, now)

	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.SceneStage = "none"
	state.ConsentState = "not_applicable"
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}

	task := &TurnTask{
		ID:            "task_user_scene",
		ThreadID:      "th_companion",
		Trigger:       TriggerUser,
		Status:        TaskGenerating,
		RequestedAt:   now.UnixMilli(),
		LastMessageAt: now.UnixMilli(),
	}
	if _, err := manager.applyTurnBrief(task, aligned, now); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if after.SceneStage != "active" || after.SceneTouchedAt != now.UnixMilli() {
		t.Fatalf("user turn did not open and time the scene: %q touched=%d", after.SceneStage, after.SceneTouchedAt)
	}
}
