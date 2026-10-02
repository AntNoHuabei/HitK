// 导演指令在转录里的样子。
//
// 它不是用户气泡。整行旁白、左对齐、左侧一道导演色竖线、斜体，形状上就和右边
// 的「她」的消息不是一类东西 —— 发错了立刻看得出来。下面挂变更摘要 chip，
// 那是控制感的来源：写了什么，落成了什么，能不能撤，一眼读完。
import { Tag, Tooltip, Typography } from "antd";
import {
  CheckCircleOutlined,
  ControlOutlined,
  ExclamationCircleOutlined,
  StopOutlined,
} from "@ant-design/icons";
import {
  ItemStatus,
  ItemType,
  type CompanionDirective,
  type CompanionDirectiveChange,
  type Item,
} from "../../../shared/repositories";

const { Text } = Typography;

const CHANGE_LABELS: Record<string, string> = {
  arrangement: "安排",
  disruption: "突发",
  mood: "情绪",
  event: "事件",
  place: "地点",
};

function changeLabel(change: CompanionDirectiveChange): string {
  const kind = CHANGE_LABELS[change.kind] ?? "改动";
  return `${kind} · ${change.summary}`;
}

/** 把台账里的一条介入合成成转录里的一行 Notice Item。 */
export function directiveItem(directive: CompanionDirective): Item {
  return {
    id: `directive:${directive.id}`,
    turnId: directive.taskId ?? "",
    threadId: directive.threadId ?? "",
    type: ItemType.ItemNotice,
    status: ItemStatus.StatusCompleted,
    createdAt: directive.createdAt ?? 0,
    text: directive.narration || directive.text || "",
    meta: { directive },
  };
}

/**
 * 按时间把旁白插进已有转录。找不到更晚的消息就追加到末尾 —— 旁白通常是刚写的，
 * 所以绝大多数时候就落在最后，正好接在她上一句话后面。
 */
export function mergeDirectives(items: Item[], directives: CompanionDirective[]): Item[] {
  if (!directives.length) return items;
  const rows = [...items];
  const ordered = [...directives].sort((a, b) => (a.createdAt ?? 0) - (b.createdAt ?? 0));
  for (const directive of ordered) {
    const at = rows.findIndex((row) => (row.createdAt ?? 0) > (directive.createdAt ?? 0));
    const row = directiveItem(directive);
    if (at === -1) rows.push(row);
    else rows.splice(at, 0, row);
  }
  return rows;
}

export function DirectorNarrationRow({ directive }: { directive: CompanionDirective }) {
  const rejected = directive.status === "rejected";
  const partial = directive.status === "partial";
  const pending = directive.status === "pending";
  const stateClass = rejected
    ? "is-rejected"
    : partial
      ? "is-partial"
      : pending
        ? "is-pending"
        : "is-applied";

  return (
    <div className={`hitk-directive-row ${stateClass}${directive.revertedAt ? " is-reverted" : ""}`}>
      <div className="hitk-directive-line">
        <span className="hitk-directive-mark" aria-hidden="true"><ControlOutlined /></span>
        <span className="hitk-directive-prefix">旁白</span>
        <span className="hitk-directive-text">
          {directive.narration || directive.text || "（没翻译出内容）"}
        </span>
        {directive.revertedAt ? <Tag bordered={false}>已撤销</Tag> : null}
      </div>

      {rejected && directive.reason ? (
        <div className="hitk-directive-reason">
          <StopOutlined />
          <span>{directive.reason}</span>
        </div>
      ) : null}

      {directive.changes?.length ? (
        <div className="hitk-directive-chips">
          {directive.changes.map((change, index) => (
            <Tooltip
              key={`${directive.id}-${change.kind}-${index}`}
              title={change.revertible ? "可以撤销这条" : "时间不会倒流，这条撤不回"}
            >
              <Tag
                bordered={false}
                className={`hitk-directive-chip${change.revertible ? "" : " is-locked"}`}
                icon={change.revertible ? <CheckCircleOutlined /> : <ExclamationCircleOutlined />}
              >
                {changeLabel(change)}
              </Tag>
            </Tooltip>
          ))}
        </div>
      ) : null}

      {!rejected && directive.reason ? (
        <div className="hitk-directive-note">
          <Text type="secondary">{directive.reason}</Text>
        </div>
      ) : null}
    </div>
  );
}
