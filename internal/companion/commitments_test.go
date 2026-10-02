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

func TestWorkdayScopeCoversMakeupSaturday(t *testing.T) {
	job := Commitment{ID: "w", Kind: "work", Label: "上班", Weight: 6,
		CalendarScope: ScopeWorkday, Start: "09:00", Duration: 480, Source: "profile"}
	if !job.matches(monday) {
		t.Fatal("an ordinary Monday should carry it")
	}
	if !job.matches(at(2026, time.October, 10)) {
		t.Fatal("调休 Saturday is a working day and must carry it too")
	}
	if job.matches(at(2026, time.October, 2)) {
		t.Fatal("国庆 is a rest day; the job stays off")
	}
	if job.matches(at(2026, time.September, 19)) {
		t.Fatal("an ordinary Saturday is not a working day")
	}
}

func TestRestdayScopeOnlyFallsOnRestDays(t *testing.T) {
	rest := Commitment{ID: "r", Kind: "errand", Label: "睡到自然醒", Weight: 3,
		CalendarScope: ScopeRestday, Start: "10:00", Duration: 60, Source: "profile"}
	if !rest.matches(at(2026, time.September, 19)) {
		t.Fatal("plain Saturday is a rest day")
	}
	if !rest.matches(at(2026, time.October, 2)) {
		t.Fatal("a holiday is a rest day")
	}
	if rest.matches(monday) {
		t.Fatal("a workday is not a rest day")
	}
	if rest.matches(at(2026, time.October, 10)) {
		t.Fatal("调休 Saturday is not a rest day")
	}
}

func TestExplicitDateOutranksScope(t *testing.T) {
	dinner := Commitment{ID: "d", Kind: "dinner", Label: "饭局", Weight: 7,
		Date: "2026-10-10", CalendarScope: ScopeRestday, Start: "19:00", Duration: 120, Source: "user"}
	if !dinner.matches(at(2026, time.October, 10)) {
		t.Fatal("an explicit date is the whole answer; a scope must not veto it")
	}
}

func TestNormalizeTurnCommitmentsKeepsOnlyWhatIsReal(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	held := []Commitment{{ID: "old", Kind: "dinner", Label: "和朋友吃饭", Date: "2026-10-05", Start: "19:00", Duration: 120, Source: "user"}}

	got := NormalizeTurnCommitments([]CommitmentDraft{
		{Kind: "dinner", Label: "和朋友吃饭", Date: "2026-10-05", Start: "19:00", Duration: 120, Weight: 7},
		{Kind: "exam", Label: "考科三", Date: "2026-10-06", Start: "14:00", Duration: 90, Weight: 9},
		{Label: "没给日期的安排", Start: "10:00"},
		{Label: "上周那顿饭", Date: "2026-09-25"},
		{Label: "明年再说", Date: "2027-05-01"},
		{Label: "周三加班", Date: "2026-10-07", Start: "20:00", Duration: 120, Weight: 5},
		{Label: "   "},
	}, held, now)

	if len(got) != 2 {
		t.Fatalf("accepted %d commitments, want the duplicate, undated, past, over-horizon and blank ones all dropped: %+v", len(got), got)
	}
	if got[0].Label != "考科三" || got[0].Date != "2026-10-06" || got[0].Kind != "exam" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[0].Start != "14:00" || got[0].Duration != 90 || got[0].Weight != 9 {
		t.Fatalf("first should keep what it was given: %+v", got[0])
	}
	if got[0].Source != "user" {
		t.Fatalf("source = %q, want user", got[0].Source)
	}
	if got[1].Label != "周三加班" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestNormalizeTurnCommitmentsFillsSaneDefaults(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	got := NormalizeTurnCommitments([]CommitmentDraft{
		{Label: "过来找我", Date: "2026-10-06"},
	}, nil, now)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	one := got[0]
	if one.Start != "09:00" {
		t.Fatalf("start = %q, want the 09:00 default", one.Start)
	}
	if one.Duration != 60 {
		t.Fatalf("duration = %d, want 60", one.Duration)
	}
	if one.Weight != 6 {
		t.Fatalf("weight = %d, want 6", one.Weight)
	}
	if one.Kind != "user" {
		t.Fatalf("kind = %q, want the user fallback", one.Kind)
	}
	if one.ID == "" {
		t.Fatal("needs an id to be storable")
	}
}

func TestNormalizeTurnCommitmentsCapsAtThree(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	var drafts []CommitmentDraft
	for i := 0; i < 6; i++ {
		drafts = append(drafts, CommitmentDraft{
			Label: "安排" + string(rune('A'+i)),
			Date:  time.Date(2026, 10, 2+i, 0, 0, 0, 0, time.Local).Format("2006-01-02"),
		})
	}
	got := NormalizeTurnCommitments(drafts, nil, now)
	if len(got) != maxTurnCommitments {
		t.Fatalf("accepted %d, want the cap of %d", len(got), maxTurnCommitments)
	}
}

func TestNormalizeTurnCommitmentsDropsNothingWhenEmpty(t *testing.T) {
	if got := NormalizeTurnCommitments(nil, nil, time.Now()); got != nil {
		t.Fatalf("no drafts should mean no commitments, got %+v", got)
	}
}

func TestTripCommitmentMovesHerWeatherCity(t *testing.T) {
	manager := openTestManager(t)
	profile, err := manager.Profile()
	if err != nil {
		t.Fatal(err)
	}
	home := profile.City
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.Local)
	if got := manager.effectiveCity(profile, now); got != home {
		t.Fatalf("city = %q, want the profile city with no trip on", got)
	}

	trip := Commitment{ID: NewCommitmentID(), Kind: "trip", Label: "国庆去成都", Weight: 7,
		Date: "2026-10-02", Start: "00:00", Duration: 1439, Source: "user", City: "成都"}
	if err := manager.store.SaveCommitment(manager.id, trip); err != nil {
		t.Fatal(err)
	}
	if got := manager.effectiveCity(profile, now); got != "成都" {
		t.Fatalf("city = %q, want 成都 while the trip covers today", got)
	}
	// Once the trip stops covering the day she is home again.
	if got := manager.effectiveCity(profile, time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)); got != home {
		t.Fatalf("city = %q, want back home after the trip", got)
	}
}
