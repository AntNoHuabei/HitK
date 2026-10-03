package companion

import (
	"strings"
	"time"

	"github.com/google/uuid"
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
	// TriggerDirector is a world edit rather than a message to her. It never
	// parks on availability and never counts as the user showing up.
	TriggerDirector TurnTaskTrigger = "director"
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
	// Blocked 是后端算好的「她现在被事情拽离了手机」。前端只认这个字段决定
	// 要不要出忙线卡，不再从 status 和活动字符串猜。以前没有这个字段，一条
	// 可用任务带着活动字符串就能在忙线位上演出「她在忙 · 亲密中」。
	Blocked       bool            `json:"blocked,omitempty"`
	Brief         *TurnBrief      `json:"brief,omitempty"`
	Resolution    *TurnResolution `json:"resolution,omitempty"`
	MessageItemID string          `json:"messageItemId,omitempty"`
	Error         string          `json:"error,omitempty"`
	Applied       bool            `json:"applied,omitempty"`
	// DirectiveID ties a director turn to its ledger entry. The id is minted
	// before the model runs so the user's raw words stay durable even if the
	// alignment call never comes back.
	DirectiveID string `json:"directiveId,omitempty"`
	// Forced 是用户按了「让她现在回」。有了它，日程可用性这一关直接跳过，
	// 否则 processTurnTaskAt 会照着日程把她再塞回等待。
	Forced bool `json:"forced,omitempty"`
	// SceneCheckIn marks a proactive turn as the one gentle "are you still there"
	// asked during a scene he has gone quiet on. It rides the same proactive
	// machinery rather than opening a new one, and it exists because the
	// alignment model must not be trusted to do the time arithmetic that tells
	// her how long the silence has run.
	SceneCheckIn bool `json:"sceneCheckIn,omitempty"`
	// Recoveries counts how many times a restart has re-queued this turn. Without
	// a cap a crash loop would keep reviving the same turn and could emit the
	// same answer several times.
	Recoveries int `json:"recoveries,omitempty"`
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
	// Commitments are plans the user actually voiced this turn. Relative time has
	// to arrive already converted to an absolute date; Go refuses anything it
	// cannot place on the calendar.
	Commitments []CommitmentDraft `json:"commitments,omitempty"`
	// World is the director's edit, present only on director turns.
	World *WorldDraft `json:"world,omitempty"`
}

// CommitmentDraft is one arrangement heard in conversation. Only things the user
// plainly said belong here -- "初六过来" or "下周三面试" -- never a guess.
type CommitmentDraft struct {
	Kind     string `json:"kind,omitempty"`
	Label    string `json:"label"`
	Date     string `json:"date"` // YYYY-MM-DD, already resolved
	Start    string `json:"start,omitempty"`
	Duration int    `json:"duration,omitempty"` // minutes
	Weight   int    `json:"weight,omitempty"`
	City     string `json:"city,omitempty"`
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
	AppliedAt     int64                `json:"appliedAt"`
	ActionMinutes int                  `json:"actionMinutes"`
	MetricDeltas  map[MetricID]float64 `json:"metricDeltas,omitempty"`
	TraitChanges  map[TraitID]float64  `json:"traitChanges,omitempty"`
	EventIDs      []string             `json:"eventIds,omitempty"`
	MemoryIDs     []string             `json:"memoryIds,omitempty"`
	ThoughtIDs    []string             `json:"thoughtIds,omitempty"`
	// CommitmentIDs are plans she just agreed to, which now show up on the day
	// they were promised for.
	CommitmentIDs   []string `json:"commitmentIds,omitempty"`
	GenerationFacts string   `json:"generationFacts,omitempty"`
	// DirectiveID is set when this turn was a director intervention.
	DirectiveID string `json:"directiveId,omitempty"`
	// Narration is the in-world wording a directive produced. It is what she is
	// told, carried on the resolution so generation never sees the raw command.
	Narration string `json:"narration,omitempty"`
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
	Blocked            bool            `json:"blocked,omitempty"`
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
		Blocked: t.Blocked,
		Brief:   t.Brief, Error: t.Error, Forced: t.Forced,
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

// planResolver hands out the plan for any day, so availability can look ahead to
// the next free block without knowing how plans are stored.
type planResolver func(time.Time) DayPlan

// taskAvailability 是 evaluate 的冷消息投影：可不可用、显示什么、下次再试是何时。
// 新代码请直接读 Availability，三个问题在那里是分开的。
func taskAvailability(state State, resolve planResolver, at time.Time) (bool, string, int64) {
	a := evaluate(state, resolve, at)
	return a.admitNew(), a.Activity, a.NextFree.UnixMilli()
}

// nextFreeStart finds the first block at or after fromMinute that she can be
// counted on to be free in. Only light blocks qualify. An unstarted heavy block is
// free right now only because she has not begun it yet, and by the time we get
// there she will have, which is what turned "busy until 12:45" into "busy until
// 17:00" and made the promise worthless.
func nextFreeStart(plan DayPlan, fromMinute int, day time.Time) (int64, bool) {
	best := -1
	for _, block := range plan.Blocks {
		if block.Weight > replyWeightCeiling || block.EndMinute <= fromMinute {
			continue
		}
		start := max(block.StartMinute, fromMinute)
		if best < 0 || start < best {
			best = start
		}
	}
	if best < 0 {
		return 0, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 0, best, 0, 0, day.Location()).UnixMilli(), true
}

// earlyRelease answers a narrower question than taskAvailability: once she has
// told him "busy until five", is there anything that makes it honest to write
// back before five? Only two things qualify. A scene that pulled her back into
// this conversation, and a block light enough that she was never really held by it.
//
// The "heavy block she has not started" escape is left out on purpose. That rule
// is right for a message arriving cold, where no promise has been made yet, but it
// is what turned "busy until 17:00" into a reply at 12:00 the moment she drifted
// into an unstarted lunch. A promise she does not keep is worse than a promise
// she keeps a little late.
func earlyRelease(state State, resolve planResolver, at time.Time) (bool, string) {
	// 读的是和冷消息同一份 evaluate，所以两条规则不可能各自漂移。睡着绝不提前
	// 放行由 ReasonAsleep 落进 default 守住：轻块那条本来就拦得住 weight 10 的
	// 睡眠，但这里写死，免得以后有人调权重把睡眠调成可打扰。
	return releaseEarly(evaluate(state, resolve, at))
}

// sceneHorizon 是一个阶段从 SceneStartedAt 起还能算活着多久。这两个窗口只有这一份：
// sceneLive 的可达性、advanceSceneByWallClock 的衰减、sceneFlow 喂给沉浸度的量，
// 全都读它。之前 3 小时在两个函数里各写一遍，改一处忘一处就会让同一个阶段对一个是
// 过期的、对另一个还是当前的，正是 I1 禁的那种病。
func sceneHorizon(stage string) (time.Duration, bool) {
	switch stage {
	case "active", "climax", "aftercare":
		return 3 * time.Hour, true
	case "flirt":
		return 6 * time.Hour, true
	}
	return 0, false
}

// sceneLive 报告场景是否仍在进行。时间边界写在这里而不是依赖 sceneStage 已被衰减，
// 所以就算持久化的 stage 还没被 tick 更新过也不会误判成「还在进行中」。
// flirt 不算 live：调情是一条敞着的线，不是扣住她的人，她可以一边调情一边做饭，
// 所以它永远不该让她变得不可达。
func sceneLive(state State, at time.Time) bool {
	if state.SceneStartedAt <= 0 || state.SceneStage == "flirt" {
		return false
	}
	horizon, live := sceneHorizon(state.SceneStage)
	return live && at.Sub(time.UnixMilli(state.SceneStartedAt)) < horizon
}

// --- Scene wind-down -----------------------------------------------------
//
// A scene is a shared thing. The wall clock can bound it from above -- a scene
// still going at its horizon ends no matter what -- but only interaction sustains
// it from below. When he stops answering, the honest read is that the shared
// thing stopped, not that it is still running unattended. Without this a scene
// hung at "亲密中" for hours after he left, and the immersion it feeds kept her
// up past a bedtime nobody was keeping.
//
// Graceful is not "when does she exit". It is "there is no hole where he comes
// back", which is why a collapse has to leave an event behind.

const (
	// sceneIdleActiveToAftercare is how long silence in the middle of things
	// before she slows down on her own. Comfortably longer than an ordinary
	// pause -- nobody texts every thirty seconds -- and far short of the 45
	// minute wall-clock step it would otherwise wait for.
	sceneIdleActiveToAftercare = 18 * time.Minute
	// sceneIdleAftercareToEnd is the comedown running out of road.
	sceneIdleAftercareToEnd = 25 * time.Minute
	// sceneIdleFlirtToEnd is how long an open flirt thread survives nobody
	// pulling on it.
	sceneIdleFlirtToEnd = 30 * time.Minute
	// sceneCheckInDelay is how long his silence lasts before she asks, once,
	// whether he fell asleep. It sits ahead of the first collapse on purpose:
	// she says something before she tidies up.
	sceneCheckInDelay = 12 * time.Minute
)

// SceneWindDown records that a scene collapsed for want of anyone in it. It is
// what turns a bare state transition into something she can refer back to.
type SceneWindDown struct {
	FromStage   string `json:"fromStage"`
	ToStage     string `json:"toStage"`
	IdleMinutes int    `json:"idleMinutes"`
}

// idleStageTarget is the one rung down the ladder for a stage, and the silence
// that earns it.
//
// There is no path to "stopped" in here and there must never be one. stopped
// means consent was withdrawn; silence means nobody is here. Reading one as the
// other would turn "he did not reply" into "she rejected him", which is far
// worse than the stale scene this replaces.
func idleStageTarget(stage string) (to string, after time.Duration, ok bool) {
	switch stage {
	case "active", "climax":
		return "aftercare", sceneIdleActiveToAftercare, true
	case "aftercare":
		return "none", sceneIdleAftercareToEnd, true
	case "flirt":
		return "none", sceneIdleFlirtToEnd, true
	}
	return "", 0, false
}

// collapseIdleScene steps a scene down one rung when he has not touched it for
// long enough.
//
// It is called only from the tick, where there is no incoming turn. On the turn
// path SceneUpdatedAt still holds the *previous* turn, so measuring idle there
// would read a man who just walked back in as one who left twenty minutes ago
// and collapse the scene in front of him.
func collapseIdleScene(state *State, at time.Time) (bool, *SceneWindDown) {
	if state == nil || state.SceneStartedAt <= 0 {
		return false, nil
	}
	to, after, ok := idleStageTarget(state.SceneStage)
	if !ok {
		return false, nil
	}
	// The silence clock anchors on SceneTouchedAt -- the last time he was
	// actually in the scene. SceneUpdatedAt cannot serve: advanceSceneByWallClock
	// stamps it on every decay, so a scene that decayed after he left would come
	// back "just touched", and the step times inside the tick loop are all
	// earlier than that stamp, which would make idle negative and lose the
	// wind-down on the longest absences. Scenes written before this field
	// existed fall back through the older timestamps rather than reading as
	// instantly stale.
	anchor := state.SceneTouchedAt
	if anchor <= 0 {
		anchor = state.SceneUpdatedAt
	}
	if anchor <= 0 {
		anchor = state.SceneStartedAt
	}
	idle := at.Sub(time.UnixMilli(anchor))
	if idle < after {
		return false, nil
	}
	from := state.SceneStage
	state.SceneStage = to
	state.SceneUpdatedAt = at.UnixMilli()
	// A collapse restarts the same silence clock, so each rung gets its own full
	// duration instead of inheriting the minutes already burned reaching it.
	// Without this, aftercare would end about seven minutes after starting.
	state.SceneTouchedAt = at.UnixMilli()
	if to == "none" {
		state.ConsentState = "not_applicable"
		state.SceneStartedAt = 0
		state.SceneCheckInAt = 0
		state.SceneTouchedAt = 0
	}
	return true, &SceneWindDown{
		FromStage:   from,
		ToStage:     to,
		IdleMinutes: int(idle.Minutes()),
	}
}

// sceneWindDownEvent is the timeline entry a collapse leaves behind.
//
// The wording is deliberately motive-free. It states that he went quiet and what
// she did, never why. Assigning a reason in Go is how a wind-down turns into an
// accusation, and the whole point is that his having a life is not a grievance.
// She gets to speak her own version of this later; this is only the fact she
// speaks it from.
func sceneWindDownEvent(wind SceneWindDown, at time.Time) LifeEvent {
	content := "他没声音了，她慢下来，自己收拾了一下躺好。"
	switch wind.FromStage {
	case "aftercare":
		content = "他没再回话，她自己躺平刷了会儿手机。"
	case "flirt":
		content = "聊到一半他没动静了，她把手机放下去做自己的事。"
	}
	return LifeEvent{
		ID:         "evt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
		Type:       "scene",
		Content:    content,
		Importance: 6,
		Shareable:  false,
		OccurredAt: at.UnixMilli(),
	}
}

// sceneCheckInDue reports whether his silence inside a live scene has run long
// enough that she should ask, once, whether he is still there.
//
// This is a Go-side judgement on purpose. The model gets the scene stage and the
// last-contact timestamp, but making it do the arithmetic to work out how long
// the silence has run is exactly the kind of timing rule it cannot be trusted
// with -- that is why the wall-clock decay lives here and not in a prompt.
func sceneCheckInDue(state State, at time.Time) bool {
	if state.SceneCheckInAt <= 0 || at.UnixMilli() < state.SceneCheckInAt {
		return false
	}
	switch state.SceneStage {
	case "flirt", "active", "climax", "aftercare":
		return true
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
	horizon, live := sceneHorizon(state.SceneStage)
	if !live {
		return false
	}
	changed := false
	switch state.SceneStage {
	case "flirt":
		if elapsed >= horizon {
			state.SceneStage = "none"
			state.ConsentState = "not_applicable"
			changed = true
		}
	case "active", "climax":
		if elapsed >= horizon {
			state.SceneStage = "none"
			state.ConsentState = "not_applicable"
			changed = true
		} else if elapsed >= 45*time.Minute {
			state.SceneStage = "aftercare"
			changed = true
		}
	case "aftercare":
		if elapsed >= horizon {
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
