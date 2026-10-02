package companion

// Director mode is a hand inside her world, not a second simulation.
//
// Every instruction lands on a primitive that already exists: an arrangement
// becomes a Commitment, a disruption becomes a Drift, a mood becomes a timed
// ActiveEffect, something that happened becomes a LifeEvent, a change of scene
// becomes a Commitment with a City. No new numeric channel is opened, so the
// invariants in docs/companion-design.md keep holding without exception.
//
// She never learns there is a hand. A directive is translated into an objective
// fact in her world -- "her mother says she is coming over tomorrow" -- and
// enters the model as narration, never as something the user said to her. That
// matters: rain falling because the user willed it is not the same as someone
// keeping her company, so a directive must not buy down her loneliness.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Directive status. A directive is written as pending when it arrives so the raw
// words survive even if the model never answers, then settled with what actually
// landed.
const (
	DirectivePending  = "pending"
	DirectiveApplied  = "applied"
	DirectivePartial  = "partial"
	DirectiveRejected = "rejected"
)

// Change kinds.
const (
	ChangeArrangement = "arrangement"
	ChangeDisruption  = "disruption"
	ChangeMood        = "mood"
	ChangeEvent       = "event"
	ChangePlace       = "place"
)

const (
	// maxDirectiveChanges caps one instruction at three world edits, matching
	// the commitment and memory caps.
	maxDirectiveChanges = 3
	// maxPlaceDays is how long one "she went to Chengdu" may cover.
	maxPlaceDays = 14
	// directiveEffectPrefix marks effects the director created, so a revert can
	// find them by prefix and nothing mistakes them for rule-driven effects.
	directiveEffectPrefix = "director:"
	// minDirectiveEffectMinutes is the shortest effect a director may write.
	// Anything shorter is indistinguishable from pinning a number, which is the
	// one thing this mode must never do.
	minDirectiveEffectMinutes = 15
)

// DirectorDirective is one intervention and its audit trail.
type DirectorDirective struct {
	ID          string            `json:"id"`
	CharacterID string            `json:"characterId"`
	ThreadID    string            `json:"threadId"`
	// Text is the user's own words, kept verbatim. The ledger is worthless if it
	// only stores the model's paraphrase.
	Text      string `json:"text"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Changes   []DirectiveChange `json:"changes,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	// Narration is the in-world wording the directive produced. This is what she
	// is told; her never sees the instruction itself.
	Narration string `json:"narration,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	RevertedAt int64 `json:"revertedAt,omitempty"`
}

// DirectiveChange is one thing the directive actually did.
type DirectiveChange struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// RefIDs are the rows this change created. A disruption has none because it
	// edits the day rather than adding a row to look up later.
	RefIDs     []string `json:"refIds,omitempty"`
	Revertible bool     `json:"revertible"`
}

// Reverted reports whether this intervention has already been taken back.
func (d DirectorDirective) Reverted() bool { return d.RevertedAt > 0 }

// WorldDraft is the director's slice of the alignment output. It is only filled
// when the turn was triggered by a directive, so ordinary chat pays nothing for
// it and no extra model call is made.
type WorldDraft struct {
	// Arrangements are dated things that will happen: a visit, an interview, a
	// trip. They become commitments.
	Arrangements []DirectorArrangement `json:"arrangements,omitempty"`
	// Disruptions are lived time pushed into today. Never revertible.
	Disruptions []DisruptionDraft `json:"disruptions,omitempty"`
	// MoodEffects are timed feeling changes. The duration is mandatory on
	// purpose: a director nudges a metric along a decay curve, never pins it.
	MoodEffects []DirectorMoodDraft `json:"moodEffects,omitempty"`
	// Events are things that just happened to her, which she can bring up later.
	Events []DirectorEventDraft `json:"events,omitempty"`
	// Place moves where she is standing, which moves the weather she talks about.
	Place *DirectorPlaceDraft `json:"place,omitempty"`
	// Narration is the in-world wording: "her mother says she is coming over
	// tomorrow". This is what reaches her, not the instruction.
	Narration string `json:"narration,omitempty"`
	// ShouldReply decides whether she speaks up about this now. A background
	// arrangement leaves it false so editing her world does not ping the user.
	ShouldReply *bool `json:"shouldReply,omitempty"`
}

// DirectorArrangement is a commitment draft plus one field that exists only so we
// can refuse it. There is no such thing as a trip she cannot miss.
type DirectorArrangement struct {
	CommitmentDraft
	MustAttend *bool `json:"mustAttend,omitempty"`
}

// DisruptionDraft is one push into today. StartBlock and CompleteBlock are
// accepted by the schema only to be rejected here: the director can make things
// happen to her, not drive her hands.
type DisruptionDraft struct {
	Start    string `json:"start,omitempty"`
	Minutes  int    `json:"minutes"`
	Severity int    `json:"severity,omitempty"`
	Reason   string `json:"reason"`
	// Forbidden: making her start or finish a scheduled thing.
	StartBlock    string `json:"startBlock,omitempty"`
	CompleteBlock string `json:"completeBlock,omitempty"`
}

// DirectorMoodDraft is one timed feeling change. SetTo exists only to be
// rejected: metrics are driven by dynamics, and a director who could set one
// outright would turn every reading in the panel into a fake.
type DirectorMoodDraft struct {
	Target          string  `json:"target"`
	RatePerHour     float64 `json:"ratePerHour,omitempty"`
	BaselineDelta   float64 `json:"baselineDelta,omitempty"`
	RecoveryDelta   float64 `json:"recoveryDelta,omitempty"`
	DurationMinutes int     `json:"durationMinutes"`
	Label           string  `json:"label,omitempty"`
	Curve           string  `json:"curve,omitempty"`
	// Forbidden: writing a metric to a fixed value.
	SetTo *float64 `json:"setTo,omitempty"`
}

// DirectorEventDraft is something that just happened to her.
type DirectorEventDraft struct {
	Content    string `json:"content"`
	Importance int    `json:"importance,omitempty"`
	Shareable  *bool  `json:"shareable,omitempty"`
}

// DirectorPlaceDraft moves her for a span of days.
type DirectorPlaceDraft struct {
	City string `json:"city"`
	Date string `json:"date,omitempty"`
	Days int    `json:"days,omitempty"`
}

// plannedChange is a validated change before it has rows of its own. Keeping the
// validation result separate from the persistence step is what lets a rejected
// item produce a reason instead of a half-written world.
type plannedChange struct {
	Kind        string
	Summary     string
	Revertible  bool
	Commitments []CommitmentDraft
	Drift       *Drift
	Effect      *ActiveEffect
	Event       *LifeEvent
}

// directivePlan is the whole outcome of reading one instruction.
type directivePlan struct {
	Directive DirectorDirective
	Changes   []plannedChange
	Reasons   []string
}

// directiveInput is everything compileDirective needs, passed in rather than
// reached for, so the whole rule set is testable without a database.
type directiveInput struct {
	CharacterID string
	ThreadID    string
	TaskID      string
	DirectiveID string
	Text        string
	Draft       WorldDraft
	Asleep      bool
	WakeAt      time.Time
	Now         time.Time
	// Metrics is the set of metric ids the engine actually knows about, so a
	// made-up target is refused rather than silently doing nothing.
	Metrics map[MetricID]bool
}

func newDirectiveID() string {
	return "dir_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// compileDirective turns a world draft into a ledger entry and a list of changes,
// refusing everything the design does not allow. It writes nothing.
func compileDirective(in directiveInput) directivePlan {
	plan := directivePlan{Directive: DirectorDirective{
		ID:          in.DirectiveID,
		CharacterID: in.CharacterID,
		ThreadID:    in.ThreadID,
		TaskID:      in.TaskID,
		Text:        strings.TrimSpace(in.Text),
		Status:      DirectivePending,
		CreatedAt:   in.Now.UnixMilli(),
	}}
	reject := func(reason string) {
		if strings.TrimSpace(reason) != "" {
			plan.Reasons = append(plan.Reasons, reason)
		}
	}
	full := func() bool {
		if len(plan.Changes) >= maxDirectiveChanges {
			reject("一次最多改三样，多出来的先不写进她的世界。")
			return true
		}
		return false
	}

	for _, item := range in.Draft.Arrangements {
		if full() {
			break
		}
		label := strings.TrimSpace(item.Label)
		if item.MustAttend != nil {
			reject("「" + firstNonEmpty(label, "这条安排") + "」不能是非去不可。只能给重要程度，她仍然可能因为太累而不想去。")
			continue
		}
		if label == "" {
			reject("有一条安排连名字都没有，写不进她的日子。")
			continue
		}
		if reason := checkDirectiveDate(item.Date, in.Now); reason != "" {
			reject("「" + label + "」" + reason)
			continue
		}
		plan.Changes = append(plan.Changes, plannedChange{
			Kind:        ChangeArrangement,
			Summary:     describeWhen(item.Date, item.Start, in.Now) + " " + label,
			Revertible:  true,
			Commitments: []CommitmentDraft{item.CommitmentDraft},
		})
	}

	for _, item := range in.Draft.Disruptions {
		if full() {
			break
		}
		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			reject("有一件突发的事没说清楚是什么。")
			continue
		}
		if strings.TrimSpace(item.StartBlock) != "" || strings.TrimSpace(item.CompleteBlock) != "" {
			reject("不能替她开始或完成一件事。只能让事情落到她身上，做不做还是她自己决定。")
			continue
		}
		// A disruption is happening right now, and now is not available to a
		// sleeping person. This is the one hard wall: nothing here wakes her.
		if in.Asleep {
			reject("她正在睡觉，「" + reason + "」这种当下发生的事落不到她身上。")
			continue
		}
		if item.Minutes < 5 {
			reject("「" + reason + "」得有个时长，不然推不动她的一天。")
			continue
		}
		minutes := item.Minutes
		if minutes > 240 {
			minutes = 240
		}
		severity := clampInt(item.Severity, 1, 10)
		if item.Severity < 1 {
			severity = 5
		}
		atMinute := parseClock(item.Start)
		if atMinute < 0 {
			atMinute = minuteOfDay(in.Now)
		}
		plan.Changes = append(plan.Changes, plannedChange{
			Kind:       ChangeDisruption,
			Summary:    fmt.Sprintf("%s（%d 分钟）", reason, minutes),
			Revertible: false,
			Drift: &Drift{
				AtMinute: atMinute, Minutes: minutes, Severity: severity,
				Reason: reason, Origin: "director",
			},
		})
	}

	for _, item := range in.Draft.MoodEffects {
		if full() {
			break
		}
		target := MetricID(strings.TrimSpace(item.Target))
		name := firstNonEmpty(strings.TrimSpace(item.Label), string(target))
		if item.SetTo != nil {
			reject("不能把「" + name + "」直接按到某个数上。只能给带时长和衰减的影响，剩下的交给她自己的身体去平衡。")
			continue
		}
		if !in.Metrics[target] {
			reject("认不出「" + name + "」这个状态。")
			continue
		}
		if item.DurationMinutes < minDirectiveEffectMinutes {
			reject("「" + name + "」没有时长。没有时长的影响等于直接把数值钉死，这个口子不开。")
			continue
		}
		if item.RatePerHour == 0 && item.BaselineDelta == 0 && item.RecoveryDelta == 0 {
			reject("「" + name + "」三个力度都是 0，那什么都没改。")
			continue
		}
		duration := time.Duration(clampInt(item.DurationMinutes, minDirectiveEffectMinutes, 1440)) * time.Minute
		effect := ActiveEffect{
			ID:            "eff_dir_" + strings.TrimPrefix(in.DirectiveID, "dir_") + "_" + string(target),
			SourceRuleID:  directiveEffectPrefix + in.DirectiveID + ":" + string(target),
			Target:        target,
			StartAt:       in.Now.UnixMilli(),
			EndAt:         in.Now.Add(duration).UnixMilli(),
			RatePerHour:   item.RatePerHour,
			BaselineDelta: item.BaselineDelta,
			RecoveryDelta: item.RecoveryDelta,
			Curve:         normalizeEffectCurve(item.Curve),
		}
		plan.Changes = append(plan.Changes, plannedChange{
			Kind:       ChangeMood,
			Summary:    fmt.Sprintf("%s · %s 影响 %d 小时", name, describeForce(item), int(duration.Hours())),
			Revertible: true,
			Effect:     &effect,
		})
	}

	for _, item := range in.Draft.Events {
		if full() {
			break
		}
		content := clipRunes(strings.TrimSpace(item.Content), 200)
		if content == "" {
			reject("有一件事没写内容。")
			continue
		}
		if in.Asleep {
			reject("她正在睡觉，「" + content + "」这种事她没醒着就遇不上。")
			continue
		}
		importance := clampInt(item.Importance, 1, 10)
		if item.Importance < 1 {
			importance = 6
		}
		shareable := true
		if item.Shareable != nil {
			shareable = *item.Shareable
		}
		event := LifeEvent{
			ID:         "evt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
			Type:       "director",
			Content:    content,
			Importance: importance,
			Shareable:  shareable,
			OccurredAt: in.Now.UnixMilli(),
		}
		plan.Changes = append(plan.Changes, plannedChange{
			Kind: ChangeEvent, Summary: content, Revertible: true, Event: &event,
		})
	}

	if place := in.Draft.Place; place != nil {
		if full() {
			// Consumed the slot either way so a bad place cannot smuggle past the cap.
		} else {
			city := strings.TrimSpace(place.City)
			if city == "" {
				reject("没说清楚她去了哪个城市。")
			} else if reason := checkDirectiveDate(orDefault(place.Date, in.Now.Format("2006-01-02")), in.Now); reason != "" {
				reject("「去" + city + "」" + reason)
			} else {
				days := clampInt(place.Days, 1, maxPlaceDays)
				if place.Days < 1 {
					days = 1
				}
				startDate := orDefault(place.Date, in.Now.Format("2006-01-02"))
				drafts := make([]CommitmentDraft, 0, days)
				for i := 0; i < days; i++ {
					day, err := time.ParseInLocation("2006-01-02", startDate, in.Now.Location())
					if err != nil {
						break
					}
					day = day.AddDate(0, 0, i)
					drafts = append(drafts, CommitmentDraft{
						Kind: "trip", Label: "在" + city, Date: day.Format("2006-01-02"),
						Start: "09:00", Duration: 600, Weight: 5, City: city,
					})
				}
				summary := "去了" + city
				if days > 1 {
					summary = fmt.Sprintf("在%s待%d天", city, days)
				}
				plan.Changes = append(plan.Changes, plannedChange{
					Kind: ChangePlace, Summary: summary, Revertible: true, Commitments: drafts,
				})
			}
		}
	}

	plan.Directive.Narration = clipRunes(strings.TrimSpace(in.Draft.Narration), 200)
	if plan.Directive.Narration == "" && len(plan.Changes) > 0 {
		plan.Directive.Narration = plan.Changes[0].Summary
	}
	return plan
}

// settleDirective recomputes status once the changes have actually been written
// and we know which ones really landed.
func settleDirective(plan directivePlan, landed []DirectiveChange) DirectorDirective {
	directive := plan.Directive
	directive.Changes = landed
	switch {
	case len(landed) == 0:
		directive.Status = DirectiveRejected
		directive.Reason = strings.Join(plan.Reasons, " ")
		if directive.Reason == "" {
			directive.Reason = "这条指令翻译不出任何能落到她世界里的东西。"
		}
	case len(plan.Reasons) > 0:
		directive.Status = DirectivePartial
		directive.Reason = strings.Join(plan.Reasons, " ")
	default:
		directive.Status = DirectiveApplied
		directive.Reason = ""
	}
	return directive
}

// applyDirectivePlan writes a compiled plan through the existing primitives and
// returns the rows that belong in the turn's own transaction. Drift is the
// exception: it edits the day in place and persists itself.
func (m *Manager) applyDirectivePlan(plan directivePlan, now time.Time) (DirectorDirective, []Commitment, []ActiveEffect, []LifeEvent) {
	landed := make([]DirectiveChange, 0, len(plan.Changes))
	reasons := append([]string(nil), plan.Reasons...)
	var commitments []Commitment
	var effects []ActiveEffect
	var events []LifeEvent

	for _, change := range plan.Changes {
		record := DirectiveChange{Kind: change.Kind, Summary: change.Summary, Revertible: change.Revertible}
		switch change.Kind {
		case ChangeArrangement:
			held, _ := m.store.Commitments(m.id)
			for _, built := range NormalizeTurnCommitments(change.Commitments, held, now) {
				commitments = append(commitments, built)
				record.RefIDs = append(record.RefIDs, built.ID)
			}
		case ChangePlace:
			held, _ := m.store.Commitments(m.id)
			// One trip is one decision however many days it covers, so it gets
			// past the three-per-turn cap that applies to plans heard in chat.
			for _, built := range NormalizeCommitmentsWithin(change.Commitments, held, now, maxPlaceDays) {
				commitments = append(commitments, built)
				record.RefIDs = append(record.RefIDs, built.ID)
			}
		case ChangeMood:
			if change.Effect != nil {
				effects = append(effects, *change.Effect)
				record.RefIDs = append(record.RefIDs, change.Effect.ID)
			}
		case ChangeEvent:
			if change.Event != nil {
				events = append(events, *change.Event)
				record.RefIDs = append(record.RefIDs, change.Event.ID)
			}
		case ChangeDisruption:
			if change.Drift != nil {
				m.ApplyDriftNow(now, *change.Drift)
			}
		}
		// A disruption leaves no row behind, so "nothing to point at" is normal
		// there. Everywhere else it means the write refused the item, most often
		// because she already had that plan.
		if !change.Revertible || len(record.RefIDs) > 0 {
			landed = append(landed, record)
			continue
		}
		reasons = append(reasons, "「"+change.Summary+"」没能落地，可能她本来就有这一条。")
	}

	settled := settleDirective(directivePlan{Directive: plan.Directive, Changes: plan.Changes, Reasons: reasons}, landed)
	return settled, commitments, effects, events
}

// RevertDirective takes an intervention back: the arrangements go, the mood
// effect goes, the event goes. A disruption never goes, because the minutes it
// pushed into her day already happened and time does not run backwards.
func (m *Manager) RevertDirective(id string) error {
	directive, err := m.store.Directive(m.id, id)
	if err != nil {
		return err
	}
	if directive.Reverted() {
		return nil
	}
	// A pending row means the turn is still writing. Reverting now would mark the
	// row reverted and then the settling turn would overwrite that with a fresh
	// status, quietly un-reverting it. Refuse instead of racing.
	if directive.Status == DirectivePending {
		return errors.New("这条指令还在处理，等她处理完再撤")
	}
	for _, change := range directive.Changes {
		if !change.Revertible {
			continue
		}
		switch change.Kind {
		case ChangeArrangement, ChangePlace:
			for _, ref := range change.RefIDs {
				if err := m.revertCommittedRow(ref); err != nil {
					return err
				}
			}
		case ChangeMood:
			if err := m.store.RemoveEffectsBySourcePrefix(directiveEffectPrefix + id); err != nil {
				return err
			}
		case ChangeEvent:
			for _, ref := range change.RefIDs {
				if err := m.store.DeleteEvent(ref); err != nil {
					return err
				}
			}
		}
	}
	directive.RevertedAt = time.Now().UnixMilli()
	for i := range directive.Changes {
		if directive.Changes[i].Kind == ChangeDisruption {
			directive.Changes[i].Revertible = false
		}
	}
	if err := m.store.SaveDirective(m.id, directive); err != nil {
		return err
	}
	// 发一份真的 Overview。空的会把头部和状态面板瞬间清空一下。
	overview, _ := m.overviewUnchecked(context.Background())
	m.emitEvent(EventStateUpdated, overview)
	return nil
}

// revertCommittedRow drops a commitment and, if that day is already written down,
// the unstarted block it produced. A block she already started is lived time and
// stays: pulling it out would be time travel, which this mode does not do.
func (m *Manager) revertCommittedRow(commitmentID string) error {
	stored, err := m.store.Commitments(m.id)
	if err != nil {
		return err
	}
	var target *Commitment
	for i := range stored {
		if stored[i].ID == commitmentID {
			target = &stored[i]
			break
		}
	}
	if target == nil {
		return m.store.DeleteCommitment(m.id, commitmentID)
	}
	if target.Date != "" {
		if plan, ok, planErr := m.store.DayPlan(m.id, target.Date); planErr == nil && ok {
			kept := plan.Blocks[:0]
			for _, block := range plan.Blocks {
				if block.Label == target.Label && block.Origin == "user" && block.StartedAt == 0 {
					continue
				}
				kept = append(kept, block)
			}
			plan.Blocks = kept
			plan.Revision++
			if err := m.store.SaveDayPlan(m.id, plan); err != nil {
				return err
			}
		}
	}
	return m.store.DeleteCommitment(m.id, commitmentID)
}

// ListDirectives returns this character's interventions, newest first.
func (m *Manager) ListDirectives(limit int) ([]DirectorDirective, error) {
	return m.store.Directives(m.id, limit)
}

// markDirectiveFailed closes out a ledger row whose turn died before it could
// settle. Without this the row sits at "pending" forever, and an intervention
// with no recorded final state is not auditable (I33). Only a row still marked
// pending is touched, so a directive that already landed and then hit a later
// failure keeps the status it really earned.
func (m *Manager) markDirectiveFailed(directiveID, reason string) {
	directive, err := m.store.Directive(m.id, directiveID)
	if err != nil || directive.Status != DirectivePending {
		return
	}
	directive.Status = DirectiveRejected
	directive.Reason = firstNonEmpty(strings.TrimSpace(reason), "这条指令没能执行完。")
	_ = m.store.SaveDirective(m.id, directive)
}

// checkDirectiveDate answers why a date will not do, in words worth showing a
// person. Empty means it is fine.
func checkDirectiveDate(date string, now time.Time) string {
	trimmed := strings.TrimSpace(date)
	if trimmed == "" {
		return "没有日期，落不到具体的哪一天。"
	}
	parsed, err := time.ParseInLocation("2006-01-02", trimmed, now.Location())
	if err != nil {
		return "的日期读不出来，要 YYYY-MM-DD。"
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if parsed.Before(today) {
		return "已经过去了，改不了。"
	}
	if parsed.After(today.AddDate(0, 0, maxCommitmentHorizonDays)) {
		return "太远了，超出她能记住的范围。"
	}
	return ""
}

// describeWhen renders a date the way a person would say it, so the ledger reads
// "明天 15:00 妈妈来访" rather than a ISO string.
func describeWhen(date, start string, now time.Time) string {
	parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(date), now.Location())
	if err != nil {
		return strings.TrimSpace(start)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := int(parsed.Sub(today).Hours() / 24)
	var when string
	switch days {
	case 0:
		when = "今天"
	case 1:
		when = "明天"
	case 2:
		when = "后天"
	default:
		when = parsed.Format("1月2日")
	}
	if clock := parseClock(start); clock >= 0 {
		when += fmt.Sprintf(" %02d:%02d", clock/60, clock%60)
	}
	return when
}

func describeForce(item DirectorMoodDraft) string {
	switch {
	case item.BaselineDelta != 0:
		return signedRound(item.BaselineDelta)
	case item.RatePerHour != 0:
		return signedRound(item.RatePerHour) + "/小时"
	default:
		return signedRound(item.RecoveryDelta)
	}
}

func signedRound(value float64) string {
	rounded := math.Round(value*10) / 10
	if rounded > 0 {
		return fmt.Sprintf("+%g", rounded)
	}
	return fmt.Sprintf("%g", rounded)
}

func normalizeEffectCurve(curve string) string {
	switch strings.ToLower(strings.TrimSpace(curve)) {
	case "constant", "ramp", "pulse", "linear_decay", "ramp_decay":
		return strings.ToLower(strings.TrimSpace(curve))
	default:
		return "constant"
	}
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

// errDirectiveEmpty is returned before a task is even created, so an empty
// instruction never becomes a turn that has to fail later.
var errDirectiveEmpty = errors.New("指令不能为空")
