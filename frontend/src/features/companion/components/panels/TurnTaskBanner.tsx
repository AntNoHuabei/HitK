// 本轮任务横幅：等待、对齐、结算、生成、失败各说一句话。
import { Button, Typography } from "antd";
import { ClockCircleOutlined } from "@ant-design/icons";
import type { CompanionTurnTaskView } from "../../../../shared/repositories";

const { Text } = Typography;

export function TurnTaskBanner({
  task,
  onCancel,
}: {
  task: CompanionTurnTaskView;
  onCancel: (taskID: string) => void;
}) {
  const label = task.status === "waiting"
    ? `${task.currentActivity || "正在忙"}，暂时不能回复`
    : task.status === "aligning"
      ? "正在对齐时间、场景和本轮细节"
      : task.status === "resolved"
        ? "状态已结算，准备生成回复"
        : task.status === "generating"
          ? "正在根据已结算内容生成回复"
          : task.status === "failed"
            ? "本轮任务失败"
            : "任务已完成";
  const notBefore = task.notBefore ?? 0;
  const expected = notBefore > Date.now()
    ? `预计 ${new Date(notBefore).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })} 后回复`
    : "";
  return (
    <div className={`hitk-companion-task is-${task.status}`}>
      <ClockCircleOutlined />
      <div>
        <Text strong>{label}</Text>
        {expected && <Text type="secondary">{expected}</Text>}
        {task.availabilityReason && <Text type="secondary">{task.availabilityReason}</Text>}
      </div>
      {task.status === "waiting" && (
        <Button type="text" size="small" onClick={() => onCancel(task.id)}>取消等待</Button>
      )}
    </div>
  );
}
