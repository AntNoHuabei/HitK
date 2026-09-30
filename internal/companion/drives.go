package companion

import (
	"math"
	"time"
)

// Three forces decide whether a scheduled thing actually happens.
//
//	need         rises the longer the body or mind has gone without it
//	reluctance   pulls the other way: what she is immersed in, her mood, her nature
//	weight     the decision made in advance, which buys tolerance for reluctance
//
// A block fires when need minus reluctance clears the threshold that weight bought.
// "Exhausted but not ready to sleep" is not a special case in here. It is just
// reluctance beating need.

const (
	fireBase      = 60.0
	firePerWeight = 3.0
	// replyWeightCeiling is how light a commitment has to be for it to never block
	// a reply. Anything heavier only blocks her once she has actually started it.
	replyWeightCeiling = 3

	// flowDecayPerHour is 0.46 so immersion halves in about ninety minutes.
	flowDecayPerHour = 0.46
	// boredomRisePerHour fills an empty stretch in roughly the same window.
	boredomRisePerHour = 38.0

	sleepNeedMinutes = 8 * 60
	sleepRepayCap    = 4 * 60
	// sleepAnchorMinute is 23:30 and wakeAnchorMinute is 07:30. Between them is the
	// window where being awake costs debt.
	sleepAnchorMinute = 23*60 + 30
	wakeAnchorMinute  = 7*60 + 30
	// sleepLateStreakMinute is how far past the anchor counts as a late night.
	sleepLateStreakMinute = 90

	// sleepOnsetBaseMinutes is the floor on how long it takes her to go under after
	// getting into bed. Flow stretches it out on top of this.
	sleepOnsetBaseMinutes = 18.0

	// dutyLeadHours is how far ahead a deadline starts weighing on her.
	dutyLeadHours = 6.0
)

// driveSpec is what pushes toward a kind of block and what pushes back.
type driveSpec struct {
	need       func(state State, block scheduleBlock, at time.Time) float64
	reluctance func(state State, block scheduleBlock, at time.Time) float64
}

var driveSpecs = map[string]driveSpec{
	"sleep": {
		need: func(state State, _ scheduleBlock, _ time.Time) float64 {
			// Debt rides on top of the raw pressure and, unlike a good mood, it does
			// not fade. A long enough streak wins no matter how good the night is.
			debt := float64(state.SleepDebtMinutes) / float64(sleepNeedMinutes) * 60
			return state.Metric(MetricSleepPressure)*0.7 + state.Metric(MetricFatigue)*0.3 + debt
		},
		reluctance: func(state State, _ scheduleBlock, _ time.Time) float64 {
			return state.Metric(MetricFlow)
		},
	},
	"meal": {
		need: mealNeed,
		reluctance: func(state State, _ scheduleBlock, _ time.Time) float64 {
			// No appetite and being in the middle of something both ruin a meal.
			return state.Metric(MetricAnxiety)*0.6 + state.Metric(MetricCognitiveLoad)*0.4
		},
	},
	"work": {
		need: func(_ State, block scheduleBlock, at time.Time) float64 {
			return dutyFromBlock(block, at) * 100
		},
		reluctance: func(state State, _ scheduleBlock, _ time.Time) float64 {
			return (100-state.Metric(MetricMotivation))*0.5 + state.Metric(MetricCognitiveLoad)*0.5
		},
	},
	"social": {
		need: func(state State, _ scheduleBlock, _ time.Time) float64 {
			// Sitting down with friends is half appetite and half plan, so there is a
			// floor under it. Raw loneliness alone never clears a weight 6 bar, which
			// left her permanently housebound.
			return 30 + state.Metric(MetricLoneliness)*0.7
		},
		reluctance: func(state State, _ scheduleBlock, _ time.Time) float64 {
			return 100 - state.Metric(MetricSocialEnergy)
		},
	},
	"intimacy": {
		need: func(state State, _ scheduleBlock, _ time.Time) float64 {
			return state.Metric(MetricLibido)
		},
		reluctance: func(state State, _ scheduleBlock, _ time.Time) float64 {
			return state.Metric(MetricFatigue)
		},
	},
	"open": {
		need: func(state State, _ scheduleBlock, _ time.Time) float64 {
			return state.Metric(MetricBoredom)
		},
		reluctance: func(state State, _ scheduleBlock, _ time.Time) float64 {
			return state.Metric(MetricFlow)
		},
	},
}

// fireThreshold is how much need it takes, once reluctance is paid for. High weight
// buys a low bar: she will drag herself to work unhappy.
func fireThreshold(weight int) float64 {
	return fireBase - float64(weight)*firePerWeight
}

// Forces breaks a block down into the three forces acting on it. Split out from the
// yes/no decision so the diagnostic dump, and any UI that wants to explain why she
// did or did not move, reads the same numbers the engine does.
func Forces(state State, block scheduleBlock, at time.Time) (need, reluctance, threshold float64, ok bool) {
	spec, ok := driveSpecs[block.Kind]
	if !ok {
		return 0, 0, fireThreshold(block.Weight), false
	}
	return spec.need(state, block, at), spec.reluctance(state, block, at), fireThreshold(block.Weight), true
}

// ShouldStart reports whether the forces clear the bar for this block.
func ShouldStart(state State, block scheduleBlock, at time.Time) bool {
	need, reluctance, threshold, ok := Forces(state, block, at)
	if !ok {
		return true
	}
	return need-reluctance >= threshold
}

// dutyFromBlock is how hard a deadline is leaning on her, 0..1. Without an explicit
// deadline the closing of the block's own window plays the part, so ordinary work
// still builds pressure instead of sitting flat.
func dutyFromBlock(block scheduleBlock, at time.Time) float64 {
	// Showing up at all carries standing duty. Without a floor under it she starts
	// the workday at zero pressure and, past a middling mood, never starts at all.
	duty := 0.7
	if span := block.EndMinute - block.StartMinute; span > 0 {
		progress := clampFloat(float64(minuteOfDay(at)-block.StartMinute)/float64(span), 0, 1)
		duty += 0.3 * progress
	}
	if block.Deadline <= 0 {
		return duty
	}
	// A real deadline is allowed to lean harder than a vague workday, but never less:
	// it adds urgency on top of showing up, it does not excuse it. Timestamps are
	// milliseconds and the lead is a Duration, so convert before dividing or every
	// deadline reads as already on top of her.
	remaining := time.Duration(block.Deadline-at.UnixMilli()) * time.Millisecond
	if remaining <= 0 {
		return 1
	}
	pressure := clampFloat(1-float64(remaining)/(dutyLeadHours*float64(time.Hour)), 0, 1)
	return math.Max(duty, pressure)
}

// mealNeed is hunger read against a floor. The body's own hunger climbs far slower
// than a weight 6 bar needs, so an all-hunger rule means she never eats; the floor
// is the habit of sitting down at a mealtime, and hunger decides whether she stays.
func mealNeed(state State, _ scheduleBlock, _ time.Time) float64 {
	return 40 + state.Metric(MetricHunger)*0.6
}

// flowBoost is how much a started block feeds immersion, which is what keeps her up
// past her bedtime and keeps her out of boredom.
func flowBoost(kind string) (float64, bool) {
	switch kind {
	case "hobby", "game":
		return 26, true
	case "intimacy":
		return 30, true
	case "social":
		return 18, true
	case "work":
		return 10, true
	case "meal":
		return 6, true
	default:
		return 0, false
	}
}

// UpdateDrives advances the two drive metrics and the sleep ledger by one tick. It
// runs before the fire check so the tick reads the freshest forces.
func UpdateDrives(state *State, block scheduleBlock, at time.Time) {
	if state.Metrics == nil {
		return
	}
	hours := tickInterval.Hours()

	flow := state.Metric(MetricFlow) * math.Exp(-flowDecayPerHour*hours)
	if block.StartedAt > 0 {
		if boost, ok := flowBoost(block.Kind); ok {
			flow += boost * hours
		}
	}
	state.Metrics[MetricFlow] = clampFloat(flow, 0, 100)

	// Boredom only climbs while nothing has her, and drains fast once something does.
	boredom := state.Metric(MetricBoredom)
	if block.Kind == "open" || block.StartedAt == 0 {
		boredom += boredomRisePerHour * hours
	} else {
		boredom -= boredom * 0.6 * hours
	}
	state.Metrics[MetricBoredom] = clampFloat(boredom, 0, 100)

	updateSleepLedger(state, block, at)
}

// updateSleepLedger keeps the debt honest: sleeping pays it down at a capped rate,
// staying up inside the sleep window keeps adding to it.
func updateSleepLedger(state *State, block scheduleBlock, at time.Time) {
	step := int(tickInterval / time.Minute)
	if block.Kind == "sleep" && block.StartedAt > 0 {
		room := sleepRepayCap - state.SleepRepaidMinutes
		if room > 0 {
			if step > room {
				step = room
			}
			state.SleepDebtMinutes -= step
			state.SleepRepaidMinutes += step
		}
		if state.SleepDebtMinutes < 0 {
			state.SleepDebtMinutes = 0
		}
		return
	}
	if inSleepWindow(at) {
		state.SleepDebtMinutes += step
	}
}

func minuteOfDay(at time.Time) int { return at.Hour()*60 + at.Minute() }

func inSleepWindow(at time.Time) bool {
	minute := minuteOfDay(at)
	return minute >= sleepAnchorMinute || minute < wakeAnchorMinute
}

// minutesPastSleepAnchor handles the wrap through midnight, so 01:30 reads as 120
// minutes late. The other side of the anchor has to come back negative rather than
// wrapping: 23:20 is early for bedtime, not 1430 minutes late, which is what a
// blind wrap makes it.
func minutesPastSleepAnchor(at time.Time) int {
	minute := minuteOfDay(at)
	switch {
	case minute >= sleepAnchorMinute:
		return minute - sleepAnchorMinute
	case minute < wakeAnchorMinute:
		return (1440 - sleepAnchorMinute) + minute
	default:
		return minute - sleepAnchorMinute
	}
}

// NoteSleepStart records whether this night counts toward the late streak. Call it
// once, when the sleep block actually begins.
func NoteSleepStart(state *State, at time.Time) {
	if minutesPastSleepAnchor(at) > sleepLateStreakMinute {
		state.SleepStreak++
	} else {
		state.SleepStreak = 0
	}
	// A fresh session gets its own repayment allowance.
	state.SleepRepaidMinutes = 0
}

// activityLabel is what she reports as doing right now. A block whose bar has not
// been cleared is not the activity itself, it is her still sitting on it, and
// naming the block would misreport the last two hours.
func activityLabel(block scheduleBlock) string {
	if block.Kind != "open" && block.StartedAt == 0 {
		return "还没开始，先拖着"
	}
	return block.Label
}

// Consciousness lives here, next to the forces that produce it, so that the
// availability gate, the force-reply button, the header, the sidebar and the
// state panel all read one answer instead of each inferring it their own way.

func isSleepBlock(block scheduleBlock) bool { return block.Kind == "sleep" }

// sleepOnset is how long she lies there before she is actually under. The fixed
// part is ordinary sleep latency; the flow term is what keeps her scrolling. Flow
// only decays once she is in bed, so she always goes under eventually -- the more
// wired she is, the longer the ceiling takes. That is where "lay down and could not
// sleep for two hours" comes from without a single scripted case.
func sleepOnset(state State) time.Duration {
	return time.Duration(sleepOnsetBaseMinutes+state.Metric(MetricFlow)*0.9) * time.Minute
}

// consciousnessAt answers whether she is asleep at `at`, given the plan for that
// day. The truth is the block she is standing in, not the latch: a started sleep
// block with the onset paid means she is under, and the moment the block is not a
// sleep block she is not. Reading the plan rather than the latch is what keeps a
// stale latch from reporting her asleep at ten in the morning after a restart,
// and carrySleepFrom hands the started sleep across midnight so a night that
// spans two plans never wakes her at 00:00.
func consciousnessAt(state State, plan DayPlan, at time.Time) Consciousness {
	block := plan.blockAt(at)
	if !isSleepBlock(block) || block.StartedAt == 0 {
		return ConsciousAwake
	}
	if state.AsleepSince > 0 {
		return ConsciousAsleep
	}
	if at.Sub(time.UnixMilli(block.StartedAt)) >= sleepOnset(state) {
		return ConsciousAsleep
	}
	return ConsciousAwake
}

// nextWakeAt is when she should surface, which is what the busy notice prints and
// what a parked message waits on.
func nextWakeAt(state State, plan DayPlan, at time.Time) time.Time {
	block := plan.blockAt(at)
	if isSleepBlock(block) {
		return minuteInDay(at, block.EndMinute)
	}
	// Asleep with no sleep block under her means drift ate it. The block she went
	// to sleep in still knows when it was going to end.
	if asleep, ok := plan.blockByID(state.AsleepBlockID); ok {
		return minuteInDay(at, asleep.EndMinute)
	}
	return minuteInDay(at, wakeAnchorMinute)
}

// settleConsciousness owns the only writes to the consciousness fields. Falling
// asleep also ends whatever scene was running: she is not in the middle of a
// conversation and under at the same time, and leaving the stage set would let the
// reply path read a scene that is really just someone asleep.
func settleConsciousness(state *State, plan DayPlan, at time.Time) {
	if consciousnessAt(*state, plan, at) == ConsciousAwake {
		state.Consciousness = ConsciousAwake
		if state.AsleepSince > 0 {
			state.AsleepSince = 0
			state.AsleepBlockID = ""
		}
		return
	}
	if state.AsleepSince == 0 {
		state.AsleepSince = at.UnixMilli()
		state.AsleepBlockID = plan.blockAt(at).ID
	}
	state.Consciousness = ConsciousAsleep
	if state.SceneStage != "" && state.SceneStage != "none" {
		state.SceneStage = "none"
		state.ConsentState = "not_applicable"
	}
}
