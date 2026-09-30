package companion

import (
	"fmt"
	"hash/fnv"
	"time"
)

// scheduleBlock is one bounded activity segment in her day.
type scheduleBlock struct {
	Kind        string
	Label       string
	StartMinute int
	EndMinute   int
}

func scheduleBlockFor(profile Profile, at time.Time) scheduleBlock {
	minute := at.Hour()*60 + at.Minute()
	blocks := scheduleBlocksForDay(profile, at)
	for _, block := range blocks {
		if minute >= block.StartMinute && minute < block.EndMinute {
			return block
		}
	}
	return blocks[len(blocks)-1]
}

func scheduleBlocksForDay(profile Profile, at time.Time) []scheduleBlock {
	key := at.Format("2006-01-02")
	shift := jitter(profile.ID, key, "day-shift", -20, 20)
	weekend := at.Weekday() == time.Saturday || at.Weekday() == time.Sunday
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
