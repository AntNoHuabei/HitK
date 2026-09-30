package companion

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

type blockSpec struct {
	kind   string
	label  string
	start  string
	end    string
	weight int
}

// mustPlan builds a contiguous day by hand so drift assertions never depend on
// template jitter.
func mustPlan(t *testing.T, specs ...blockSpec) DayPlan {
	t.Helper()
	plan := DayPlan{Date: "2026-09-30", Kind: "workday"}
	for i, spec := range specs {
		plan.Blocks = append(plan.Blocks, scheduleBlock{
			ID:          "b_" + strconv.Itoa(i),
			Kind:        spec.kind,
			Label:       spec.label,
			StartMinute: mustMinute(t, spec.start),
			EndMinute:   mustMinute(t, spec.end),
			Weight:      spec.weight,
			Origin:      "plan",
		})
	}
	return plan
}

func mustMinute(t *testing.T, hhmm string) int {
	t.Helper()
	parts := strings.Split(hhmm, ":")
	if len(parts) != 2 {
		t.Fatalf("bad minute %q", hhmm)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("bad hour in %q: %v", hhmm, err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("bad minute in %q: %v", hhmm, err)
	}
	return h*60 + m
}

func assertBlock(t *testing.T, plan DayPlan, id, start, end string) {
	t.Helper()
	for _, block := range plan.Blocks {
		if block.ID != id {
			continue
		}
		if block.StartMinute != mustMinute(t, start) || block.EndMinute != mustMinute(t, end) {
			t.Fatalf("%s = %02d:%02d-%02d:%02d, want %s-%s",
				id,
				block.StartMinute/60, block.StartMinute%60,
				block.EndMinute/60, block.EndMinute%60,
				start, end)
		}
		return
	}
	t.Fatalf("block %q missing from plan", id)
}

func TestDriftAbsorbedInsideCurrentBlock(t *testing.T) {
	plan := mustPlan(t,
		blockSpec{kind: "rest", label: "在家放松", start: "12:00", end: "14:00", weight: 2},
		blockSpec{kind: "work", label: "下午上班", start: "14:00", end: "18:00", weight: 6},
	)
	outcome := plan.ApplyDrift(Drift{AtMinute: mustMinute(t, "12:30"), Minutes: 60, Severity: 3})
	if outcome.Absorbed != 60 {
		t.Fatalf("absorbed = %d, want 60", outcome.Absorbed)
	}
	if outcome.Late != 0 || outcome.MissedID != "" {
		t.Fatalf("nothing should move, got %+v", outcome)
	}
	assertBlock(t, plan, "b_1", "14:00", "18:00")
}

func TestDriftCompressesLighterBlocksToProtectTheWall(t *testing.T) {
	plan := mustPlan(t,
		blockSpec{kind: "break", label: "吃午饭", start: "12:00", end: "13:00", weight: 5},
		blockSpec{kind: "rest", label: "午休", start: "13:00", end: "14:00", weight: 2},
		blockSpec{kind: "work", label: "下午上班", start: "14:00", end: "18:00", weight: 6},
	)
	// 90 minutes lost from 12:30 pushes 60 past lunch. The nap gives up 40 of it,
	// so work only slips 20 instead of a full hour.
	outcome := plan.ApplyDrift(Drift{AtMinute: mustMinute(t, "12:30"), Minutes: 90, Severity: 3})
	if outcome.Clawed != 40 {
		t.Fatalf("clawed = %d, want 40", outcome.Clawed)
	}
	if outcome.Late != 20 {
		t.Fatalf("late = %d, want 20", outcome.Late)
	}
	if outcome.MissedID != "" {
		t.Fatalf("work should not be missed, got %q", outcome.MissedID)
	}
	assertBlock(t, plan, "b_1", "14:00", "14:20")
	assertBlock(t, plan, "b_2", "14:20", "18:20")
}

func TestDriftSkipsHeavyCommitmentWhenTooLate(t *testing.T) {
	plan := mustPlan(t,
		blockSpec{kind: "break", label: "吃午饭", start: "12:00", end: "13:00", weight: 5},
		blockSpec{kind: "rest", label: "午休", start: "13:00", end: "14:00", weight: 2},
		blockSpec{kind: "exam", label: "考试", start: "14:00", end: "17:00", weight: 10},
	)
	// An exam tolerates five minutes. Three hours of overrun cannot be clawed back,
	// so she loses it and the block's own hours come back to the day.
	outcome := plan.ApplyDrift(Drift{AtMinute: mustMinute(t, "12:30"), Minutes: 180, Severity: 3, Reason: "临时被拉住帮忙"})
	if outcome.MissedID != "b_2" {
		t.Fatalf("missed = %q, want b_2", outcome.MissedID)
	}
	var exam scheduleBlock
	for _, block := range plan.Blocks {
		if block.ID == "b_2" {
			exam = block
		}
	}
	if !exam.Missed || exam.MissReason != "临时被拉住帮忙" {
		t.Fatalf("exam not marked missed with reason: %+v", exam)
	}
	if exam.EndMinute != exam.StartMinute {
		t.Fatalf("missed block should collapse, got %d-%d", exam.StartMinute, exam.EndMinute)
	}
}

func TestDriftSeverityOverwhelmsWeight(t *testing.T) {
	plan := mustPlan(t,
		blockSpec{kind: "rest", label: "在家放松", start: "12:00", end: "13:00", weight: 2},
		blockSpec{kind: "hobby", label: "画画", start: "13:00", end: "18:00", weight: 2},
	)
	// Nothing downstream is worth fighting for against a severity of 5, so the day
	// simply slides and nothing gets skipped.
	outcome := plan.ApplyDrift(Drift{AtMinute: mustMinute(t, "12:30"), Minutes: 120, Severity: 5})
	if outcome.MissedID != "" {
		t.Fatalf("nothing should be missed, got %q", outcome.MissedID)
	}
	if outcome.Late != 90 {
		t.Fatalf("late = %d, want 90", outcome.Late)
	}
	assertBlock(t, plan, "b_1", "14:30", "19:30")
}

func TestUserCommitmentRearrangesWithoutCuttingTheDay(t *testing.T) {
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "12:00", weight: 6},
		blockSpec{kind: "break", label: "吃午饭", start: "12:00", end: "13:00", weight: 5},
		blockSpec{kind: "rest", label: "在家待着", start: "13:00", end: "18:00", weight: 2},
	)
	block := plan.AddUserCommitment(mustMinute(t, "13:30"), 90, "陪他去看展", 8)
	if block.Weight != 8 || block.Origin != "user" {
		t.Fatalf("user block = %+v", block)
	}
	assertBlock(t, plan, "b_2", "13:00", "13:30")
	assertBlock(t, plan, block.ID, "13:30", "15:00")
	// The evening survives behind the request instead of being trimmed away.
	assertBlock(t, plan, block.ID+"_tail", "15:00", "18:00")
}

func TestLateToleranceRunsInverseToWeight(t *testing.T) {
	if lateTolerance(10) >= lateTolerance(5) {
		t.Fatal("a heavier commitment should have a tighter window, not a looser one")
	}
	if lateTolerance(1) <= lateTolerance(4) {
		t.Fatal("a light commitment should tolerate more lateness")
	}
}

// seedDay writes a hand-built plan for the day around at so the scenario does not
// depend on template jitter.
func seedDay(t *testing.T, manager *Manager, at time.Time, specs ...blockSpec) {
	t.Helper()
	plan := mustPlan(t, specs...)
	plan.Date = at.Format("2006-01-02")
	if err := manager.store.SaveDayPlan(manager.id, plan); err != nil {
		t.Fatal(err)
	}
}

// startBlockAt marks whatever the persisted day holds at `at` as begun. The tick
// loop does this as time passes, and availability now keys off it, so a test that
// wants her busy has to put her inside the block rather than just point at a busy
// hour on the clock.
func startBlockAt(t *testing.T, manager *Manager, at time.Time) {
	t.Helper()
	profile, err := manager.store.Profile(manager.id)
	if err != nil {
		t.Fatal(err)
	}
	plan := manager.planAt(profile, at)
	block := plan.blockAt(at)
	if block.Kind == "open" {
		t.Fatalf("no committed block at %s to be busy with", at.Format("15:04"))
	}
	if !plan.markStarted(block.ID, at) {
		t.Fatalf("could not start block %q", block.ID)
	}
	if err := manager.store.SaveDayPlan(manager.id, plan); err != nil {
		t.Fatal(err)
	}
}

func TestTurnTimeMakesHerLateForWork(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	// A turn that just ended at 14:30 and took 90 minutes started at 13:00, inside
	// the nap. Work is heavier than the drift, so she keeps it and shows up late.
	at := time.Date(2026, 9, 30, 14, 30, 0, 0, time.Local)
	seedDay(t, manager, at,
		blockSpec{kind: "break", label: "吃午饭", start: "12:00", end: "13:00", weight: 5},
		blockSpec{kind: "rest", label: "午休", start: "13:00", end: "14:00", weight: 2},
		blockSpec{kind: "work", label: "下午上班", start: "14:00", end: "18:00", weight: 6},
	)

	manager.scheduleTurnDrift(at, 90)

	saved, ok, err := manager.store.DayPlan(manager.id, "2026-09-30")
	if err != nil || !ok {
		t.Fatalf("plan should be saved: ok=%v err=%v", ok, err)
	}
	assertBlock(t, saved, "b_2", "14:30", "18:30")

	thoughts, err := manager.store.ActiveThoughts(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(thoughts) == 0 || !strings.Contains(thoughts[0].Content, "30") {
		t.Fatalf("she should have something to say about being late: %+v", thoughts)
	}
}

func TestTurnTimeCanCostHerAHeavyCommitment(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	// A three hour turn that started during the nap leaves two hours of overrun, and
	// an exam forgives five minutes. It is gone and she knows it.
	at := time.Date(2026, 9, 30, 16, 0, 0, 0, time.Local)
	seedDay(t, manager, at,
		blockSpec{kind: "rest", label: "午休", start: "13:00", end: "14:00", weight: 2},
		blockSpec{kind: "exam", label: "考试", start: "14:00", end: "17:00", weight: 10},
	)

	manager.scheduleTurnDrift(at, 180)

	saved, ok, err := manager.store.DayPlan(manager.id, "2026-09-30")
	if err != nil || !ok {
		t.Fatalf("plan should be saved: ok=%v err=%v", ok, err)
	}
	var missed bool
	for _, block := range saved.Blocks {
		if block.Kind == "exam" && block.Missed {
			missed = true
		}
	}
	if !missed {
		t.Fatalf("the exam should be marked missed: %+v", saved.Blocks)
	}
	thoughts, err := manager.store.ActiveThoughts(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(thoughts) == 0 || !strings.Contains(thoughts[0].Content, "考试") {
		t.Fatalf("she should know she lost the exam: %+v", thoughts)
	}
}

func TestShortTurnsDoNotChurnThePlan(t *testing.T) {
	manager := openTestManager(t)
	manager.SetPorts(&fakeModel{}, &fakeConversation{threadID: "th_companion"})
	at := time.Date(2026, 9, 30, 14, 30, 0, 0, time.Local)
	seedDay(t, manager, at,
		blockSpec{kind: "rest", label: "午休", start: "13:00", end: "14:00", weight: 2},
		blockSpec{kind: "work", label: "下午上班", start: "14:00", end: "18:00", weight: 6},
	)
	before, _, err := manager.store.DayPlan(manager.id, "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}

	manager.scheduleTurnDrift(at, 5)

	after, _, err := manager.store.DayPlan(manager.id, "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("a five minute turn should not touch the plan: %d -> %d", before.Revision, after.Revision)
	}
}

func TestPlanRoundTripsThroughStore(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/companion.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "12:00", weight: 6},
		blockSpec{kind: "exam", label: "考试", start: "14:00", end: "17:00", weight: 10},
	)
	plan.ApplyDrift(Drift{AtMinute: mustMinute(t, "09:30"), Minutes: 300, Severity: 2, Reason: "早上磨蹭"})
	if err := store.SaveDayPlan(DefaultID, plan); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := store.DayPlan(DefaultID, plan.Date)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("plan should be present after saving")
	}
	if len(loaded.Blocks) != len(plan.Blocks) || loaded.Blocks[1].Missed != plan.Blocks[1].Missed {
		t.Fatalf("round trip lost drift: %+v", loaded.Blocks)
	}
	if _, ok, err := store.DayPlan(DefaultID, "1999-01-01"); err != nil || ok {
		t.Fatalf("unknown date should read as absent, ok=%v err=%v", ok, err)
	}
}
