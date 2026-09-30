package companion

import (
	"testing"
	"time"
)

var monday = time.Date(2026, 9, 28, 12, 0, 0, 0, time.Local)
var saturday = time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)

func blocksOfKind(plan DayPlan, kind string) []scheduleBlock {
	var out []scheduleBlock
	for _, block := range plan.Blocks {
		if block.Kind == kind {
			out = append(out, block)
		}
	}
	return out
}

func TestBodyOnlyDayCarriesNoWork(t *testing.T) {
	profile := DefaultProfile(1)
	plan := buildDayPlan(profile, monday, nil)
	if len(blocksOfKind(plan, "work")) != 0 {
		t.Fatalf("a day with no commitments should not schedule work: %+v", plan.Blocks)
	}
	if len(blocksOfKind(plan, "sleep")) < 2 {
		t.Fatal("the body should bookend the day with sleep")
	}
	if len(blocksOfKind(plan, "meal")) != 3 {
		t.Fatalf("meals = %d, want three", len(blocksOfKind(plan, "meal")))
	}
	if len(blocksOfKind(plan, "open")) == 0 {
		t.Fatal("whatever the body does not fill should stay open, not vanish")
	}
}

func TestWeekdayCommitmentDoesNotAppearOnWeekend(t *testing.T) {
	profile := DefaultProfile(1)
	commits := defaultCommitments(profile)
	weekday := buildDayPlan(profile, monday, commits)
	weekend := buildDayPlan(profile, saturday, commits)
	if len(blocksOfKind(weekday, "work")) == 0 {
		t.Fatal("Monday should carry the weekday job")
	}
	if len(blocksOfKind(weekend, "work")) != 0 {
		t.Fatalf("Saturday should not: %+v", weekend.Blocks)
	}
}

func TestOneOffCommitmentOnlyMatchesItsOwnDate(t *testing.T) {
	oneOff := Commitment{ID: "cmt_one", Kind: "exam", Label: "考试", Weight: 10, Date: "2026-09-28", Start: "14:00", Duration: 120, Source: "user"}
	if !oneOff.matches(monday) {
		t.Fatal("should match its own date")
	}
	if oneOff.matches(saturday) {
		t.Fatal("should not match another date")
	}

	// Neither a date nor a weekday set means it fires nowhere, not everywhere.
	never := Commitment{ID: "cmt_none", Kind: "work", Label: "上班", Weight: 6, Start: "09:00", Duration: 480}
	if never.matches(monday) || never.matches(saturday) {
		t.Fatal("a commitment with no date and no weekdays should match nothing")
	}
}

func TestMergeCommitmentsSortsByStart(t *testing.T) {
	profile := DefaultProfile(1)
	extra := []Commitment{
		{ID: "late", Kind: "dinner", Label: "饭局", Weight: 7, Date: "2026-09-28", Start: "19:30", Duration: 150, Source: "user"},
		{ID: "early", Kind: "errand", Label: "取快递", Weight: 4, Date: "2026-09-28", Start: "08:00", Duration: 30, Source: "user"},
	}
	merged := mergeCommitments(profile, extra)
	if merged[0].ID != "early" {
		t.Fatalf("first = %q, want the 08:00 errand", merged[0].ID)
	}
	if merged[len(merged)-1].ID != "late" {
		t.Fatalf("last = %q, want the 19:30 dinner", merged[len(merged)-1].ID)
	}
}

func TestLunchCarvesThroughAFullWorkday(t *testing.T) {
	profile := DefaultProfile(1)
	plan := buildDayPlan(profile, monday, []Commitment{
		{ID: "w", Kind: "work", Label: "上班", Weight: 6, Weekdays: weekdaySet(), Start: "09:00", Duration: 480, Source: "profile"},
	})
	var lunch scheduleBlock
	found := false
	for _, block := range blocksOfKind(plan, "meal") {
		if block.Label == "吃午饭" {
			lunch, found = block, true
		}
	}
	if !found {
		t.Fatalf("an eight hour workday should not delete lunch: %+v", plan.Blocks)
	}
	if lunch.StartMinute < 11*60 || lunch.StartMinute > 14*60 {
		t.Fatalf("lunch starts at %02d:%02d, want around midday", lunch.StartMinute/60, lunch.StartMinute%60)
	}
	// The workday survives on both sides of it rather than being pushed out.
	work := blocksOfKind(plan, "work")
	if len(work) < 2 {
		t.Fatalf("work should be split around lunch, got %+v", work)
	}
}

func TestHeavierCommitmentTakesItsSlotAndSplitsTheWorkday(t *testing.T) {
	profile := DefaultProfile(1)
	plan := buildDayPlan(profile, monday, []Commitment{
		{ID: "w", Kind: "work", Label: "上班", Weight: 6, Weekdays: weekdaySet(), Start: "09:00", Duration: 480, Source: "profile"},
		{ID: "e", Kind: "exam", Label: "考试", Weight: 10, Date: "2026-09-28", Start: "11:30", Duration: 180, Source: "user"},
	})
	exams := blocksOfKind(plan, "exam")
	if len(exams) != 1 {
		t.Fatalf("exams = %d, want the exam placed exactly once", len(exams))
	}
	if exams[0].StartMinute != 11*60+30 || exams[0].EndMinute != 14*60+30 {
		t.Fatalf("exam = %02d:%02d-%02d:%02d, want 11:30-14:30",
			exams[0].StartMinute/60, exams[0].StartMinute%60, exams[0].EndMinute/60, exams[0].EndMinute%60)
	}
	// The job is still there, just broken around it. It was outranked, not erased.
	if len(blocksOfKind(plan, "work")) < 2 {
		t.Fatalf("work should survive on both sides of the exam: %+v", plan.Blocks)
	}
}

func TestSleepCarriesAcrossMidnight(t *testing.T) {
	began := time.Date(2026, 9, 27, 23, 40, 0, 0, time.Local).UnixMilli()
	prev := DayPlan{Blocks: []scheduleBlock{{ID: "s", Kind: "sleep", StartMinute: 1380, EndMinute: 1440, StartedAt: began}}}
	cur := DayPlan{Blocks: []scheduleBlock{{ID: "h", Kind: "sleep", StartMinute: 0, EndMinute: 460}}}

	cur.carrySleepFrom(prev)
	if cur.Blocks[0].StartedAt != began {
		t.Fatalf("head sleep startedAt = %d, want it inherited %d", cur.Blocks[0].StartedAt, began)
	}

	// A night she never started must not carry a fake sleep into the next morning.
	awake := DayPlan{Blocks: []scheduleBlock{{ID: "s", Kind: "sleep", StartMinute: 1380, EndMinute: 1440}}}
	target := DayPlan{Blocks: []scheduleBlock{{ID: "h", Kind: "sleep", StartMinute: 0, EndMinute: 460}}}
	target.carrySleepFrom(awake)
	if target.Blocks[0].StartedAt != 0 {
		t.Fatal("an unfinished night should not carry sleep forward")
	}
}

func TestCommitmentsRoundTripThroughStore(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/companion.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first := Commitment{ID: NewCommitmentID(), Kind: "dinner", Label: "饭局", Weight: 7, Date: "2026-09-28", Start: "19:30", Duration: 150, Source: "user"}
	second := Commitment{ID: NewCommitmentID(), Kind: "errand", Label: "取快递", Weight: 4, Date: "2026-09-29", Start: "08:00", Duration: 30, Source: "user"}
	for _, commitment := range []Commitment{first, second} {
		if err := store.SaveCommitment(DefaultID, commitment); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := store.Commitments(DefaultID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("commitments = %d, want 2", len(loaded))
	}

	if err := store.DeleteCommitment(DefaultID, first.ID); err != nil {
		t.Fatal(err)
	}
	after, err := store.Commitments(DefaultID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].ID != second.ID {
		t.Fatalf("after delete = %+v, want only the errand", after)
	}

	// A different character's ledger stays empty regardless.
	other, err := store.Commitments("cmp_someoneelse")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other character has %d commitments, want 0", len(other))
	}
}
