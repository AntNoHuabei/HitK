package companion

import (
	"strings"
	"testing"
	"time"
)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.Local)
}

func TestNationalDayReadsAsHoliday(t *testing.T) {
	info := DayInfoAt(at(2026, time.October, 3))
	if info.Kind != DayHoliday {
		t.Fatalf("kind = %s, want holiday", info.Kind)
	}
	if info.Holiday != "国庆节" {
		t.Fatalf("holiday = %q, want 国庆节", info.Holiday)
	}
	if info.DayIndex != 3 || info.TotalDays != 7 {
		t.Fatalf("position = %d/%d, want 3/7", info.DayIndex, info.TotalDays)
	}
	if !info.IsRestDay() || info.IsWorkDay() {
		t.Fatal("国庆 is a rest day")
	}
	if line := info.Describe(); !strings.Contains(line, "第3天共7天") {
		t.Fatalf("describe = %q, want the day position in it", line)
	}
}

func TestHolidayLastDaySaysTomorrowIsWork(t *testing.T) {
	info := DayInfoAt(at(2026, time.October, 7))
	if !info.IsLastDay {
		t.Fatal("Oct 7 is the last day of the 2026 run")
	}
	if line := info.Describe(); !strings.Contains(line, "明天要上班") {
		t.Fatalf("describe = %q, want the warning about tomorrow", line)
	}
}

func TestMakeupSaturdayIsAWorkingDay(t *testing.T) {
	// 2026-10-10 is a Saturday the notice puts back on the calendar.
	info := DayInfoAt(at(2026, time.October, 10))
	if info.Kind != DayMakeup {
		t.Fatalf("kind = %s, want makeup", info.Kind)
	}
	if info.IsRestDay() {
		t.Fatal("a 调休 Saturday is not a rest day, whatever the weekday says")
	}
	if !strings.Contains(info.Describe(), "调休") {
		t.Fatalf("describe = %q, want 调休 called out", info.Describe())
	}
}

func TestMakeupSundayIsAWorkingDay(t *testing.T) {
	info := DayInfoAt(at(2026, time.September, 20))
	if info.Kind != DayMakeup || info.IsRestDay() {
		t.Fatalf("Sep 20 2026 = %+v, want a working makeup Sunday", info)
	}
}

func TestHolidayOnAWeekendDayStillReadsAsHoliday(t *testing.T) {
	// 2026 中秋 covers Sat 26th. Holiday outranks weekend so the label is right.
	info := DayInfoAt(at(2026, time.September, 26))
	if info.Kind != DayHoliday || info.Holiday != "中秋节" {
		t.Fatalf("Sep 26 2026 = %+v, want 中秋节", info)
	}
}

func TestPlainWeekendIsStillJustAWeekend(t *testing.T) {
	info := DayInfoAt(at(2026, time.September, 19))
	if info.Kind != DayWeekend || !info.IsRestDay() {
		t.Fatalf("Sep 19 2026 = %+v, want a plain weekend", info)
	}
}

func TestEveIsNamedAndStaysUpLateIsAllowed(t *testing.T) {
	eve := DayInfoAt(at(2026, time.February, 16))
	if eve.Eve != "除夕" {
		t.Fatalf("eve = %q, want 除夕", eve.Eve)
	}
	if !strings.Contains(eve.Describe(), "除夕") {
		t.Fatalf("describe = %q, want 除夕 in it", eve.Describe())
	}
	newYear := DayInfoAt(at(2026, time.December, 31))
	if newYear.Eve != "跨年夜" {
		t.Fatalf("Dec 31 2026 eve = %q, want 跨年夜", newYear.Eve)
	}
}

func TestUnannouncedYearFallsBackToPlainWeekday(t *testing.T) {
	// 2028 has no published notice. The table must not invent one.
	tue := DayInfoAt(at(2028, time.October, 3))
	if tue.Announced {
		t.Fatal("2028 is not in the table")
	}
	if tue.Kind != DayWorkday {
		t.Fatalf("Oct 3 2028 = %s, want the plain weekday reading", tue.Kind)
	}
	sun := DayInfoAt(at(2028, time.October, 1))
	if sun.Kind != DayWeekend {
		t.Fatalf("Oct 1 2028 = %s, want weekend", sun.Kind)
	}
}

func TestUnannouncedYearWarnsOnlyOnce(t *testing.T) {
	cal, err := LoadCalendar()
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	cal.SetLogger(func(string, ...any) { calls++ })
	for _, day := range []time.Time{at(2029, time.March, 5), at(2029, time.March, 6), at(2029, time.April, 1)} {
		cal.DayInfo(day)
	}
	if calls != 1 {
		t.Fatalf("warnings = %d, want exactly one per year", calls)
	}
	// A year that is in the table never warns.
	cal.DayInfo(at(2026, time.October, 1))
	if calls != 1 {
		t.Fatalf("announced year raised a warning, total %d", calls)
	}
}

func TestEmbeddedCalendarTableIsValid(t *testing.T) {
	if _, err := LoadCalendar(); err != nil {
		t.Fatalf("embedded holiday table is broken: %v", err)
	}
}

func TestCalendarRejectsSelfInconsistentTables(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "makeup on a weekday",
			yaml: `version: 1
years:
  "2031":
    makeup:
      - "2031-03-05"
`,
		},
		{
			name: "makeup inside a holiday",
			yaml: `version: 1
years:
  "2031":
    holidays:
      - { name: 测试节, start: "2031-03-01", end: "2031-03-08" }
    makeup:
      - "2031-03-02"
`,
		},
		{
			name: "range ends before it starts",
			yaml: `version: 1
years:
  "2031":
    holidays:
      - { name: 倒着来, start: "2031-03-08", end: "2031-03-01" }
`,
		},
		{
			name: "two holidays overlap",
			yaml: `version: 1
years:
  "2031":
    holidays:
      - { name: 甲节, start: "2031-03-01", end: "2031-03-06" }
      - { name: 乙节, start: "2031-03-05", end: "2031-03-09" }
`,
		},
		{
			name: "holiday outside its own year",
			yaml: `version: 1
years:
  "2031":
    holidays:
      - { name: 串门, start: "2032-01-01", end: "2032-01-03" }
`,
		},
		{
			name: "wrong version",
			yaml: `version: 7
years: {}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseCalendar([]byte(tc.yaml)); err == nil {
				t.Fatalf("%s: expected an error, got none", tc.name)
			}
		})
	}
}

func TestSleepShiftFollowsTheDayType(t *testing.T) {
	cal, err := LoadCalendar()
	if err != nil {
		t.Fatal(err)
	}
	firstWake, firstSleep := cal.SleepShift(DayInfo{Kind: DayHoliday, DayIndex: 1, TotalDays: 7})
	laterWake, laterSleep := cal.SleepShift(DayInfo{Kind: DayHoliday, DayIndex: 5, TotalDays: 7})
	if firstWake <= laterWake {
		t.Fatalf("first holiday day wakes at +%d, later at +%d; the first day should be the sleepiest", firstWake, laterWake)
	}
	if firstSleep <= laterSleep {
		t.Fatalf("first holiday day bedtime shift +%d, later +%d; day one should stay up latest", firstSleep, laterSleep)
	}
	weekendWake, _ := cal.SleepShift(DayInfo{Kind: DayWeekend})
	if weekendWake <= 0 {
		t.Fatal("weekends should let her sleep in")
	}
	makeupWake, _ := cal.SleepShift(DayInfo{Kind: DayMakeup})
	if makeupWake >= 0 {
		t.Fatalf("调休 shift = %d, want negative: the alarm is crueler, not kinder", makeupWake)
	}
	eveWake, eveSleep := cal.SleepShift(DayInfo{Kind: DayWorkday, Eve: "除夕"})
	if eveSleep <= 0 {
		t.Fatal("除夕 should push bedtime back")
	}
	if eveWake <= 0 {
		t.Fatal("除夕 should also soften the alarm")
	}
}

func TestCalendarMoodTargetsAreRealMetrics(t *testing.T) {
	rules, err := LoadRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	cal, err := LoadCalendar()
	if err != nil {
		t.Fatal(err)
	}
	for group, list := range cal.mood {
		for _, rule := range list {
			if _, ok := rules.Metrics[rule.Target]; !ok {
				t.Errorf("mood group %q targets unknown metric %q", group, rule.Target)
			}
			if rule.Duration.Std() <= 0 {
				t.Errorf("mood group %q target %s has no duration", group, rule.Target)
			}
		}
	}
}

func TestHolidayMoodDoesNotStackAcrossTicks(t *testing.T) {
	day := at(2026, time.October, 3)
	var effects []ActiveEffect
	for i := 0; i < 8; i++ {
		effects = mergeEffects(effects, MoodEffectsAt(day.Add(time.Duration(i)*15*time.Minute)))
	}
	if len(effects) != 2 {
		t.Fatalf("effects after 8 ticks = %d, want the two holiday effects refreshed not stacked", len(effects))
	}
	for _, effect := range effects {
		if !strings.HasPrefix(effect.SourceRuleID, "calendar:2026-10-03:holiday") {
			t.Fatalf("unexpected effect source %q", effect.SourceRuleID)
		}
	}
}

func TestMakeupAndHolidayLastDayMood(t *testing.T) {
	makeup := MoodEffectsAt(at(2026, time.October, 10))
	if len(makeup) != 3 {
		t.Fatalf("makeup effects = %d, want irritability, motivation and stress", len(makeup))
	}
	lastDay := MoodEffectsAt(at(2026, time.October, 7))
	if len(lastDay) != 4 {
		t.Fatalf("holiday last day effects = %d, want the holiday lift plus the tomorrow-is-work dread", len(lastDay))
	}
	if plain := MoodEffectsAt(at(2026, time.September, 28)); len(plain) != 0 {
		t.Fatalf("ordinary workday produced %d mood effects, want none", len(plain))
	}
}

func TestCalendarStripGivesTheModelSomethingToResolveAgainst(t *testing.T) {
	strip := CalendarStrip(at(2026, time.February, 14), 10)
	if !strings.HasPrefix(strip, "今天：2026-02-14") {
		t.Fatalf("strip should start at today: %q", strip)
	}
	// 除夕 has to be named, otherwise 初六 cannot be counted out.
	if !strings.Contains(strip, "除夕") {
		t.Fatal("strip must name 除夕 so relative lunar dates can be counted")
	}
	if !strings.Contains(strip, "上班（调休补班）") {
		t.Fatal("strip must show the makeup day as work")
	}
	if lines := strings.Split(strings.TrimRight(strip, "\n"), "\n"); len(lines) != 10 {
		t.Fatalf("strip has %d lines, want 10", len(lines))
	}
}

func TestWorldClockCarriesTheCalendar(t *testing.T) {
	clock := worldClock(at(2026, time.October, 3))
	if !clock.IsHoliday || clock.Holiday != "国庆节" {
		t.Fatalf("clock = %+v, want 国庆节", clock)
	}
	if clock.HolidayProgress != "第3天共7天" {
		t.Fatalf("progress = %q", clock.HolidayProgress)
	}
	if !clock.IsRestDay {
		t.Fatal("holiday is a rest day")
	}

	makeup := worldClock(at(2026, time.October, 10))
	if !makeup.IsMakeupWorkday || makeup.IsRestDay {
		t.Fatalf("makeup clock = %+v, want a working Saturday", makeup)
	}
	// The literal weekend fact stays true on purpose; it is not the rest answer.
	if !makeup.IsWeekend {
		t.Fatal("IsWeekend is the literal Saturday fact and should stay true")
	}
}

func TestModelContextSaysWhatDayItIs(t *testing.T) {
	holidayNow := time.Date(2026, time.October, 3, 9, 0, 0, 0, time.Local)
	block := formatContextBlock(
		DefaultProfile(1), DefaultState(holidayNow.UnixMilli()), DerivedState{},
		nil, nil, nil, nil, WakeSummary{Clock: worldClock(holidayNow)},
	)
	if !strings.Contains(block, "国庆节") || !strings.Contains(block, "第3天共7天") {
		t.Fatalf("the model context lost the holiday: %s", block)
	}

	// An ordinary workday gets no calendar line at all.
	plainNow := time.Date(2026, time.September, 28, 9, 0, 0, 0, time.Local)
	plain := formatContextBlock(
		DefaultProfile(1), DefaultState(plainNow.UnixMilli()), DerivedState{},
		nil, nil, nil, nil, WakeSummary{Clock: worldClock(plainNow)},
	)
	if strings.Contains(plain, "调休") || strings.Contains(plain, "假期") {
		t.Fatal("an ordinary workday should not mention the calendar")
	}
}
