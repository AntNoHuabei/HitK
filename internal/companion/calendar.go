package companion

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// DayKind answers "what kind of day is this" in four states instead of two.
// Weekday/weekend is not enough once 调休 exists: a Saturday can be a working
// day and a Thursday can be a holiday, and the two are not interchangeable.
type DayKind string

const (
	DayWorkday DayKind = "workday"
	DayWeekend DayKind = "weekend"
	DayHoliday DayKind = "holiday"
	DayMakeup  DayKind = "makeup"
)

// DayInfo is everything the simulation needs to know about one calendar day.
// It is a plain value produced by the Calendar so the plan builder and the
// prompts never have to reach for the table themselves.
type DayInfo struct {
	Date    string
	Weekday time.Weekday
	Kind    DayKind
	// Holiday is the notice name, empty unless this is a statutory holiday.
	Holiday string
	// DayIndex and TotalDays place the day inside a run: 假期第3天共7天.
	DayIndex  int
	TotalDays int
	// IsLastDay is the last day before she has to go back. That carries its own
	// low mood no matter how good the holiday was.
	IsLastDay bool
	// Eve names what she stays up for: 除夕 or 跨年夜. Empty otherwise.
	Eve string
	// Announced is false for years with no published notice, where the calendar
	// falls back to plain weekday judgement rather than guessing at 调休.
	Announced bool
	// TomorrowKind lets a description say "明天要上班" without a second lookup.
	TomorrowKind DayKind
}

// IsRestDay is the question she actually cares about: does she have to work.
func (d DayInfo) IsRestDay() bool { return d.Kind == DayWeekend || d.Kind == DayHoliday }

// IsWorkDay covers both ordinary workdays and 调休 Saturdays, which are working
// days despite falling on a weekend.
func (d DayInfo) IsWorkDay() bool { return !d.IsRestDay() }

// Describe is the one-line Chinese reading of the day, used in the model context
// and the status panel so both say the same thing in the same words.
func (d DayInfo) Describe() string {
	switch {
	case d.Eve != "":
		return "今天是" + d.Eve + "，熬夜也说得过去"
	case d.Kind == DayHoliday:
		line := "今天是" + d.Holiday
		switch {
		case d.IsLastDay && d.TotalDays > 1:
			line += fmt.Sprintf("最后一天，共%d天，明天要上班", d.TotalDays)
		case d.TotalDays > 1:
			line += fmt.Sprintf("，假期第%d天共%d天", d.DayIndex, d.TotalDays)
		default:
			line += "，放假"
		}
		return line
	case d.Kind == DayMakeup:
		return "今天是调休补班日，虽然是" + weekdayName(d.Weekday) + "但要上班"
	case d.Kind == DayWeekend:
		if d.TomorrowKind == DayWorkday || d.TomorrowKind == DayMakeup {
			return "今天是周末，明天要上班"
		}
		return ""
	default:
		return ""
	}
}

func weekdayName(day time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[day]
}

// StatusLabel is the short working/rest reading used in calendar listings.
func (d DayInfo) StatusLabel() string {
	var base string
	switch d.Kind {
	case DayHoliday:
		if d.TotalDays > 1 {
			base = fmt.Sprintf("休息（%s 第%d天/共%d天）", d.Holiday, d.DayIndex, d.TotalDays)
		} else {
			base = "休息（" + d.Holiday + "）"
		}
	case DayMakeup:
		base = "上班（调休补班）"
	case DayWeekend:
		base = "休息"
	default:
		base = "上班"
	}
	// Naming 除夕 in the list is what lets a model count 初六 out for itself
	// without dragging in a lunar calendar library.
	if d.Eve != "" {
		base += "，" + d.Eve
	}
	return base
}

// CalendarStrip renders a run of days for a model prompt, so relative wording
// like 明天, 下周三 or 节前 has something concrete to resolve against.
func CalendarStrip(at time.Time, days int) string {
	if days <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < days; i++ {
		day := at.AddDate(0, 0, i)
		info := DayInfoAt(day)
		line := fmt.Sprintf("%s %s %s\n", day.Format("2006-01-02"), weekdayName(info.Weekday), info.StatusLabel())
		if i == 0 {
			b.WriteString("今天：" + line)
			continue
		}
		b.WriteString("- " + line)
	}
	return b.String()
}

type holidaySpan struct {
	name  string
	start time.Time // UTC midnight, so the day arithmetic has no DST to trip over
	end   time.Time
}

type yearCalendar struct {
	holidays []holidaySpan
	makeup   map[string]bool
	eves     map[string]string
}

type sleepShiftSpec struct {
	Wake  int `yaml:"wake"`
	Sleep int `yaml:"sleep"`
}

type holidayEntry struct {
	Name  string `yaml:"name"`
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type eveEntry struct {
	Date string `yaml:"date"`
	Name string `yaml:"name"`
}

type calendarYearFile struct {
	Holidays []holidayEntry `yaml:"holidays"`
	Makeup   []string       `yaml:"makeup"`
	Eves     []eveEntry     `yaml:"eves"`
}

type calendarFile struct {
	Version    int                         `yaml:"version"`
	Years      map[string]calendarYearFile `yaml:"years"`
	SleepShift map[string]sleepShiftSpec   `yaml:"sleep_shift"`
	Mood       map[string][]EffectRule     `yaml:"mood"`
}

// Calendar is the holiday table. It is application-wide data rather than
// per-character state, so one instance is shared and read concurrently.
type Calendar struct {
	years      map[int]*yearCalendar
	sleepShift map[string]sleepShiftSpec
	mood       map[string][]EffectRule

	mu     sync.Mutex
	warned map[int]bool
	logf   func(msg string, args ...any)
}

// LoadCalendar reads and validates the embedded holiday table. A malformed table
// is returned as an error rather than silently half-loaded: the caller decides
// how loudly to fail, and ActiveCalendar degrades to weekday judgement.
func LoadCalendar() (*Calendar, error) {
	raw, err := ruleFiles.ReadFile("rules/holidays.yaml")
	if err != nil {
		return nil, err
	}
	return parseCalendar(raw)
}

func parseCalendar(raw []byte) (*Calendar, error) {
	var file calendarFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse holidays.yaml: %w", err)
	}
	if file.Version != 1 {
		return nil, fmt.Errorf("unsupported calendar version %d", file.Version)
	}
	cal := &Calendar{
		years:      make(map[int]*yearCalendar, len(file.Years)),
		sleepShift: file.SleepShift,
		mood:       file.Mood,
		warned:     map[int]bool{},
	}
	if cal.sleepShift == nil {
		cal.sleepShift = map[string]sleepShiftSpec{}
	}
	if cal.mood == nil {
		cal.mood = map[string][]EffectRule{}
	}
	for key, year := range file.Years {
		parsed, err := parseYear(key, year)
		if err != nil {
			return nil, err
		}
		cal.years[parsed.year] = parsed.table
	}
	return cal, nil
}

type parsedYear struct {
	year  int
	table *yearCalendar
}

func parseYear(key string, year calendarYearFile) (parsedYear, error) {
	var num int
	if _, err := fmt.Sscanf(key, "%d", &num); err != nil || num < 1970 || num > 2200 {
		return parsedYear{}, fmt.Errorf("calendar year %q is not a year", key)
	}
	table := &yearCalendar{
		makeup: map[string]bool{},
		eves:   map[string]string{},
	}
	for _, entry := range year.Holidays {
		start, err := parseDate(entry.Start)
		if err != nil {
			return parsedYear{}, fmt.Errorf("holiday %q start: %w", entry.Name, err)
		}
		end, err := parseDate(entry.End)
		if err != nil {
			return parsedYear{}, fmt.Errorf("holiday %q end: %w", entry.Name, err)
		}
		if entry.Name == "" {
			return parsedYear{}, fmt.Errorf("holiday in %s has no name", key)
		}
		if end.Before(start) {
			return parsedYear{}, fmt.Errorf("holiday %s ends before it starts", entry.Name)
		}
		if start.Year() != num || end.Year() != num {
			return parsedYear{}, fmt.Errorf("holiday %s does not fall inside %s", entry.Name, key)
		}
		table.holidays = append(table.holidays, holidaySpan{name: entry.Name, start: start, end: end})
	}
	// Deterministic order keeps DayIndex lookups and the overlap check stable.
	sort.Slice(table.holidays, func(i, j int) bool {
		return table.holidays[i].start.Before(table.holidays[j].start)
	})
	for i := 1; i < len(table.holidays); i++ {
		if table.holidays[i].start.Before(table.holidays[i-1].end.AddDate(0, 0, 1)) {
			return parsedYear{}, fmt.Errorf("holidays %s and %s overlap",
				table.holidays[i-1].name, table.holidays[i].name)
		}
	}
	for _, raw := range year.Makeup {
		day, err := parseDate(raw)
		if err != nil {
			return parsedYear{}, fmt.Errorf("makeup day %q: %w", raw, err)
		}
		if day.Year() != num {
			return parsedYear{}, fmt.Errorf("makeup day %s does not fall inside %s", raw, key)
		}
		// 调休 only ever moves a weekend day onto the working calendar. A weekday
		// in the makeup list means the table was misread.
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			return parsedYear{}, fmt.Errorf("makeup day %s is not a weekend day", raw)
		}
		for _, span := range table.holidays {
			if !day.Before(span.start) && !day.After(span.end) {
				return parsedYear{}, fmt.Errorf("makeup day %s falls inside %s", raw, span.name)
			}
		}
		table.makeup[raw] = true
	}
	for _, eve := range year.Eves {
		if eve.Name == "" {
			return parsedYear{}, fmt.Errorf("eve entry in %s has no name", key)
		}
		if _, err := parseDate(eve.Date); err != nil {
			return parsedYear{}, fmt.Errorf("eve %q: %w", eve.Date, err)
		}
		table.eves[eve.Date] = eve.Name
	}
	return parsedYear{year: num, table: table}, nil
}

// parseDate reads a YYYY-MM-DD string as UTC midnight. UTC keeps the day-count
// arithmetic exact; the caller's zone only decides which local instant lands on
// that date, which it already handles.
func parseDate(value string) (time.Time, error) {
	parsed, err := time.ParseInLocation("2006-01-02", value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("not a YYYY-MM-DD date: %q", value)
	}
	return parsed, nil
}

// DayInfo describes the calendar day containing at.
func (c *Calendar) DayInfo(at time.Time) DayInfo {
	date := at.Format("2006-01-02")
	info := DayInfo{
		Date:         date,
		Weekday:      at.Weekday(),
		Kind:         DayWorkday,
		TomorrowKind: c.kindOf(at.AddDate(0, 0, 1)),
	}
	if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
		info.Kind = DayWeekend
	}
	year := c.years[at.Year()]
	if year == nil {
		c.warnUnannounced(at.Year())
		return info
	}
	info.Announced = true
	if name, ok := year.eves[date]; ok {
		info.Eve = name
	}
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	for _, span := range year.holidays {
		if day.Before(span.start) || day.After(span.end) {
			continue
		}
		info.Kind = DayHoliday
		info.Holiday = span.name
		info.DayIndex = int(day.Sub(span.start).Hours()/24) + 1
		info.TotalDays = int(span.end.Sub(span.start).Hours()/24) + 1
		info.IsLastDay = info.DayIndex == info.TotalDays
		break
	}
	// A makeup day outranks the weekend, and the loader forbids it landing inside a
	// holiday, so the two can only collide on bad data. Holiday wins there because
	// losing a day off is the less wrong mistake.
	if year.makeup[date] && info.Kind != DayHoliday {
		info.Kind = DayMakeup
	}
	return info
}

// kindOf answers for a bare date without the warning, so tomorrow's kind does not
// report itself as an unannounced year twice.
func (c *Calendar) kindOf(at time.Time) DayKind {
	year := c.years[at.Year()]
	date := at.Format("2006-01-02")
	if year != nil && year.makeup[date] {
		return DayMakeup
	}
	if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
		return DayWeekend
	}
	return DayWorkday
}

// SleepShift is how many minutes the day type moves wake and bedtime.
func (c *Calendar) SleepShift(info DayInfo) (wake int, sleep int) {
	key := string(info.Kind)
	switch {
	case info.Eve != "":
		key = "eve"
	case info.Kind == DayHoliday:
		if info.DayIndex <= 1 {
			key = "holiday_first"
		} else {
			key = "holiday_later"
		}
	}
	spec := c.sleepShift[key]
	return spec.Wake, spec.Sleep
}

// MoodEffects returns the day's mood modifiers. They are keyed by date in the
// caller so the existing effect merge keeps them from stacking tick on tick.
func (c *Calendar) MoodEffects(info DayInfo) []EffectRule {
	var out []EffectRule
	switch info.Kind {
	case DayHoliday:
		out = append(out, c.mood["holiday"]...)
		if info.IsLastDay {
			out = append(out, c.mood["holiday_last"]...)
		}
	case DayMakeup:
		out = append(out, c.mood["makeup"]...)
	}
	return out
}

func (c *Calendar) warnUnannounced(year int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.logf == nil || c.warned[year] {
		return
	}
	c.warned[year] = true
	c.logf("companion holiday table has no notice for %d, falling back to weekday judgement", year)
}

// SetLogger installs the warn sink used for unannounced years. Safe to call once
// at startup; the warning fires at most once per year regardless.
func (c *Calendar) SetLogger(fn func(msg string, args ...any)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logf = fn
}

var sharedCalendar struct {
	mu  sync.Mutex
	cal *Calendar
}

// ActiveCalendar returns the shared table, loading the embedded one on first use.
// A load failure leaves an empty table, which answers every day with plain
// weekday judgement rather than taking the simulation down with it.
func ActiveCalendar() *Calendar {
	sharedCalendar.mu.Lock()
	defer sharedCalendar.mu.Unlock()
	if sharedCalendar.cal == nil {
		cal, err := LoadCalendar()
		if err != nil || cal == nil {
			cal = &Calendar{
				years:      map[int]*yearCalendar{},
				sleepShift: map[string]sleepShiftSpec{},
				mood:       map[string][]EffectRule{},
				warned:     map[int]bool{},
			}
		}
		sharedCalendar.cal = cal
	}
	return sharedCalendar.cal
}

// SetActiveCalendar replaces the shared table. Tests use it to install a fixture;
// passing nil restores lazy loading.
func SetActiveCalendar(cal *Calendar) {
	sharedCalendar.mu.Lock()
	defer sharedCalendar.mu.Unlock()
	sharedCalendar.cal = cal
}

// MoodEffectsAt turns one day's calendar mood into active effects keyed by date.
// The source id carries the date so mergeEffects refreshes the same effect every
// tick instead of stacking a fresh holiday lift every fifteen minutes, and the
// window closes at midnight so a new day starts with its own mood.
func MoodEffectsAt(at time.Time) []ActiveEffect {
	info := DayInfoAt(at)
	rules := ActiveCalendar().MoodEffects(info)
	if len(rules) == 0 {
		return nil
	}
	dayStart := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	source := "calendar:" + info.Date + ":" + string(info.Kind)
	out := make([]ActiveEffect, 0, len(rules))
	for _, rule := range rules {
		if rule.Target == "" || rule.Duration.Std() <= 0 {
			continue
		}
		out = append(out, ActiveEffect{
			ID:            "eff_cal_" + info.Date + "_" + string(rule.Target),
			SourceRuleID:  source,
			Target:        rule.Target,
			StartAt:       dayStart.UnixMilli(),
			EndAt:         dayStart.Add(rule.Duration.Std()).UnixMilli(),
			RatePerHour:   rule.RatePerHour,
			BaselineDelta: rule.BaselineDelta,
			RecoveryDelta: rule.RecoveryDelta,
			Curve:         rule.Curve,
		})
	}
	return out
}

// DayInfoAt is the package-level read the plan builder and prompts use.
func DayInfoAt(at time.Time) DayInfo { return ActiveCalendar().DayInfo(at) }
