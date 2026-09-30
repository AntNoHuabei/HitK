package companion

import (
	"context"
	"testing"
	"time"
)

func TestConversationBoundaryOverrideReducesSexualAndRelationshipState(t *testing.T) {
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricLibido] = 100
	state.Metrics[MetricSexualSatisfaction] = 80
	state.Metrics[MetricIntimacy] = 60
	state.Metrics[MetricTrust] = 70
	beforeLibido := state.Metric(MetricLibido)
	beforeIntimacy := state.Metric(MetricIntimacy)
	const signal = "conversation_boundary"
	rule := engine.Rules().Interactions[signal]
	_ = engine.ApplyEvent(&state, rule, signal, time.Now())
	if state.Metric(MetricLibido) >= beforeLibido {
		t.Fatalf("boundary violation did not reduce libido: %.1f", state.Metric(MetricLibido))
	}
	if state.Metric(MetricIntimacy) >= beforeIntimacy {
		t.Fatalf("boundary violation did not reduce intimacy: %.1f", state.Metric(MetricIntimacy))
	}
}

func TestRoleplayResistanceCanIncreaseIntensityInConsentedScene(t *testing.T) {
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricLibido] = 40
	state.Metrics[MetricSexualSatisfaction] = 45
	before := state.Metric(MetricLibido)
	_ = engine.ApplyConversationOutcome(&state, ConversationOutcome{
		Signals:      []string{"roleplay_resistance"},
		SceneStage:   "active",
		ConsentState: "roleplay",
		Intensity:    1,
		Impulses: map[MetricID]float64{
			MetricLibido:             25,
			MetricSexualSatisfaction: 12,
		},
	}, time.Now(), false)
	if state.Metric(MetricLibido) <= before {
		t.Fatalf("roleplay resistance did not increase libido: %.1f", state.Metric(MetricLibido))
	}
}

func TestWithdrawnConsentAlwaysAppliesSafetyFloor(t *testing.T) {
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricLibido] = 100
	state.Metrics[MetricSexualSatisfaction] = 85
	state.Metrics[MetricIntimacy] = 70
	state.Metrics[MetricTrust] = 75
	state.Metrics[MetricMoodValence] = 80
	beforeLibido := state.Metric(MetricLibido)
	beforeIntimacy := state.Metric(MetricIntimacy)
	_ = engine.ApplyConversationOutcome(&state, ConversationOutcome{
		Signals:      []string{"boundary"},
		SceneStage:   "stopped",
		ConsentState: "withdrawn",
		Intensity:    1,
	}, time.Now(), true)
	if state.Metric(MetricLibido) > beforeLibido-35 {
		t.Fatalf("withdrawal floor was not enforced for libido: %.1f", state.Metric(MetricLibido))
	}
	if state.Metric(MetricIntimacy) > beforeIntimacy-3 {
		t.Fatalf("withdrawal floor was not enforced for intimacy: %.1f", state.Metric(MetricIntimacy))
	}
}

func TestChineseMetricAliasesAreApplied(t *testing.T) {
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricLibido] = 50
	effects := engine.ApplyConversationOutcome(&state, ConversationOutcome{
		Intensity: 2,
		Impulses: map[MetricID]float64{
			MetricID("性欲"):   25,
			MetricID("性满足度"): 20,
		},
		Effects: []ConversationEffect{
			{Target: MetricID("情绪"), RatePerHour: 1, DurationMin: 60},
		},
	}, time.Now(), false)
	if state.Metric(MetricLibido) != 75 {
		t.Fatalf("final delta was scaled or alias was ignored: %.1f, want 75", state.Metric(MetricLibido))
	}
	if state.Metric(MetricSexualSatisfaction) < 64 {
		t.Fatalf("Chinese alias for sexual satisfaction was ignored: %.1f", state.Metric(MetricSexualSatisfaction))
	}
	if len(effects) != 1 || effects[0].Target != MetricMoodValence {
		t.Fatalf("Chinese target alias for effect was not normalized: %#v", effects)
	}
}

func TestConversationSignalsApplyImmediateMetricChanges(t *testing.T) {
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricLibido] = 40
	state.Metrics[MetricSexualSatisfaction] = 40
	beforeLibido := state.Metric(MetricLibido)
	beforeSatisfaction := state.Metric(MetricSexualSatisfaction)
	const signal = "conversation_explicit"
	rule := engine.Rules().Interactions[signal]
	_ = engine.ApplyEvent(&state, rule, signal, time.Now())
	if state.Metric(MetricLibido) <= beforeLibido {
		t.Fatalf("explicit conversation did not immediately raise libido: %.1f -> %.1f", beforeLibido, state.Metric(MetricLibido))
	}
	if state.Metric(MetricSexualSatisfaction) <= beforeSatisfaction {
		t.Fatalf("explicit conversation did not immediately raise sexual satisfaction: %.1f -> %.1f", beforeSatisfaction, state.Metric(MetricSexualSatisfaction))
	}
}

func TestConversationBuildAdvancesWorldClock(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	base := fixedMorning
	pinClock(t, manager, base)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	// Submit against the pinned instant so the background kick leaves this task
	// alone and the test owns the processing.
	task, err := manager.submitUserTurnAt(context.Background(), "th_companion", "陪我说会儿话吧", "", "", "", base)
	if err != nil {
		t.Fatal(err)
	}
	_ = processTaskNow(t, manager, task)
	updated, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastTickAt <= state.LastTickAt {
		t.Fatalf("conversation did not advance the world clock: %d -> %d", state.LastTickAt, updated.LastTickAt)
	}
}

func TestLLMSemanticAnalysisCanCreateLargeImmediateJump(t *testing.T) {
	manager := openTestManager(t)
	model := &fakeModel{brief: TurnBrief{
		Resolution: ResolutionDraft{
			SceneStage: "active", ConsentState: "affirmed",
			Impulses: map[MetricID]float64{MetricLibido: 50, MetricSexualSatisfaction: 30},
		},
		Reply:      ReplyBrief{Send: true, Goal: "继续", Tone: "亲密", MaxChars: 400},
		Confidence: 0.9,
	}}
	conversation := &fakeConversation{threadID: "th_companion"}
	manager.SetPorts(model, conversation)
	state, err := manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	state.Metrics[MetricLibido] = 50
	state.Metrics[MetricSexualSatisfaction] = 50
	if err := manager.store.SaveState(manager.id, state); err != nil {
		t.Fatal(err)
	}
	// Keep the world one step behind the fixed processing instant so advance()
	// runs a single tick instead of a full seven-day catch-up.
	pinClock(t, manager, fixedMorning)
	task, err := manager.SubmitUserTurn(context.Background(), "th_companion", "继续，我忍不住了", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	updatedTask := processTaskNow(t, manager, task)
	var updated State
	updated, err = manager.store.State(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Metric(MetricLibido) < 90 {
		t.Fatalf("semantic analysis did not create the requested libido jump: %.2f", updated.Metric(MetricLibido))
	}
	if updated.Metric(MetricSexualSatisfaction) < 79 {
		t.Fatalf("semantic analysis did not raise sexual satisfaction: %.2f", updated.Metric(MetricSexualSatisfaction))
	}
	if updatedTask.Status != TaskGenerating {
		t.Fatalf("task status = %s, want generating", updatedTask.Status)
	}
	conversation.mu.Lock()
	aligned, started := conversation.aligned, conversation.started
	conversation.mu.Unlock()
	if aligned != 1 || started != 1 {
		t.Fatalf("task phases = aligned:%d started:%d", aligned, started)
	}
	model.mu.Lock()
	analyses := model.analyses
	model.mu.Unlock()
	if analyses != 1 {
		t.Fatalf("semantic analyzer calls = %d, want 1", analyses)
	}
}

func TestConversationLLMAnalysisDoesNotBlockSubmit(t *testing.T) {
	manager := openTestManager(t)
	model := &slowAnalysisModel{}
	manager.SetPorts(model, &fakeConversation{threadID: "th_companion"})
	start := time.Now()
	if _, err := manager.SubmitUserTurn(context.Background(), "th_companion", "你今天在做什么", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("submit waited for alignment: %s", elapsed)
	}
}

type slowAnalysisModel struct{}

func (*slowAnalysisModel) Available() bool { return true }
func (*slowAnalysisModel) Decide(context.Context, DecisionInput) (ProactiveDecision, error) {
	return ProactiveDecision{}, nil
}
func (*slowAnalysisModel) ExtractMemories(context.Context, MemoryInput) ([]MemoryCandidate, error) {
	return nil, nil
}
func (*slowAnalysisModel) AlignTurn(ctx context.Context, _ TurnAlignmentInput) (TurnBrief, error) {
	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
	}
	return TurnBrief{
		Resolution: ResolutionDraft{SceneStage: "none", ConsentState: "not_applicable"},
		Reply:      ReplyBrief{Send: true, Goal: "回应", Tone: "自然", MaxChars: 400},
	}, nil
}
