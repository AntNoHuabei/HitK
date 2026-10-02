// 介入台账：你往她世界里写过的每一件事，以及哪些撤不回来。
import { Button, Empty, Flex, Popconfirm, Tag, Tooltip, Typography } from "antd";
import { LockOutlined, UndoOutlined } from "@ant-design/icons";
import type { CompanionDirective } from "../../../../shared/repositories";

const { Text } = Typography;

const CHANGE_LABELS: Record<string, string> = {
  arrangement: "安排",
  disruption: "突发",
  mood: "情绪",
  event: "事件",
  place: "地点",
};

const STATUS_LABELS: Record<string, { text: string; color: string }> = {
  pending: { text: "处理中", color: "default" },
  applied: { text: "已写入", color: "green" },
  partial: { text: "部分写入", color: "orange" },
  rejected: { text: "被拒绝", color: "red" },
};

function formatTime(value: number): string {
  if (!value) return "";
  return new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function DirectivePanel({
  directives,
  characterName,
  onRevert,
}: {
  directives: CompanionDirective[];
  characterName: string;
  onRevert: (id: string) => void;
}) {
  if (directives.length === 0) {
    return (
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={`还没有往${characterName}的世界里写过东西`}
      />
    );
  }

  return (
    <div className="hitk-directive-ledger">
      {directives.map((directive) => {
        const status = STATUS_LABELS[directive.status ?? "pending"] ?? STATUS_LABELS.pending;
        const revertible = (directive.changes ?? []).some((change) => change.revertible);
        const reverted = Boolean(directive.revertedAt);
        return (
          <article
            key={directive.id}
            className={`hitk-directive-item${reverted ? " is-reverted" : ""}`}
          >
            <Flex justify="space-between" align="baseline" gap={8}>
              <time className="hitk-directive-time">{formatTime(directive.createdAt)}</time>
              <Tag bordered={false} color={status.color}>{status.text}</Tag>
            </Flex>
            <Text className="hitk-directive-raw">「{directive.text}」</Text>
            {directive.reason ? (
              <Text type="secondary" className="hitk-directive-ledger-reason">
                {directive.reason}
              </Text>
            ) : null}
            {(directive.changes ?? []).length > 0 ? (
              <ul className="hitk-directive-change-list">
                {(directive.changes ?? []).map((change, index) => (
                  <li key={`${directive.id}-${change.kind}-${index}`}>
                    <Tag bordered={false}>{CHANGE_LABELS[change.kind] ?? "改动"}</Tag>
                    <span>{change.summary}</span>
                    {change.revertible ? null : (
                      <Tooltip title="时间不会倒流，这条撤不回">
                        <LockOutlined className="hitk-directive-locked" />
                      </Tooltip>
                    )}
                  </li>
                ))}
              </ul>
            ) : null}
            <div className="hitk-directive-actions">
              {reverted ? (
                <Text type="secondary">已撤销</Text>
              ) : (
                <Popconfirm
                  title="撤销这次介入？"
                  description="安排、情绪和事件会撤掉；已经发生的时间撤不回。"
                  onConfirm={() => onRevert(directive.id)}
                  disabled={!revertible}
                >
                  <Tooltip title={revertible ? "撤掉能撤的部分" : "这条里没有任何可撤销的东西"}>
                    <Button size="small" type="text" icon={<UndoOutlined />} disabled={!revertible}>
                      撤销
                    </Button>
                  </Tooltip>
                </Popconfirm>
              )}
            </div>
          </article>
        );
      })}
    </div>
  );
}
