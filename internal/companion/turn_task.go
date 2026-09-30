package companion

import (
	"fmt"
	"strings"
	"time"
)

// TurnTaskStatus is the durable lifecycle of one companion response task.
type TurnTaskStatus string

const (
	TaskWaiting     TurnTaskStatus = "waiting"
	TaskAligning    TurnTaskStatus = "aligning"
	TaskResolved    TurnTaskStatus = "resolved"
	TaskGenerating  TurnTaskStatus = "generating"
	TaskCompleted   TurnTaskStatus = "completed"
	TaskFailed      TurnTaskStatus = "failed"
	TaskInterrupted TurnTaskStatus = "interrupted"
)

// TurnTaskTrigger identifies why a task exists.
type TurnTaskTrigger string

const (
	TriggerUser      TurnTaskTrigger = "user"
	TriggerProactive TurnTaskTrigger = "proactive"
)

// TurnTaskInput is one user message folded into a task.
type TurnTaskInput struct {
	Text string `json:"text"`
	At   int64  `json:"at"`
}

// TurnTask is the durable unit of one companion response.
type TurnTask struct {
	ID                 string          `json:"id"`
	ThreadID           string          `json:"threadId"`
	Trigger            TurnTaskTrigger `json:"trigger"`
	Status             TurnTaskStatus  `json:"status"`
	Provider           string          `json:"provider,omitempty"`
	Model              string          `json:"model,omitempty"`
	ReasoningEffort    string          `json:"reasoningEffort,omitempty"`
	Inputs             []TurnTaskInput `json:"inputs,omitempty"`
	RequestedAt        int64           `json:"requestedAt"`
	LastMessageAt      int64           `json:"lastMessageAt"`
	NotBefore          int64           `json:"notBefore"`
	StartedAt          int64           `json:"startedAt,omitempty"`
	ResolvedAt         int64           `json:"resolvedAt,omitempty"`
	CompletedAt        int64           `json:"completedAt,omitempty"`
	CurrentActivity    string          `json:"currentActivity,omitempty"`
	AvailabilityReason string          `json:"availabilityReason,omitempty"`
	Brief              *TurnBrief      `json:"brief,omitempty"`
	Resolution         *TurnResolution `json:"resolution,omitempty"`
	MessageItemID      string          `json:"messageItemId,omitempty"`
	Error              string          `json:"error,omitempty"`
	Applied            bool            `json:"applied,omitempty"`
	// Forced 是用户按了「让她现在回」。有了它，日程可用性这一关直接跳过，
	// 否则 processTurnTaskAt 会照着日程把她再塞回等待。
	Forced             bool            `json:"forced,omitempty"`
}

// InputText joins the user messages folded into this task.
func (t TurnTask) InputText() string {
	parts := make([]string, 0, len(t.Inputs))
	for _, input := range t.Inputs {
		if text := strings.TrimSpace(input.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// ClockAlignment records the physical-time decision made before generation.
type ClockAlignment struct {
	Now              int64  `json:"now"`
	LocalTime        string `json:"localTime"`
	ElapsedMinutes   int    `json:"elapsedMinutes"`
	AbsenceHours     int    `json:"absenceHours"`
	DaysCrossed      int    `json:"daysCrossed"`
	Activity         string `json:"activity"`
	Available        bool   `json:"available"`
	AvailabilityNote string `json:"availabilityNote,omitempty"`
	NextAvailableAt  int64  `json:"nextAvailableAt,omitempty"`
}

// SceneAlignment records scene time and continuity.
type SceneAlignment struct {
	Stage               string   `json:"stage"`
	ConsentState        string   `json:"consentState"`
	StartedAt           int64    `json:"startedAt,omitempty"`
	DurationMinutes     int      `json:"durationMinutes"`
	Location            string   `json:"location,omitempty"`
	Participants        []string `json:"participants,omitempty"`
	PhysicalState       string   `json:"physicalState,omitempty"`
	ContinuityNote      string   `json:"continuityNote,omitempty"`
	TimeConstraintNote  string   `json:"timeConstraintNote,omitempty"`
	AutoAdvancedByClock bool     `json:"autoAdvancedByClock,omitempty"`
}

// InputAlignment separates intent from surface wording.
type InputAlignment struct {
	Intent           string   `json:"intent"`
	ExplicitRequests []string `json:"explicitRequests,omitempty"`
	ImpliedNeeds     []string `json:"impliedNeeds,omitempty"`
	Boundaries       []string `json:"boundaries,omitempty"`
	RoleplayNote     string   `json:"roleplayNote,omitempty"`
	FactClaims       []string `json:"factClaims,omitempty"`
	MissingDetails   []string `json:"missingDetails,omitempty"`
	Ambiguities      []string `json:"ambiguities,omitempty"`
}

// ResolutionEvent is a durable event produced by alignment.
type ResolutionEvent struct {
	Type       string               `json:"type"`
	Content    string               `json:"content"`
	Importance float64              `json:"importance"`
	Shareable  bool                 `json:"shareable"`
	Deltas     map[MetricID]float64 `json:"deltas,omitempty"`
}

// ResolutionMemory is the model-facing memory shape. Importance is numeric so
// vendors that emit 5.5 do not fail the whole turn.
type ResolutionMemory struct {
	Kind       string  `json:"kind"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
}

// ResolutionDraft is the model-proposed state change. Go validates it before
// anything is applied.
type ResolutionDraft struct {
	ActionMinutes int                  `json:"actionMinutes"`
	SceneStage    string               `json:"sceneStage"`
	ConsentState  string               `json:"consentState"`
	Impulses      map[MetricID]float64 `json:"impulses,omitempty"`
	TraitChanges  map[TraitID]float64  `json:"traitChanges,omitempty"`
	Effects       []ConversationEffect `json:"effects,omitempty"`
	Events        []ResolutionEvent    `json:"events,omitempty"`
	Memories      []ResolutionMemory   `json:"memories,omitempty"`
	Thoughts      []string             `json:"thoughts,omitempty"`
}

// ReplyBrief is the only creative instruction handed to the narration model.
type ReplyBrief struct {
	Send           bool     `json:"send"`
	Goal           string   `json:"goal"`
	Tone           string   `json:"tone"`
	Beats          []string `json:"beats,omitempty"`
	MustInclude    []string `json:"mustInclude,omitempty"`
	MustNotInclude []string `json:"mustNotInclude,omitempty"`
	MaxChars       int      `json:"maxChars,omitempty"`
	Delivery       string   `json:"delivery,omitempty"` // either | both | action_only | speech_only
}

// TurnBrief is the validated output of the alignment phase.
type TurnBrief struct {
	Clock      ClockAlignment  `json:"clock"`
	Scene      SceneAlignment  `json:"scene"`
	Input      InputAlignment  `json:"input"`
	Resolution ResolutionDraft `json:"resolution"`
	Reply      ReplyBrief      `json:"reply"`
	Warnings   []string        `json:"warnings,omitempty"`
	Confidence float64         `json:"confidence"`
}

// TurnResolution records what Go actually committed.
type TurnResolution struct {
	AppliedAt       int64                `json:"appliedAt"`
	ActionMinutes   int                  `json:"actionMinutes"`
	MetricDeltas    map[MetricID]float64 `json:"metricDeltas,omitempty"`
	TraitChanges    map[TraitID]float64  `json:"traitChanges,omitempty"`
	EventIDs        []string             `json:"eventIds,omitempty"`
	MemoryIDs       []string             `json:"memoryIds,omitempty"`
	ThoughtIDs      []string             `json:"thoughtIds,omitempty"`
	GenerationFacts string               `json:"generationFacts,omitempty"`
}

// TurnAlignmentInput is the structured input to the alignment model.
type TurnAlignmentInput struct {
	Profile  Profile
	State    State
	Derived  DerivedState
	Clock    WorldClock
	Task     TurnTask
	Recent   []ConversationMessage
	Events   []LifeEvent
	Thoughts []PendingThought
	Memories []Memory
	Weather  *WeatherSnapshot
}

// TurnTaskView is the frontend projection of a task.
type TurnTaskView struct {
	ID                 string          `json:"id"`
	ThreadID           string          `json:"threadId"`
	Trigger            TurnTaskTrigger `json:"trigger"`
	Status             TurnTaskStatus  `json:"status"`
	RequestedAt        int64           `json:"requestedAt"`
	NotBefore          int64           `json:"notBefore,omitempty"`
	StartedAt          int64           `json:"startedAt,omitempty"`
	ResolvedAt         int64           `json:"resolvedAt,omitempty"`
	CompletedAt        int64           `json:"completedAt,omitempty"`
	CurrentActivity    string          `json:"currentActivity,omitempty"`
	AvailabilityReason string          `json:"availabilityReason,omitempty"`
	Brief              *TurnBrief      `json:"brief,omitempty"`
	Error              string          `json:"error,omitempty"`
	Forced             bool            `json:"forced,omitempty"`
}

func (t TurnTask) View() TurnTaskView {
	return TurnTaskView{
		ID: t.ID, ThreadID: t.ThreadID, Trigger: t.Trigger, Status: t.Status,
		RequestedAt: t.RequestedAt, NotBefore: t.NotBefore, StartedAt: t.StartedAt,
		ResolvedAt: t.ResolvedAt, CompletedAt: t.CompletedAt,
		CurrentActivity: t.CurrentActivity, AvailabilityReason: t.AvailabilityReason,
		Brief: t.Brief, Error: t.Error, Forced: t.Forced,
	}
}

func (t TurnTask) Active() bool {
	switch t.Status {
	case TaskWaiting, TaskAligning, TaskResolved, TaskGenerating:
		return true
	default:
		return false
	}
}

func taskAvailability(profile Profile, state State, at time.Time) (bool, string, int64) {
	// 场景还活着，人就在这段对话里，日程表上写什么都拦不住回消息。
	// 之前亲密场景进行中发一条，会被判成「处理工作和日常琐事，18:30 再回」，
	// 活活晾五个半小时。
	if sceneLive(state, at) {
		return true, displayActivity(state), 0
	}
	blocks := scheduleBlocksForDay(profile, at)
	minute := at.Hour()*60 + at.Minute()
	for _, block := range blocks {
		if minute < block.StartMinute || minute >= block.EndMinute {
			continue
		}
		if blockAvailable(block.Kind) {
			return true, block.Label, 0
		}
		for _, candidate := range blocks {
			if candidate.StartMinute < block.EndMinute || !blockAvailable(candidate.Kind) {
				continue
			}
			start := time.Date(at.Year(), at.Month(), at.Day(), 0, candidate.StartMinute, 0, 0, at.Location())
			return false, fmt.Sprintf("%s，暂时不能回复", block.Label), start.UnixMilli()
		}
		tomorrow := at.AddDate(0, 0, 1)
		for _, candidate := range scheduleBlocksForDay(profile, tomorrow) {
			if !blockAvailable(candidate.Kind) {
				continue
			}
			start := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, candidate.StartMinute, 0, 0, tomorrow.Location())
			return false, fmt.Sprintf("%s，暂时不能回复", block.Label), start.UnixMilli()
		}
	}
	return true, "空闲", 0
}

func blockAvailable(kind string) bool {
	switch kind {
	case "morning", "break", "rest", "hobby":
		return true
	default:
		return false
	}
}

// sceneLive 报告场景是否仍在进行。时间边界写在这里而不是依赖 sceneStage 已被衰减，
// 所以就算持久化的 stage 还没被 tick 更新过也不会误判成「还在进行中」。
func sceneLive(state State, at time.Time) bool {
	if state.SceneStartedAt <= 0 {
		return false
	}
	switch state.SceneStage {
	case "active", "climax", "aftercare":
		return at.Sub(time.UnixMilli(state.SceneStartedAt)) < 3*time.Hour
	}
	return false
}

func conservativeTurnBrief(state State, at time.Time, activity, availabilityNote string, available bool, nextAt int64, reason string) TurnBrief {
	confidence := 0.25
	return TurnBrief{
		Clock: ClockAlignment{
			Now: at.UnixMilli(), LocalTime: at.Format("15:04"), Activity: activity,
			Available: available, AvailabilityNote: availabilityNote, NextAvailableAt: nextAt,
		},
		Scene: SceneAlignment{
			Stage: state.SceneStage, ConsentState: state.ConsentState,
			StartedAt: state.SceneStartedAt, DurationMinutes: sceneDurationMinutes(state, at),
			TimeConstraintNote: "对齐失败，仅延续已经确认的事实。",
		},
		Input: InputAlignment{Intent: "延续当前对话；不新增世界事实。"},
		Resolution: ResolutionDraft{
			SceneStage: state.SceneStage, ConsentState: state.ConsentState,
		},
		Reply: ReplyBrief{
			Send: true,
			Goal: "自然回应最近消息，并严格延续现有时间、场景和关系。",
			Tone: "克制、自然、符合当前活动状态",
			MustNotInclude: []string{
				"新增地点、人物、事件、承诺或身体动作",
				"声称发生过未在本轮结算中确认的事情",
			},
			MaxChars: 600,
		},
		Warnings:   []string{"alignment_fallback: " + reason},
		Confidence: confidence,
	}
}

func sceneDurationMinutes(state State, at time.Time) int {
	if state.SceneStartedAt <= 0 {
		return 0
	}
	minutes := int(at.Sub(time.UnixMilli(state.SceneStartedAt)).Minutes())
	if minutes < 0 {
		return 0
	}
	return minutes
}

// displayActivity 回答「她现在正在做什么」。场景还活着的时候答案是场景，
// 不是日程表上那格 —— 否则亲密场景进行中，头部会一脸认真地写着「吃午饭，短暂休息」。
// flirt 不覆盖：调情可以和别的事同时进行。
func displayActivity(state State) string {
	switch state.SceneStage {
	case "active", "climax":
		return "亲密中"
	case "aftercare":
		return "亲密后的温存"
	}
	return state.CurrentActivity
}

func advanceSceneByWallClock(state *State, at time.Time) bool {
	if state.SceneStartedAt <= 0 {
		return false
	}
	elapsed := at.Sub(time.UnixMilli(state.SceneStartedAt))
	changed := false
	switch state.SceneStage {
	case "flirt":
		if elapsed >= 6*time.Hour {
			state.SceneStage = "none"
			state.ConsentState = "not_applicable"
			changed = true
		}
	case "active", "climax":
		if elapsed >= 3*time.Hour {
			state.SceneStage = "none"
			state.ConsentState = "not_applicable"
			changed = true
		} else if elapsed >= 45*time.Minute {
			state.SceneStage = "aftercare"
			changed = true
		}
	case "aftercare":
		if elapsed >= 3*time.Hour {
			state.SceneStage = "none"
			state.ConsentState = "not_applicable"
			changed = true
		}
	}
	if changed {
		state.SceneUpdatedAt = at.UnixMilli()
	}
	return changed
}

// Since 2026-09-28 15:47 Asia/Shanghai was the current local user date in the
// development environment, keep formatting deterministic for tests.
func taskTitle(task TurnTask) string {
	preview := strings.TrimSpace(task.InputText())
	if preview == "" {
		preview = "主动联系"
	}
	runes := []rune(preview)
	if len(runes) > 48 {
		runes = runes[:48]
	}
	return string(runes)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
