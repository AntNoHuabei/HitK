// 近日生活：她留下的事件片段。
import { Empty, Tag, Typography } from "antd";
import type { LifeEvent } from "../../../../shared/repositories";

const { Text } = Typography;

export function LifePanel({ events }: { events: LifeEvent[] }) {
  if (events.length === 0) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有留下生活片段" />;
  }
  return (
    <div className="hitk-companion-life-list">
      {events.map((event) => (
        <article key={event.id} className="hitk-companion-life-item">
          <time>{formatEventTime(event.occurredAt)}</time>
          <Text>{event.content}</Text>
          {event.importance >= 7 && <Tag bordered={false}>重要</Tag>}
        </article>
      ))}
    </div>
  );
}

function formatEventTime(value: number): string {
  if (!value) return "";
  return new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
