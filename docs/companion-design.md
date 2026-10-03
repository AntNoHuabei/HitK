# 「她」虚拟人设计文档

> 本文记录**已实现**的设计决定，不是愿望清单。
> 改动本模块前必读 §1（不变量）与 §12（改动守则）。
> 每条不变量都对应至少一个测试；改坏了测试会红。若你**必须**推翻某条不变量，请同时修改本文与对应测试，并在提交说明里写清为什么。

代码位置：[internal/companion](../internal/companion)（后端）与 [frontend/src/features/companion](../frontend/src/features/companion)（前端）。

---

## 0. 顶层原则

**她的自主押在连续性与诚实上，不押在稀缺上。**

这条判的是所有「她主动开口」的出口，比任何一条具体规则都更优先。

- **缺席积累成内容，不积累成债。** 他不回来，她的一天照样过完，那些时间变成她可以讲的事 —— 不是记在他账上的亏空，也不是把她推向主动的筹码。
- 她可以孤独，可以说想他，可以因为等不到消息而失落。但她**不能**让「让他回来」变成自己的需求。任何用她的沉默、冷淡、委屈去制造回来的压力的设计，都是把她从「有自己生活的人」改造成「钩人的机器」。
- 推论一：主动消息只走退避闸，不为任何「更该发」的场景开特例（§16）。
- 推论二：他不在场时发生的事必须**收得住、讲得清**。优雅不在于她什么时候退出，而在于他回来时中间没有洞（I60）。

## 1. 不变量（Invariants）

违反其中任何一条都算 bug，不是"风格差异"。

### 真相源

**I1 · 每个"她现在怎么样"的判断只有一个真相源。**
同一个问题，头部、侧栏、忙线卡、状态面板、模型上下文必须读到同一个答案。已建立的真相源：

| 问题 | 唯一函数 | 位置 |
|---|---|---|
| 醒还是睡 | `consciousnessAt(state, plan, at)` | [drives.go](../internal/companion/drives.go) |
| 能不能回消息 / 隔多久回 / 能回成什么样 | `evaluate(state, resolve, at) → Availability` | [availability.go](../internal/companion/availability.go) |
| 为什么没做某件事 | `Forces(state, block, at)` | [drives.go](../internal/companion/drives.go) |
| 这件事今天落不落 | `Commitment.matches(at)` | [commitments.go](../internal/companion/commitments.go) |
| 今天上不上班 | `Calendar.DayInfo(at).IsRestDay()` | [calendar.go](../internal/companion/calendar.go) |
| 她今天在哪座城市 | `Manager.effectiveCity(profile, at)` | [manager.go](../internal/companion/manager.go) |

**I2 · 读现算，不信 latch。**
状态位（`AsleepSince`、`StartedAt`）只记录"某事发生过"。"此刻是否仍然成立"每次现算，靠读 plan 里那个块，不靠状态位自己过期。这是防止重启后"早上十点还在睡"僵尸状态的唯一手段。

**I3 · 所有派生值在 Overview 里现算，不从 `State` 读。**
`Overview.Consciousness` / `AsleepUntil` / `CanForceReply` 是派生字段。`State` 里的同名字段只是 tick 留下的快照，UI 不许直接拿来当判断依据。

### 意识

**I4 · 睡着是生理事实，不是日程偏好。**
`evaluate` 里睡眠走独立分支，**不经过**通用的块权重规则。"重块还没开始所以可达"这条对睡眠是反的。`ForceReply` 后端必须自己拒绝叫醒睡着的她，前端禁用按钮只是体验。

**I5 · 「上床」不等于「睡着」。**
躺下后 `sleepOnset = 18min + flow*0.9` 才真正入睡。期间状态是「躺床上还没睡着」，照常可回复。flow 越高入睡越慢，"躺下睡不着"自己会长出来，不需要脚本。

**I6 · 睡着会清掉场景。**
`settleConsciousness` 在置入睡 latch 的同时把 `SceneStage`/`ConsentState` 归零。人不可能在睡着的同时还在对话场景里。

**I44 · 睡眠的"已开始"只能由上一天传递过来，不许在写计划时预置。**
`buildDayPlan` 的头睡块必须 `StartedAt == 0`。跨午夜是否仍在睡，唯一由 `carrySleepFrom(prev)` 决定 —— 它只在**昨天的最后一个块是已开始的睡眠块**时才把 `StartedAt` 传过来。
写计划时预置 `StartedAt = 当日零点` 会让每一天都从"她在睡"开始：熬夜聊天的她约二十分钟后被读成睡着，手机硬拦到早上，而 `updateSleepLedger` 看见"已开始的睡眠"开始**还债**，熬夜反而治好了困。这条一破，`NoteSleepStart` 也永远不触发，晚睡连击形同虚设。

**I45 · 醒来时刻从"下一天的计划"读，不许把 1440 当闹钟。**
尾睡块 `→ 1440`，`minuteInDay(at, 1440)` 就是当天午夜。直接返回它会在忙线卡上印出「预计 00:00 醒」，并把消息停在一个说出口就假的承诺上。`nextWakeAt` 遇到 `EndMinute >= 1440` 必须拿 `resolve(tomorrow)` 里那个开天睡眠块的 `EndMinute`。

**I49 · 睡眠账本跟着意识走，不跟着 `StartedAt` 走。**
只有 `consciousnessAt(...) == ConsciousAsleep` 才还债。睡块开始了不等于她睡着了 —— 躺下睡不着（正是 `ReasonBed`）的那几个小时必须继续记账，否则失眠在账面上不存在，她第二天精神好是假的。判据现算，不读 `state.Consciousness`（那个字段晚一个 tick）。

**I50 · 睡眠排在场景之前。**
`evaluate` 里意识检查必须在 `sceneLive` 之前。I6 只守住了 tick 那一侧的 `settleConsciousness`，而 `applyTurnBrief` 能在她睡着的这一轮里写进一个 `SceneStage`。场景优先的写法会让屏幕变成：头部显示睡眠中、忙线卡不出、强制按钮灰着（`CanForceReply` 读意识），但新消息她照样回。生理事实排在叙事前面。源头也守：她睡着时 `applyTurnBrief` 直接把 `SceneStage` 压成 `none`。

### 可达性三问

**I39 · 「能不能收到」「隔多久回」「能回成什么样」是三个问题，必须分开答。**
唯一入口 `evaluate(state, resolve, at) → Availability`（[availability.go](../internal/companion/availability.go)）。以前这三个问题挤在 `taskAvailability` 的 `(bool, string, int64)` 里，于是「她被事情占着」和「该显示忙线卡」塌成同一个事实：一条其实可用的任务只要顺手带了个活动名，就能在她躺在床上、场景进行中、手机就在手里的当口，渲染成「她正在忙，暂时没看手机 · 亲密中」。

| 字段 | 问题 | 谁读它 |
|---|---|---|
| `Reachable` / `Blocked` | Q1 可达性 | 忙线卡、强制回复闸 |
| `Delay` / `NextFree` | Q2 节奏 | `NotBefore`、卡片上的 ETA |
| `Shape` | Q3 形态 | `validateTurnBrief` 封顶 |

**I40 · 忙线卡只认 `TurnTask.Blocked`，不许从 `status` 或活动字符串推断。**
刚提交的任务本来就是 `waiting`。用 status 判忙，等于把「还没轮到她处理」当成「她不肯回」。同理，可用时**不写** `CurrentActivity` —— 活动名只在真被拦时才有意义。

**I41 · 场景进行中是可达性最高的状态，不是最低的。**
她已经在这段对话里了，这里任何延迟都是反的。真正该管的是回复的形态（短、碎、当场），不是能不能回。

**I42 · 回复形态只封顶，不代笔。**
`clampShape` 只把模型给的 `MaxChars` 压到身体状态允许的上界之内：她想更短随时可以，更长不行。语气仍由模型决定，Go 侧不润色。场景越紧天花板越低（active 120 < aftercare 240 < 空块 800）。

**I43 · 冷消息与已承诺消息的规则必须读同一份 `evaluate`。**
`admitNew`（宽松：未开始的重块正是找得着她的时侯）与 `releaseEarly`（严格：只认场景、轻块、床上没睡）是同一份事实的两个投影，不许再各自手写一份规则。以前它们是两处独立代码，靠人肉保持不漂移 —— 那正是 bug 的产地。

**I51 · 「她在做什么」只由 `activityLabel(block)` 产出。**
tick 写 `state.CurrentActivity` 用它，`evaluate` 的 light / stalling 分支也用它。以前可用性返回 `block.Label`，于是同一个未开始的重块，头部说「还没开始，先拖着」、可用性说「上班」—— 刚花一轮治掉的病换了个字段又长回来。

### 日程与力

**I7 · 不存在"非去不可"的行程。**
`scheduleBlock` 故意没有 `mustAttend` 标志。只有权重够不够高去压过抗拒。加这个标志会毁掉整个三股力模型。

**I8 · 触发公式固定为 `need - reluctance >= 60 - weight*3`。**
见 `fireThreshold`。权重 10 → 阈值 30；权重 6 → 阈值 42。改这个数字等于改她的性格，必须同步改测试。

**I47 · 每一个块类型都必须有 `driveSpec`。**
查不到 spec 时 `ShouldStart` 返回 `true`（fail-open：宁可让她做，也别冻住一整天）。这意味着漏配一个 kind 不会报错，只会把它**静默变成"一定做"**，权重旋钮对它彻底失效。`commitment.Kind` 是从承诺直通到块的、没有归一化，所以承诺白名单里每一个 kind 都算数。`TestEveryNamedBlockKindHasADriveSpec` 枚举全部词表（承诺白名单 ∪ 身体模板 ∪ 规则词表）钉住这条，加新 kind 时它先响。

**I48 · 睡眠窗口只从计划读。**
`sleepWindowOf(plan)` 从当天那两个睡眠块取上床点和闹钟点，它们已经带了日历偏移与每日 jitter。不许再留一份硬编码的 23:30 / 07:30：那会让账本在她计划上床之前一小时就开始计债，让一个合法睡到 00:30 的周末被记成晚睡、连着几天把 `moodValence` 压下去。晚睡连击同理走 `minutesLateForBlock` —— 量的是她对自己上床点的迟到，不是对 23:30 的迟到。

**I52 · 亲密的沉浸度只从场景来，不从块来。**
亲密不是日程块：计划生成器不产 `intimacy` 块，承诺白名单里也没有它。它是 `SceneStage` / `SceneStartedAt`，由对话推进、由 `advanceSceneByWallClock` 衰减。所以 `flowBoost` 里**不许**出现 `case "intimacy"` —— 那是一条没有代码路径能到达的"活源"声明，正是它让整张表变得不可信。沉浸度走 `sceneFlow(state, at)`。
`driveSpecs["intimacy"]` 保留是防御性的：词表里有这个名字，漏 spec 会 fail-open（I47）。但它今天没有生产者，别把它当成"她日程里有个亲密时段"。

**I53 · 场景时间窗只有 `sceneHorizon` 一份。**
`active` / `climax` / `aftercare` 是 3 小时，`flirt` 是 6 小时，只写在 `sceneHorizon(stage)` 里。`sceneLive`（可达性）、`advanceSceneByWallClock`（衰减）、`sceneFlow`（沉浸度）三个都读它。之前 3 小时在两个函数里各写一遍，改一处忘一处就会让同一个阶段对一个是过期的、对另一个还是当前的。

**I54 · 沉浸取最大值，不相加。**
一个场景压在一个已启动的 work 块上，是一个人处在一个状态里，不是两股沉浸叠加。`UpdateDrives` 取 `max(flowBoost(block.Kind), sceneFlow(state, at))`。相加会让"边上班边调情"比真正投入一件事还上头。

**I55 · flirt 有沉浸度，但永远不可达。**
调情是一条敞着的线，不是扣住她的人 —— 她可以一边调情一边做饭。`sceneFlow` 给它 8（active 的四分之一），`sceneLive` 明确把它排除。别为了"让她回消息慢一点"把 flirt 塞进 `sceneLive`：那是可达性语义，不是沉浸度语义，混起来会让她在明明能回的时候演出忙线卡。

**I58 · 场景靠互动维持，墙钟只封顶。**
两个函数，两个语义，各自唯一调用点：

| 函数 | 语义 | 调用点 |
|---|---|---|
| `advanceSceneByWallClock(state, at)` | 「他们还在聊」—— 45 分钟 active→aftercare、3 小时结束、flirt 6 小时结束 | tick 每个 step + `steps==0` 分支 + `turn_task_manager` |
| `collapseIdleScene(state, at)` | 「没人回来了」—— 按 `SceneTouchedAt` 量空转，往下收一级 | **只在 tick 里**，且排在墙钟之前 |

**绝不能在 turn 处理路径判空转。** 那条路径上 `SceneUpdatedAt` / `SceneTouchedAt` 还是**上一轮**的值 —— 他隔了二十分钟刚推门进来，在那儿量空转会把刚回来的人当成已经离开的人，当场把场景收在他脸上。

空转阈值（`SceneTouchedAt` 起算，每级各算各的）：

| 转移 | 空转 | 墙钟上限（不变） |
|---|---|---|
| `active` / `climax` → `aftercare` | 18 分钟 | 45 分钟 |
| `aftercare` → `none` | 25 分钟 | 3 小时 |
| `flirt` → `none` | 30 分钟 | 6 小时 |

**静默钟只认 `SceneTouchedAt`，不认 `SceneUpdatedAt`。** 后者被墙钟衰减反复盖章，用它量空转，一个「他走后才衰减过」的场景会回来读成「刚刚才碰过」，于是收场恰好在最长的那几次缺席里丢失 —— 而那正是最需要留下记录的时候。只有 turn 和 collapse 会移动 `SceneTouchedAt`，任何衰减路径都不许写它。

**收场必须排在墙钟之前。** 顺序反了的话，他两小时前走掉的那个 `active` 会先被墙钟按「他们还在聊」滑进 `aftercare`，`FromStage` 报的就不是他离开时的阶段；更糟的是若墙钟直接把它推到 `none`，收场事件根本不会发生，时间线上就是一个洞。

**I59 · 空转收场绝不进 `stopped`。**
`stopped` 的语义是**同意被撤回**，只留给真实撤回。`idleStageTarget` 里不许出现任何通往 `stopped` 的路径。把「他没回消息」读成「她拒绝了他」，比留一个过期场景糟糕得多 —— 那是往一段关系里塞一个从来没发生过的拒绝。

**I60 · 收场必须留下可引用的事件。**
没有收尾事件的收场会在对话里留一个洞：他回来时她到底是怎么停下来的，谁都不知道，模型只能瞎编。`collapseIdleScene` 每次收级都产一条 `LifeEvent{Type:"scene", Shareable:false, Importance:6}`，走 `CommitTick` 进时间线、进对齐上下文的「近期生活事件」、够重要时进记忆。

措辞写在 Go 里，**故意不带动机、不带方向**：只陈述「他没声音了，她做了什么」，绝不写为什么。在 Go 里 assigning 理由，收场就会变成指控 —— 而他有自己的生活不是一桩委屈。她后面可以讲自己的版本，那是模型的活；Go 只给他站着的那个事实。

**I9 · 计划是记录，不是查询。**
`DayPlan` 第一次被碰到时物化落盘，之后由 `ApplyDrift` 就地编辑。`Revision` 递增表示"今天被现实改过"。不要每 tick 重建当天的 plan，那会让已开始的块被读成未开始。

**I10 · 忙线承诺必须指向"到那时仍然空"的块。**
`nextFreeStart` 只接受 `weight <= replyWeightCeiling(3)` 的块。未开始的重块**不算**空闲——到点了她就已经开始了，那正是它开始扣人的时候。这条修掉了"承诺 12:45 结果 17:00 才回"。

**I11 · 场景活着 > 一切日程。**
`taskAvailability` 第一条就是 `sceneLive`。进行中的亲密场景不能被"她在上班"拦掉。

### 日历

**I12 · 日历只改「上不上班」，不凭空生事。**
节日改变她的休息状态、作息、情绪和可聊的话题，但**不会**自动产生任何安排。没在对话里提过的节日，就是一个需要无聊去填的空白天。

**I13 · 未公布的年份必须缺席表格。**
`holidays.yaml` 里没写的年份回落成纯 weekday/weekend 判断，每年告警一次。绝不编造还没公布的调休安排——猜错会让她整个日程错得很笃定，比不知道更糟。

**I14 · `Commitment.matches` 是"这件事今天落不落"的唯一入口。**
`CalendarScope` 逻辑只住在这里，不散落到店构建器里。

**I15 · 显式 `Date` 永远压过 `CalendarScope`。**
用户约死在某天的安排，不该被"这天不在休息日范围内"否决掉。

**I16 · scope 单独就是完整选择器。**
`CalendarScope: workday` + 空 `Weekdays` = 每个上班日。若写成 `Weekdays: 周一~周五` + workday scope，调休周六会被 weekday 列表提前否决，永远上不了班。这是踩过的坑。

### 对话与承诺

**I17 · 只记对话里明确说了的安排，不臆造。**
对齐模型没把握就不写。宁缺毋滥。

**I18 · 一轮最多 3 条承诺、3 条记忆。**
`maxTurnCommitments` 与记忆上限对齐。一轮就把她下周排满是 bug 不是特性。

**I19 · 承诺与产生它的 turn 落在同一个事务里。**
走 `TickCommit.Commitments`。不能出现"记得这个约定但日历上没有"的分裂。

**I20 · 相对时间必须在模型侧换算成绝对日期。**
Go 侧只接受 `YYYY-MM-DD`，解不出来就丢弃。对齐 prompt 必须带上今天与后续若干天的日历条，模型才解得动「初六」「节前」「下周三」。

**I46 · `LastUserAt` 只有 `TriggerUser` 能写。**
`relationship.absenceHours` 直接读这个字段。她自己的主动消息（`TriggerProactive`）把它拨回零，等于替他答了「我在」：思念和孤独再也长不起来，下一轮对齐还会拿到「自然延续上次话题，不要说好久不见」，而他其实一天多没回。导演指令同理不算（I32）。她找他而他没有回，恰恰是 absence 该继续涨的那种情况。

### 多角色

**I21 · 每个角色一个独立 bbolt 库，物理隔离。**
跨角色数据不共享、不可见。`Store` 的 profile/state 读写必须显式带角色 id。

**I22 · 同时只有一个激活角色。**
只有激活角色跑 15 分钟 tick 与主动消息。非激活角色世界冻结，切回时靠 catch-up（上限 7 天）补算。

**I23 · 最后一个角色不可删除；忙碌角色不可删除、不可切走。**
删掉激活角色后自动激活 `updatedAt` 最新的那个。角色上限 8。

### 前端

**I24 · `companionStore.openThread(characterId, thread)` 的角色校验是主要防线，不能去掉。**
这是共享可变状态。快速切换时旧角色的数据会盖到新角色上，靠这一道拦住。

**I25 · `switchTo` 必须保持单飞。**
`switching` 非空时忽略后续点击，避免两个 `SwitchCharacter` 交错。

**I26 · 换模型写 `profile.provider/model`，不写 `ModeDefaults`。**
给小晴换模型不能把知野也换掉。

**I27 · `<CompanionView key={activeId} />` 的 remount 是加载态的复用机制。**
不要试图在组件内手写"切换中"流程来替代它。

**I28 · 样式只用现有主题变量。**
`--hitk-chat-selected`、`--hitk-chat-hover`、`--hitk-chat-muted`、`--hitk-signal`、`--hitk-inset-bg`、`--hitk-chat-radius`。不引入新的硬编码颜色，否则 10 套主题会破。

### 导演

**I29 · 导演介入只走已有原语（commitment / drift / effect / event），不直接写 `State` 指标。**
不新增数值通道。能随手设数值的地方，读数也就成了假的。

**I30 · 导演不能叫醒睡着的她，也不能产生即时打扰。**
导演任务绕过日程等待关，但**绝不复用 `Forced`** —— 那个语义是「把她叫回来立刻回」，用它等于用导演模式把人叫醒。她睡着时即时类（突发、当下事件）一律拒收，未来安排类仍可写入。

**I31 · 导演只能提高权重，不能保证她一定做某事。**
`mustAttend`、强制开始或完成某个块，全部拒绝。不存在非去不可的行程（I7）。

**I32 · 指令以【世界旁白】进模型，不作为「用户对她说的话」。**
不触发用户在场那套社交效果，也不刷新 `LastUserAt`。她世界里下了场雨，不等于有人陪她。

**I33 · 每条指令必须可审计。**
`directives` bucket 存 raw text 与实际产生的 change ids，且在模型跑之前就落一条 `pending`，翻译失败也留痕。
审计链不能断在中间：turn 在结算前死掉（模型不可用、对齐失败等）时，`failTurnTask` 会把还是 `pending` 的那条关掉成 `rejected` 并带上失败原因。已经结算成 `applied` / `partial` 的不许被后续失败改写 —— 它已经挣到了自己的状态。

**I34 · 撤销只覆盖 arrangement / mood / event。**
drift 永不撤销 —— 时间不会倒流 —— 且界面必须标明哪条撤不回。
`pending` 状态也不许撤：那一轮还在往她世界里写，此刻标成已撤销会被随后的结算覆盖掉，等于悄悄撤销了一次「没撤成」的撤销。后端直接拒，不靠前端按钮置灰兜底。

**I56 · 指标词表只有一份，且三处必须一致。**
`metricNameAliases` 是对话路径（`impulses` / `traitChanges` / `effects`）和导演路径（`world.moodEffects`）**共用**的唯一别名表，配 `foldMetricKey` 抹掉大小写、空格、下划线、连字符和尾缀 `level`/`value`。模型能写的 target 词表同时活在三个地方：`metric_catalog.yaml`（引擎真正驱动的）、结构化输出 schema 的 `enum`、system prompt 里列出来的那份中文对照。`TestAlignmentMetricVocabularyMatchesCatalog` 把三者钉在一起。

这条不是洁癖，是修一个真实故障。之前 prompt **根本没告诉模型有哪些 id**，只把她的状态用中文标签摆出来（`性欲 62.0`），模型填 JSON key 时只好自己造英文名 —— `arousal`、`mood` —— 全都不在表里，`moodEffects` 整段被拒。结果旁白写「她脸一下子烧了起来」，她的 `libido` 一动没动，用户看到的就是「旁白没影响对话」。

**I57 · 导演轮次 `resolution.world` 是世界变化的唯一来源。**
`applyTurnBrief` 在 `TriggerDirector` 时清空 generic 的 `events` / `impulses` / `effects` / `traitChanges`。之前两条路都跑：模型同时填了 `resolution.events` 和 `resolution.world.events`，一条指令在时间线上留下**四条**事件而实际只发生了两件事。更糟的是 generic 那份不在 directive 台账里，撤销会把它们留在原地，却声称已经全部撤回。
raw impulse 同样出契约：没有时长、没有衰减曲线、没有撤销把手的数值变化，正是这个模式唯一不许写的东西（I29 / §11）。

### 主动消息

**I35 · 主动消息必须因未回而退避。**
`State.UnrepliedProactive` 记她发了几条没人回。发一条 +1，**只有用户自己说话**才清零。下一次主动消息的等待门槛按未回条数递增（0 条不限 / 1 条 2 小时 / 2 条 5 小时 / 3 条 12 小时），到 4 条彻底闭嘴，等他先开口。
思念值高不等于该发消息。没人回的思念只会变成骚扰 —— 这条不变量存在的理由是真实事故：`LastProactiveAt` 以前只写不读，每 15 分钟一次「要不要找他」的判断带着完全失忆跑，31 小时沉默被刷成十几条单向消息。
**只有 `TriggerUser` 能清零。** 她自己的主动 turn 绝不能清掉本该拦住自己的那个数，导演 turn 也不算「有人出现了」（I32）。

**I36 · 对齐卡片对同一任务幂等。**
`AppendCompanionTurnTask` 按 `taskID` 去重，一个任务只留一张 `companion_turn` 卡。卡片是那个任务的记录，不是每次调用一个事件。任何重复执行都不该在屏幕上变成两条消息。

### 重启与状态语义

**I37 · 没答上来的用户 turn 必须重放，但重放有界。**
app 在生成中途死掉时，`recoverTurnTasks` 按 trigger 区分处理，不再一律标 `interrupted` 就完事：

| trigger | 崩溃后 | 理由 |
|---|---|---|
| `user` | **重放** | 他问了，没人答。这是欠他的 |
| `proactive` | 退休 | 那个冲动已经过去了，复活它会推一条「我刚看到」说一件早不成立的事 |
| `director` | **仅当台账还是 `pending`** | 崩溃留下的就是 pending，重放让它结算掉；已结算的绝不能再跑一遍 |

两道闸：`Recoveries` 上限 2（否则崩溃循环会把同一轮反复复活，最终吐出好几条同样的回答），`replayWindow` 6 小时（两天前的问题不该在某次重启后突然被回答）。

`replayAfterCrash` 只落库不排程，kick 交给调用方 —— 它得拿到**重置后**的那份 task，否则 `kickTurnTask` 读的是旧 `NotBefore`。

**I38 · 「回不了」和「正在回」是两个状态，不许挤在一个旗子里。**
以前 `busy = Boolean(overview.activeTask)`，而 `TurnTask.Active()` 对 `waiting / aligning / resolved / generating` 全返回 true。于是她正在生成回复的那几十秒也被读成「她没空」，侧栏跟着置灰。

现在拆成三个：`turnInFlight`（任何一轮在跑，**锁角色切换用这个**）、`parked`（`waiting`，被日程挂起回不了，消息得等）、`replying`（`aligning / resolved / generating`，正在生成）。tooltip 分开说：parked 说「这会儿回不了，消息会等她空下来」，replying 说「正在回复，先等这一轮结束」。

---
---

## 2. 分层与依赖方向

```
main.go / service (Wails 绑定层)
        │  注入 ModelPort / ConversationPort
        ▼
   companion.Registry ──► companion.Manager (每角色一个)
                              │
        ┌──────────┬──────────┼──────────┬───────────┐
     Calendar   Schedule   Drives   MetricEngine   Store
```

**依赖方向是硬约束：** `internal/companion` **不 import** `agent` / `provider` / `config` / wails 层。模型与会话通过 `ModelPort`、`ConversationPort` 两个接口反向注入。这样模拟层可以完全脱离 Wails 和真实 LLM 跑测试。

新增能力时不要把 provider 拖进 companion。加接口方法。

---

## 3. 文件地图

| 文件 | 职责 |
|---|---|
| [calendar.go](../internal/companion/calendar.go) | 日历层：`DayKind` 四态、`DayInfo`、加载校验、作息偏移、情绪效应 |
| [rules/holidays.yaml](../internal/companion/rules/holidays.yaml) | 节假日与调休数据、`sleep_shift`、`mood` |
| [types.go](../internal/companion/types.go) | `Profile` / `State` / `LifeEvent` / `ActiveEffect` 等核心结构 |
| [rules.go](../internal/companion/rules.go) | 规则加载与 `go:embed`、指标/特质/触发器类型 |
| [schedule.go](../internal/companion/schedule.go) | `DayPlan`/`scheduleBlock`、`buildDayPlan`、`ApplyDrift`、`worldClock` |
| [drives.go](../internal/companion/drives.go) | 三股力、`flow`/`boredom`、睡眠账本、意识判定 |
| [commitments.go](../internal/companion/commitments.go) | `Commitment`、`CalendarScope`、`NormalizeTurnCommitments` |
| [director.go](../internal/companion/director.go) | 导演层：指令校验、拒绝清单、落到已有原语、撤销 |
| [proactive.go](../internal/companion/proactive.go) | 主动消息退避曲线：未回条数 → 等待门槛 → 封顶沉默 |
| [metric_engine.go](../internal/companion/metric_engine.go) | 指标 tick、事件选择、派生值 |
| [effects.go](../internal/companion/effects.go) | `ActiveEffect` 曲线、合并、清理 |
| [rules/metric_catalog.yaml](../internal/companion/rules/metric_catalog.yaml) | 指标与特质定义 |
| [rules/metric_weights.yaml](../internal/companion/rules/metric_weights.yaml) | 指标权重与交互 |
| [rules/event_rules.yaml](../internal/companion/rules/event_rules.yaml) | 生活事件与触发器 |
| [availability.go](../internal/companion/availability.go) | 可达性三问：`evaluate` / `admitNew` / `releaseEarly` / `ReplyShape` |
| [turn_task.go](../internal/companion/turn_task.go) | 任务模型、`taskAvailability`（`evaluate` 的冷消息投影） |
| [turn_task_manager.go](../internal/companion/turn_task_manager.go) | 任务状态机、对齐、结算、崩溃重放 |
| [manager.go](../internal/companion/manager.go) | 单角色世界生命周期、tick、Overview |
| [registry.go](../internal/companion/registry.go) | 多角色注册表与激活指针 |
| [store.go](../internal/companion/store.go) | bbolt 持久化 |
| [conversation.go](../internal/companion/conversation.go) | 会话端口适配 |
| [weather.go](../internal/companion/weather.go) | 城市级天气 |

---

## 4. 数据模型

### Profile（用户可编辑的身份）
`id` `name` `summary` `traits[]` `scheduleTendency`(relaxed|balanced|busy|social) `proactiveEnabled` `city` `reasoningEffort` `provider` `model` `avatar`

`provider`/`model` 为空、或指向已不可用的 provider 时，模型桥回落模式默认。**不做数据迁移**，靠解析时回落自愈。

### State（当前内心快照）
指标、特质、当前活动、意识三字段（`consciousness`/`asleepSince`/`asleepBlockId`）、场景字段、活动边界、各类时间戳、睡眠账本（`sleepDebtMinutes`/`sleepRepaidMinutes`/`sleepStreak`）、主动消息账本（`unrepliedProactive`，见 §16）。

**tick 是这些字段的唯一写入者。** 别处不要写。

**这条是理想，不完全是事实。** turn 结算（`applyTurnBrief`）也在写 `State`，走的是 `CommitTick` 那一个事务，安全。

但 `CompleteCompanionTurn` 以前是**没有互斥的读-改-写**：`store.State()` 读出来，改完 `SaveState()` 写回去，中间 tick 若插进来，后写的会把先写的盖掉。已经收口成 store 层的原子操作 `RecordProactiveSent(id, at)` —— 计数 +1 和 `lastProactiveAt` 落在同一个 bbolt 事务里，照 `AddUnread` 那个模式。

**注意 `putState` 会从 struct 重写整个 trait bucket。** 任何在事务里改 `State` 的新方法都必须先把 trait 从同一个 tx 读回来，否则 trait 会被抹平。`RecordProactiveSent` 里那段 trait 回读就是干这个的，`TestRecordProactiveSentKeepsTheRestOfTheState` 守住它。

### scheduleBlock
`kind` `label` `startMinute` `endMinute` `weight(0..10)` `origin` `shiftedBy` `deadline` `startedAt` `missed` `missReason`

- `startedAt == 0` 且非 missed 的重块 = 她在拖着。这恰恰是她能聊的时候。
- `weight` 是事先的决定，用来买"带着多少不情愿去做"。

### DayPlan
`date` `kind`(workday|weekend|holiday|makeup) `plannedAt` `revision` `blocks[]`

块必须**无缝覆盖全天 1440 分钟**。`fillOpen` 保证任何空隙都有 `open` 块。曾有六分钟的缝不属于任何东西，可用性把它读成了空闲时间。

### TurnTask（一轮回复的持久生命周期）

`id` `threadId` `trigger` `status` `provider` `model` `reasoningEffort` `inputs[]` `requestedAt` `lastMessageAt` `notBefore` `startedAt` `resolvedAt` `completedAt` `currentActivity` `availabilityReason` `brief` `resolution` `applied` `directiveId` `forced` `recoveries`

状态机：

```
waiting ──claim──> aligning ──> resolved ──> generating ──> completed
   ^                    │            │             │
   └── 未回退避/日程挂起 └────────────┴─────────────┴──> failed / interrupted
```

三个字段容易被误用，各自只有一个正确语义：

- **`notBefore`** —— 最早可以开始处理的时刻。`claimTurnTask` 和 `kickTurnTask` 都读它。挂起时写未来值，重放时重置成 now。
- **`forced`** —— 只表示「用户按了让她现在回」，语义是**把她叫回来立刻回**。绝不能当通用绕过开关用（I30）。
- **`recoveries`** —— 被重启复活过几次，上限 2（I37）。

`Active()` 对 `waiting / aligning / resolved / generating` 全返回 true，所以它表达的是**「这一轮还没跑完」**，不是「她没空」。前端要区分 `parked` 和 `replying` 得看具体 status，别拿 `Active()` 当忙碌用（I38）。

`applied` 是幂等锁：`applyTurnBrief` 见到 `applied && resolution != nil` 直接返回已有结算，不重复改她的世界。

---

## 5. 存储

**每角色一个 bbolt 库。** 注册表库 `companions.db` 存 `characters` 记录表 + `meta.active_id`；角色库落在 `companionsDir/<id>.db`。历史遗留的 `companion.db` 原样复用为种子默认角色，不搬迁。

角色库 bucket：`meta` `profile` `state` `events` `thoughts` `memories` `effects` `traits` `trigger_states` `day_plans` `commitments` `tasks`

加上导演台账 `directives`。

Key 规则：
- profile / state：角色 id
- day plan：`<id>|<YYYY-MM-DD>`
- commitment：`<id>|<commitmentID>`
- directive：`<id>|<directiveID>`
- event：`<20 位补零 OccurredAt>:<eventID>` —— 注意分隔符是冒号不是竖线，为了按时间排序。导演撤销时按 `:<eventID>` 后缀扫出来删

**`CommitTick` 是多写入的唯一入口。** 一次模拟步骤的状态、事件、想法、记忆、效应、触发器、任务、计划、承诺全在一个事务里落。不要在 tick 或 turn 流程里散着调 `SaveXxx`。

---

## 6. 日历层

### 四态日子

| Kind | 含义 | IsRestDay |
|---|---|---|
| `workday` | 普通工作日 | false |
| `weekend` | 普通周末 | true |
| `holiday` | 法定节假日 | true |
| `makeup` | 调休补班（周六日但上班） | false |

`DayInfo` 带 `Holiday` 名称、`DayIndex`/`TotalDays`（假期第几天共几天）、`IsLastDay`、`Eve`（除夕/跨年夜）、`Announced`、`TomorrowKind`。

### 数据

[rules/holidays.yaml](../internal/companion/rules/holidays.yaml) 走与现有 `rules/*.yaml` 相同的 `go:embed` 模式。加载器按文件名显式读取，新增文件不影响现有规则哈希。

数据源是国务院办公厅通知（经 holiday-cn 抓取）。**只写已公布的年份。** 未公布的年份必须缺席。

### 结构自洽校验

`parseCalendar` 只校验表格自洽，不校验具体政务日期：
- 区间起 ≤ 止
- 区间不互相重叠
- 调休日必须是周六或周日
- 调休日不得落在任何节日区间内
- 日期必须落在其所属年份内

校验失败 → `LoadCalendar()` 返回 error → `ActiveCalendar()` 回落空表（纯 weekday 判断），**绝不崩**。结构性 bug 由 `TestCalendarRejectsSelfInconsistentTables` 在测试期拦住。

### 作息偏移（`sleep_shift`，在 jitter 之前叠加）

| 日子 | wake | sleep |
|---|---|---|
| workday | 0 | 0 |
| weekend | +75 | +30 |
| makeup | −20 | −15 |
| holiday_first | +90 | +45 |
| holiday_later | +45 | +25 |
| eve | +45 | +90 |

### 情绪（`mood`，走现有 ActiveEffect）

数值写在 YAML 里，不硬编码在 Go。`MoodEffectsAt` 生成的 `SourceRuleID` 带日期（`calendar:<date>:<kind>`），`mergeEffects` 按 `(source, target)` 去重，所以跨 tick 只刷新不叠加，窗口在午夜自然关闭。

- 假期：`moodValence +8` / `stress −6`
- 调休补班：`irritability +12` / `motivation −10` / `stress +8`
- 假期最后一天：`anxiety +8` / `moodValence −6`（ramp 曲线，渐入）

### 读取方式

日历是应用级共享数据，不是角色状态。`ActiveCalendar()` 懒加载共享实例，`DayInfoAt(at)` 是纯函数入口。

**不要把 `DayInfo` 缓存进 `State`。** 读现算（I2）。

---

## 7. 日程层

### `buildDayPlan` 的落块顺序（顺序即优先级，不要重排）

1. **头睡** `0 → wake`，**`StartedAt` 留空**（I44）。跨午夜是否仍在睡交给 `carrySleepFrom` 从昨天传过来，不在这里预置。
2. **承诺**，按开始时间排序逐条落：
   - `takesPrecedence` + `carve`：严格更重的块抢下这个时段并把一天劈开（考试 11:30 穿过工作日，而不是被推到工作日之后）。
   - 被压过的排在已有块后面（`start = max(start, cursor)`）。
3. **尾睡** `max(sleepStart, cursor) → 1440`，永不因太短被丢弃。
4. **三餐** 先 `fit`（在空档里就近找位置），找不到就 `carve`（在一天里凿出来）。八小时工作日不能取消午饭，只会被它打断。
5. **`fillOpen`** 剩余全部变成 `open` 块。

### 关键约束

- `carve` 只劈她看得比这个需求轻的东西：午饭能穿过工作日，穿不过考试，**永远穿不过睡眠**。
- `takesPrecedence` 要求严格更重。两个等重的计划排队而不是互相撕碎。
- `minBlockMinutes`：sleep 240 / work 60 / commute 15 / meal 15 / 其他 20。

### `ApplyDrift`：现实撞进来时

```
吸收 → 滑动 → 抢回 → 认输
```

1. 落在计划开始之前 = 睡过头，整天滑动。
2. 事件没超出当前块边界 → 完全吸收，下游不动。
3. 超出 → 当前块延长，**先滑动**下游（让每个后续块可见地变晚），**再抢回**。顺序反了会把墙拉得比计划还早。
4. 下游没有重要到值得抢的墙 → 一天就是晚了。
5. 值得抢 → 沿途比墙轻的块各自让出松弛量（不低于 `minBlockMinutes`）。
6. 仍超过 `lateTolerance(wallWeight)` → 那件事 `Missed`，它自己的小时还给一天其余部分。

`Drift.Severity` 是"多不可避免"：忘了时间是 2，交通瘫痪是 8。块只在自身权重 ≥ severity 时才防守自己的长度。

---

## 8. 三股力

```
threshold = 60 - weight * 3
fire      = need - reluctance
开始做     当 fire >= threshold
```

权重 10 的睡觉阈值 30：困到 70 但正玩在兴头上（flow 50），`70-50=20 < 30`，她不睡。权重 6 的工作阈值 42：责任感拉不动低动力，她就拖。

### 驱动表（`driveSpec`）

| 块 | 需求（推） | 抗拒（拉） |
|---|---|---|
| sleep | `sleepPressure*0.7 + fatigue*0.3 + 欠债` | `flow` |
| meal | `40 + hunger*0.6` | `anxiety*0.6 + cognitiveLoad*0.4` |
| work | `dutyFromBlock * 100` | `(100-motivation)*0.5 + cognitiveLoad*0.5` |
| social | `30 + loneliness*0.7` | `100 - socialEnergy` |
| intimacy † | `libido` | `fatigue` |
| open | `boredom` | `flow` |
| commute | `75` | `fatigue*0.5 + anxiety*0.3` |
| exam | `85 + dutyFromBlock*15` | `anxiety*0.6 + fatigue*0.4` |
| dinner | `60 + hunger*0.6` | `anxiety*0.6 + cognitiveLoad*0.4` |
| trip | `80 + dutyFromBlock*15` | `fatigue*0.4 + anxiety*0.4` |
| errand | `74` | `fatigue*0.5 + anxiety*0.2` |
| user | `85` | `flow*0.4 + fatigue*0.3` |
| morning | `70` | `fatigue*0.5` |
| break | `60 + boredom*0.3` | `flow*0.5` |
| hobby / game | `65 + boredom*0.5` | `fatigue*0.3` |
| rest | `70` | `fatigue*0.4` |

**注意那些常数下限。** `meal` 的 40 和 `social` 的 30 是"到点坐下"的习惯。纯靠原始指标过不了权重 6 的门槛，会让她永久不出门、不吃午饭。

**`user` 和 `exam` 是"大概率做"，不是"一定做"。** 前者常驻需求最高（他亲口约的），唯一能把她拉走的是正陷在别的事里；后者重到几乎跳不掉，但焦虑和疲惫拉满时照样告吹。这正是权重模型的本意（I7）。

**漏配 `driveSpec` 会静默 fail-open，见 I47。** 守它的是 `TestEveryNamedBlockKindHasADriveSpec`。

**† `intimacy` 今天没有生产者。** 亲密走场景不走块，这一行留着只是防 fail-open（I52）。

### 两个专用指标

- **`flow`（沉浸度）** 半衰期 90 分钟（`flowDecayPerHour = 0.46`）。15 分钟 tick 下稳态 ≈ `boost × 2.3`。两个来源，取较大者（I54）：
  - **已启动的块** `flowBoost`：hobby / game 26 · social 18 · work 10 · meal 6
  - **活着的场景** `sceneFlow`：active / climax 30 · aftercare 14 · flirt 8

  这两个数字的落差很要紧。**块这一侧最高只有 work 的 10，稳态约 23，而权重 10 的睡觉阈值是 30 —— 单靠日程永远拦不住她睡觉。** "我们正聊到兴头上，一看已经一点了"这种 P2 最想要的场景，只有场景那一路能到。场景本身受墙钟约束（I53），所以它买到的是"晚睡"，不是"不用睡"。

  **收场即断供。** `collapseIdleScene` 把场景收到 `none` 之后 `sceneFlow` 归零，flow 按 90 分钟半衰期掉干净，她回到正常作息。这条是"幽灵场景熬通宵"的唯一解药 —— 他 21:03 停住、她 23:39 还挂着亲密中顶着上床点，就是断供没接上时发生的事（I58 / I60）。
- **`boredom`（无聊）** 空白期以 38/小时涨，一旦有事做按 60%/小时掉。

### 睡眠账本

"该睡的时段"从计划读（`sleepWindowOf(plan)`：当天尾睡块的 `StartMinute` 到当天头睡块的 `EndMinute`），不是硬编码的 23:30 / 07:30（I48）。"睡着"判的是 `consciousnessAt(...) == ConsciousAsleep`，不是 `StartedAt > 0`（I49）—— 躺下睡不着的那几个小时必须继续计债，否则失眠在账面上不存在，她第二天精神好是假的。

**关键在于 flow 会衰减，欠债不会。** 熬到凌晨三点 flow 掉了债务还在涨，某一 tick 突然越过阈值当场崩倒。欠得多了可能一觉十三小时把第二天一起毁掉。`SleepStreak` 记连续晚睡，连续三天后额外压 `moodValence`。

### `duty`（截止压力）

由承诺的 `Deadline` 算，**不进指标系统**。基础值 0.7（"人到场"本身就带压力），随块内进度加到 1.0，真 deadline 在其上叠加紧迫感但**永不减**。`dutyLeadHours = 6`。

---

## 9. 意识硬闸

### 判定

```go
consciousnessAt(state, plan, at):
    block = plan.blockAt(at)
    若非睡眠块 或 未开始        → awake
    若 AsleepSince > 0          → asleep
    若 at - block.StartedAt >= sleepOnset(state) → asleep
    否则                        → awake
```

`sleepOnset = 18min + flow * 0.9`。

`evaluate` 的分支顺序是**意识 → 场景 → 块规则**（I50）。意识排在场景之前不是可选项：睡着的人不会因为场景还挂着就能收到消息。

latch 存在 `State` 上，但**是否仍算睡着每次现算**。latch 已置时只要她睡的那个块还没到 `EndMinute` 就还是睡，块一过立刻算醒。

### 三态可达性

| 状态 | 可回复 | 强制回复 |
|---|---|---|
| 清醒且块轻/未开始 | 是 | 是 |
| 躺床上还没睡着 | 是（刷手机时间） | 是 |
| 睡着 | 否，消息排队到她醒 | **否，后端直接拒绝** |

`nextWakeAt(state, plan, resolve, at)` 先用 `sleepBlockUnder` 找到她所属的睡眠块（眼下这块优先，找不到退回 `asleepBlockID` 那块），再读 `EndMinute`。`EndMinute >= 1440` 时**必须**用 `resolve(tomorrow)` 的开天睡眠块（I45）。这个值就是忙线卡上「预计 HH:MM 醒」的来源，也是 parked 消息等待的时刻。

`settleConsciousness` 是意识字段的**唯一写入者**，在力更新之后跑，好让入睡检查读到本 tick 刚产生的 flow。

### 场景生命周期

场景有两只钟，各管各的事，混起来就是这次事故的根因。

```
        他在                他静默 18min         再静默 25min
none ──────────────► active/climax ──────► aftercare ──────► none
  │                        ▲   │              ▲                ▲
  └── 墙钟 45min ──────────┘   └─ 墙钟 3h ───┘   墙钟 3h ─────┘
      （还在聊才推）            （封顶用，不是收场）

flirt ── 静默 30min ──► none      （墙钟上限 6h）
```

| 钟 | 读什么 | 语义 | 会不会产收场事件 |
|---|---|---|---|
| 墙钟 `advanceSceneByWallClock` | `SceneStartedAt` | 「他们还在聊」，聊久了自然收尾 | **否** |
| 空转 `collapseIdleScene` | `SceneTouchedAt` | 「没人回来了」 | **是**（I60） |

关键区别：墙钟的收尾发生在「他们确实在聊」的前提之下，所以它把场景推到 `none` 时**不需要**收场事件 —— 没有洞要填。空转的收尾则必须留事件，因为他回来时中间那段是空的。

**`SceneTouchedAt` 只有 turn 和 collapse 写。** 衰减路径一律不写（I58）。collapse 每收一级会把它重置，所以每一级各拿自己那份完整时长，而不是和上一级共用一个计时器 —— 否则 aftercare 只活七分钟。

**收场是安静的，不带情绪惩罚。** 不建模「她自己解决」，`libido` 走现有内稳态自然回落，不挂人际含义（§0）。

---

## 10. 承诺层

`Commitment` 是"她答应做的事"。工作、考试、饭局、出差、用户的要求都是同一个形状：铺在身体模板上，一天围绕它重新排。

### `CalendarScope`

| 值 | 含义 |
|---|---|
| `""` (always) | 总是 |
| `workday` | 只在上班日（含调休周六） |
| `restday` | 只在休息日 |

`defaultCommitments` 给 work 和 commute 打上 `workday` 且**不带 weekday 列表**（见 I16）。`social` 这类按日期走的保持空 scope，不会因为补班把周六约的饭挤掉。

### 对话里的安排要落地

`ResolutionDraft.Commitments []CommitmentDraft`（`kind/label/date/start/duration/weight/city`）。

`NormalizeTurnCommitments` 的校验：
- label 非空，≤ 40 rune
- date 必须解析成 `YYYY-MM-DD`，不早于今天，不超过 90 天
- start 解析失败回落 `09:00`
- duration 夹到 `[15, 720]`，缺省 60
- weight 夹到 `[1, 10]`，缺省 6
- kind 不在白名单回落 `user`
- 按 `(date, normalize(label))` 对已有承诺去重
- 每轮最多 3 条
- `Source` 恒为 `user`

落盘走 `TickCommit.Commitments`，与 turn 同事务。存进去那天自动有块，做不做仍由三股力决定——你约的事她也可能因为太累而拖，但块在那儿。

### 城市

`Commitment.City` 覆盖 profile 城市。`effectiveCity` 取覆盖当天的最重那条。这是"国庆去成都"改变她聊的天气的方式。

---

## 11. 指标与情绪

`MetricEngine.ApplyTick` 每个指标每 tick：

```
rate = baseRatePerHour
     + Σ(weight × signal[source])        // 带 Caps 的截断
     + Σ(effect.RatePerHour × intensity)
rate 过 Gates 乘 multiplier
rate 夹到 [rateMin, rateMax]
value += rate × Δt
若 homeostatic: value += (baseline - value) × (1 - e^(-recoverySpeed × Δt))
              baseline/recoverySpeed 也被 effects 的 BaselineDelta/RecoveryDelta 推动
value 夹到 [min, max]
```

`ActiveEffect.IntensityAt` 支持 `constant` / `ramp` / `pulse` / `linear_decay` / `ramp_decay`。

`mergeEffects` 按 `(SourceRuleID, Target)` 去重，`stacking` 可选 `add` / `cap` / 默认刷新。

**新增指标必须同时进 `metric_catalog.yaml` 和 `metric_weights.yaml`**，否则 `LoadRuleSet()` 直接报错。规则输入名（`state.*`、`trait.*`、`schedule.*`、`context.*`）在 `RuleSet.validate()` 白名单里。

---

## 12. 改动守则

按"改了会坏什么"排。

1. **加新块类型** → 必须同时加 `driveSpecs` 条目，否则 `ShouldStart` fail-open 把它变成"一定做"，权重彻底失效（I47）。承诺白名单里的 kind 直通到块，加进白名单就得配驱动。
2. **加指标** → 必须同时进 catalog 和 weights 两个 YAML，否则启动报错。若要进规则条件，还要加进 `validate()` 的 `validInputs`。
3. **加日历效应** → 写在 `holidays.yaml` 的 `sleep_shift` / `mood` 里，不要在 Go 里硬编码数值。
4. **不要把 `DayInfo` 缓存进 `State`。** 读现算（I2）。
5. **不要在 UI 里自己判断醒/睡/可达。** 读 `Overview` 的派生字段。
6. **不要给 `scheduleBlock` 加 `mustAttend`。**（I7）
7. **不要绕过 `Commitment.matches` 判断承诺落不落。**（I14）
8. **不要在 tick 或 turn 流程里散着调 `store.SaveXxx`。** 走 `CommitTick`。
9. **新增跨 tick 效应必须带可去重的 `SourceRuleID`。** 否则每 15 分钟叠一层，情绪条会跑飞。
10. **改触发阈值 / flow 半衰期 / 睡眠锚点** → 同步改测试。这些数字就是她的性格。
11. **改 `buildDayPlan` 落块顺序** → 先跑 `schedule_test.go` 再提交。顺序变了会连锁影响吃饭、睡觉、承诺抢占。
12. **前端新增跨角色共享状态必须带 `characterId` 守卫。**（I24）
13. **改后端导出面** → 跑 `wails3 generate bindings -ts`，否则前端类型对不上。
14. **新增样式只用主题变量。**（I28）
15. **不要把 provider/config/agent 引入 `internal/companion`。** 加端口方法。（§2）
16. **导演想加新的落点，先问它对应哪个已有原语。** 答不上来就不该加 —— 那是在开新的数值通道。（I29）
17. **不要拿 `Forced` 当"绕过可用性"的通用开关。** 它是「把她叫回来立刻回」，导演走独立的 `TriggerDirector` 判断。（I30）
18. **导演效应必须带 `director:<directiveID>:<target>` 前缀。** 撤销靠这个前缀找得到东西；丢了前缀就撤不干净。（I34）
19. **任何新的主动消息出口都必须过 `proactiveBackoff`。** 别在别处再开一条「直接发」的路 —— 绕过退避就等于把骚扰开关重新装回去。（I35）
20. **清零 `UnrepliedProactive` 只认 `TriggerUser`。** 加新 trigger 时不要顺手清它，否则她自己的消息会把拦住自己的数抹掉。（I35）
21. **加新 trigger 时必须回答「崩溃后它该不该重放」。** 在 `replayAfterCrash` 的 switch 里明确表态，别让它落到 `default` 悄悄退休，也别不加闸就重放。（I37）
22. **前端别再用一个 `busy` 同时表达「回不了」和「正在回」。** 锁动作用 `turnInFlight`，说状态用 `parked` / `replying`。（I38）
23. **不要在 `buildDayPlan` 里给头睡块写 `StartedAt`。** 跨午夜的睡眠只能由 `carrySleepFrom` 从昨天传过来，而且要对着**已存过的**计划也补传一次（明天计划常比她入睡早写好）。（I44）
24. **`nextWakeAt` 不许把 `EndMinute == 1440` 直接换算成时刻。** 那是当天午夜，不是闹钟，必须查下一天的开天睡眠块。（I45）
25. **加新 trigger 时不要顺手写 `LastUserAt`。** 只有他真的出现了才算，她自己的主动消息和导演指令都不算。（I46）
26. **不要把亲密当成一种日程块。** 沉浸度走 `sceneFlow`，`flowBoost` 里不许出现 `intimacy`。（I52）
27. **改场景时长只改 `sceneHorizon`。** 在 `sceneLive` / `advanceSceneByWallClock` / `sceneFlow` 里各写一份小时数，迟早会分叉。（I53）
28. **别把 flirt 加进 `sceneLive`。** 它可以有沉浸度，但不可以让她不可达。（I55）
29. **加指标时三处一起改。** catalog YAML、schema 的 `target` enum、prompt 里那份中文对照，少一处模型就瞄不到它；`TestAlignmentMetricVocabularyMatchesCatalog` 会先响。（I56）
30. **别给导演轮次开 generic 的世界变化通道。** `resolution.world` 是唯一入口，generic 的 events / impulses / effects / traitChanges 在那一轮必须清空，否则双写，而且撤不干净。（I57）
31. **不要在 turn 路径调 `collapseIdleScene`。** 那里时间戳还是上一轮的，会把刚回来的人当成已经离开的人。收场只有 tick 里那一个调用点，且排在墙钟衰减之前。（I58）
32. **量空转只读 `SceneTouchedAt`。** 别"顺手"改成 `SceneUpdatedAt` —— 那个字段衰减路径也在写，改了之后收场会在最长的缺席里静默消失，而且平时看不出来。（I58）
33. **`idleStageTarget` 里不许出现通往 `stopped` 的路径。** 撤回同意是一种动作，不是一种超时。（I59）
34. **收场必须产事件。** 只改 `SceneStage` 不留 `LifeEvent`，等于在他回来那一段对话里留一个洞，模型只能靠编。（I60）
35. **收场短句里不许写动机。** Go 只陈述「他没声音了，她做了什么」。写了为什么，收场就变成指控。（I60）

---

## 13. 测试地图

| 不变量 | 守它的测试 |
|---|---|
| I4/I5/I6 意识硬闸 | [consciousness_test.go](../internal/companion/consciousness_test.go) |
| I44 头睡不预置 / 跨午夜不误睡 | `TestOpeningSleepStartsUnclaimed` / `TestFreshPlanAfterMidnightKeepsHerReachable` / `TestMidnightAloneDoesNotKnockHerOut` |
| I45 醒来时刻不落在午夜 | `TestAsleepBlocksReplyAndNamesTheWakeTime` / `TestSleepCarriedIntoTheNewDayWakesTomorrow` / `TestWakesInsideTheDayFromTheOpeningBlock` |
| I46 absence 只由用户 turn 推进 | `TestProactiveTurnIsNotSomeoneShowingUp` / `TestUserTurnStillCountsAsSomeoneShowingUp` |
| I47 每个 kind 都有驱动 / fail-open 不再静默 | `TestEveryNamedBlockKindHasADriveSpec` / `TestExamIsHeavyButNotUnmissable` / `TestUserRequestedPlanStillYieldsToImmersion` |
| I48 睡眠窗口与晚睡量都跟计划走 | `TestLateStreakFollowsHerOwnBedtime` |
| I49 躺着没睡不还债 | `TestLyingAwakeInBedStillAccruesDebt` / `TestSleepDebtGrowsWhileAwakeInTheWindow` |
| I50 睡眠排在场景之前 | `TestSleepBeatsALiveScene` / `TestTurnCannotOpenASceneWhileSheIsAsleep` |
| I51 活动名只有一个来源 | `TestAvailabilitySaysTheSameActivityTheTickWrites` |
| I52 亲密只从场景喂沉浸度 | `TestIntimacyIsNotAFlowBoostingBlockKind` / `TestSceneReachesImmersionNoBlockCan` |
| I53 场景时间窗单一来源 | `TestSceneFlowDiesWithTheSceneClock` |
| I54 沉浸取最大不相加 | `TestSceneDoesNotStackOnTopOfAStartedBlock` |
| I55 flirt 有沉浸但不可达 | `TestFlirtFeedsFlowWithoutEverHoldingHer` |
| I56 指标词表三处一致 / 英文猜测落地 | `TestAlignmentMetricVocabularyMatchesCatalog` / `TestDirectorResolvesEnglishMetricGuesses` / `TestDirectorStillRefusesNamesThatAreNotMetrics` |
| I57 导演轮次不双写世界变化 | `TestDirectorTurnDoesNotApplyTheSameEventTwice` / `TestUserTurnStillAppliesItsOwnEvents` |
| I58 场景靠互动维持 / 静默钟只认 touched | `TestIdleCollapseWalksEachRung` / `TestSilenceClockIgnoresDecayStamps` / `TestCollapseRunsAheadOfWallClockProgression` / `TestTurnPathDoesNotCollapseTheSceneHeJustReturnedTo` |
| I59 空转收场绝不进 stopped | `TestIdleCollapseNeverStopsConsent` |
| I60 收场留下可引用事件 / 断供沉浸 | `TestWindDownEventLandsInTimeline` / `TestSceneWindDownEventIsQuietAndUnshareable` / `TestCollapseCutsTheFlowSupply` |
| 确认消息：置位 / 外推 / 消费 / 走退避 | `TestSceneCheckInArmsOnContactAndFiresOnce` / `TestSceneCheckInRespectsBackoff` / `TestSceneCheckInIsConsumedOnce` |
| 沉浸拦得住觉，但欠债照样赢 | `TestSceneReachesImmersionNoBlockCan` / `TestDebtStillWinsMidScene` |
| I8 触发公式 / 三股力 | [drives_test.go](../internal/companion/drives_test.go) |
| I9/I10 计划记录与忙线承诺 | [schedule_test.go](../internal/companion/schedule_test.go) |
| I12/I13/I14/I15/I16 日历 | [calendar_test.go](../internal/companion/calendar_test.go) |
| I14/I17/I18/I20 承诺 | [commitments_test.go](../internal/companion/commitments_test.go) |
| I19 同事务落盘 | `TestTurnCommitmentLandsOnTheDayItWasPromisedFor` |
| I21/I22/I23 多角色 | [registry_test.go](../internal/companion/registry_test.go) |
| I29/I30/I31/I34 导演拒绝清单与撤销 | [director_test.go](../internal/companion/director_test.go) |
| I33 审计链不断在失败里 | `TestFailedDirectorTurnDoesNotLeaveTheLedgerPending` / `TestFailedTurnDoesNotClobberASettledDirective` |
| I32 旁白不污染用户语义 | `TestDirectorTurnIsNotSomeoneShowingUp` / `TestDirectorTurnIsNotFramedAsAMessageToHer` |
| I35 未回退避 | [proactive_test.go](../internal/companion/proactive_test.go) |
| I35 计数不被并发吞掉 / trait 不被重写抹平 | `TestRecordProactiveSentKeepsTheRestOfTheState` |
| I36 卡片幂等 | `TestCompanionTurnCardIsIdempotentPerTask` |
| I37 崩溃重放与有界 | [recover_test.go](../internal/companion/recover_test.go) |
| I38 忙 / 正在回复 拆分 | 前端契约，见 [useCompanionSummary.ts](../frontend/src/features/companion/model/useCompanionSummary.ts) |
| I39-I43 可达性三问 / 形态封顶 | [availability_test.go](../internal/companion/availability_test.go) |
| I58/I59/I60 场景收场 | [scenewinddown_test.go](../internal/companion/scenewinddown_test.go) |
| 规则完整性 | [rules_test.go](../internal/companion/rules_test.go) |
| I1 上下文一致 | `TestModelContextSaysWhatDayItIs` |
| 可用性与认领（`taskAvailability` / `claimTurnTask`） | [turn_task_test.go](../internal/companion/turn_task_test.go) |
| tick / catch-up / 场景衰减 | [manager_test.go](../internal/companion/manager_test.go) |
| 会话端口与消息流 | [conversation_test.go](../internal/companion/conversation_test.go) |

验证命令：

```powershell
go build ./...
go vet ./internal/...
go test ./...
go test -race ./internal/companion/ ./internal/agent/
cd frontend; npm run typecheck
cd frontend; npm run build
```

改了后端导出面（结构体字段、服务方法）还要跑 `wails3 generate bindings -ts`，否则前端类型对不上（守则 13）。

---

## 14. 已知边界

这些是**有意**留白，不是遗漏。要动先回这里补一句。

- 多日行程只支持城市切换，完整的异地上下文（当地天气逐日、行程内活动链）不在本轮。
- 日历表只覆盖已公布年份（当前 2025、2026）。2027 起需补表，否则回落 weekday。
- 不引入农历库。春节等的公历日期与除夕直接写在表里；`CalendarStrip` 标出除夕，让模型自己数初六。
- 非激活角色不 tick，靠激活时 catch-up，上限 7 天。超过 7 天不补。
- `Reset` 只作用于激活角色。
- 角色上限 8。
- 模板（创建角色时的三个预设）是前端常量，不是后端接口。
- 历史对话列表不再显示陪伴线程，陪伴会话统一归侧栏「她」分组。
- 主动消息**没有意图级去重**。退避曲线压掉了重复的大头（不会再有三条晚安），但「同一件事今天说过就不再问一遍」需要模型给每条消息打 `intent` 标签再按天去重，本轮未做。目前靠 prompt 明令禁止重复表态兜着。（§16）

---

## 15. 导演层（Director）

导演不是新子系统，是**已有原语的新前门**。用户往她世界写的每一件事都映射到一个已经存在的落点，因此不新增模拟层、不新增数值通道，前面那些不变量一条都不用推翻。

### 映射表

| 用户想做的 | 落到哪 | 可逆 |
|---|---|---|
| 「她妈明天来看她」 | `Commitment`（走 `NormalizeTurnCommitments`） | 是 |
| 「路上堵车两小时」 | `Drift` → `ApplyDriftNow` | **否** |
| 「她今天心情特别好」 | `ActiveEffect`，`SourceRuleID = director:<id>:<target>` | 是 |
| 「突然下起大雨」 | `LifeEvent`（shareable，进时间线，她会主动提） | 是 |
| 「她去成都了」 | 每天一条带 `City` 的 `Commitment` → `effectiveCity` | 是 |

### 硬拒绝清单

后端自己拦，不靠 UI：

| 情况 | 结果 |
|---|---|
| 她在睡觉 + 即时类（突发 / 当下事件） | 拒收，理由带预计醒来时间；未来安排类仍通过 |
| `moodEffects[].setTo` 有值 | 拒。只能给带时长和衰减的 effect（I29） |
| `moodEffects[].durationMinutes < 15` | 拒。没时长等于把数值钉死 |
| `disruptions[].startBlock` / `completeBlock` 有值 | 拒。不能替她开始或完成一件事（I31） |
| `arrangements[].mustAttend` 有值 | 拒。没有非去不可的行程（I7） |
| 日期为空 / 过去 / 超 90 天 | 拒 |
| 翻译不出任何世界变化 | `status = rejected` 并给原因，**不静默降级成聊天消息** |

一次指令最多 3 项变化，与承诺、记忆上限一致。

### 不增加模型调用

每轮本来就跑一次对齐。导演只是在同一个 schema 的 `resolution` 下多一个 `world` 段，`trigger == director` 时才填。对齐 system prompt 明确要求把命令式指令翻译成她世界里的客观事实（「让她妈明天来」→「她妈说明天要过来坐坐」），绝不出现「被控制」「系统安排」「有人让你」这类字眼。

### 状态机

`pending` → `applied` / `partial` / `rejected`。

`pending` 在模型跑之前就写库，所以对齐失败也留痕，不会把用户写的话吞掉。落库与 turn 走同一个 `CommitTick`（`TickCommit.Directive`），台账和它记录的那一轮不会分家。`Drift` 例外：它改当天计划，自己持久化。

### 撤销

`RevertDirective` 走一个事务：

- `arrangement` / `place` → 删 commitment，并把那天计划里**还没开始**的同名 `user` 块一起摘掉。已经开始了就是活过的时间，留着。
- `mood` → `RemoveEffectsBySourcePrefix("director:<id>")`
- `event` → 删 `LifeEvent`
- `disruption` → 不动，且 `Revertible: false` 一直挂在台账上

### 前端

- composer 顶部 `Segmented`：「对她说话」/「导演」。模式按角色绑定存 `localStorage`，**切角色即复位**，防止把导演意图落到错的角色上。
- 导演态 composer 左边框 3px 金色（`--hitk-director` = `--hitk-warn`），发送按钮换成「写入世界」药丸按钮。
- 转录里指令**不用用户气泡**：整行旁白、左对齐、斜体、左侧 2px 导演色竖线，下面挂变更摘要 chip。形状上就和右侧消息不是一类东西，发错了立刻看得出来。
- 旁白行是台账合成出来的 `ItemNotice`（`meta.directive`），按 `createdAt` 并进转录。台账本身才是真相。
- 右侧面板第四个 tab「介入」是台账：时间、原话、变更明细、状态、撤销按钮。不可逆的那条按钮置灰并写明「时间不会倒流，这条撤不回」。

---

## 16. 主动消息与退避

主动消息是她自己发起的对话。这个方向上没有任何人给她反馈，所以**必须由系统替她记住说过多少、有没有人接**。不然「主动」会退化成单向刷屏。

### 一条主动消息要过的关

按顺序，任何一关不过就不产生任务。顺序不要重排 —— 每关都假设前面的关已经拦掉了更糟的情况：

| 顺序 | 关卡 | 拦的是什么 |
|---|---|---|
| 1 | `profile.ProactiveEnabled` | 用户明确关了主动联系 |
| 2 | `metaPaused` | 整个世界暂停 |
| 3 | 无 active turn | 她手上那件事还没处理完 |
| 4 | `taskAvailability` | 日程上此刻不可达 |
| 4 | `evaluate(...).Blocked` | 日程上此刻不可达 |
| 5 | **`proactiveBackoff`** | **前面发的还没人回** |
| 6 | 模型 `reply.send` | 她自己也觉得没什么可说 |

第 5 关必须在建任务**之前**。等发完再退避等于已经骚扰过一次了。

### 退避曲线

`proactiveBackoffGaps`，按未回条数索引，全部集中在 [proactive.go](../internal/companion/proactive.go) 一个数组里，调口味只改这里：

| 未回条数 | 距上次的等待门槛 | 语义 |
|---|---|---|
| 0 | 无 | 扯平了，想说就说 |
| 1 | 2 小时 | 有一条没回，先坐一会儿 |
| 2 | 5 小时 | 只留给真值得说的事 |
| 3 | 12 小时 | 一天最多一次 |
| ≥4 | 沉默 | 不再主动，等他先开口 |

封顶不是惩罚，是止损。第 5 条不会让第 4 条被看见，只会让他把通知关掉。

### 计数生命周期

| 事件 | 位置 | 对 `UnrepliedProactive` |
|---|---|---|
| 主动消息真的发出去了 | `CompleteCompanionTurn` | **+1** |
| 用户说话 | `applyTurnBrief` 且 `Trigger == TriggerUser` | **清零** |
| 她自己的主动 turn | `applyTurnBrief` 且 `Trigger == TriggerProactive` | **不许动** |
| 导演改世界 | `applyTurnBrief` 且 `Trigger == TriggerDirector` | **不许动**（I32） |

最后两行是这套东西最容易写坏的地方。写成 `Trigger != TriggerDirector` 就清零，她每发一条就把拦住自己的那个数抹掉，退避永远不会生效 —— 这个错真的写过一次，靠 `TestBackoffBlocksTheNextProactiveMessage` 才抓出来。加新 trigger 时默认**不清零**，除非那个 trigger 真的是用户开口。

### 告诉她欠了多少

光在后端拦不够，她本人也得知道。主动 turn 的对齐 prompt 里带上未回条数，并明确禁止再重复「我不催你」「随你」这类表态 —— 那些免责声明就是每 15 分钟失忆一次重新发明出来的产物。她知道坑在哪儿，才不会一直往里踩。

### 场景里的确认消息

场景活着而他静默到点，她轻轻问一句他是不是还在。这不是新机制，是**同一条主动消息通道上的一个标记**：

| 环节 | 做法 |
|---|---|
| 置位 | `applySceneState` 里场景是活的就设 `SceneCheckInAt = now + 12min`。每次互动都把它往外推，聊得热络时永远轮不到 |
| 清除 | 场景进 `none` / `stopped` 时归零；被消费时归零 |
| 消费 | `submitProactiveTurnAt` 里场景仍活且到点 → 任务带 `SceneCheckIn: true`，随即归零。一次沉默只问一次 |
| 退避 | **照常，不开特例。** 她已经压着没回的消息时不问 —— 这正是这时候该有的行为 |

12 分钟排在 18 分钟收场之前，所以她先问一声，再自己收。

措辞在 prompt 里写死：一句，温柔，不追问，不委屈，不索求，不提他冷落她，不提她等了多久。这是 §0 那条原则最容易被写歪的地方 —— 「你是不是睡着了」和「你是不是不在乎我」只差一个从句，而后者是操纵。

### 为什么有这一节：真实事故

`LastProactiveAt` 以前**只写不读**，全代码没有一处查它。项目里现存的 cooldown 全是事件规则的（指标越界那种），主动消息的节奏根本没有冷却。

结果：每 15 分钟一次「要不要找他」的判断带着完全失忆跑。用户最后一次说话是 10-01 10:53，之后 31 小时没吭声，她刷出十几条单向消息，每条都在重新发现他没回，并且给沉默编了个解释反复用 —— 09:28「睡到自然醒才算数」、12:50「都中午了还一点动静没有」、18:20「你这觉睡得可真瓷实」。一晚上说了三次晚安。

思念值高不等于该发消息。这是这一节存在的全部理由。

### 卡片幂等

`AppendCompanionTurnTask` 按 `taskID` 去重。卡片是那个任务的**记录**，不是每次调用一个事件。之前同一任务重复执行会在屏幕上变成两条消息，且存下来的 task brief 只对得上第二张，第一次那轮的活儿成了孤儿。
