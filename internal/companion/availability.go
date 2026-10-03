package companion

import (
	"fmt"
	"time"
)

// AvailabilityReason 给「是什么把她占住了」一个名字。以前只有一个 bool，
// 于是「她在忙」和「该弹忙线卡」变成同一件事，卡片只能靠猜。
type AvailabilityReason string

const (
	// ReasonScene 是她正和他处在一段场景里。这是她最可达的时刻，不是最远的。
	ReasonScene AvailabilityReason = "scene"
	// ReasonAsleep 是生理事实，唯一真正把消息关在门外的东西。
	ReasonAsleep AvailabilityReason = "asleep"
	// ReasonBed 是睡块里但还没睡过去，那是刷手机时间。
	ReasonBed AvailabilityReason = "bed"
	// ReasonLight 是轻到根本拦不住她的块。
	ReasonLight AvailabilityReason = "light"
	// ReasonStalling 是她还没开始的重块 —— 而那正是找得到她的时侯。
	ReasonStalling AvailabilityReason = "stalling"
	// ReasonHeavy 是她真正身处其中的重块。
	ReasonHeavy AvailabilityReason = "heavy"
)

// ReplyShape 是此刻一条回复在形态上的天花板。只封顶，不代笔：她永远可以比
// 上限更短，不能更长。场景正酣时憋出六百字长文比晚回更出戏，这里是唯一
// 拦住它的地方。
type ReplyShape struct {
	MaxChars  int
	Delivery  string // both | action_only | speech_only | either
	ToneFloor string
}

// Availability 一次回答三个以前被焊在一个 (bool, string, int64) 里的问题：
//
//	Q1 可达性  Reachable / Blocked -- 消息到底收不收得到
//	Q2 节奏    Delay / NextFree    -- 她会隔多久回
//	Q3 形态    Shape               -- 能回多长、以什么交付形式
//
// 拆开是重点。原来的 tuple 让「她被事情占着」和「该显示忙线卡」变成同一个
// 事实，于是一条其实可用的任务只要顺手带了个活动字符串，就能在她躺在床上
// 场景进行中、手机就在手里的时候，渲染成「她正在忙，暂时没看手机 · 亲密中」。
type Availability struct {
	// Reachable 是消息能不能送到她眼前。只有睡眠关得上这道门。
	Reachable bool
	// Blocked 是可达性的显示面：她被事情拽离了手机，这条消息要等。
	Blocked bool
	// Activity 是此刻该显示她在做什么。
	Activity string
	// Reason 命名是什么在拦她。
	Reason AvailabilityReason
	// Delay 是从 at 起她会拖多久才回。
	Delay time.Duration
	// NextFree 是拦截何时结束。没被拦时就是 at 本身。
	NextFree time.Time
	// Shape 是此刻回复形态的上界。
	Shape ReplyShape
}

// evaluate 是三个问题的唯一真相源。taskAvailability 与 earlyRelease 都只是
// 读它一份结果的投影，这样两条规则不可能各自漂移。
func evaluate(state State, resolve planResolver, at time.Time) Availability {
	plan := resolve(at)

	// 睡眠排在场景之前。I6 说「睡着会清掉场景」，但那只守住了 tick 那一侧的
	// settleConsciousness；applyTurnBrief 仍然能在她睡着时写进一个 SceneStage，
	// 于是可用性读到「场景活着」就放行，而 CanForceReply 读意识说「不行」。
	// 屏幕上的组合就成了：头部显示睡眠中、忙线卡不出、强制按钮灰着，但新消息
	// 她照样回。生理事实排在叙事前面，这里和源头都要守。
	if consciousnessAt(state, plan, at) == ConsciousAsleep {
		wake := nextWakeAt(state, plan, resolve, at)
		return Availability{
			Blocked:  true,
			Activity: "她在睡觉",
			Reason:   ReasonAsleep,
			Delay:    wake.Sub(at),
			NextFree: wake,
		}
	}

	// 场景还活着，人就在这段对话里，日程表上写什么都拦不住回消息。
	if sceneLive(state, at) {
		return Availability{
			Reachable: true,
			Activity:  displayActivity(state),
			Reason:    ReasonScene,
			NextFree:  at,
			Shape:     sceneShape(state.SceneStage),
		}
	}

	block := plan.blockAt(at)

	// 躺床上还没睡着就是刷手机时间。睡眠块权重再高也拦不住，因为她人还醒着，
	// 走下面的通用规则会被「已开始的重量级块」扣住，等于把失眠当成昏迷。
	if isSleepBlock(block) && block.StartedAt > 0 {
		return Availability{
			Reachable: true,
			Activity:  "躺床上还没睡着",
			Reason:    ReasonBed,
			NextFree:  at,
			Shape:     ReplyShape{MaxChars: 400, Delivery: "either", ToneFloor: "松弛，带着点上床之后的困意"},
		}
	}

	// 轻块从不拦人；重块只有真的开始了才拦。未开始的重块是她在拖，
	// 而那正是找得到她的时侯。
	if block.Weight <= replyWeightCeiling {
		return Availability{
			Reachable: true,
			// 活动名走 activityLabel，和 tick 写进 state.CurrentActivity 的是同一个
			// 函数。头部和可用性各说一个字符串，正是上一轮刚治过的那类病换了个
			// 字段又长回来。
			Activity: activityLabel(block),
			Reason:   ReasonLight,
			NextFree: at,
			Shape:    ReplyShape{MaxChars: 800, Delivery: "either", ToneFloor: "自然"},
		}
	}
	if block.StartedAt == 0 {
		return Availability{
			Reachable: true,
			Activity:  activityLabel(block),
			Reason:    ReasonStalling,
			NextFree:  at,
			Shape:     ReplyShape{MaxChars: 400, Delivery: "either", ToneFloor: "有点心虚，知道自己该做事"},
		}
	}

	// 她真的被一件重事占住了。
	label := fmt.Sprintf("%s，暂时不能回复", block.Label)
	next := at.Add(time.Hour)
	if found, ok := nextFreeStart(plan, block.EndMinute, at); ok {
		next = time.UnixMilli(found)
	} else {
		tomorrow := at.AddDate(0, 0, 1)
		if found, ok := nextFreeStart(resolve(tomorrow), 0, tomorrow); ok {
			next = time.UnixMilli(found)
		}
	}
	return Availability{
		Blocked:  true,
		Activity: label,
		Reason:   ReasonHeavy,
		Delay:    next.Sub(at),
		NextFree: next,
		Shape:    ReplyShape{MaxChars: 200, Delivery: "speech_only", ToneFloor: "简短，正被事情打断"},
	}
}

// admitNew 回答「刚到的这条怎么办」。宽松是故意的：她还没开始的重块等于她在拖，
// 而那正是能找着她的时侯。
func (a Availability) admitNew() bool { return !a.Blocked }

// releaseEarly 回答「已经承诺过晚点回的那条，能不能提前放出来」。严格，
// 而且刻意比 admitNew 窄 —— 「未开始的重块」这条出口故意不留，因为它正是
// 把「17 点回」变成 12 点一进那格没开始的午饭就回话的东西。
// 不守约的她比晚一点守约的她更糟。
func releaseEarly(a Availability) (bool, string) {
	switch a.Reason {
	case ReasonScene, ReasonLight, ReasonBed:
		return true, a.Activity
	default:
		return false, ""
	}
}

// sceneShape 把场景阶段翻译成回复形态。场景越紧，字越少。
func sceneShape(stage string) ReplyShape {
	switch stage {
	case "active", "climax":
		return ReplyShape{MaxChars: 120, Delivery: "both", ToneFloor: "急促、碎片化，喘不出完整的句子"}
	case "aftercare":
		return ReplyShape{MaxChars: 240, Delivery: "both", ToneFloor: "软、慵懒、黏人"}
	}
	return ReplyShape{MaxChars: 800, Delivery: "either", ToneFloor: "自然"}
}

// clampShape 把模型给的回复长度压到身体状态允许的上界之内。只做上界：
// 她想更短随时可以，更长不行。语气仍然由模型自己决定，这里不代笔。
func clampShape(shape ReplyShape, maxChars int) int {
	if shape.MaxChars > 0 && maxChars > shape.MaxChars {
		return shape.MaxChars
	}
	return maxChars
}
