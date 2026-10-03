package companion

import (
	"math"
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

// drivePlan wraps a block in a day of its own. UpdateDrives needs the plan, not
// just the block, because the sleep ledger asks whether she is actually under and
// that answer is read from the day rather than from the block's StartedAt.
func drivePlan(block scheduleBlock) DayPlan {
	return DayPlan{Date: "2026-09-28", Kind: "workday", Blocks: []scheduleBlock{block}}
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
	plan := mustPlan(t,
		blockSpec{kind: "open", label: "晚上空着", start: "18:00", end: "23:30", weight: 1},
		blockSpec{kind: "sleep", label: "该睡了", start: "23:30", end: "24:00", weight: 10},
	)
	state := DefaultState(time.Now().UnixMilli())

	// Past her planned bedtime and nowhere near asleep: the hour still costs her.
	late := time.Date(2026, 9, 28, 23, 45, 0, 0, time.Local)
	UpdateDrives(&state, plan, plan.blockAt(late), late)
	if state.SleepDebtMinutes != 15 {
		t.Fatalf("debt = %d, want 15 after one tick up past bedtime", state.SleepDebtMinutes)
	}

	// Mid-afternoon does not cost anything.
	noon := time.Date(2026, 9, 28, 15, 0, 0, 0, time.Local)
	UpdateDrives(&state, plan, plan.blockAt(noon), noon)
	if state.SleepDebtMinutes != 15 {
		t.Fatalf("debt = %d, want it unchanged at 15:00", state.SleepDebtMinutes)
	}
}

// TestLyingAwakeInBedStillAccruesDebt is the whole of P1. A started sleep block is
// not sleep: she got into bed on time and flow will not let her go under, so the
// ledger must keep charging. Repayment follows consciousness, never StartedAt,
// otherwise the hours she spent staring at the ceiling are erased from the books
// and she wakes up looking rested after a night she did not sleep.
func TestLyingAwakeInBedStillAccruesDebt(t *testing.T) {
	at := time.Date(2026, 9, 28, 23, 50, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "sleep", label: "该睡了", start: "23:30", end: "24:00", weight: 10},
	)
	block := plan.Blocks[0]
	block.StartedAt = at.Add(-5 * time.Minute).UnixMilli()

	state := driveState(map[MetricID]float64{MetricFlow: 70})
	// onset = 18 + 70*0.9 = 81 minutes, so five minutes in she is nowhere near under.
	if consciousnessAt(state, plan, at) != ConsciousAwake {
		t.Fatal("setup: she should still be awake five minutes in while wired")
	}

	UpdateDrives(&state, plan, block, at)
	if state.SleepDebtMinutes != 15 {
		t.Fatalf("debt = %d, want 15; lying awake in bed charges, it does not repay", state.SleepDebtMinutes)
	}
	if state.SleepRepaidMinutes != 0 {
		t.Fatalf("repaid = %d, want nothing repaid while she is awake", state.SleepRepaidMinutes)
	}
}

func TestSleepPaysDebtBackUpToTheSessionCap(t *testing.T) {
	at := time.Date(2026, 9, 28, 1, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "sleep", label: "还在睡", start: "00:00", end: "08:00", weight: 10},
	)
	// Started an hour before the loop so she is properly under from the first tick.
	plan.markStarted("b_0", at.Add(-time.Hour))
	block := plan.blockAt(at)

	state := DefaultState(time.Now().UnixMilli())
	state.SleepDebtMinutes = 8 * 60

	// Four hours of sleep clears four hours of debt.
	for i := 0; i < 16; i++ {
		UpdateDrives(&state, plan, block, at.Add(time.Duration(i)*tickInterval))
	}
	if state.SleepDebtMinutes != 4*60 {
		t.Fatalf("debt = %d, want 4h repaid", state.SleepDebtMinutes)
	}

	// The cap holds after that, so a long lie-in cannot wipe out a week of damage in
	// one go.
	for i := 0; i < 16; i++ {
		UpdateDrives(&state, plan, block, at.Add(time.Duration(16+i)*tickInterval))
	}
	if state.SleepDebtMinutes != 4*60 {
		t.Fatalf("debt = %d, want repayment capped at %d minutes", state.SleepDebtMinutes, 4*60)
	}
}

func TestLateNightStreakBuildsAndAnOnTimeNightBreaksIt(t *testing.T) {
	state := DefaultState(time.Now().UnixMilli())
	bedtime := driveBlock("sleep", 10)
	bedtime.StartMinute = 23*60 + 30

	NoteSleepStart(&state, bedtime, time.Date(2026, 9, 28, 23, 35, 0, 0, time.Local))
	if state.SleepStreak != 0 {
		t.Fatalf("streak = %d, want 0 for an on-time night", state.SleepStreak)
	}

	for _, day := range []int{28, 29, 30} {
		NoteSleepStart(&state, bedtime, time.Date(2026, 9, day, 2, 0, 0, 0, time.Local))
	}
	if state.SleepStreak != 3 {
		t.Fatalf("streak = %d, want 3 late nights", state.SleepStreak)
	}

	NoteSleepStart(&state, bedtime, time.Date(2026, 10, 1, 23, 20, 0, 0, time.Local))
	if state.SleepStreak != 0 {
		t.Fatalf("streak = %d, want it broken by an early night", state.SleepStreak)
	}
}

// TestLateStreakFollowsHerOwnBedtime is P3: lateness is measured against the block
// she is starting, not against a hardcoded 23:30. A 除夕 bedtime of 01:00 means
// going to bed at 02:00 is one hour late, not two and a half, and a weekend that
// runs to 00:30 is not a late night at all.
func TestLateStreakFollowsHerOwnBedtime(t *testing.T) {
	eveBed := driveBlock("sleep", 10)
	eveBed.StartMinute = 60 // 01:00
	if got := minutesLateForBlock(eveBed, time.Date(2026, 2, 16, 2, 0, 0, 0, time.Local)); got != 60 {
		t.Fatalf("late = %d, want 60 against a 01:00 bedtime", got)
	}
	if got := minutesLateForBlock(eveBed, time.Date(2026, 2, 16, 0, 30, 0, 0, time.Local)); got != -30 {
		t.Fatalf("late = %d, want negative for an early night", got)
	}

	weekendBed := driveBlock("sleep", 10)
	weekendBed.StartMinute = 0
	state := DefaultState(time.Now().UnixMilli())
	NoteSleepStart(&state, weekendBed, time.Date(2026, 10, 3, 0, 30, 0, 0, time.Local))
	if state.SleepStreak != 0 {
		t.Fatalf("streak = %d, want 0; half past midnight against a midnight bedtime is on time", state.SleepStreak)
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
		UpdateDrives(&state, drivePlan(open), open, start.Add(time.Duration(i)*tickInterval))
	}
	risen := state.Metrics[MetricBoredom]
	if risen < 20 {
		t.Fatalf("boredom = %.1f, want it to climb through empty time", risen)
	}

	// Something with her in it takes the boredom back down again.
	busy := driveBlock("hobby", 4)
	busy.StartedAt = start.UnixMilli()
	for i := 0; i < 4; i++ {
		UpdateDrives(&state, drivePlan(busy), busy, start.Add(time.Duration(i)*tickInterval))
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
	UpdateDrives(&state, drivePlan(idle), idle, start)
	if state.Metric(MetricFlow) > 1 {
		t.Fatalf("flow = %.1f, want nothing feeding immersion before she begins", state.Metric(MetricFlow))
	}

	busy := driveBlock("hobby", 4)
	busy.StartedAt = start.UnixMilli()
	UpdateDrives(&state, drivePlan(busy), busy, start)
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

// TestEveryNamedBlockKindHasADriveSpec closes the fail-open hole above. A kind with
// no entry makes ShouldStart return true, which quietly turns a weighted thing into
// a thing that always happens: weight stops meaning anything and her day reads as a
// schedule she cannot miss, which is the opposite of the model. It fails silently,
// so the whole vocabulary is enumerated here instead of trusted to memory. That
// covers the commitment whitelist, which passes its Kind straight through to the
// block, plus everything the body template and the rule vocabulary can name.
func TestEveryNamedBlockKindHasADriveSpec(t *testing.T) {
	vocabulary := map[string]bool{
		"sleep": true, "open": true, "rest": true, "intimacy": true,
		"morning": true, "break": true, "hobby": true, "game": true, "commute": true,
	}
	for kind := range commitmentKinds {
		vocabulary[kind] = true
	}
	for kind := range vocabulary {
		if _, ok := driveSpecs[kind]; !ok {
			t.Fatalf("block kind %q has no driveSpec; ShouldStart would fail open and make it unconditional", kind)
		}
	}
}

// TestExamIsHeavyButNotUnmissable is the case the weight model exists for. An exam
// at weight 10 is about as sure a thing as her day contains, and a bad enough day
// still takes it away.
func TestExamIsHeavyButNotUnmissable(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	block := driveBlock("exam", 10)

	calm := driveState(map[MetricID]float64{MetricAnxiety: 18, MetricFatigue: 25})
	if !ShouldStart(calm, block, at) {
		t.Fatal("an exam should go ahead on an ordinary day")
	}

	breakdown := driveState(map[MetricID]float64{MetricAnxiety: 95, MetricFatigue: 95})
	if ShouldStart(breakdown, block, at) {
		t.Fatal("a total breakdown should cost her the exam; nothing is unmissable")
	}
}

// TestUserRequestedPlanStillYieldsToImmersion keeps a thing he asked her to do from
// hardening into a must-attend. It carries the highest standing need there is, and
// being deep inside something else is the one thing that pulls her off it.
func TestUserRequestedPlanStillYieldsToImmersion(t *testing.T) {
	at := time.Date(2026, 9, 28, 15, 0, 0, 0, time.Local)
	block := driveBlock("user", 7)

	normal := driveState(map[MetricID]float64{MetricFlow: 10, MetricFatigue: 25})
	if !ShouldStart(normal, block, at) {
		t.Fatal("what he asked her to do should normally happen")
	}

	absorbed := driveState(map[MetricID]float64{MetricFlow: 95, MetricFatigue: 90})
	if ShouldStart(absorbed, block, at) {
		t.Fatal("buried in something else she should be able to not do it")
	}
}

// --- Scene immersion -------------------------------------------------------
//
// A scene is where intimacy actually lives: SceneStage and SceneStartedAt, moved
// by the conversation rather than by the plan builder. These tests hold the line
// that the scene, not a phantom block kind, is what puts immersion on her.

func sceneState(stage string, startedAt time.Time, values map[MetricID]float64) State {
	state := driveState(values)
	state.SceneStage = stage
	state.ConsentState = "affirmed"
	state.SceneStartedAt = startedAt.UnixMilli()
	return state
}

// TestIntimacyIsNotAFlowBoostingBlockKind pins the removal. Intimacy is a scene,
// not a scheduled block, so the block table must not claim it as a source of
// immersion. If some future plan builder does mint a block of that kind, the scene
// path still owns the number.
func TestIntimacyIsNotAFlowBoostingBlockKind(t *testing.T) {
	if _, ok := flowBoost("intimacy"); ok {
		t.Fatal("intimacy is a scene, not a block; flowBoost must not claim it")
	}
}

func TestSceneFlowLadderMatchesStage(t *testing.T) {
	started := time.Date(2026, 9, 28, 22, 0, 0, 0, time.Local)
	cases := []struct {
		stage string
		want  float64
	}{
		{"climax", sceneFlowActive},
		{"active", sceneFlowActive},
		{"aftercare", sceneFlowAftercare},
		{"flirt", sceneFlowFlirt},
		{"none", 0},
		{"stopped", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := sceneFlow(sceneState(tc.stage, started, nil), started.Add(time.Hour)); got != tc.want {
			t.Fatalf("stage %q flow = %v, want %v", tc.stage, got, tc.want)
		}
	}
}

// TestSceneFlowDiesWithTheSceneClock keeps the immersion window tied to
// sceneHorizon rather than to a second copy of the same numbers.
func TestSceneFlowDiesWithTheSceneClock(t *testing.T) {
	started := time.Date(2026, 9, 28, 22, 0, 0, 0, time.Local)
	horizon, ok := sceneHorizon("active")
	if !ok {
		t.Fatal("active should have a horizon")
	}
	if got := sceneFlow(sceneState("active", started, nil), started.Add(horizon-time.Minute)); got != sceneFlowActive {
		t.Fatalf("just inside the horizon flow = %v, want %v", got, sceneFlowActive)
	}
	if got := sceneFlow(sceneState("active", started, nil), started.Add(horizon)); got != 0 {
		t.Fatalf("at the horizon flow = %v, want 0", got)
	}

	// Flirt keeps the longer window advanceSceneByWallClock already gave it, so
	// the two agree without either restating the number.
	flirtHorizon, ok := sceneHorizon("flirt")
	if !ok || flirtHorizon <= horizon {
		t.Fatalf("flirt horizon = %v, want longer than active's %v", flirtHorizon, horizon)
	}
	if got := sceneFlow(sceneState("flirt", started, nil), started.Add(horizon+time.Hour)); got != sceneFlowFlirt {
		t.Fatalf("flirt past the active window flow = %v, want %v", got, sceneFlowFlirt)
	}

	// No start timestamp means no scene, whatever the stage claims.
	loose := sceneState("active", started, nil)
	loose.SceneStartedAt = 0
	if got := sceneFlow(loose, started.Add(time.Hour)); got != 0 {
		t.Fatalf("scene with no start time flow = %v, want 0", got)
	}
}

// TestFlirtFeedsFlowWithoutEverHoldingHer keeps two questions separate. Flirt is
// immersive enough to nudge bedtime, and never immersive enough to make her
// unreachable: she can flirt and cook dinner at the same time.
func TestFlirtFeedsFlowWithoutEverHoldingHer(t *testing.T) {
	started := time.Date(2026, 9, 28, 22, 0, 0, 0, time.Local)
	flirt := sceneState("flirt", started, nil)
	if sceneLive(flirt, started.Add(time.Hour)) {
		t.Fatal("flirt must never make her unreachable")
	}
	if sceneFlow(flirt, started.Add(time.Hour)) <= 0 {
		t.Fatal("flirt should still put a little immersion on her")
	}
}

// TestSceneReachesImmersionNoBlockCan is the hole itself. The richest block she
// can be handed is work at +10, which settles near 23 against a weight 10
// bedtime bar of 30, so no schedule on its own can ever keep her up. A live scene
// clears that bar; the identical night without one does not.
func TestSceneReachesImmersionNoBlockCan(t *testing.T) {
	plan := mustPlan(t,
		blockSpec{kind: "open", label: "晚上空着", start: "18:00", end: "23:30", weight: 1},
		blockSpec{kind: "sleep", label: "该睡了", start: "23:30", end: "24:00", weight: 10},
	)
	started := time.Date(2026, 9, 28, 21, 0, 0, 0, time.Local)
	bedtime := time.Date(2026, 9, 28, 23, 45, 0, 0, time.Local)
	tired := map[MetricID]float64{MetricSleepPressure: 80, MetricFatigue: 60}

	withScene := sceneState("active", started, tired)
	for step := 1; step <= 10; step++ {
		at := started.Add(time.Duration(step) * tickInterval)
		UpdateDrives(&withScene, plan, plan.blockAt(at), at)
	}
	// Roughly 2.5 hours in, immersion should be well past anything a block could
	// have built.
	if flow := withScene.Metric(MetricFlow); flow < 44 {
		t.Fatalf("flow after 2.5h of scene = %v, want past 44", flow)
	}

	// Same woman, same tiredness, same debt. The only difference is whether
	// something has her.
	alone := driveState(tired)
	alone.SleepDebtMinutes = withScene.SleepDebtMinutes
	if !ShouldStart(alone, plan.blockAt(bedtime), bedtime) {
		t.Fatal("control: exhausted with nothing going on, she should be asleep")
	}
	if ShouldStart(withScene, plan.blockAt(bedtime), bedtime) {
		t.Fatalf("a live scene should hold her up past the same bedtime (flow=%v debt=%d)",
			withScene.Metric(MetricFlow), withScene.SleepDebtMinutes)
	}
}

// TestSceneDoesNotStackOnTopOfAStartedBlock keeps one person at one immersion
// level. Mid-scene while a work block also runs is not two things pulling at once.
func TestSceneDoesNotStackOnTopOfAStartedBlock(t *testing.T) {
	work := driveBlock("work", 6)
	work.StartedAt = time.Date(2026, 9, 28, 21, 0, 0, 0, time.Local).UnixMilli()
	plan := DayPlan{Date: "2026-09-28", Kind: "workday", Blocks: []scheduleBlock{work}}
	at := time.Date(2026, 9, 28, 21, 15, 0, 0, time.Local)

	// Zeroed so the tick's own arithmetic is what the numbers show.
	plain := driveState(map[MetricID]float64{MetricFlow: 0})
	UpdateDrives(&plain, plan, work, at)
	if got := plain.Metric(MetricFlow); math.Abs(got-2.5) > 0.001 {
		t.Fatalf("work alone for one tick flow = %v, want 2.5", got)
	}

	blocked := sceneState("active", at.Add(-time.Hour), map[MetricID]float64{MetricFlow: 0})
	UpdateDrives(&blocked, plan, work, at)
	// The scene's 30 wins outright and the block adds nothing on top. Stacked,
	// this would read 10 instead of 7.5.
	if got := blocked.Metric(MetricFlow); math.Abs(got-7.5) > 0.001 {
		t.Fatalf("scene over work flow = %v, want 7.5 (the scene alone, not work+scene stacked)", got)
	}
}

// TestDebtStillWinsMidScene keeps the ceiling honest. Immersion borrows bedtime,
// it does not cancel sleep: enough accumulated debt puts her under with a scene
// running at full tilt.
func TestDebtStillWinsMidScene(t *testing.T) {
	at := time.Date(2026, 9, 28, 2, 0, 0, 0, time.Local)
	block := driveBlock("sleep", 10)
	deep := sceneState("active", at.Add(-time.Hour), map[MetricID]float64{
		MetricSleepPressure: 90, MetricFatigue: 80, MetricFlow: sceneFlowActive * 2.3,
	})
	// Three hours in the hole. Immersion tops out around 69 and does not move;
	// debt keeps climbing, so it is the force that eventually wins.
	deep.SleepDebtMinutes = 3 * 60
	if !ShouldStart(deep, block, at) {
		t.Fatalf("enough debt should put her under even mid-scene (flow=%v)", deep.Metric(MetricFlow))
	}
}
