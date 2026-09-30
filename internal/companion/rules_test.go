package companion

import (
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestEmbeddedRulesLoadAndHashIsStable(t *testing.T) {
	first, err := LoadRuleSet()
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	second, err := LoadRuleSet()
	if err != nil {
		t.Fatalf("reload rules: %v", err)
	}
	if first.Hash == "" || first.Hash != second.Hash {
		t.Fatalf("rule hash is not stable: %q / %q", first.Hash, second.Hash)
	}
	if len(first.Metrics) != 20 {
		t.Fatalf("metric count = %d, want 20", len(first.Metrics))
	}
	if len(first.Traits) != 6 {
		t.Fatalf("trait count = %d, want 6", len(first.Traits))
	}
	if len(first.Events) < 40 || len(first.Triggers) == 0 {
		t.Fatalf("event rule pool is too small: %d events, %d triggers", len(first.Events), len(first.Triggers))
	}
}

func TestEmbeddedDefaultsMatchRuntimeDefaults(t *testing.T) {
	rules, err := LoadRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	state := DefaultState(time.Now().UnixMilli())
	for id, value := range rules.DefaultMetrics() {
		if state.Metric(id) != value {
			t.Fatalf("metric %s default = %.1f, want %.1f", id, state.Metric(id), value)
		}
	}
	for id, value := range rules.DefaultTraits() {
		if state.Trait(id) != value {
			t.Fatalf("trait %s default = %.1f, want %.1f", id, state.Trait(id), value)
		}
	}
}

func TestRuleValidationRejectsUnknownInput(t *testing.T) {
	rules, err := LoadRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	metric := rules.Metrics[MetricStress]
	metric.Weights["state.not_a_metric"] = 1
	rules.Metrics[MetricStress] = metric
	if err := rules.validate(); err == nil {
		t.Fatal("unknown weight input was accepted")
	}
}

func TestWeightAggregationUsesMultipleInputs(t *testing.T) {
	rules, err := LoadRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewMetricEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	profile := DefaultProfile(1)
	work := scheduleBlock{Kind: "work", Label: "工作", StartMinute: 540, EndMinute: 720}

	calm := DefaultState(time.Now().UnixMilli())
	calm.Metrics[MetricStress] = 20
	calm.Metrics[MetricFatigue] = 10
	calm.Metrics[MetricAnxiety] = 10
	engine.ApplyTick(&calm, profile, work, nil, time.Now().UnixMilli())

	loaded := calm
	loaded.Metrics = cloneMetricDeltas(calm.Metrics)
	loaded.Metrics[MetricStress] = 20
	loaded.Metrics[MetricFatigue] = 80
	loaded.Metrics[MetricAnxiety] = 75
	engine.ApplyTick(&loaded, profile, work, nil, time.Now().UnixMilli())

	if loaded.Metric(MetricStress) <= calm.Metric(MetricStress) {
		t.Fatalf("combined fatigue/anxiety weights did not raise stress: calm=%.2f loaded=%.2f", calm.Metric(MetricStress), loaded.Metric(MetricStress))
	}
}

func TestTriggerHysteresisAndCooldown(t *testing.T) {
	rules, err := LoadRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewMetricEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricHunger] = 70
	now := time.Now()
	events, states := engine.EvaluateTriggers(state, nil, now)
	if len(events) != 1 {
		t.Fatalf("first threshold crossing produced %d events, want 1", len(events))
	}
	events, states = engine.EvaluateTriggers(state, states, now.Add(time.Minute))
	if len(events) != 0 {
		t.Fatal("trigger repeated while above threshold")
	}
	state.Metrics[MetricHunger] = 40
	_, states = engine.EvaluateTriggers(state, states, now.Add(2*time.Minute))
	state.Metrics[MetricHunger] = 70
	events, states = engine.EvaluateTriggers(state, states, now.Add(3*time.Minute))
	if len(events) != 0 {
		t.Fatal("cooldown did not block retrigger")
	}
	state.Metrics[MetricHunger] = 40
	_, states = engine.EvaluateTriggers(state, states, now.Add(4*time.Hour))
	state.Metrics[MetricHunger] = 70
	events, _ = engine.EvaluateTriggers(state, states, now.Add(4*time.Hour+time.Minute))
	if len(events) != 1 {
		t.Fatalf("trigger did not fire after cooldown: %d", len(events))
	}
}

func TestActiveEffectCurveAndExpiry(t *testing.T) {
	start := time.Now()
	effect := ActiveEffect{
		StartAt: start.UnixMilli(),
		EndAt:   start.Add(2 * time.Hour).UnixMilli(),
		Curve:   "ramp_decay",
	}
	if got := effect.IntensityAt(start.UnixMilli()); got != 1 {
		t.Fatalf("start intensity = %.2f, want 1", got)
	}
	if got := effect.IntensityAt(start.Add(time.Hour).UnixMilli()); got != 0.5 {
		t.Fatalf("midpoint intensity = %.2f, want .5", got)
	}
	if got := effect.IntensityAt(start.Add(3 * time.Hour).UnixMilli()); got != 0 {
		t.Fatalf("expired intensity = %.2f, want 0", got)
	}
}

func TestLegacyStateMigratesToMetricsMap(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UnixMilli()
	legacy := legacyState{
		Energy: 42, Hunger: 61, Fatigue: 73, Stress: 66, MissingYou: 54,
		Trust: 63, Intimacy: 37, ActivityKind: "work", CurrentActivity: "工作中",
		LastTickAt: now - time.Hour.Milliseconds(),
	}
	if err := store.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketState), DefaultID, legacy)
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.State(DefaultID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Metric(MetricVitality) != 42 || state.Metric(MetricHunger) != 61 || state.Metric(MetricFatigue) != 73 {
		t.Fatalf("legacy metrics were not migrated: %+v", state.Metrics)
	}
	if state.Metric(MetricTrust) != 63 || state.Metric(MetricIntimacy) != 37 {
		t.Fatalf("relationship metrics were not migrated: %+v", state.Metrics)
	}
	if state.CurrentActivity != "工作中" {
		t.Fatalf("activity was not migrated: %+v", state)
	}
}

func TestExplicitIntimacyRequiresRelationshipGates(t *testing.T) {
	engine, err := NewMetricEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := DefaultProfile(time.Now().UnixMilli())
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricLibido] = 80
	state.Metrics[MetricIntimacy] = 80
	state.Metrics[MetricTrust] = 75
	state.Metrics[MetricStress] = 20
	state.Traits[TraitAttachmentSecure] = 80
	block := scheduleBlock{Kind: "rest", Label: "休息", StartMinute: 1200, EndMinute: 1260}
	at := time.Date(2026, time.September, 28, 21, 0, 0, 0, time.Local)
	rule := engine.rules.Events["explicit_intimate_encounter"]
	_, raw := engine.inputSignals(state, profile, block, nil, at.UnixMilli())
	if !rule.When.Evaluate(raw, conditionTexts(block, at)) {
		t.Fatal("explicit encounter did not pass with all relationship gates satisfied")
	}
	state.Metrics[MetricStress] = 80
	_, raw = engine.inputSignals(state, profile, block, nil, at.UnixMilli())
	if rule.When.Evaluate(raw, conditionTexts(block, at)) {
		t.Fatal("explicit encounter passed while stress gate was unsafe")
	}
}
