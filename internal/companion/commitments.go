package companion

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CalendarScope narrows a recurring commitment to one side of the rest-day line.
// Without it a weekday job shows up on a statutory holiday, and a rest-day plan
// disappears on a 调休 Saturday that is really a working day.
type CalendarScope string

const (
	ScopeAlways  CalendarScope = ""
	ScopeWorkday CalendarScope = "workday"
	ScopeRestday CalendarScope = "restday"
)

// Commitment is one thing she has agreed to do. Work, an exam, a dinner, a business
// trip and a user request are all the same shape: they get laid over the body
// template and the day rearranges around them.
type Commitment struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // work | meal | exam | dinner | trip | errand | social | user
	Label string `json:"label"`
	// Weight is the prior decision. It buys tolerance for reluctance.
	Weight int `json:"weight"`
	// Weekdays makes a commitment recurring. Empty Weekdays plus a Date makes it a
	// one-off. Neither set means it never fires.
	Weekdays []time.Weekday `json:"weekdays,omitempty"`
	Date     string         `json:"date,omitempty"` // "2026-09-30"
	Start    string         `json:"start"`          // "18:00"
	Duration int            `json:"duration"`       // minutes
	// CalendarScope restricts a recurring commitment to working days or rest days.
	// It is checked in matches so no caller has to remember the holiday rule.
	CalendarScope CalendarScope `json:"calendarScope,omitempty"`
	// Deadline is when the thing stops being possible. Zero means the block's own
	// window closing is the deadline.
	Deadline int64  `json:"deadline,omitempty"`
	Source   string `json:"source"` // profile | user | model | trip
	// City overrides the profile city for the span, which is how a trip changes the
	// weather she talks about.
	City string `json:"city,omitempty"`
}

// matches reports whether this commitment lands on the given calendar day.
func (c Commitment) matches(at time.Time) bool {
	if c.Date != "" {
		// An explicit date is the strongest constraint; scope cannot override it.
		return c.Date == at.Format("2006-01-02")
	}
	if !c.scopeAllows(at) {
		return false
	}
	// A scope with no weekday list is a complete selector: "every working day"
	// has to include a 调休 Saturday, which a Mon-Fri list never would.
	if c.CalendarScope != ScopeAlways && len(c.Weekdays) == 0 {
		return true
	}
	if len(c.Weekdays) == 0 {
		return false
	}
	for _, day := range c.Weekdays {
		if day == at.Weekday() {
			return true
		}
	}
	return false
}

func (c Commitment) scopeAllows(at time.Time) bool {
	switch c.CalendarScope {
	case ScopeWorkday:
		return DayInfoAt(at).IsWorkDay()
	case ScopeRestday:
		return DayInfoAt(at).IsRestDay()
	default:
		return true
	}
}

func weekdaySet() []time.Weekday {
	return []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
}

// defaultCommitments turns a schedule tendency into the set of things she normally
// carries. The tendency no longer draws a whole day; it only decides what hangs on
// top of the body.
// defaultCommitments turns a schedule tendency into the set of things she normally
// carries. The tendency no longer draws a whole day; it only decides what hangs on
// top of the body.
//
// Work and commute carry the workday scope with no weekday list on purpose. A
// Mon-Fri list would keep them off a 调休 Saturday that the notice puts back on the
// calendar, and would keep them on for a year the table knows nothing about, which
// is exactly the fallback we want.
func defaultCommitments(profile Profile) []Commitment {
	switch profile.ScheduleTendency {
	case "busy":
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "上班，手头事情很多", Weight: 8, CalendarScope: ScopeWorkday, Start: "09:00", Duration: 540, Source: "profile"},
			{ID: "d_commute_am", Kind: "commute", Label: "通勤去公司", Weight: 4, CalendarScope: ScopeWorkday, Start: "08:10", Duration: 45, Source: "profile"},
			{ID: "d_commute_pm", Kind: "commute", Label: "通勤回家", Weight: 4, CalendarScope: ScopeWorkday, Start: "18:15", Duration: 45, Source: "profile"},
		}
	case "relaxed":
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "处理点不急的事", Weight: 4, CalendarScope: ScopeWorkday, Start: "10:00", Duration: 330, Source: "profile"},
		}
	case "social":
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "白天的事", Weight: 5, CalendarScope: ScopeWorkday, Start: "09:30", Duration: 420, Source: "profile"},
			{ID: "d_social_wed", Kind: "social", Label: "和朋友吃饭", Weight: 6, Weekdays: []time.Weekday{time.Wednesday}, Start: "19:00", Duration: 150, Source: "profile"},
			{ID: "d_social_sat", Kind: "social", Label: "和朋友出去逛", Weight: 7, Weekdays: []time.Weekday{time.Saturday}, Start: "15:00", Duration: 240, Source: "profile"},
		}
	default:
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "上班", Weight: 6, CalendarScope: ScopeWorkday, Start: "09:00", Duration: 480, Source: "profile"},
			{ID: "d_commute_am", Kind: "commute", Label: "通勤去公司", Weight: 3, CalendarScope: ScopeWorkday, Start: "08:30", Duration: 30, Source: "profile"},
			{ID: "d_commute_pm", Kind: "commute", Label: "通勤回家", Weight: 3, CalendarScope: ScopeWorkday, Start: "17:50", Duration: 30, Source: "profile"},
		}
	}
}

// mergeCommitments puts the stored extras on top of the profile defaults and sorts
// by start time so blocks get laid down in chronological order.
func mergeCommitments(profile Profile, stored []Commitment) []Commitment {
	out := append([]Commitment(nil), defaultCommitments(profile)...)
	out = append(out, stored...)
	sort.SliceStable(out, func(i, j int) bool {
		return parseClock(out[i].Start) < parseClock(out[j].Start)
	})
	return out
}

// NewCommitmentID mints an id for a commitment built at runtime.
func NewCommitmentID() string {
	return "cmt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

const (
	// maxTurnCommitments caps how many plans one turn may add, mirroring the
	// memory cap. A model that books her whole week in one reply is a bug.
	maxTurnCommitments = 3
	// maxCommitmentHorizonDays keeps a misread "next year" from quietly filling
	// her calendar far past where anyone would notice it was wrong.
	maxCommitmentHorizonDays = 90
)

var commitmentKinds = map[string]bool{
	"work": true, "meal": true, "exam": true, "dinner": true,
	"trip": true, "errand": true, "social": true, "user": true,
}

// NormalizeTurnCommitments validates what the alignment model heard and drops
// everything it cannot stand behind. Anything vague, undated, already on the
// books, or too far out simply does not land, which is the correct outcome: a
// plan she never really made should not appear in her day.
func NormalizeTurnCommitments(drafts []CommitmentDraft, existing []Commitment, now time.Time) []Commitment {
	return normalizeCommitments(drafts, existing, now, maxTurnCommitments)
}

// NormalizeCommitmentsWithin is NormalizeTurnCommitments with the cap supplied by
// the caller. "She moved to Chengdu for a week" is one decision that covers
// seven dated days, so it needs a bigger budget than a plan heard in chat. The
// validation rules stay in exactly one place.
func NormalizeCommitmentsWithin(
	drafts []CommitmentDraft, existing []Commitment, now time.Time, limit int,
) []Commitment {
	return normalizeCommitments(drafts, existing, now, limit)
}

func normalizeCommitments(
	drafts []CommitmentDraft, existing []Commitment, now time.Time, limit int,
) []Commitment {
	if len(drafts) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, held := range existing {
		if held.Date != "" {
			seen[commitmentKey(held.Date, held.Label)] = true
		}
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	horizon := today.AddDate(0, 0, maxCommitmentHorizonDays)
	var out []Commitment
	for _, draft := range drafts {
		if limit > 0 && len(out) >= limit {
			break
		}
		label := strings.TrimSpace(draft.Label)
		if label == "" {
			continue
		}
		if runes := []rune(label); len(runes) > 40 {
			label = string(runes[:40])
		}
		date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(draft.Date), now.Location())
		if err != nil {
			continue
		}
		// Same-day is fine -- "tonight" is a real plan. Last week is not.
		if date.Before(today) || date.After(horizon) {
			continue
		}
		start := parseClock(draft.Start)
		if start < 0 {
			start = 9 * 60
		}
		duration := draft.Duration
		if duration < 15 {
			duration = 60
		}
		if duration > 720 {
			duration = 720
		}
		weight := draft.Weight
		if weight < 1 || weight > 10 {
			weight = 6
		}
		dateText := date.Format("2006-01-02")
		key := commitmentKey(dateText, label)
		if seen[key] {
			continue
		}
		seen[key] = true
		kind := strings.TrimSpace(draft.Kind)
		if !commitmentKinds[kind] {
			kind = "user"
		}
		out = append(out, Commitment{
			ID:       NewCommitmentID(),
			Kind:     kind,
			Label:    label,
			Weight:   weight,
			Date:     dateText,
			Start:    fmt.Sprintf("%02d:%02d", start/60, start%60),
			Duration: duration,
			Source:   "user",
			City:     strings.TrimSpace(draft.City),
		})
	}
	return out
}

func commitmentKey(date, label string) string {
	return date + "|" + normalize(label)
}
