// 忙线通知：对话流里的系统消息。她在忙就直说，顺手给一个「现在就把她叫回来」的出口。
//
// 出不出这张卡只看后端算好的 task.blocked，不看 status，也不看活动字符串。
// 以前这里只判 status === "waiting"，而刚提交的任务本来就是 waiting，于是只要
// 她身上带着个活动名，卡片就能在她躺在床上场景进行中、手机就在手里的当口，
// 演出一句「她正在忙，暂时没看手机 · 亲密中」。
//
// 睡着是另一种忙：出口还在，但此刻封着。禁用而不是隐藏，是为了让人看清
// 「这个按钮存在，只是叫不醒一个睡着的人」，而不是以为功能没了。
import { useEffect, useRef } from "react";
import { Button, Tooltip, Typography } from "antd";
import {
  ClockCircleOutlined,
  LockOutlined,
  MoonFilled,
  ThunderboltOutlined,
} from "@ant-design/icons";
import type { CompanionTurnTaskView } from "../../../shared/repositories";

const { Text } = Typography;

function clockAt(ms: number): string {
  if (!ms || ms <= Date.now()) return "";
  return new Date(ms).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

export function BusyNotice({
  characterName,
  task,
  asleep = false,
  wakeAt = 0,
  onForce,
}: {
  characterName: string;
  task: CompanionTurnTaskView;
  /** 后端算好的意识状态；睡着时强制回复这条路封死。 */
  asleep?: boolean;
  /** 预计醒来时刻，只在睡着时有意义。 */
  wakeAt?: number;
  onForce: (taskID: string) => void;
}) {
  const ref = useRef<HTMLDivElement | null>(null);

  // 刚冒出来时把它带进视野；已经在视野里就不动，免得抢走用户自己的滚动位置。
  useEffect(() => {
    ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [task.id, task.forced, asleep]);

  // 只有 waiting 且真被拦下才打扰用户；对齐和生成交给流式指示。
  if (task.status !== "waiting" || !task.blocked) return null;

  const name = characterName || "她";
  const activity = task.currentActivity || task.availabilityReason || "手上有事";
  // 睡着时读后端单独给的 wakeAt，文案要说的是「醒」而不是「能看手机」。
  const expected = clockAt(asleep ? wakeAt : (task.notBefore ?? 0));
  const headline = task.forced
    ? `你已经把${name}叫回来了`
    : asleep
      ? `${name}正在睡觉`
      : `${name}正在忙，暂时没看手机`;
  const eta = task.forced
    ? "正在回你刚才的话"
    : expected
      ? asleep
        ? `预计 ${expected} 醒`
        : `预计 ${expected} 能看手机`
      : "";
  // 已经叫过的人不再拦；睡着的人拦下。
  const unforceable = asleep && !task.forced;

  return (
    <div
      ref={ref}
      className={`hitk-companion-busy${task.forced ? " is-forced" : ""}${asleep ? " is-asleep" : ""}`}
      role="status"
    >
      <span className="hitk-companion-busy-icon" aria-hidden="true">
        {asleep ? <MoonFilled /> : task.forced ? <ThunderboltOutlined /> : <ClockCircleOutlined />}
      </span>
      <div className="hitk-companion-busy-body">
        <Text strong>{headline}</Text>
        <Text type="secondary" className="hitk-companion-busy-meta">
          <span className="hitk-companion-busy-activity">{activity}</span>
          {eta ? <span className="hitk-companion-busy-eta">{eta}</span> : null}
          {unforceable ? <span className="hitk-companion-busy-note">叫她也没用</span> : null}
        </Text>
      </div>
      {/* 只有还没叫人的时候有得选；叫过之后这张卡只是回执。 */}
      {!task.forced && (
        <div className="hitk-companion-busy-actions">
          {/* antd 的 Tooltip 不在 disabled 按钮上触发，套一层 span 接住 hover。 */}
          <Tooltip title={unforceable ? `${name}睡着了，叫不醒` : "把她叫回来立刻回"}>
            <span className="hitk-companion-busy-force-wrap">
              <Button
                type="primary"
                size="small"
                icon={unforceable ? <LockOutlined /> : <ThunderboltOutlined />}
                disabled={unforceable}
                onClick={() => onForce(task.id)}
              >
                {unforceable ? "叫不醒" : "让她现在回"}
              </Button>
            </span>
          </Tooltip>
        </div>
      )}
    </div>
  );
}
