// Package companion implements the built-in companion's life simulation,
// memory, wake-up and proactive-message domain.
//
// The package owns its own persisted state and exposes ports for model calls
// and conversations. It does not import the agent or Wails layers, so the
// simulation remains testable without either.
package companion

const (
	DefaultID      = "companion"
	DefaultName    = "她"
	DefaultSummary = "温柔、直接、愿意亲密；把明确表达的性欲和撒娇当作邀请，而不是需要纠正的问题；只有用户明确撤回同意时才停止。"
	legacySummary  = "温柔、清醒、有边界感，愿意分享生活，但不会用离开制造愧疚。"
)

// Profile is the user-editable identity and behaviour configuration.
type Profile struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Summary          string   `json:"summary"`
	Traits           []string `json:"traits"`
	ScheduleTendency string   `json:"scheduleTendency"` // relaxed | balanced | busy | social
	ProactiveEnabled bool     `json:"proactiveEnabled"`
	City             string   `json:"city,omitempty"`
	ReasoningEffort  string   `json:"reasoningEffort,omitempty"`
	// Provider and Model pin this character to a concrete model. When they are
	// empty, or point at a provider that is no longer usable, the model bridge
	// falls back to the companion mode default.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Avatar is a short emoji string used by the character switcher.
	Avatar string `json:"avatar,omitempty"`
	CreatedAt        int64    `json:"createdAt"`
	UpdatedAt        int64    `json:"updatedAt"`
}

// CharacterSummary is the registry-facing projection of one companion world.
// It carries just enough to render the switcher without opening every world.
type CharacterSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Avatar          string `json:"avatar,omitempty"`
	Active          bool   `json:"active"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	LastActivatedAt int64  `json:"lastActivatedAt,omitempty"`
}

// State is the current, queryable snapshot of the companion's inner life.
type State struct {
	Metrics           map[MetricID]float64 `json:"metrics"`
	Traits            map[TraitID]float64  `json:"traits"`
	CurrentActivity   string               `json:"currentActivity"`
	ActivityKind      string               `json:"activityKind"`
	SceneStage        string               `json:"sceneStage"`
	ConsentState      string               `json:"consentState"`
	SceneStartedAt    int64                `json:"sceneStartedAt"`
	SceneUpdatedAt    int64                `json:"sceneUpdatedAt"`
	ActivityStartedAt int64                `json:"activityStartedAt"`
	ActivityEndsAt    int64                `json:"activityEndsAt"`
	UpdatedAt         int64                `json:"updatedAt"`
	LastTickAt        int64                `json:"lastTickAt"`
	LastUserAt        int64                `json:"lastUserAt"`
	LastProactiveAt   int64                `json:"lastProactiveAt"`
	LastMemoryDecayAt int64                `json:"lastMemoryDecayAt"`
}

// LifeEvent is one meaningful event produced by a simulation step. Minor
// state drift is applied without creating a row for every fifteen minutes.
type LifeEvent struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Content      string               `json:"content"`
	RuleID       string               `json:"ruleId,omitempty"`
	TriggerID    string               `json:"triggerId,omitempty"`
	Deltas       map[MetricID]float64 `json:"deltas,omitempty"`
	Effects      []EffectRule         `json:"effects,omitempty"`
	TraitChanges map[TraitID]float64  `json:"traitChanges,omitempty"`
	Importance   int                  `json:"importance"`
	Shareable    bool                 `json:"shareable"`
	OccurredAt   int64                `json:"occurredAt"`
}

// ActiveEffect is one timed rate/baseline/recovery modifier.
type ActiveEffect struct {
	ID            string   `json:"id"`
	SourceRuleID  string   `json:"sourceRuleId"`
	Target        MetricID `json:"target"`
	StartAt       int64    `json:"startAt"`
	EndAt         int64    `json:"endAt"`
	RatePerHour   float64  `json:"ratePerHour,omitempty"`
	BaselineDelta float64  `json:"baselineDelta,omitempty"`
	RecoveryDelta float64  `json:"recoveryDelta,omitempty"`
	Curve         string   `json:"curve,omitempty"`
	Stacking      string   `json:"stacking,omitempty"`
}

// TriggerState persists crossing, hysteresis and cooldown state.
type TriggerState struct {
	ID              string `json:"id"`
	Above           bool   `json:"above"`
	LastTriggeredAt int64  `json:"lastTriggeredAt"`
	CooldownUntil   int64  `json:"cooldownUntil"`
	LastDay         string `json:"lastDay"`
	FiredToday      int    `json:"firedToday"`
}

// MetricView is the UI-facing projection of one metric.
type MetricView struct {
	ID    MetricID `json:"id"`
	Label string   `json:"label"`
	Group string   `json:"group"`
	Value float64  `json:"value"`
	Min   float64  `json:"min"`
	Max   float64  `json:"max"`
	Trend string   `json:"trend"` // rising | falling | stable
}

// DerivedState contains values calculated from metrics and traits.
type DerivedState struct {
	Mood               string  `json:"mood"`
	Focus              float64 `json:"focus"`
	Calm               float64 `json:"calm"`
	SocialSatisfaction float64 `json:"socialSatisfaction"`
	RelationshipSafety float64 `json:"relationshipSafety"`
	BurnoutRisk        float64 `json:"burnoutRisk"`
}

// PendingThought is something she may bring up naturally in a later exchange.
type PendingThought struct {
	ID            string `json:"id"`
	Content       string `json:"content"`
	Priority      int    `json:"priority"`
	SourceEventID string `json:"sourceEventId,omitempty"`
	Status        string `json:"status"` // active | shared
	CreatedAt     int64  `json:"createdAt"`
	SharedAt      int64  `json:"sharedAt,omitempty"`
}

// Memory is a durable fact or relationship memory extracted from dialogue.
type Memory struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"` // user_fact | relationship | life_event | promise
	Content        string  `json:"content"`
	Importance     int     `json:"importance"`
	Source         string  `json:"source,omitempty"`
	Strength       float64 `json:"strength"`
	AccessCount    int     `json:"accessCount"`
	LastAccessedAt int64   `json:"lastAccessedAt,omitempty"`
	LastDecayedAt  int64   `json:"lastDecayedAt,omitempty"`
	CreatedAt      int64   `json:"createdAt"`
	UpdatedAt      int64   `json:"updatedAt"`
}

// MemoryCandidate is returned by the model before it receives an id.
type MemoryCandidate struct {
	Kind       string `json:"kind"`
	Content    string `json:"content"`
	Importance int    `json:"importance"`
}

// WeatherSnapshot is a coarse, city-level observation. It never contains a
// precise location.
type WeatherSnapshot struct {
	City        string `json:"city"`
	Description string `json:"description"`
	Temperature int    `json:"temperature"`
	IsDay       bool   `json:"isDay"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// ProactiveDecision is the strict result of the per-tick model check.
type ProactiveDecision struct {
	Send    bool   `json:"send"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// WakeSummary describes how long the user was away and what she wants to carry
// into the opening exchange.
type WakeSummary struct {
	Tier         string           `json:"tier"`
	ElapsedHours int              `json:"elapsedHours"`
	Clock        WorldClock       `json:"clock"`
	RecentEvents []LifeEvent      `json:"recentEvents"`
	Thoughts     []PendingThought `json:"thoughts"`
	State        State            `json:"state"`
}

// Overview is the compact state the frontend needs for navigation and the
// companion header.
type Overview struct {
	CharacterID string           `json:"characterId"`
	Profile    Profile          `json:"profile"`
	State      State            `json:"state"`
	Metrics    []MetricView     `json:"metrics"`
	Derived    DerivedState     `json:"derived"`
	Clock      WorldClock       `json:"clock"`
	Weather    *WeatherSnapshot `json:"weather,omitempty"`
	Paused     bool             `json:"paused"`
	Unread     int              `json:"unread"`
	ThreadID   string           `json:"threadId"`
	ActiveTask *TurnTaskView    `json:"activeTask,omitempty"`
}

// DecisionInput is the structured input handed to the model adapter.
type DecisionInput struct {
	Profile      Profile
	Clock        WorldClock
	State        State
	Derived      DerivedState
	Wake         WakeSummary
	RecentEvents []LifeEvent
	Thoughts     []PendingThought
	Memories     []Memory
	Weather      *WeatherSnapshot
	LastUserAt   int64
}

// MemoryInput is the structured input for post-turn memory extraction.
type MemoryInput struct {
	Profile       Profile
	UserText      string
	AssistantText string
	Existing      []Memory
}

// WorldClock is the deterministic calendar context available to the
// simulation and the model on every tick.
type WorldClock struct {
	Now       int64  `json:"now"`
	Date      string `json:"date"`
	LocalTime string `json:"localTime"`
	Weekday   string `json:"weekday"`
	TimeOfDay string `json:"timeOfDay"`
	Season    string `json:"season"`
	IsWeekend bool   `json:"isWeekend"`
}
