package companion

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
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
	// Deadline is when the thing behind this block stops being possible, carried over
	// from the commitment. Zero means the block's own window is the deadline.
	Deadline int64
	// StartedAt is when she actually began this block, zero while the forces have not
	// cleared the bar. A heavy block with StartedAt == 0 is her putting it off, which
	// is exactly when she is free to chat.
	StartedAt int64
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
	// A hole left by drift belongs to nothing. Handing back the last block of the
	// day here would tell her she is asleep at ten in the morning.
	if minute < p.Blocks[len(p.Blocks)-1].EndMinute {
		return openBlock(minute, minute)
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

// markStarted records that she actually began a block, and only the first time.
// The bool return lets a caller fire one-time side effects, like opening a late
// night streak, without keeping a second guard of its own.
func (p *DayPlan) markStarted(blockID string, at time.Time) bool {
	for i := range p.Blocks {
		if p.Blocks[i].ID != blockID {
			continue
		}
		if p.Blocks[i].StartedAt > 0 {
			return false
		}
		p.Blocks[i].StartedAt = at.UnixMilli()
		p.Revision++
		return true
	}
	return false
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
	if weight <= 0 {
		weight = 7
	}
	return p.InsertBlock(scheduleBlock{Kind: "user", Label: label, Origin: "user", Weight: weight}, atMinute, minutes)
}

// InsertBlock drops a block into the day at atMinute. Whatever it lands on gets split
// rather than cut short: the head keeps its slot and the tail waits behind the new
// block. Anything already scheduled at or after it keeps its length and slides back.
func (p *DayPlan) InsertBlock(block scheduleBlock, atMinute, minutes int) scheduleBlock {
	if minutes <= 0 {
		minutes = 60
	}
	if atMinute < 0 {
		atMinute = 0
	}
	if block.ID == "" {
		block.ID = fmt.Sprintf("%s_%s_%02d", p.Date, block.Kind, len(p.Blocks))
	}
	if block.Origin == "" {
		block.Origin = "plan"
	}
	block.StartMinute = atMinute
	block.EndMinute = atMinute + minutes

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

	// Make room against blocks that now overlap the inserted one. They keep their
	// length but wait until it is over. Pushing one block can drop it on top of the
	// next, so this cascades down the day instead of stopping at the first block
	// that was clear of the request, which is how lunch used to end up at 17:00.
	prevEnd := block.EndMinute
	for i := pos + 1; i < len(p.Blocks); i++ {
		existing := &p.Blocks[i]
		if existing.StartMinute >= prevEnd {
			break
		}
		duration := existing.EndMinute - existing.StartMinute
		push := prevEnd - existing.StartMinute
		existing.StartMinute = prevEnd
		existing.EndMinute = existing.StartMinute + duration
		existing.ShiftedBy += push
		prevEnd = existing.EndMinute
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

// carrySleepFrom marks this plan's opening sleep block as already running when the
// day before it ended with her asleep. Without it, midnight silently resets a
// started sleep back to "not begun", so she would appear to wake up every night at
// exactly 00:00 and then have to fall asleep all over again.
func (p *DayPlan) carrySleepFrom(prev DayPlan) {
	if len(p.Blocks) == 0 || len(prev.Blocks) == 0 {
		return
	}
	head := &p.Blocks[0]
	if head.Kind != "sleep" || head.StartMinute != 0 || head.StartedAt > 0 {
		return
	}
	tail := prev.Blocks[len(prev.Blocks)-1]
	if tail.Kind != "sleep" || tail.StartedAt == 0 {
		return
	}
	head.StartedAt = tail.StartedAt
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

// sleepWindow is the body's own rhythm, which is the one part of the day she does
// not negotiate. Everything else gets fitted around it.
func sleepWindow(profile Profile, at time.Time) (wake int, sleepStart int) {
	key := at.Format("2006-01-02")
	wake = wakeAnchorMinute + jitter(profile.ID, key, "wake-jitter", -30, 30)
	sleepStart = sleepAnchorMinute + jitter(profile.ID, key, "sleep-jitter", -30, 45)
	// The jitter must not overshoot midnight, or the day ends up with no bedtime at
	// all and the sleep ledger never gets to repay anything.
	if sleepStart > 1435 {
		sleepStart = 1435
	}
	return wake, sleepStart
}

// dayBuilder assembles one day as a set of non-overlapping, sorted blocks. Fitting
// happens here rather than by pushing blocks around after the fact, which is how an
// eight hour workday used to end up relocating lunch to 20:41 and deleting dinner.
type dayBuilder struct {
	blocks []scheduleBlock
}

func (b *dayBuilder) overlaps(start, end int) bool {
	for _, block := range b.blocks {
		if start < block.EndMinute && end > block.StartMinute {
			return true
		}
	}
	return false
}

// force drops a block at an exact slot. Used for the things that do not move.
func (b *dayBuilder) force(block scheduleBlock) {
	if block.EndMinute <= block.StartMinute {
		return
	}
	if block.ID == "" {
		block.ID = blockID(block)
	}
	b.blocks = append(b.blocks, block)
	sort.SliceStable(b.blocks, func(i, j int) bool {
		return b.blocks[i].StartMinute < b.blocks[j].StartMinute
	})
}

// fit places a block as near its ideal time as the day allows, working outward in
// quarter hours and trying later before earlier. It reports false when the flex
// window is fully blocked, which is a real outcome: there was no room to eat.
func (b *dayBuilder) fit(block scheduleBlock, idealStart, duration, flex int) bool {
	for step := 0; step*15 <= flex; step++ {
		for _, candidate := range []int{idealStart + step*15, idealStart - step*15} {
			if step == 0 && candidate != idealStart {
				continue
			}
			start := candidate
			if start < 0 || start+duration > 1440 {
				continue
			}
			if b.overlaps(start, start+duration) {
				continue
			}
			block.StartMinute = start
			block.EndMinute = start + duration
			if block.ID == "" {
				block.ID = blockID(block)
			}
			b.blocks = append(b.blocks, block)
			sort.SliceStable(b.blocks, func(i, j int) bool {
				return b.blocks[i].StartMinute < b.blocks[j].StartMinute
			})
			return true
		}
	}
	return false
}

// fillOpen turns every leftover stretch into open time. An unfilled stretch is a
// real state she sits in, not a hole to hide.
func (b *dayBuilder) fillOpen() {
	sort.SliceStable(b.blocks, func(i, j int) bool {
		return b.blocks[i].StartMinute < b.blocks[j].StartMinute
	})
	filled := make([]scheduleBlock, 0, len(b.blocks)*2)
	cursor := 0
	for _, block := range b.blocks {
		// Every leftover stretch gets a block, however short. A skipped six minute
		// gap between breakfast and the commute left minutes belonging to nothing,
		// and availability read those as free time.
		if block.StartMinute > cursor {
			filled = append(filled, openBlock(cursor, block.StartMinute))
		}
		filled = append(filled, block)
		cursor = block.EndMinute
	}
	if cursor < 1440 {
		filled = append(filled, openBlock(cursor, 1440))
	}
	b.blocks = filled
}

// carve drops a block at its ideal time and splits whatever it lands on, so a body
// need can open a hole in a workday instead of being cancelled by one. It only
// splits things she holds no more important than the need itself: a meal goes
// through a workday, but not through an exam, and never through sleep.
func (b *dayBuilder) carve(block scheduleBlock, idealStart, duration int) bool {
	if idealStart < 0 || idealStart+duration > 1440 {
		return false
	}
	end := idealStart + duration
	out := make([]scheduleBlock, 0, len(b.blocks)+1)
	carved := false
	for _, existing := range b.blocks {
		if existing.EndMinute <= idealStart || existing.StartMinute >= end {
			out = append(out, existing)
			continue
		}
		if existing.Kind == "sleep" || existing.Weight > block.Weight {
			return false
		}
		carved = true
		if existing.StartMinute < idealStart {
			head := existing
			head.EndMinute = idealStart
			head.ID = blockID(head)
			out = append(out, head)
		}
		out = append(out, scheduleBlock{
			ID:          blockID(block),
			Kind:        block.Kind,
			Label:       block.Label,
			Weight:      block.Weight,
			Origin:      block.Origin,
			StartMinute: idealStart,
			EndMinute:   end,
		})
		if existing.EndMinute > end {
			tail := existing
			tail.StartMinute = end
			tail.ID = blockID(tail)
			out = append(out, tail)
		}
	}
	if !carved {
		return false
	}
	b.blocks = out
	sort.SliceStable(b.blocks, func(i, j int) bool {
		return b.blocks[i].StartMinute < b.blocks[j].StartMinute
	})
	return true
}

// takesPrecedence reports whether a new commitment outranks everything already
// holding that stretch. Only a strictly heavier one does, so two equally weighted
// plans queue instead of shredding each other.
func (b *dayBuilder) takesPrecedence(block scheduleBlock, start, end int) bool {
	for _, existing := range b.blocks {
		if existing.EndMinute <= start || existing.StartMinute >= end {
			continue
		}
		if existing.Kind == "sleep" || existing.Weight >= block.Weight {
			return false
		}
	}
	return true
}

func openBlock(start, end int) scheduleBlock {
	return scheduleBlock{
		ID:          fmt.Sprintf("open_%04d", start),
		Kind:        "open",
		Label:       openLabel(start),
		StartMinute: start,
		EndMinute:   end,
		Weight:      1,
		Origin:      "plan",
	}
}

// blockID keys a block by kind and start minute. Blocks in a built day never share
// a start, so this is stable across rebuilds, which matters because StartedAt and
// drift both find blocks by id.
func blockID(block scheduleBlock) string {
	return fmt.Sprintf("%s_%04d", block.Kind, block.StartMinute)
}

func openLabel(startMinute int) string {
	switch {
	case startMinute < 5*60:
		return "深夜空着"
	case startMinute < 11*60:
		return "早上空着"
	case startMinute < 14*60:
		return "中午空着"
	case startMinute < 18*60:
		return "下午空着"
	case startMinute < 23*60:
		return "晚上空着"
	default:
		return "深夜空着"
	}
}

// buildDayPlan lays out a day. Commitments are the skeleton because they are what
// she agreed to in advance; sleep bookends that and meals are fitted into whatever
// room is left. Anything still unfilled is open time.
func buildDayPlan(profile Profile, at time.Time, commitments []Commitment) DayPlan {
	date := at.Format("2006-01-02")
	kind := "workday"
	if isWeekend(at) {
		kind = "weekend"
	}
	wake, sleepStart := sleepWindow(profile, at)
	midnight := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location()).UnixMilli()

	builder := &dayBuilder{}
	// The sleep that spans midnight is already running when the day is written down,
	// so it starts pre-claimed. Left unstarted it reads as "procrastinating in bed"
	// all night: available to chat, and accruing debt instead of paying it off.
	builder.force(scheduleBlock{
		Kind: "sleep", Label: "还在睡", Weight: 10, Origin: "body",
		StartMinute: 0, EndMinute: wake, StartedAt: midnight,
	})

	cursor := wake
	for _, commitment := range commitments {
		// A weekday job must not show up on Saturday just because it was in the set.
		if !commitment.matches(at) {
			continue
		}
		start := parseClock(commitment.Start)
		if start < 0 {
			continue
		}
		duration := commitment.Duration
		if duration <= 0 {
			duration = 60
		}
		block := scheduleBlock{
			Kind:     commitment.Kind,
			Label:    commitment.Label,
			Weight:   commitment.Weight,
			Origin:   commitment.Source,
			Deadline: commitment.Deadline,
		}
		// Something that outranks what already holds that slot takes it and splits
		// the day around itself. An exam at 11:30 goes through a workday rather
		// than being shunted to after it, which is the whole reason weight exists.
		if builder.takesPrecedence(block, start, start+duration) && builder.carve(block, start, duration) {
			cursor = max(cursor, start+duration)
			continue
		}
		// Outranked, so it waits behind whatever is already there.
		start = max(start, cursor)
		end := min(start+duration, 1440)
		if end-start < 15 {
			continue
		}
		block.StartMinute = start
		block.EndMinute = end
		builder.force(block)
		cursor = end
	}

	// Bedtime comes after the last thing she has to do, and is never dropped for
	// being short. A thin bedtime is the honest shape of a late day: the drives get
	// to argue about whether she actually sleeps in it, but the block stays.
	tailStart := max(sleepStart, cursor)
	if tailStart < 1440 {
		builder.force(scheduleBlock{
			Kind: "sleep", Label: "该睡了", Weight: 10, Origin: "body",
			StartMinute: tailStart, EndMinute: 1440,
		})
	}

	// Meals get fitted into a gap if there is one, and carved out of the day if
	// there is not. A solid eight hour workday does not get to cancel lunch; it
	// gets interrupted by it, which is what actually happens.
	for _, meal := range []struct {
		label   string
		ideal   int
		minutes int
		flex    int
	}{
		{"吃早饭", wake + 20, 30, 90},
		{"吃午饭", 12 * 60, 45, 150},
		{"吃晚饭", 19 * 60, 45, 150},
	} {
		block := mealBlock(meal.label)
		if !builder.fit(block, meal.ideal, meal.minutes, meal.flex) {
			builder.carve(block, meal.ideal, meal.minutes)
		}
	}

	builder.fillOpen()
	return DayPlan{
		Date:      date,
		Kind:      kind,
		PlannedAt: at.UnixMilli(),
		Blocks:    builder.blocks,
	}
}

func mealBlock(label string) scheduleBlock {
	return scheduleBlock{Kind: "meal", Label: label, Weight: 6, Origin: "body"}
}

// parseClock turns "18:00" into minutes since midnight, or -1 when it is nonsense.
func parseClock(value string) int {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return -1
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return -1
	}
	minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return -1
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return -1
	}
	return hour*60 + minute
}

func isWeekend(at time.Time) bool {
	return at.Weekday() == time.Saturday || at.Weekday() == time.Sunday
}

// templateBlockFor answers what the bare body plus default commitments says for a
// moment, ignoring any saved plan. Used for first-run seeding and tests.
func templateBlockFor(profile Profile, at time.Time) scheduleBlock {
	return buildDayPlan(profile, at, defaultCommitments(profile)).blockAt(at)
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
