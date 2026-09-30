// 状态面板：先看"现在"和"需要留意"，再按需展开具体分组。
//
// 24 项指标平铺会把真正不对劲的那几项埋掉，所以默认只展开有异常的分组。
import { useState } from "react";
import { Progress, Typography } from "antd";
import { DownOutlined } from "@ant-design/icons";
import type {
  CompanionMetricView,
  CompanionOverview,
} from "../../../../shared/repositories";
import { metricToneColor } from "../../model/metricTone";
import { BAD_WHEN_HIGH, notableMetrics } from "../../model/metricSalience";

const { Text } = Typography;

const GROUP_ORDER = ["physical", "cognitive", "emotion", "social", "relationship"];

const GROUP_LABELS: Record<string, string> = {
  physical: "生理状态",
  cognitive: "认知与动机",
  emotion: "情绪状态",
  social: "社交状态",
  relationship: "亲密关系",
};

// 配色沿用 salience 的同一份「值高=不好」名单，避免进度条和「需要留意」各说一套。
const toneFor = (metric: CompanionMetricView) => {
  if (metric.group === "relationship") return "signal";
  return BAD_WHEN_HIGH.has(metric.id) ? "warn" : "success";
};

// 「需要留意」要点到下面那一行，两边得用同一个 id。
const metricRowId = (metricId: string) => `hitk-companion-metric-${metricId}`;

export function StatePanel({
  metrics,
  derived,
  activity,
  activityEndsAt,
  clock,
}: {
  metrics: CompanionMetricView[];
  derived: CompanionOverview["derived"];
  activity: string;
  activityEndsAt: number;
  clock: CompanionOverview["clock"];
}) {
  const [override, setOverride] = useState<Record<string, boolean>>({});
  const notable = notableMetrics(metrics);
  const notableGroups = new Set(notable.map((item) => item.metric.group));

  const groups = GROUP_ORDER.map((group, index) => {
    const rows = metrics.filter((metric) => metric.group === group);
    // 手动点过的以手动为准；否则只展开有异常的组，一组都没有就展开第一组。
    const collapsed =
      override[group] ?? (notableGroups.size > 0 ? !notableGroups.has(group) : index > 0);
    return {
      group,
      title: GROUP_LABELS[group] ?? group,
      rows,
      flagged: notableGroups.has(group),
      collapsed,
    };
  }).filter((group) => group.rows.length > 0);

  return (
    <div className="hitk-companion-state-panel">
      <div className="hitk-companion-state-now">
        <Text type="secondary">
          {clock.date} · {clock.weekday} · {clock.season} · {clock.timeOfDay}
        </Text>
        <Text strong>{activity}</Text>
        <Text type="secondary">
          {activityEndsAt > Date.now()
            ? `预计 ${new Date(activityEndsAt).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })} 切换`
            : "正在确认接下来的安排"}
        </Text>
        <Text type="secondary">
          专注 {derived.focus.toFixed(1)} · 平静 {derived.calm.toFixed(1)} · 倦怠风险 {derived.burnoutRisk.toFixed(1)}
        </Text>
      </div>

      <div className="hitk-companion-state-notable">
        <h4>需要留意</h4>
        {notable.length === 0 ? (
          <Text type="secondary" className="hitk-companion-state-calm">
            都还平稳
          </Text>
        ) : (
          notable.map((item) => (
            // 保证分组是开的，再把那条指标滚进视野；面板一长，光靠展开找不到它。
            <button
              key={item.metric.id}
              type="button"
              className="hitk-companion-state-notable-row"
              title={`定位到${item.metric.label}`}
              onClick={() => {
                setOverride((current) => ({ ...current, [item.metric.group]: false }));
                requestAnimationFrame(() => {
                  document
                    .getElementById(metricRowId(item.metric.id))
                    ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
                });
              }}
            >
              <span>{item.metric.label}</span>
              <b className={item.direction === "high" ? "is-warn" : "is-low"}>
                {item.read} {item.metric.value.toFixed(1)}
              </b>
            </button>
          ))
        )}
      </div>

      {groups.map((group) => (
        <section key={group.group} className="hitk-companion-state-group">
          <button
            type="button"
            className="hitk-companion-state-group-head"
            aria-expanded={!group.collapsed}
            onClick={() => setOverride((current) => ({ ...current, [group.group]: !group.collapsed }))}
          >
            <span>{group.title}</span>
            {group.flagged && <span className="hitk-companion-state-flag">留意</span>}
            <span className="hitk-companion-state-count">{group.rows.length}</span>
            <DownOutlined className="hitk-companion-state-chevron" />
          </button>
          {group.collapsed ? null : group.rows.map((metric) => {
            const tone = toneFor(metric);
            const percent = ((metric.value - metric.min) / Math.max(1, metric.max - metric.min)) * 100;
            return <div key={metric.id} id={metricRowId(metric.id)} className="hitk-companion-state-row">
              <span>{metric.label}</span>
              <Progress
                percent={Math.max(0, Math.min(100, percent))}
                showInfo={false}
                size="small"
                strokeColor={metricToneColor(tone)}
                trailColor="color-mix(in srgb, var(--hitk-divider) 72%, transparent)"
              />
              <b title={metric.trend}>
                {metric.value.toFixed(1)}
                <span className={`is-${metric.trend}`}>
                  {metric.trend === "rising" ? "↑" : metric.trend === "falling" ? "↓" : "·"}
                </span>
              </b>
            </div>;
          })}
        </section>
      ))}
    </div>
  );
}
