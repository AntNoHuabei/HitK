// 激活角色 overview 里"侧栏和视图都会顺手要"的那几样东西。
//
// 抽出来的理由有两个：一是未读与忙碌状态不止侧栏要用，与其两处各自订阅
// 一遍事件，不如一份轮子；二是省得在组件里手写"半个 Overview"的本地类型。
import { useEffect, useState } from "react";
import {
  Consciousness,
  TurnTaskStatus,
  companionRepository,
  type CompanionOverview,
  type CompanionProfile,
} from "../../../shared/repositories";
import { onCompanionEvent } from "../../../shared/wails/events";

export interface CompanionSummary {
  unread: number;
  /** 有一轮还没跑完：等待、对齐、生成中都算。锁角色切换用这个。 */
  turnInFlight: boolean;
  /**
   * 她被日程挂起，此刻回不了话，消息要等她空下来。
   * 这只说「回不了」，不说「正在生成」—— 两者以前挤在同一个 busy 里，
   * 于是她正在回你话的那几十秒也被读成了「她没空」。
   */
  parked: boolean;
  /** 她正在对齐或生成你的回复。 */
  replying: boolean;
  /** 激活角色此刻睡着了吗。非激活角色的世界是冻住的，所以只有这一行有值。 */
  asleep: boolean;
  profile: CompanionProfile | null;
}

const EMPTY: CompanionSummary = {
  unread: 0,
  turnInFlight: false,
  parked: false,
  replying: false,
  asleep: false,
  profile: null,
};

function read(overview: CompanionOverview | null | undefined): CompanionSummary {
  if (!overview) return EMPTY;
  const status = overview.activeTask?.status;
  const parked = status === TurnTaskStatus.TaskWaiting;
  const replying =
    status === TurnTaskStatus.TaskAligning ||
    status === TurnTaskStatus.TaskResolved ||
    status === TurnTaskStatus.TaskGenerating;
  return {
    unread: overview.unread ?? 0,
    turnInFlight: Boolean(overview.activeTask),
    parked,
    replying,
    asleep: overview.consciousness === Consciousness.ConsciousAsleep,
    profile: overview.profile ?? null,
  };
}

/** refreshKey 变化时重新拉一次，例如切了激活角色。 */
export function useCompanionSummary(refreshKey: string): CompanionSummary {
  const [summary, setSummary] = useState<CompanionSummary>(EMPTY);

  useEffect(() => {
    void companionRepository
      .overview()
      .then((overview) => setSummary(read(overview)))
      .catch(() => undefined);
    return onCompanionEvent((event) => setSummary(read(event.overview)));
  }, [refreshKey]);

  return summary;
}
