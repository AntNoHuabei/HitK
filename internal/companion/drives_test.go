package companion

import (
	"testing"
	"time"
)

func driveState(values map[MetricID]float64) State {
	state := DefaultState(time.Now().UnixMilli())
	for id, value := range values {
		state.Metrics[id] = value
	}
	return state
}

func driveBlock(kind string, weight int) scheduleBlock {
	return scheduleBlock{
		ID:          kind + "_drive",
		Kind:        kind,
		Label:       kind,
		StartMinute: 9 * 60,
		EndMinute:   17 * 60,
		Weight:      weight,
	}
}

func TestImmersionHoldsHerUpPastHerBedtime(t *testing.T) {
	at := time.Date(2026, 9, 28, 23, 45, 0, 0, time.Local)
	block := driveBlock("sleep", 10)
	// Tired enough to drop, and right in the middle of something. The need is real
	// and the reluctance is real, and reluctance wins, which is the whole point.
	state := driveState(map[MetricID]float64{MetricSleepPressure: 80, MetricFatigue: 60, MetricFlow: 70})
	if ShouldStart(state, block, at) {
		t.Fatal("high immersion should hold her up despite being tired")
	}

	// Immersion decays and tiredness does not, so the same she goes to bed once the
	// thing she was into stops feeding her.
	decayed := state
	decayed.Metrics[MetricFlow] = 30
	if !ShouldStart(decayed, block, at) {
		t.Fatal("once immersion decays the same tiredness should put her to bed")
	}
}

func TestHeavySleepDebtOutweighsImmersion(t *testing.T) {
	at := time.Date(2026, 9, 28, 2, 0, 0, 0, time.Local)
	block := driveBlock("sleep", 10)
	// Nine hours in the hole, at the immersion a hobby actually tops out at. Debt is
	// the one force that does not fade, so it is the one that eventually wins.
	state := driveState(map[MetricID]float64{MetricSleepPressure: 50, MetricFatigue: 40, MetricFlow: 55})
	state.SleepDebtMinutes = 9 * 60
	if !ShouldStart(state, block, at) {
		t.Fatal("nine hours of debt should beat a good time")
	}
}

func TestSleepDebtGrowsWhileAwakeInTheWindow(t *testing.T) {
	state := DefaultState(time.Now().UnixMilli())
	block := driveBlock("open", 1)

	UpdateDrives(&state, block, time.Date(2026, 9, 28, 23, 45, 0, 0, time.Local))
	if state.SleepDebtMinutes != 15 {
		t.Fatalf("debt = %d, want 15 after one tick up past bedtime", state.SleepDebtMinutes)
	}

	// Mid-afternoon does not cost anything.
	UpdateDrives(&state, block, time.Date(2026, 9, 28, 15, 0, 0, 0, time.Local))
	if state.SleepDebtMinutes != 15 {
		t.Fatalf("debt = %d, want it unchanged at 15:00", state.SleepDebtMinutes)
	}
}

func TestSleepPaysDebtBackUpToTheSessionCap(t *testing.T) {
	at := time.Date(2026, 9, 28, 1, 0, 0, 0, time.Local)
	block := driveBlock("sleep", 10)
	block.StartedAt = at.UnixMilli()

	state := DefaultState(time.Now().UnixMilli())
	state.SleepDebtMinutes = 8 * 60

	// Four hours of sleep clears four hours of debt.
	for i := 0; i < 16; i++ {
		UpdateDrives(&state, block, at.Add(time.Duration(i)*tickInterval))
	}
	if state.SleepDebtMinutes != 4*60 {
		t.Fatalf("debt = %d, want 4h repaid", state.SleepDebtMinutes)
	}

	// The cap holds after that, so a long lie-in cannot wipe out a week of damage in
	// one go.
	for i := 0; i < 16; i++ {
		UpdateDrives(&state, block, at.Add(time.Duration(16+i)*tickInterval))
	}
	if state.SleepDebtMinutes != 4*60 {
		t.Fatalf("debt = %d, want repayment capped at %d minutes", state.SleepDebtMinutes, 4*60)
	}
}

func TestLateNightStreakBuildsAndAnOnTimeNightBreaksIt(t *testing.T) {
	state := DefaultState(time.Now().UnixMilli())

	NoteSleepStart(&state, time.Date(2026, 9, 28, 23, 35, 0, 0, time.Local))
	if state.SleepStreak != 0 {
		t.Fatalf("streak = %d, want 0 for an on-time night", state.SleepStreak)
	}

	for _, day := range []int{28, 29, 30} {
		NoteSleepStart(&state, time.Date(2026, 9, day, 2, 0, 0, 0, time.Local))
	}
	if state.SleepStreak != 3 {
		t.Fatalf("streak = %d, want 3 late nights", state.SleepStreak)
	}

	NoteSleepStart(&state, time.Date(2026, 10, 1, 23, 20, 0, 0, time.Local))
	if state.SleepStreak != 0 {
		t.Fatalf("streak = %d, want it broken by an early night", state.SleepStreak)
	}
}

func TestMealSkippedWhenSheIsNotHungry(t *testing.T) {
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.Local)
	block := driveBlock("meal", 6)
	state := driveState(map[MetricID]float64{MetricHunger: 10, MetricAnxiety: 18, MetricCognitiveLoad: 20})
	if ShouldStart(state, block, at) {
		t.Fatal("she should skip a meal she has no appetite for")
	}
}

func TestMealTakenWhenSheIsHungry(t *testing.T) {
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.Local)
	block := driveBlock("meal", 6)
	state := driveState(map[MetricID]float64{MetricHunger: 70, MetricAnxiety: 18, MetricCognitiveLoad: 20})
	if !ShouldStart(state, block, at) {
		t.Fatal("a genuinely hungry she should eat")
	}
}

func TestAnxietyRuinsHerAppetite(t *testing.T) {
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.Local)
	block := driveBlock("meal", 6)
	// Starving, but too wound up to eat. Same hunger that cleared the bar a moment
	// ago, with the mood changed underneath it.
	state := driveState(map[MetricID]float64{MetricHunger: 70, MetricAnxiety: 80, MetricCognitiveLoad: 60})
	if ShouldStart(state, block, at) {
		t.Fatal("anxiety and a heavy head should keep her from eating")
	}
}

func TestLowMotivationPutsWorkOffUntilTheDeadlineForcesIt(t *testing.T) {
	block := driveBlock("work", 6)
	early := time.Date(2026, 9, 28, 9, 30, 0, 0, time.Local)
	block.Deadline = early.Add(30 * time.Hour).UnixMilli()
	state := driveState(map[MetricID]float64{MetricMotivation: 30, MetricCognitiveLoad: 40})
	if ShouldStart(state, block, early) {
		t.Fatal("with no deadline near and low drive she should not start")
	}

	// The same she, with the deadline on top of her, gets moving.
	// Five minutes from the deadline, which is close enough that even this she moves.
	later := time.UnixMilli(block.Deadline).Add(-5 * time.Minute)
	if !ShouldStart(state, block, later) {
		t.Fatal("a closing deadline should drag her into it")
	}
}

func TestHighMotivationStartsWorkWhenItBegins(t *testing.T) {
	block := driveBlock("work", 6)
	at := time.Date(2026, 9, 28, 9, 5, 0, 0, time.Local)
	block.Deadline = at.Add(30 * time.Hour).UnixMilli()
	state := driveState(map[MetricID]float64{MetricMotivation: 90, MetricCognitiveLoad: 15})
	if !ShouldStart(state, block, at) {
		t.Fatal("a keen she should start without needing the deadline")
	}
}

func TestLonelinessCannotBeatAnEmptySocialBattery(t *testing.T) {
	at := time.Date(2026, 9, 28, 19, 0, 0, 0, time.Local)
	block := driveBlock("social", 6)
	state := driveState(map[MetricID]float64{MetricLoneliness: 90, MetricSocialEnergy: 5})
	if ShouldStart(state, block, at) {
		t.Fatal("lonely but unable should stay home")
	}

	charged := state
	charged.Metrics[MetricSocialEnergy] = 60
	if !ShouldStart(charged, block, at) {
		t.Fatal("a charged she should take the same loneliness and go out")
	}
}

func TestOpenTimeFillsWithBoredomAndSomethingElseDrainsIt(t *testing.T) {
	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricBoredom] = 0
	open := driveBlock("open", 1)
	start := time.Date(2026, 9, 28, 14, 0, 0, 0, time.Local)

	for i := 0; i < 4; i++ {
		UpdateDrives(&state, open, start.Add(time.Duration(i)*tickInterval))
	}
	risen := state.Metrics[MetricBoredom]
	if risen < 20 {
		t.Fatalf("boredom = %.1f, want it to climb through empty time", risen)
	}

	// Something with her in it takes the boredom back down again.
	busy := driveBlock("hobby", 4)
	busy.StartedAt = start.UnixMilli()
	for i := 0; i < 4; i++ {
		UpdateDrives(&state, busy, start.Add(time.Duration(i)*tickInterval))
	}
	if state.Metrics[MetricBoredom] >= risen {
		t.Fatalf("boredom = %.1f, want it drained by a started block", state.Metrics[MetricBoredom])
	}
}

func TestStartedBlockFeedsImmersionAndAnUnstartedOneDoesNot(t *testing.T) {
	start := time.Date(2026, 9, 28, 14, 0, 0, 0, time.Local)

	state := DefaultState(time.Now().UnixMilli())
	state.Metrics[MetricFlow] = 0
	idle := driveBlock("hobby", 4)
	UpdateDrives(&state, idle, start)
	if state.Metric(MetricFlow) > 1 {
		t.Fatalf("flow = %.1f, want nothing feeding immersion before she begins", state.Metric(MetricFlow))
	}

	busy := driveBlock("hobby", 4)
	busy.StartedAt = start.UnixMilli()
	UpdateDrives(&state, busy, start)
	// One tick of a hobby is a quarter of its hourly boost, which is already well
	// clear of the nothing-feeding-it case above.
	if state.Metric(MetricFlow) < 6 {
		t.Fatalf("flow = %.1f, want a started hobby to pull immersion up", state.Metric(MetricFlow))
	}
}

func TestFireThresholdFallsAsWeightRises(t *testing.T) {
	if fireThreshold(10) >= fireThreshold(6) {
		t.Fatal("a heavier commitment should need less convincing, not more")
	}
	if fireThreshold(10) != fireBase-firePerWeight*10 {
		t.Fatalf("threshold = %.1f, want the weight formula to hold", fireThreshold(10))
	}
}

func TestUnknownBlockKindIsNotBlocked(t *testing.T) {
	if !ShouldStart(DefaultState(time.Now().UnixMilli()), driveBlock("mystery", 10), time.Now()) {
		t.Fatal("a block with no drive spec should default to doing it rather than freezing her")
	}
}
