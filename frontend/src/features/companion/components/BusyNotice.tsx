// 忙线通知：对话流里的系统消息。她在忙就直说，顺手给一个「现在就把她叫回来」的出口。
import { useEffect, useRef } from "react";
import { Button, Typography } from "antd";
import { ClockCircleOutlined, ThunderboltOutlined } from "@ant-design/icons";
import type { CompanionTurnTaskView } from "../../../shared/repositories";

const { Text } = Typography;

function expectedAt(notBefore: number): string {
  if (!notBefore || notBefore <= Date.now()) return "";
  return new Date(notBefore).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function BusyNotice({
  characterName,
  task,
  onForce,
}: {
  characterName: string;
  task: CompanionTurnTaskView;
  onForce: (taskID: string) => void;
}) {
  const ref = useRef<HTMLDivElement | null>(null);

  // 刚冒出来时把它带进视野；已经在视野里就不动，免得抢走用户自己的滚动位置。
  useEffect(() => {
    ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [task.id, task.forced]);

  // 只有 waiting 需要打扰用户，后面的对齐和生成交给流式指示。
  if (task.status !== "waiting") return null;

  const name = characterName || "她";
  const activity = task.currentActivity || task.availabilityReason || "手上有事";
  const expected = expectedAt(task.notBefore ?? 0);

  return (
    <div
      ref={ref}
      className={`hitk-companion-busy${task.forced ? " is-forced" : ""}`}
      role="status"
    >
      <span className="hitk-companion-busy-icon" aria-hidden="true">
        {task.forced ? <ThunderboltOutlined /> : <ClockCircleOutlined />}
      </span>
      <div className="hitk-companion-busy-body">
        <Text strong>
          {task.forced ? `你已经把${name}叫回来了` : `${name}正在忙，暂时没看手机`}
        </Text>
        <Text type="secondary" className="hitk-companion-busy-meta">
          <span className="hitk-companion-busy-activity">{activity}</span>
          {task.forced ? (
            <span className="hitk-companion-busy-eta">正在回你刚才的话</span>
          ) : (
            expected && <span className="hitk-companion-busy-eta">预计 {expected} 能看手机</span>
          )}
        </Text>
      </div>
      {/* 只有还没叫人的时候有得选；叫过之后这张卡只是回执。 */}
      {!task.forced && (
        <div className="hitk-companion-busy-actions">
          <Button
            type="primary"
            size="small"
            icon={<ThunderboltOutlined />}
            onClick={() => onForce(task.id)}
          >
            让她现在回
          </Button>
        </div>
      )}
    </div>
  );
}
