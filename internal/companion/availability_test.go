package companion

import (
	"testing"
	"time"
)

// 三个问题各自有归属，测试也按三个问题分。Q1 可达性、Q2 节奏、Q3 形态。

func liveSceneState(at time.Time, stage string) State {
	state := DefaultState(at.UnixMilli())
	state.SceneStage = stage
	state.ConsentState = "affirmed"
	state.SceneStartedAt = at.Add(-10 * time.Minute).UnixMilli()
	return state
}

// Q1: 场景进行中是可达性最高的状态。哪怕日程上一格重块已经开始并标了忙，
// 她也该立刻收得到 —— 这就是以前把「亲密中」演成「她在忙」的反面。
func TestLiveSceneIsTheMostReachableState(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 8},
	)
	plan.markStarted("b_0", at)
	resolve := func(time.Time) DayPlan { return plan }

	a := evaluate(liveSceneState(at, "active"), resolve, at)
	if !a.Reachable || a.Blocked {
		t.Fatalf("live scene must be reachable and unblocked, got reachable=%v blocked=%v reason=%s",
			a.Reachable, a.Blocked, a.Reason)
	}
	if a.Reason != ReasonScene {
		t.Fatalf("reason = %s, want %s", a.Reason, ReasonScene)
	}
	if a.Activity != "亲密中" {
		t.Fatalf("activity = %q, want 亲密中", a.Activity)
	}
}

// Q3: 场景越紧，回复越短。active 的天花板必须比 aftercare 低，
// 而两者都必须远低于空块的 800，否则封顶等于没封。
func TestSceneShapeShrinksWithIntensity(t *testing.T) {
	active := sceneShape("active").MaxChars
	climax := sceneShape("climax").MaxChars
	after := sceneShape("aftercare").MaxChars
	open := sceneShape("none").MaxChars

	if active != climax {
		t.Fatalf("active(%d) and climax(%d) should share a ceiling", active, climax)
	}
	if !(active < after && after < open) {
		t.Fatalf("ceilings must tighten with intensity: active=%d aftercare=%d open=%d", active, after, open)
	}
}

func TestClampShapeOnlyCapsAndNeverReplaces(t *testing.T) {
	shape := ReplyShape{MaxChars: 120}
	if got := clampShape(shape, 800); got != 120 {
		t.Fatalf("800 under a 120 ceiling should cap at 120, got %d", got)
	}
	if got := clampShape(shape, 60); got != 60 {
		t.Fatalf("shorter than the ceiling must pass through untouched, got %d", got)
	}
	// 没有上界时完全不干预，模型说了算。
	if got := clampShape(ReplyShape{}, 5000); got != 5000 {
		t.Fatalf("empty shape must not clamp, got %d", got)
	}
}

// Q1 + Q2: 睡着是唯一关死门的理由，而且它得说清什么时候开。
func TestAsleepBlocksAndNamesHowLong(t *testing.T) {
	at := time.Date(2030, 9, 30, 23, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-40*time.Minute))
	resolve := func(time.Time) DayPlan { return plan }

	a := evaluate(DefaultState(at.UnixMilli()), resolve, at)
	if a.Reachable || !a.Blocked {
		t.Fatalf("asleep must be unreachable, got reachable=%v blocked=%v", a.Reachable, a.Blocked)
	}
	if a.Reason != ReasonAsleep {
		t.Fatalf("reason = %s, want %s", a.Reason, ReasonAsleep)
	}
	if a.Delay <= 0 {
		t.Fatalf("delay must name a real wait, got %v", a.Delay)
	}
	// NextFree - at 必须正好等于 Delay，否则卡片上的 ETA 和实际放行时刻会错开。
	if a.NextFree.Sub(at) != a.Delay {
		t.Fatalf("NextFree(%v) - at != Delay(%v)", a.NextFree, a.Delay)
	}
}

// 躺下但没睡过去是刷手机时间：冷消息收得到，已停车的消息也该提前放出来。
func TestBedIsReachableAndReleasesEarly(t *testing.T) {
	at := time.Date(2030, 9, 30, 22, 30, 0, 0, time.Local)
	plan := nightPlan(t, at.Add(-3*time.Minute))
	resolve := func(time.Time) DayPlan { return plan }

	a := evaluate(DefaultState(at.UnixMilli()), resolve, at)
	if !a.Reachable || a.Blocked {
		t.Fatalf("awake in bed must be reachable, got reachable=%v blocked=%v", a.Reachable, a.Blocked)
	}
	if a.Reason != ReasonBed {
		t.Fatalf("reason = %s, want %s", a.Reason, ReasonBed)
	}
	if released, _ := releaseEarly(a); !released {
		t.Fatal("a parked message should come out while she is awake scrolling in bed")
	}
}

// 未开始的重块：冷消息收得到（她在拖），但已经承诺过晚点回的不许提前。
// 这条不对称是整个闸的核心，两个方向都得钉住。
func TestStallingReachesColdButNotEarly(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 8},
	)
	resolve := func(time.Time) DayPlan { return plan }

	a := evaluate(DefaultState(at.UnixMilli()), resolve, at)
	if !a.admitNew() {
		t.Fatalf("an unstarted heavy block should leave her reachable, reason=%s", a.Reason)
	}
	if a.Reason != ReasonStalling {
		t.Fatalf("reason = %s, want %s", a.Reason, ReasonStalling)
	}
	if released, _ := releaseEarly(a); released {
		t.Fatal("an unstarted block must not break a promise already made")
	}
}

// Q1 + Q2: 真被重事占住时，Blocked 为真且 NextFree 指向下一个靠得住的空档。
func TestHeavyStartedBlockHoldsAndPointsAtFreeTime(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "work", label: "上班", start: "09:00", end: "17:00", weight: 6},
		blockSpec{kind: "open", label: "晚上空着", start: "17:00", end: "23:00", weight: 1},
	)
	plan.markStarted("b_0", at)
	resolve := func(time.Time) DayPlan { return plan }

	a := evaluate(DefaultState(at.UnixMilli()), resolve, at)
	if !a.Blocked {
		t.Fatal("a started heavy block must block")
	}
	if a.Reason != ReasonHeavy {
		t.Fatalf("reason = %s, want %s", a.Reason, ReasonHeavy)
	}
	want := time.Date(2026, 9, 28, 17, 0, 0, 0, time.Local)
	if !a.NextFree.Equal(want) {
		t.Fatalf("next free = %v, want %v", a.NextFree, want)
	}
	if released, _ := releaseEarly(a); released {
		t.Fatal("a heavy started block must not release early")
	}
}

// 可达的时候 NextFree 就是此刻，这样 NotBefore 落在现在，任务不会被莫名推迟。
func TestReachableAvailabilityPointsAtNow(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	plan := mustPlan(t,
		blockSpec{kind: "open", label: "上午空着", start: "09:00", end: "12:00", weight: 1},
	)
	resolve := func(time.Time) DayPlan { return plan }

	a := evaluate(DefaultState(at.UnixMilli()), resolve, at)
	if a.Blocked {
		t.Fatal("a light block must not block")
	}
	if !a.NextFree.Equal(at) {
		t.Fatalf("NextFree = %v, want exactly at (%v)", a.NextFree, at)
	}
}

// TestAvailabilitySaysTheSameActivityTheTickWrites pins P5. The header reads
// state.CurrentActivity, which the tick sets through activityLabel, while
// availability handed back block.Label for the very same moment. Two surfaces
// disagreeing about what she is doing is the exact disease we just spent a round
// killing; one question gets one function.
func TestAvailabilitySaysTheSameActivityTheTickWrites(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		weight int
	}{
		{"stalling heavy work", "work", 8},
		{"unstarted light errand", "errand", 2},
	}
	for _, tc := range cases {
		at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
		plan := mustPlan(t, blockSpec{kind: tc.kind, label: "约好的事", start: "09:00", end: "17:00", weight: tc.weight})
		a := evaluate(DefaultState(at.UnixMilli()), func(time.Time) DayPlan { return plan }, at)
		want := activityLabel(plan.blockAt(at))
		if a.Activity != want {
			t.Fatalf("%s: availability says %q while the tick writes %q", tc.name, a.Activity, want)
		}
	}
}
