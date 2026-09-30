package companion

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed rules/*.yaml
var ruleFiles embed.FS

type MetricID string
type TraitID string

const (
	MetricVitality           MetricID = "vitality"
	MetricHunger             MetricID = "hunger"
	MetricFatigue            MetricID = "fatigue"
	MetricSleepPressure      MetricID = "sleepPressure"
	MetricAlertness          MetricID = "alertness"
	MetricCognitiveLoad      MetricID = "cognitiveLoad"
	MetricMotivation         MetricID = "motivation"
	MetricMoodValence        MetricID = "moodValence"
	MetricStress             MetricID = "stress"
	MetricAnxiety            MetricID = "anxiety"
	MetricIrritability       MetricID = "irritability"
	MetricLoneliness         MetricID = "loneliness"
	MetricSocialEnergy       MetricID = "socialEnergy"
	MetricLibido             MetricID = "libido"
	MetricSexualSatisfaction MetricID = "sexualSatisfaction"
	MetricMissing            MetricID = "missing"
	MetricTrust              MetricID = "trust"
	MetricIntimacy           MetricID = "intimacy"
	TraitResilience          TraitID  = "resilience"
	TraitFitness             TraitID  = "fitness"
	TraitSleepDebt           TraitID  = "sleepDebt"
	TraitSensitivity         TraitID  = "sensitivity"
	TraitAttachmentSecure    TraitID  = "attachmentSecurity"
	TraitNutritionBalance    TraitID  = "nutritionBalance"
)

type RecoveryMode string

const (
	RecoveryLinear      RecoveryMode = "linear"
	RecoveryHomeostatic RecoveryMode = "homeostatic"
)

type RecoverySpec struct {
	Mode         RecoveryMode `yaml:"mode" json:"mode"`
	Baseline     float64      `yaml:"baseline" json:"baseline"`
	SpeedPerHour float64      `yaml:"speedPerHour" json:"speedPerHour"`
}

type TraitDefinition struct {
	ID      TraitID `yaml:"-" json:"id"`
	Label   string  `yaml:"label" json:"label"`
	Min     float64 `yaml:"min" json:"min"`
	Max     float64 `yaml:"max" json:"max"`
	Default float64 `yaml:"default" json:"default"`
}

type MetricDefinition struct {
	ID              MetricID     `yaml:"-" json:"id"`
	Label           string       `yaml:"label" json:"label"`
	Group           string       `yaml:"group" json:"group"`
	Min             float64      `yaml:"min" json:"min"`
	Max             float64      `yaml:"max" json:"max"`
	Default         float64      `yaml:"default" json:"default"`
	BaseRatePerHour float64      `yaml:"baseRatePerHour" json:"baseRatePerHour"`
	Recovery        RecoverySpec `yaml:"recovery" json:"recovery"`
}

type MetricWeightRule struct {
	RateMin float64            `yaml:"rateMin" json:"rateMin"`
	RateMax float64            `yaml:"rateMax" json:"rateMax"`
	Weights map[string]float64 `yaml:"weights" json:"weights"`
	Caps    map[string]float64 `yaml:"caps" json:"caps,omitempty"`
	Gates   []StateGate        `yaml:"gates" json:"gates,omitempty"`
}

type StateGate struct {
	When       ConditionExpr `yaml:"when" json:"when"`
	Multiplier float64       `yaml:"multiplier" json:"multiplier"`
}

type ConditionExpr struct {
	Source string          `yaml:"source,omitempty" json:"source,omitempty"`
	Op     string          `yaml:"op,omitempty" json:"op,omitempty"`
	Value  *float64        `yaml:"value,omitempty" json:"value,omitempty"`
	Text   string          `yaml:"text,omitempty" json:"text,omitempty"`
	All    []ConditionExpr `yaml:"all,omitempty" json:"all,omitempty"`
	Any    []ConditionExpr `yaml:"any,omitempty" json:"any,omitempty"`
}

type EffectRule struct {
	Target        MetricID `yaml:"target" json:"target"`
	RatePerHour   float64  `yaml:"ratePerHour,omitempty" json:"ratePerHour,omitempty"`
	BaselineDelta float64  `yaml:"baselineDelta,omitempty" json:"baselineDelta,omitempty"`
	RecoveryDelta float64  `yaml:"recoveryDelta,omitempty" json:"recoveryDelta,omitempty"`
	Delay         Duration `yaml:"delay,omitempty" json:"delay,omitempty"`
	Duration      Duration `yaml:"duration" json:"duration"`
	Curve         string   `yaml:"curve,omitempty" json:"curve,omitempty"`
	Stacking      string   `yaml:"stacking,omitempty" json:"stacking,omitempty"`
}

type EventRule struct {
	ID          string               `yaml:"id" json:"id"`
	Type        string               `yaml:"type" json:"type"`
	Content     string               `yaml:"content" json:"content"`
	Weight      int                  `yaml:"weight" json:"weight"`
	Importance  int                  `yaml:"importance" json:"importance"`
	Shareable   bool                 `yaml:"shareable" json:"shareable"`
	When        ConditionExpr        `yaml:"when,omitempty" json:"when,omitempty"`
	Impulses    map[MetricID]float64 `yaml:"impulses" json:"impulses"`
	Effects     []EffectRule         `yaml:"effects" json:"effects,omitempty"`
	TraitChange map[TraitID]float64  `yaml:"traits" json:"traits,omitempty"`
}

type TriggerRule struct {
	ID             string   `yaml:"id" json:"id"`
	Metric         MetricID `yaml:"metric" json:"metric"`
	Threshold      float64  `yaml:"threshold" json:"threshold"`
	ResetThreshold float64  `yaml:"resetThreshold" json:"resetThreshold"`
	Cooldown       Duration `yaml:"cooldown" json:"cooldown"`
	OncePerDay     bool     `yaml:"oncePerDay" json:"oncePerDay"`
	Event          string   `yaml:"event" json:"event"`
}

type MetricRule struct {
	MetricDefinition `yaml:",inline"`
	MetricWeightRule `yaml:",inline"`
}

type RuleSet struct {
	Version      int                         `json:"version"`
	Hash         string                      `json:"hash"`
	Metrics      map[MetricID]MetricRule     `json:"metrics"`
	Traits       map[TraitID]TraitDefinition `json:"traits"`
	Events       map[string]EventRule        `json:"events"`
	Interactions map[string]EventRule        `json:"interactions"`
	Triggers     []TriggerRule               `json:"triggers"`
}

type metricCatalogFile struct {
	Version int                           `yaml:"version"`
	Metrics map[MetricID]MetricDefinition `yaml:"metrics"`
	Traits  map[TraitID]TraitDefinition   `yaml:"traits"`
}

type metricWeightsFile struct {
	Version      int                           `yaml:"version"`
	Metrics      map[MetricID]MetricWeightRule `yaml:"metrics"`
	Interactions map[string]EventRule          `yaml:"interactions"`
}

type eventRulesFile struct {
	Version  int           `yaml:"version"`
	Events   []EventRule   `yaml:"events"`
	Triggers []TriggerRule `yaml:"triggers"`
}

// Duration decodes compact YAML durations such as 3h or 30m.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Value == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// LoadRuleSet loads and validates the embedded rule files.
func LoadRuleSet() (*RuleSet, error) {
	catalogRaw, err := ruleFiles.ReadFile("rules/metric_catalog.yaml")
	if err != nil {
		return nil, err
	}
	weightsRaw, err := ruleFiles.ReadFile("rules/metric_weights.yaml")
	if err != nil {
		return nil, err
	}
	eventsRaw, err := ruleFiles.ReadFile("rules/event_rules.yaml")
	if err != nil {
		return nil, err
	}

	var catalog metricCatalogFile
	var weights metricWeightsFile
	var events eventRulesFile
	if err := yaml.Unmarshal(catalogRaw, &catalog); err != nil {
		return nil, fmt.Errorf("parse metric_catalog.yaml: %w", err)
	}
	if err := yaml.Unmarshal(weightsRaw, &weights); err != nil {
		return nil, fmt.Errorf("parse metric_weights.yaml: %w", err)
	}
	if err := yaml.Unmarshal(eventsRaw, &events); err != nil {
		return nil, fmt.Errorf("parse event_rules.yaml: %w", err)
	}
	if catalog.Version != 1 || weights.Version != 1 || events.Version != 1 {
		return nil, fmt.Errorf("unsupported rule version")
	}

	set := &RuleSet{
		Version:      catalog.Version,
		Metrics:      make(map[MetricID]MetricRule, len(catalog.Metrics)),
		Traits:       catalog.Traits,
		Events:       make(map[string]EventRule, len(events.Events)),
		Interactions: make(map[string]EventRule, len(weights.Interactions)),
		Triggers:     append([]TriggerRule(nil), events.Triggers...),
	}
	for id, definition := range catalog.Metrics {
		definition.ID = id
		weightRule, ok := weights.Metrics[id]
		if !ok {
			return nil, fmt.Errorf("metric %s has no weight rule", id)
		}
		set.Metrics[id] = MetricRule{MetricDefinition: definition, MetricWeightRule: weightRule}
	}
	for id := range weights.Metrics {
		if _, ok := set.Metrics[id]; !ok {
			return nil, fmt.Errorf("weight rule references unknown metric %s", id)
		}
	}
	for id, definition := range set.Traits {
		definition.ID = id
		set.Traits[id] = definition
	}
	for _, event := range events.Events {
		if event.ID == "" {
			return nil, fmt.Errorf("event rule has empty id")
		}
		if _, exists := set.Events[event.ID]; exists {
			return nil, fmt.Errorf("duplicate event rule %s", event.ID)
		}
		set.Events[event.ID] = event
	}
	for id, interaction := range weights.Interactions {
		interaction.ID = id
		set.Interactions[id] = interaction
	}
	if err := set.validate(); err != nil {
		return nil, err
	}
	hash := sha256.New()
	for _, raw := range [][]byte{catalogRaw, weightsRaw, eventsRaw} {
		_, _ = hash.Write(raw)
	}
	set.Hash = fmt.Sprintf("%x", hash.Sum(nil))
	return set, nil
}

func (r *RuleSet) validate() error {
	validInputs := map[string]bool{
		"relationship.user_presence": true,
		"relationship.absence":       true,
		"relationship.absenceHours":  true,
		"relationship.intimacy":      true,
	}
	for id := range r.Metrics {
		validInputs["state."+string(id)] = true
	}
	for id := range r.Traits {
		validInputs["trait."+string(id)] = true
	}
	for _, block := range []string{"sleep", "morning", "work", "break", "social", "hobby", "commute", "rest"} {
		validInputs["schedule."+block] = true
	}
	for _, input := range []string{"effect.meal", "effect.exercise", "effect.intimacy"} {
		validInputs[input] = true
	}
	validInputs["context.block"] = true
	validInputs["context.hour"] = true
	validInputs["context.dayOfWeek"] = true
	validInputs["context.weekend"] = true
	validInputs["context.weekday"] = true
	validInputs["context.timeOfDay"] = true
	validInputs["context.season"] = true

	var validateCondition func(expr ConditionExpr) error
	validateCondition = func(expr ConditionExpr) error {
		if expr.Source != "" && !validInputs[expr.Source] {
			return fmt.Errorf("unknown rule input %q", expr.Source)
		}
		if expr.Source != "" {
			switch expr.Op {
			case "gt", "gte", "lt", "lte", "equals", "neq":
			default:
				return fmt.Errorf("unknown condition operator %q", expr.Op)
			}
		}
		for _, child := range append(append([]ConditionExpr(nil), expr.All...), expr.Any...) {
			if err := validateCondition(child); err != nil {
				return err
			}
		}
		return nil
	}

	for id, metric := range r.Metrics {
		if metric.Label == "" || metric.Group == "" {
			return fmt.Errorf("metric %s is missing label or group", id)
		}
		if metric.Min >= metric.Max || metric.Default < metric.Min || metric.Default > metric.Max {
			return fmt.Errorf("metric %s has invalid range/default", id)
		}
		if metric.RateMin > metric.RateMax {
			return fmt.Errorf("metric %s has invalid rate range", id)
		}
		if metric.Recovery.Mode != RecoveryLinear && metric.Recovery.Mode != RecoveryHomeostatic {
			return fmt.Errorf("metric %s has invalid recovery mode", id)
		}
		for source, weight := range metric.Weights {
			if !validInputs[source] {
				return fmt.Errorf("metric %s references unknown input %q", id, source)
			}
			if math.IsNaN(weight) || math.IsInf(weight, 0) {
				return fmt.Errorf("metric %s input %s has invalid weight", id, source)
			}
		}
		for _, gate := range metric.Gates {
			if err := validateCondition(gate.When); err != nil {
				return fmt.Errorf("metric %s gate: %w", id, err)
			}
		}
	}
	for id, event := range r.Events {
		if event.Content == "" || event.Weight <= 0 || event.Type == "" {
			return fmt.Errorf("event %s is incomplete", id)
		}
		if err := validateCondition(event.When); err != nil {
			return fmt.Errorf("event %s condition: %w", id, err)
		}
		for metricID := range event.Impulses {
			if _, ok := r.Metrics[metricID]; !ok {
				return fmt.Errorf("event %s references unknown impulse metric %s", id, metricID)
			}
		}
		for _, effect := range event.Effects {
			if _, ok := r.Metrics[effect.Target]; !ok {
				return fmt.Errorf("event %s references unknown effect metric %s", id, effect.Target)
			}
			if effect.Duration.Std() <= 0 {
				return fmt.Errorf("event %s effect %s has invalid duration", id, effect.Target)
			}
		}
		for traitID := range event.TraitChange {
			if _, ok := r.Traits[traitID]; !ok {
				return fmt.Errorf("event %s references unknown trait %s", id, traitID)
			}
		}
	}
	for id, interaction := range r.Interactions {
		for metricID := range interaction.Impulses {
			if _, ok := r.Metrics[metricID]; !ok {
				return fmt.Errorf("interaction %s references unknown impulse metric %s", id, metricID)
			}
		}
		for traitID := range interaction.TraitChange {
			if _, ok := r.Traits[traitID]; !ok {
				return fmt.Errorf("interaction %s references unknown trait %s", id, traitID)
			}
		}
		for _, effect := range interaction.Effects {
			if _, ok := r.Metrics[effect.Target]; !ok {
				return fmt.Errorf("interaction %s references unknown effect metric %s", id, effect.Target)
			}
			if effect.Duration.Std() <= 0 {
				return fmt.Errorf("interaction %s effect %s has invalid duration", id, effect.Target)
			}
		}
	}
	eventIDs := make([]string, 0, len(r.Events))
	for id := range r.Events {
		eventIDs = append(eventIDs, id)
	}
	sort.Strings(eventIDs)
	for _, trigger := range r.Triggers {
		if _, ok := r.Metrics[trigger.Metric]; !ok {
			return fmt.Errorf("trigger %s references unknown metric %s", trigger.ID, trigger.Metric)
		}
		if _, ok := r.Events[trigger.Event]; !ok {
			return fmt.Errorf("trigger %s references unknown event %s", trigger.ID, trigger.Event)
		}
		if trigger.ResetThreshold >= trigger.Threshold {
			return fmt.Errorf("trigger %s reset threshold must be lower than threshold", trigger.ID)
		}
	}
	return nil
}

func (r *RuleSet) DefaultMetrics() map[MetricID]float64 {
	out := make(map[MetricID]float64, len(r.Metrics))
	for id, metric := range r.Metrics {
		out[id] = metric.Default
	}
	return out
}

func (r *RuleSet) DefaultTraits() map[TraitID]float64 {
	out := make(map[TraitID]float64, len(r.Traits))
	for id, trait := range r.Traits {
		out[id] = trait.Default
	}
	return out
}

func (c ConditionExpr) Evaluate(numbers map[string]float64, texts map[string]string) bool {
	if len(c.All) > 0 {
		for _, child := range c.All {
			if !child.Evaluate(numbers, texts) {
				return false
			}
		}
		return true
	}
	if len(c.Any) > 0 {
		for _, child := range c.Any {
			if child.Evaluate(numbers, texts) {
				return true
			}
		}
		return false
	}
	if c.Source == "" {
		return true
	}
	left := numbers[c.Source]
	switch strings.ToLower(c.Op) {
	case "equals":
		return texts[c.Source] == c.Text
	case "neq":
		return texts[c.Source] != c.Text
	}
	if c.Value == nil {
		return false
	}
	switch strings.ToLower(c.Op) {
	case "gt":
		return left > *c.Value
	case "gte":
		return left >= *c.Value
	case "lt":
		return left < *c.Value
	case "lte":
		return left <= *c.Value
	default:
		return false
	}
}
