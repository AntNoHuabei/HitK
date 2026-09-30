package companion

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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
		return c.Date == at.Format("2006-01-02")
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

func weekdaySet() []time.Weekday {
	return []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
}

// defaultCommitments turns a schedule tendency into the set of things she normally
// carries. The tendency no longer draws a whole day; it only decides what hangs on
// top of the body.
func defaultCommitments(profile Profile) []Commitment {
	week := weekdaySet()
	switch profile.ScheduleTendency {
	case "busy":
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "上班，手头事情很多", Weight: 8, Weekdays: week, Start: "09:00", Duration: 540, Source: "profile"},
			{ID: "d_commute_am", Kind: "commute", Label: "通勤去公司", Weight: 4, Weekdays: week, Start: "08:10", Duration: 45, Source: "profile"},
			{ID: "d_commute_pm", Kind: "commute", Label: "通勤回家", Weight: 4, Weekdays: week, Start: "18:15", Duration: 45, Source: "profile"},
		}
	case "relaxed":
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "处理点不急的事", Weight: 4, Weekdays: week, Start: "10:00", Duration: 330, Source: "profile"},
		}
	case "social":
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "白天的事", Weight: 5, Weekdays: week, Start: "09:30", Duration: 420, Source: "profile"},
			{ID: "d_social_wed", Kind: "social", Label: "和朋友吃饭", Weight: 6, Weekdays: []time.Weekday{time.Wednesday}, Start: "19:00", Duration: 150, Source: "profile"},
			{ID: "d_social_sat", Kind: "social", Label: "和朋友出去逛", Weight: 7, Weekdays: []time.Weekday{time.Saturday}, Start: "15:00", Duration: 240, Source: "profile"},
		}
	default:
		return []Commitment{
			{ID: "d_work", Kind: "work", Label: "上班", Weight: 6, Weekdays: week, Start: "09:00", Duration: 480, Source: "profile"},
			{ID: "d_commute_am", Kind: "commute", Label: "通勤去公司", Weight: 3, Weekdays: week, Start: "08:30", Duration: 30, Source: "profile"},
			{ID: "d_commute_pm", Kind: "commute", Label: "通勤回家", Weight: 3, Weekdays: week, Start: "17:50", Duration: 30, Source: "profile"},
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
