package companion

import (
	"fmt"
	"hash/fnv"
	"time"
)

// scheduleBlock is one bounded activity segment in her day.
type scheduleBlock struct {
	ID          string
	Kind        string
	Label       string
	StartMinute int
	EndMinute   int
	// Weight is how much this commitment matters to her, 0..10. There is
	// deliberately no must-attend flag: nothing is unmissable, there is only
	// whether the importance is high enough to beat whatever is pushing against it.
	Weight int
	// Origin records where the block came from: plan, drift, or user.
	Origin string
	// ShiftedBy is the net minutes this block moved from where it was planned.
	ShiftedBy int
	// Missed means the pressure won and she is not doing this at all.
	Missed bool
	// MissReason keeps the reason she would give for skipping it.
	MissReason string
}

// DayPlan is her day as a mutable record rather than a lookup. The plan is edited
// in place as the day actually happens, so Origin and ShiftedBy together carry the
// plan-versus-actual story without needing a second log.
type DayPlan struct {
	Date string
	// Kind is workday, weekend or dayoff.
	Kind string
	// PlannedAt is when this plan was written, which is how "planned last night"
	// stays distinguishable from "made it up this morning".
	PlannedAt int64
	Revision  int
	Blocks    []scheduleBlock
}

// Drift is one disruption pushing lived time into the day.
type Drift struct {
	// AtMinute is where the disruption lands, as minutes since midnight.
	AtMinute int
	// Minutes is how much lived time it consumes.
	Minutes int
	// Severity is how unavoidable it is, 0..10. Losing track of time is a 2, a
	// transit failure is an 8. A block only defends its own length when its weight
	// is at least this, and a commitment only gets fought for at all when its weight
	// clears this bar.
	Severity int
	Reason   string
	Origin   string
}

// DriftOutcome reports what the day had to give up.
type DriftOutcome struct {
	// Absorbed is minutes eaten by the interrupted block's own remaining time.
	Absorbed int
	// Clawed is minutes reclaimed from lighter blocks downstream.
	Clawed int
	// Late is how far the tail still slid.
	Late int
	// MissedID is a commitment she fought for and still lost.
	MissedID string
	// MissedLabel carries what that commitment was, so the loss can be narrated
	// without digging through the plan.
	MissedLabel string
}

func (p DayPlan) blockAt(at time.Time) scheduleBlock {
	minute := at.Hour()*60 + at.Minute()
	for _, block := range p.Blocks {
		if minute >= block.StartMinute && minute < block.EndMinute {
			return block
		}
	}
	if len(p.Blocks) == 0 {
		return scheduleBlock{Kind: "rest", Label: "空闲", Origin: "plan", Weight: 2, StartMinute: 0, EndMinute: 1440}
	}
	// Past the end of the plan: hold the last block rather than falling off the map.
	return p.Blocks[len(p.Blocks)-1]
}

func (p DayPlan) indexAt(minute int) int {
	for i, block := range p.Blocks {
		if minute >= block.StartMinute && minute < block.EndMinute {
			return i
		}
	}
	return -1
}

// heaviestBlockAfter finds the most important live block downstream of idx. That is
// the thing the day will be fought for, if anything.
func (p DayPlan) heaviestBlockAfter(idx int) (int, int) {
	bestIdx, bestWeight := -1, -1
	for i := idx + 1; i < len(p.Blocks); i++ {
		block := p.Blocks[i]
		if block.Missed {
			continue
		}
		if block.Weight > bestWeight {
			bestIdx, bestWeight = i, block.Weight
		}
	}
	return bestIdx, bestWeight
}

// shiftAfter moves every block after index i by delta minutes.
func (p *DayPlan) shiftAfter(i, delta int) {
	if delta == 0 {
		return
	}
	for k := i + 1; k < len(p.Blocks); k++ {
		p.Blocks[k].StartMinute += delta
		p.Blocks[k].EndMinute += delta
		p.Blocks[k].ShiftedBy += delta
	}
}

// slideTail pushes everything after idx later, capped so the day still ends at
// midnight, and reports how much actually landed.
func (p *DayPlan) slideTail(idx, minutes int) int {
	if minutes <= 0 || len(p.Blocks) == 0 {
		return 0
	}
	if last := p.Blocks[len(p.Blocks)-1].EndMinute; last+minutes > 1440 {
		minutes = 1440 - last
	}
	if minutes <= 0 {
		return 0
	}
	p.shiftAfter(idx, minutes)
	return minutes
}

// ApplyDrift pushes lived time into the day and lets importance decide who loses.
// Light blocks give up their slack; a heavy one gets fought for, and is only skipped
// when even fighting cannot get her there in time.
func (p *DayPlan) ApplyDrift(d Drift) DriftOutcome {
	var out DriftOutcome
	if d.Minutes <= 0 || len(p.Blocks) == 0 {
		return out
	}
	idx := p.indexAt(d.AtMinute)
	if idx < 0 && d.AtMinute < p.Blocks[0].StartMinute {
		// Landed before the plan begins, which is oversleeping. The whole day slides
		// rather than the time quietly vanishing.
		out.Late = p.slideTail(-1, d.Minutes)
		p.Revision++
		return out
	}
	if idx < 0 {
		// After the day is already over.
		return out
	}
	eventEnd := d.AtMinute + d.Minutes
	if eventEnd <= p.Blocks[idx].EndMinute {
		// The disruption fit inside the block's own time; nothing downstream moves.
		out.Absorbed = d.Minutes
		return out
	}
	overflow := eventEnd - p.Blocks[idx].EndMinute
	p.Blocks[idx].EndMinute = eventEnd
	// Slide first so every downstream block is visibly late, then claw time back.
	// Clawing before sliding would pull the wall earlier than it was ever planned.
	overflow = p.slideTail(idx, overflow)
	if overflow <= 0 {
		p.Revision++
		return out
	}

	wallIdx, wallWeight := p.heaviestBlockAfter(idx)
	// Nothing downstream matters enough to fight for. The day just slides.
	if wallIdx < 0 || wallWeight < d.Severity {
		out.Late = overflow
		p.Revision++
		return out
	}

	// Worth fighting: every lighter block between here and the wall gives up slack.
	for i := idx + 1; i < wallIdx && overflow > 0; i++ {
		block := &p.Blocks[i]
		if block.Missed || block.Weight >= wallWeight {
			continue
		}
		slack := (block.EndMinute - block.StartMinute) - minBlockMinutes(block.Kind)
		if slack <= 0 {
			continue
		}
		take := slack
		if take > overflow {
			take = overflow
		}
		block.EndMinute -= take
		p.shiftAfter(i, -take)
		overflow -= take
		out.Clawed += take
	}

	// Still makeable, just behind. She goes.
	if overflow <= lateTolerance(wallWeight) {
		out.Late = overflow
		p.Revision++
		return out
	}

	// Fought and lost. Skipping it hands its own hours back to the rest of the day.
	block := &p.Blocks[wallIdx]
	block.Missed = true
	block.MissReason = d.Reason
	out.MissedID = block.ID
	out.MissedLabel = block.Label
	freed := block.EndMinute - block.StartMinute
	p.shiftAfter(wallIdx, -freed)
	block.EndMinute = block.StartMinute
	p.Revision++
	return out
}

// lateTolerance is how many minutes past the planned start still counts as making
// it. It runs inverse to weight on purpose: the commitments that matter most are
// usually the ones with a hard start, so they are also the easiest to lose.
func lateTolerance(weight int) int {
	switch {
	case weight >= 9:
		return 5
	case weight >= 7:
		return 15
	case weight >= 5:
		return 60
	case weight >= 3:
		return 120
	default:
		return 240
	}
}

// AddUserCommitment inserts something the user asked her to do. It arrives heavy on
// purpose: the rest of the day rearranges around it, so a request carries weight in
// her schedule rather than only earning a reply.
func (p *DayPlan) AddUserCommitment(atMinute, minutes int, label string, weight int) scheduleBlock {
	if minutes <= 0 {
		minutes = 60
	}
	if weight <= 0 {
		weight = 7
	}
	if atMinute < 0 {
		atMinute = 0
	}
	block := scheduleBlock{
		ID:          fmt.Sprintf("%s_user_%02d", p.Date, len(p.Blocks)),
		Kind:        "user",
		Label:       label,
		StartMinute: atMinute,
		EndMinute:   atMinute + minutes,
		Origin:      "user",
		Weight:      weight,
	}
	pos := len(p.Blocks)
	for i, existing := range p.Blocks {
		if existing.StartMinute >= atMinute {
			pos = i
			break
		}
	}

	// Split whatever the request lands in the middle of. The head keeps its slot and
	// the tail waits behind the request, so the day gets rearranged rather than cut
	// short.
	var tail scheduleBlock
	hasTail := false
	if pos > 0 {
		prev := p.Blocks[pos-1]
		if prev.EndMinute > atMinute {
			remaining := prev.EndMinute - block.EndMinute
			p.Blocks[pos-1].EndMinute = atMinute
			if remaining > 0 {
				tail = prev
				tail.ID = block.ID + "_tail"
				tail.StartMinute = block.EndMinute
				tail.EndMinute = block.EndMinute + remaining
				tail.ShiftedBy += remaining
				hasTail = true
			}
		}
	}

	p.insertAt(pos, block)
	if hasTail {
		p.insertAt(pos+1, tail)
	}

	// Make room against blocks already scheduled at or after the request: they keep
	// their length but wait until it is over.
	for i := pos + 1; i < len(p.Blocks); i++ {
		existing := &p.Blocks[i]
		if existing.StartMinute >= block.EndMinute {
			break
		}
		duration := existing.EndMinute - existing.StartMinute
		push := block.EndMinute - existing.StartMinute
		existing.StartMinute = block.EndMinute
		existing.EndMinute = existing.StartMinute + duration
		existing.ShiftedBy += push
	}
	p.clampDay()
	p.Revision++
	return block
}

func (p *DayPlan) insertAt(i int, block scheduleBlock) {
	p.Blocks = append(p.Blocks, scheduleBlock{})
	copy(p.Blocks[i+1:], p.Blocks[i:])
	p.Blocks[i] = block
}

// clampDay keeps the plan inside one calendar day and drops anything that collapsed
// to nothing while things were being pushed around.
func (p *DayPlan) clampDay() {
	kept := p.Blocks[:0]
	for _, block := range p.Blocks {
		if block.StartMinute < 0 {
			block.StartMinute = 0
		}
		if block.EndMinute > 1440 {
			block.EndMinute = 1440
		}
		if block.EndMinute > block.StartMinute {
			kept = append(kept, block)
		}
	}
	p.Blocks = kept
}

// defaultBlockWeight is the template's opinion about each kind of activity.
func defaultBlockWeight(kind string) int {
	switch kind {
	case "sleep":
		return 9
	case "work":
		return 6
	case "break", "meal":
		return 5
	case "social":
		return 5
	case "commute":
		return 4
	case "morning":
		return 4
	default:
		return 2
	}
}

// minBlockMinutes is how short a block of this kind can be squeezed before it
// stops being that thing at all.
func minBlockMinutes(kind string) int {
	switch kind {
	case "sleep":
		return 240
	case "work":
		return 60
	case "commute":
		return 15
	case "break", "meal":
		return 15
	default:
		return 20
	}
}

// templateDayPlan builds the skeleton for a day and stamps it as a plan. The
// template stays the generator; the plan is what gets lived and edited.
func templateDayPlan(profile Profile, at time.Time) DayPlan {
	date := at.Format("2006-01-02")
	kind := "workday"
	if isWeekend(at) {
		kind = "weekend"
	}
	blocks := templateBlocksForDay(profile, at)
	for i := range blocks {
		blocks[i].ID = fmt.Sprintf("%s_%02d", date, i)
		blocks[i].Origin = "plan"
		blocks[i].Weight = blockWeight(profile, blocks[i].Kind)
	}
	return DayPlan{Date: date, Kind: kind, PlannedAt: at.UnixMilli(), Blocks: blocks}
}

// blockWeight starts from the template's opinion and bends it by personality, so a
// busy character treats work as a heavier wall than a relaxed one does.
func blockWeight(profile Profile, kind string) int {
	weight := defaultBlockWeight(kind)
	switch profile.ScheduleTendency {
	case "busy":
		if kind == "work" {
			weight += 2
		}
	case "relaxed":
		if kind == "work" {
			weight -= 2
		}
	case "social":
		if kind == "social" {
			weight += 2
		}
	}
	if weight < 0 {
		weight = 0
	}
	if weight > 10 {
		weight = 10
	}
	return weight
}

func isWeekend(at time.Time) bool {
	return at.Weekday() == time.Saturday || at.Weekday() == time.Sunday
}

// templateBlockFor answers what the bare template says for a moment, ignoring any
// saved plan. Used for first-run seeding and tests.
func templateBlockFor(profile Profile, at time.Time) scheduleBlock {
	return templateDayPlan(profile, at).blockAt(at)
}

// templateBlocksForDay is the pure day template. It no longer answers "what is she
// doing now"; it only produces the starting skeleton for a DayPlan.
func templateBlocksForDay(profile Profile, at time.Time) []scheduleBlock {
	key := at.Format("2006-01-02")
	shift := jitter(profile.ID, key, "day-shift", -20, 20)
	weekend := isWeekend(at)
	if weekend {
		wake := 465 + jitter(profile.ID, key, "weekend-wake", -30, 35)
		out := 630 + jitter(profile.ID, key, "weekend-out", -25, 30)
		return fillDay([]scheduleBlock{
			{Kind: "sleep", Label: "还在睡"},
			{Kind: "morning", Label: "刚醒来，慢慢准备今天"},
			{Kind: "hobby", Label: "做自己喜欢的事"},
			{Kind: "social", Label: "和朋友见面或出门走走"},
			{Kind: "rest", Label: "回家后放松"},
			{Kind: "sleep", Label: "准备休息"},
		}, []int{0, wake, wake + 90, out, out + 210, 1320, 1440})
	}

	switch profile.ScheduleTendency {
	case "relaxed":
		wake := 465 + shift
		workStart := 630 + shift
		workEnd := 1020 + shift
		return fillDay([]scheduleBlock{
			{Kind: "sleep", Label: "还在睡"},
			{Kind: "morning", Label: "慢慢吃早餐、整理房间"},
			{Kind: "work", Label: "处理一些不急的工作"},
			{Kind: "break", Label: "吃午饭，短暂休息"},
			{Kind: "work", Label: "处理工作和日常琐事"},
			{Kind: "rest", Label: "在家放松"},
			{Kind: "sleep", Label: "准备休息"},
		}, []int{0, wake, workStart, 720, 780, workEnd, 1260, 1440})
	case "busy":
		wake := 390 + shift
		workStart := 510 + shift
		workEnd := 1170 + shift
		return fillDay([]scheduleBlock{
			{Kind: "sleep", Label: "还在睡"},
			{Kind: "morning", Label: "匆匆准备开始一天"},
			{Kind: "work", Label: "在工作，手头事情很多"},
			{Kind: "break", Label: "匆忙吃点东西"},
			{Kind: "work", Label: "还在忙工作"},
			{Kind: "commute", Label: "刚忙完，正在回家"},
			{Kind: "rest", Label: "回到家，正在缓一缓"},
			{Kind: "sleep", Label: "准备休息"},
		}, []int{0, wake, workStart, 720, 765, workEnd, 1260, 1350, 1440})
	case "social":
		wake := 420 + shift
		workStart := 540 + shift
		workEnd := 1050 + shift
		return fillDay([]scheduleBlock{
			{Kind: "sleep", Label: "还在睡"},
			{Kind: "morning", Label: "刚醒来，和朋友聊了几句"},
			{Kind: "work", Label: "处理白天的事情"},
			{Kind: "break", Label: "和同事或朋友一起吃饭"},
			{Kind: "work", Label: "继续处理手头的事"},
			{Kind: "social", Label: "和朋友吃饭或散步"},
			{Kind: "rest", Label: "回到家，慢慢放松"},
			{Kind: "sleep", Label: "准备休息"},
		}, []int{0, wake, workStart, 720, 780, workEnd, 1230, 1350, 1440})
	default:
		wake := 420 + shift
		workStart := 540 + shift
		workEnd := 1080 + shift
		return fillDay([]scheduleBlock{
			{Kind: "sleep", Label: "还在睡"},
			{Kind: "morning", Label: "刚醒来，在准备开始一天"},
			{Kind: "work", Label: "工作上正忙"},
			{Kind: "break", Label: "吃午饭，短暂休息"},
			{Kind: "work", Label: "处理工作和日常琐事"},
			{Kind: "commute", Label: "刚下班，在路上"},
			{Kind: "rest", Label: "下班后做自己的事"},
			{Kind: "sleep", Label: "准备休息"},
		}, []int{0, wake, workStart, 720, 780, workEnd, 1110, 1350, 1440})
	}
}

func fillDay(blocks []scheduleBlock, boundaries []int) []scheduleBlock {
	for i := range blocks {
		blocks[i].StartMinute = boundaries[i]
		blocks[i].EndMinute = boundaries[i+1]
	}
	return blocks
}

func blockBounds(block scheduleBlock, day time.Time) (int64, int64) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, block.StartMinute, 0, 0, day.Location())
	end := time.Date(day.Year(), day.Month(), day.Day(), 0, block.EndMinute, 0, 0, day.Location())
	return start.UnixMilli(), end.UnixMilli()
}

func worldClock(at time.Time) WorldClock {
	weekday := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[at.Weekday()]
	timeOfDay := "深夜"
	switch {
	case at.Hour() >= 5 && at.Hour() < 9:
		timeOfDay = "早晨"
	case at.Hour() >= 9 && at.Hour() < 12:
		timeOfDay = "上午"
	case at.Hour() >= 12 && at.Hour() < 14:
		timeOfDay = "中午"
	case at.Hour() >= 14 && at.Hour() < 18:
		timeOfDay = "下午"
	case at.Hour() >= 18 && at.Hour() < 23:
		timeOfDay = "晚上"
	}
	season := "冬"
	switch at.Month() {
	case time.March, time.April, time.May:
		season = "春"
	case time.June, time.July, time.August:
		season = "夏"
	case time.September, time.October, time.November:
		season = "秋"
	}
	return WorldClock{
		Now:       at.UnixMilli(),
		Date:      at.Format("2006年1月2日"),
		LocalTime: at.Format("15:04"),
		Weekday:   weekday,
		TimeOfDay: timeOfDay,
		Season:    season,
		IsWeekend: at.Weekday() == time.Saturday || at.Weekday() == time.Sunday,
	}
}

func jitter(id, date, key string, low, high int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	_, _ = h.Write([]byte(date))
	_, _ = h.Write([]byte(key))
	return low + int(h.Sum64()%uint64(high-low+1))
}

func scheduleTrace(block scheduleBlock) string {
	return fmt.Sprintf("%s[%02d:%02d-%02d:%02d]", block.Kind, block.StartMinute/60, block.StartMinute%60, block.EndMinute/60, block.EndMinute%60)
}
