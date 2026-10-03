package companion

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MetricEngine applies embedded metric rules to one companion state.
type MetricEngine struct {
	rules *RuleSet
}

func NewMetricEngine(rules *RuleSet) (*MetricEngine, error) {
	if rules == nil {
		var err error
		rules, err = LoadRuleSet()
		if err != nil {
			return nil, err
		}
	}
	return &MetricEngine{rules: rules}, nil
}

func (e *MetricEngine) Rules() *RuleSet { return e.rules }

// ApplyTick advances all metrics for one 15-minute step.
func (e *MetricEngine) ApplyTick(state *State, profile Profile, block scheduleBlock, effects []ActiveEffect, now int64) {
	deltaHours := tickInterval.Hours()
	signals, raw := e.inputSignals(*state, profile, block, effects, now)
	for id, rule := range e.rules.Metrics {
		value := state.Metric(id)
		rate := rule.BaseRatePerHour
		contribution := 0.0
		for source, weight := range rule.Weights {
			part := weight * signals[source]
			if cap, ok := rule.Caps[source]; ok {
				part = clampFloat(part, -math.Abs(cap), math.Abs(cap))
			}
			contribution += part
		}
		rate += contribution
		for _, effect := range effects {
			if effect.Target == id {
				rate += effect.RatePerHour * effect.IntensityAt(now)
			}
		}
		for _, gate := range rule.Gates {
			if gate.When.Evaluate(raw, conditionTexts(block, time.UnixMilli(now))) {
				rate *= gate.Multiplier
			}
		}
		rate = clampFloat(rate, rule.RateMin, rule.RateMax)
		value += rate * deltaHours

		if rule.Recovery.Mode == RecoveryHomeostatic {
			baseline := rule.Recovery.Baseline
			recoverySpeed := rule.Recovery.SpeedPerHour
			for _, effect := range effects {
				if effect.Target != id {
					continue
				}
				intensity := effect.IntensityAt(now)
				baseline += effect.BaselineDelta * intensity
				recoverySpeed += effect.RecoveryDelta * intensity
			}
			recoverySpeed = math.Max(0, recoverySpeed)
			value += (baseline - value) * (1 - math.Exp(-recoverySpeed*deltaHours))
		}
		state.Metrics[id] = clampFloat(value, rule.Min, rule.Max)
	}
}

func (e *MetricEngine) ApplyImpulses(state *State, impulses map[MetricID]float64) {
	for id, delta := range impulses {
		rule, ok := e.rules.Metrics[id]
		if !ok {
			continue
		}
		state.Metrics[id] = clampFloat(state.Metric(id)+delta, rule.Min, rule.Max)
	}
}

func (e *MetricEngine) ApplyEvent(state *State, rule EventRule, eventID string, now time.Time) []ActiveEffect {
	e.ApplyImpulses(state, rule.Impulses)
	e.ApplyTraitChanges(state, rule.TraitChange)
	_ = eventID
	return activeEffects(rule, now)
}

func (e *MetricEngine) ChooseEvent(state State, profile Profile, block scheduleBlock, at time.Time, recent []LifeEvent) (EventRule, bool) {
	_, raw := e.inputSignals(state, profile, block, nil, at.UnixMilli())
	texts := conditionTexts(block, at)
	chance := 38
	switch block.Kind {
	case "sleep":
		chance = 10
	case "work", "social", "commute":
		chance = 52
	case "break":
		chance = 45
	}
	hash := hashAt(profile.ID, at)
	if hash%100 >= uint64(chance) {
		return EventRule{}, false
	}

	type candidate struct {
		rule   EventRule
		weight int
	}
	var candidates []candidate
	total := 0
	for _, rule := range e.rules.Events {
		if !rule.When.Evaluate(raw, texts) {
			continue
		}
		duplicate := false
		for _, event := range recent {
			if normalize(event.Content) == normalize(rule.Content) && at.UnixMilli()-event.OccurredAt < 7*24*time.Hour.Milliseconds() {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		weight := rule.Weight
		switch profile.ScheduleTendency {
		case "busy":
			if rule.Type == "work" {
				weight *= 2
			}
		case "social":
			if rule.Type == "social" {
				weight *= 2
			}
		case "relaxed":
			if rule.Type == "hobby" || rule.Type == "daily" {
				weight *= 2
			}
		}
		if weight <= 0 {
			continue
		}
		candidates = append(candidates, candidate{rule: rule, weight: weight})
		total += weight
	}
	if total <= 0 {
		return EventRule{}, false
	}
	target := int(hash % uint64(total))
	for _, candidate := range candidates {
		if target < candidate.weight {
			return candidate.rule, true
		}
		target -= candidate.weight
	}
	return EventRule{}, false
}

func (e *MetricEngine) EvaluateTriggers(
	state State,
	states map[string]TriggerState,
	now time.Time,
) ([]LifeEvent, map[string]TriggerState) {
	if states == nil {
		states = map[string]TriggerState{}
	}
	day := now.Format("2006-01-02")
	var events []LifeEvent
	for _, trigger := range e.rules.Triggers {
		current := states[trigger.ID]
		if current.LastDay != day {
			current.LastDay = day
			current.FiredToday = 0
		}
		value := state.Metric(trigger.Metric)
		if current.Above && value <= trigger.ResetThreshold {
			current.Above = false
		}
		if current.Above || value < trigger.Threshold {
			states[trigger.ID] = current
			continue
		}
		if now.UnixMilli() < current.CooldownUntil {
			current.Above = true
			states[trigger.ID] = current
			continue
		}
		if trigger.OncePerDay && current.FiredToday > 0 {
			current.Above = true
			states[trigger.ID] = current
			continue
		}
		rule, ok := e.rules.Events[trigger.Event]
		if !ok {
			continue
		}
		events = append(events, e.lifeEvent(rule, now.UnixMilli(), trigger.ID))
		current.Above = true
		current.LastTriggeredAt = now.UnixMilli()
		current.CooldownUntil = now.Add(trigger.Cooldown.Std()).UnixMilli()
		current.FiredToday++
		states[trigger.ID] = current
	}
	return events, states
}

func (e *MetricEngine) EventByID(id string) (EventRule, bool) {
	event, ok := e.rules.Events[id]
	return event, ok
}

func (e *MetricEngine) lifeEvent(rule EventRule, occurredAt int64, triggerID string) LifeEvent {
	return LifeEvent{
		ID:           "evt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
		Type:         rule.Type,
		Content:      rule.Content,
		RuleID:       rule.ID,
		TriggerID:    triggerID,
		Deltas:       cloneMetricDeltas(rule.Impulses),
		Effects:      append([]EffectRule(nil), rule.Effects...),
		TraitChanges: cloneTraitDeltas(rule.TraitChange),
		Importance:   rule.Importance,
		Shareable:    rule.Shareable,
		OccurredAt:   occurredAt,
	}
}

func cloneMetricDeltas(input map[MetricID]float64) map[MetricID]float64 {
	out := make(map[MetricID]float64, len(input))
	for id, value := range input {
		out[id] = value
	}
	return out
}

func cloneTraitDeltas(input map[TraitID]float64) map[TraitID]float64 {
	out := make(map[TraitID]float64, len(input))
	for id, value := range input {
		out[id] = value
	}
	return out
}

func (e *MetricEngine) ApplyTraitChanges(state *State, changes map[TraitID]float64) {
	for id, delta := range changes {
		rule, ok := e.rules.Traits[id]
		if !ok {
			continue
		}
		state.Traits[id] = clampFloat(state.Trait(id)+delta, rule.Min, rule.Max)
	}
}

func (e *MetricEngine) ApplyInteraction(state *State, id string) {
	rule, ok := e.rules.Interactions[id]
	if !ok {
		return
	}
	e.ApplyImpulses(state, rule.Impulses)
	e.ApplyTraitChanges(state, rule.TraitChange)
}

func (e *MetricEngine) ApplyConversationOutcome(
	state *State,
	outcome ConversationOutcome,
	now time.Time,
	enforceWithdrawalFloor bool,
) []ActiveEffect {
	outcome = canonicalizeConversationOutcome(outcome)
	applySceneState(state, outcome, now)
	start := cloneMetricDeltas(state.Metrics)
	for id, delta := range outcome.Impulses {
		rule, ok := e.rules.Metrics[id]
		if !ok {
			continue
		}
		cap, exists := conversationMetricCap[id]
		if !exists {
			continue
		}
		delta = clampFloat(delta, -cap, cap)
		state.Metrics[id] = clampFloat(state.Metric(id)+delta, rule.Min, rule.Max)
	}
	if enforceWithdrawalFloor && outcome.ConsentState == "withdrawn" {
		e.applyConsentWithdrawalFloor(state, start)
	}
	for id, delta := range outcome.TraitChanges {
		rule, ok := e.rules.Traits[id]
		if !ok {
			continue
		}
		cap, exists := conversationTraitCap[id]
		if !exists {
			continue
		}
		delta = clampFloat(delta, -cap, cap)
		state.Traits[id] = clampFloat(state.Trait(id)+delta, rule.Min, rule.Max)
	}
	var effects []ActiveEffect
	for _, effect := range outcome.Effects {
		_, ok := e.rules.Metrics[effect.Target]
		if !ok || effect.DurationMin <= 0 || effect.DurationMin > 24*60 {
			continue
		}
		duration := time.Duration(effect.DurationMin) * time.Minute
		effects = append(effects, ActiveEffect{
			ID:            "conv_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
			SourceRuleID:  "conversation",
			Target:        effect.Target,
			StartAt:       now.UnixMilli(),
			EndAt:         now.Add(duration).UnixMilli(),
			RatePerHour:   clampFloat(effect.RatePerHour, -3, 3),
			BaselineDelta: clampFloat(effect.BaselineDelta, -30, 30),
			RecoveryDelta: clampFloat(effect.RecoveryDelta, -0.2, 0.2),
			Curve:         effect.Curve,
			Stacking:      "refresh",
		})
	}
	return effects
}

func applySceneState(state *State, outcome ConversationOutcome, now time.Time) {
	stage := outcome.SceneStage
	consent := outcome.ConsentState
	if consent == "withdrawn" {
		stage = "stopped"
	}
	previous := state.SceneStage
	if validSceneStages[stage] {
		state.SceneStage = stage
	}
	if validConsentStates[consent] {
		state.ConsentState = consent
	}
	if state.SceneStage == "none" || state.SceneStage == "stopped" {
		state.SceneStartedAt = 0
	} else if previous == "none" || previous == "stopped" || state.SceneStartedAt == 0 {
		state.SceneStartedAt = now.UnixMilli()
	}
	state.SceneUpdatedAt = now.UnixMilli()
	// SceneTouchedAt is the silence clock's anchor, and only a turn may move it.
	// The decay paths write SceneUpdatedAt constantly, so measuring idle against
	// that would let a wall-clock step taken after he leaves reset the clock to
	// now -- and the wind-down would go missing on exactly the longest absences.
	// Every turn that leaves him inside a live scene pushes it out; a scene that
	// is over has nothing left to touch.
	if state.SceneStage == "none" || state.SceneStage == "stopped" {
		state.SceneTouchedAt = 0
	} else {
		state.SceneTouchedAt = now.UnixMilli()
	}
	// Every turn that touches a live scene pushes the check-in further out, so a
	// warm exchange never fires it and only real silence lets it arrive. Once the
	// scene is over there is nothing to check in on.
	if state.SceneStage == "none" || state.SceneStage == "stopped" {
		state.SceneCheckInAt = 0
	} else {
		state.SceneCheckInAt = now.Add(sceneCheckInDelay).UnixMilli()
	}
}

func (e *MetricEngine) applyConsentWithdrawalFloor(state *State, before map[MetricID]float64) {
	floor := []struct {
		id    MetricID
		delta float64
	}{
		{MetricLibido, -35},
		{MetricSexualSatisfaction, -25},
		{MetricIntimacy, -3},
		{MetricTrust, -1.5},
		{MetricStress, 25},
		{MetricAnxiety, 25},
		{MetricMoodValence, -30},
		{MetricIrritability, 20},
	}
	for _, item := range floor {
		rule, ok := e.rules.Metrics[item.id]
		if !ok {
			continue
		}
		minimum := clampFloat(before[item.id]+item.delta, rule.Min, rule.Max)
		if item.delta < 0 {
			state.Metrics[item.id] = math.Min(state.Metric(item.id), minimum)
		} else {
			state.Metrics[item.id] = math.Max(state.Metric(item.id), minimum)
		}
	}
}

func (e *MetricEngine) Derived(state State) DerivedState {
	clamp100 := func(value float64) float64 { return clampFloat(value, 0, 100) }
	mood := e.moodLabel(state)
	return DerivedState{
		Mood:               mood,
		Focus:              clamp100(state.Metric(MetricAlertness) - state.Metric(MetricCognitiveLoad)*0.6 - state.Metric(MetricAnxiety)*0.3),
		Calm:               clamp100(100 - state.Metric(MetricStress)*0.55 - state.Metric(MetricAnxiety)*0.45),
		SocialSatisfaction: clamp100(100 - state.Metric(MetricLoneliness)),
		RelationshipSafety: clamp100(
			state.Metric(MetricTrust)*0.45 +
				state.Metric(MetricIntimacy)*0.35 +
				(100-state.Metric(MetricAnxiety))*0.20,
		),
		BurnoutRisk: clamp100(
			state.Metric(MetricStress)*0.40 +
				state.Metric(MetricFatigue)*0.40 +
				state.Trait(TraitSleepDebt)*0.20 -
				state.Trait(TraitResilience)*0.12,
		),
	}
}

func (e *MetricEngine) Views(state State) []MetricView {
	ids := make([]string, 0, len(e.rules.Metrics))
	for id := range e.rules.Metrics {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	views := make([]MetricView, 0, len(ids))
	for _, rawID := range ids {
		id := MetricID(rawID)
		rule := e.rules.Metrics[id]
		value := state.Metric(id)
		trend := "stable"
		if rule.Recovery.Mode == RecoveryHomeostatic {
			if value > rule.Recovery.Baseline+0.2 {
				trend = "falling"
			} else if value < rule.Recovery.Baseline-0.2 {
				trend = "rising"
			}
		}
		views = append(views, MetricView{
			ID: id, Label: rule.Label, Group: rule.Group,
			Value: value, Min: rule.Min, Max: rule.Max, Trend: trend,
		})
	}
	return views
}

func (e *MetricEngine) inputSignals(state State, profile Profile, block scheduleBlock, effects []ActiveEffect, now int64) (map[string]float64, map[string]float64) {
	signals := map[string]float64{
		"schedule." + block.Kind: 1,
	}
	raw := map[string]float64{}
	raw["context.hour"] = float64(time.UnixMilli(now).Hour())
	raw["context.dayOfWeek"] = float64(time.UnixMilli(now).Weekday())
	if at := time.UnixMilli(now); at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
		raw["context.weekend"] = 1
	} else {
		raw["context.weekend"] = 0
	}
	for id, rule := range e.rules.Metrics {
		value := state.Metric(id)
		raw["state."+string(id)] = value
		signals["state."+string(id)] = normalizeMetric(value, rule.MetricDefinition)
	}
	for id, rule := range e.rules.Traits {
		value := state.Trait(id)
		raw["trait."+string(id)] = value
		signals["trait."+string(id)] = normalizeMetric(value, MetricDefinition{Min: rule.Min, Max: rule.Max})
	}
	raw["context.block"] = 0
	absenceHours := 0.0
	if state.LastUserAt > 0 {
		absenceHours = math.Max(0, float64(now-state.LastUserAt)/float64(timeHourMilliseconds))
	}
	raw["relationship.absenceHours"] = absenceHours
	raw["relationship.absence"] = 0
	if absenceHours >= 4 {
		raw["relationship.absence"] = 1
	}
	signals["relationship.absence"] = raw["relationship.absence"]
	signals["relationship.absenceHours"] = clampFloat(absenceHours/72, 0, 1)
	signals["relationship.intimacy"] = normalizeMetric(state.Metric(MetricIntimacy), e.rules.Metrics[MetricIntimacy].MetricDefinition)
	signals["relationship.user_presence"] = 0
	for _, effect := range effects {
		if effect.SourceRuleID == "" {
			continue
		}
		key := "effect." + effect.SourceRuleID
		signals[key] = math.Max(signals[key], effect.IntensityAt(now))
	}
	signals["effect.meal"] = math.Max(signals["effect.meal"], effectsIntensity(effects, "meal", now))
	signals["effect.exercise"] = math.Max(signals["effect.exercise"], effectsIntensity(effects, "exercise", now))
	signals["effect.intimacy"] = math.Max(signals["effect.intimacy"], effectsIntensity(effects, "intimacy", now))
	return signals, raw
}

func conditionTexts(block scheduleBlock, at time.Time) map[string]string {
	clock := worldClock(at)
	return map[string]string{
		"context.block":     block.Kind,
		"context.weekday":   clock.Weekday,
		"context.timeOfDay": clock.TimeOfDay,
		"context.season":    clock.Season,
	}
}

func effectsIntensity(effects []ActiveEffect, source string, now int64) float64 {
	value := 0.0
	for _, effect := range effects {
		if strings.Contains(strings.ToLower(effect.SourceRuleID), source) {
			value = math.Max(value, effect.IntensityAt(now))
		}
	}
	return value
}

func normalizeMetric(value float64, rule MetricDefinition) float64 {
	if rule.Max <= rule.Min {
		return 0
	}
	return clampFloat((value-rule.Min)/(rule.Max-rule.Min), 0, 1)
}

func (e *MetricEngine) moodLabel(state State) string {
	stress := state.Metric(MetricStress)
	anxiety := state.Metric(MetricAnxiety)
	fatigue := state.Metric(MetricFatigue)
	vitality := state.Metric(MetricVitality)
	missing := state.Metric(MetricMissing)
	loneliness := state.Metric(MetricLoneliness)
	valence := state.Metric(MetricMoodValence)
	switch {
	case stress >= 75 || anxiety >= 75:
		return "紧绷"
	case vitality <= 25 || fatigue >= 80:
		return "疲惫"
	case missing >= 70:
		return "想念"
	case loneliness >= 70:
		return "孤独"
	case valence >= 45:
		return "开心"
	case stress <= 25 && vitality >= 65 && anxiety <= 35:
		return "轻快"
	case valence >= 5 && stress <= 50:
		return "平静"
	default:
		return "有点低落"
	}
}

const timeHourMilliseconds = int64(60 * 60 * 1000)
