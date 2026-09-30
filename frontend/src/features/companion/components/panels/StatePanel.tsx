// 状态面板：按维度分组的内部指标读数。
import { Progress, Typography } from "antd";
import type {
  CompanionMetricView,
  CompanionOverview,
} from "../../../../shared/repositories";
import { metricToneColor } from "../../model/metricTone";

const { Text } = Typography;

const GROUP_ORDER = ["physical", "cognitive", "emotion", "social", "relationship"];

const GROUP_LABELS: Record<string, string> = {
  physical: "生理状态",
  cognitive: "认知与动机",
  emotion: "情绪状态",
  social: "社交状态",
  relationship: "亲密关系",
};

// 值越高越不好的指标，决定进度条往警告色还是健康色走。
const BAD_WHEN_HIGH = [
  "hunger",
  "fatigue",
  "sleepPressure",
  "cognitiveLoad",
  "stress",
  "anxiety",
  "irritability",
  "loneliness",
  "missing",
];

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
  const groups = GROUP_ORDER.map((group) => ({
    group,
    title: GROUP_LABELS[group] ?? group,
    rows: metrics.filter((metric) => metric.group === group),
  }));
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
      {groups.map((group) => (
        <section key={group.group} className="hitk-companion-state-group">
          <h4>{group.title}</h4>
          {group.rows.map((metric) => {
            const tone = metric.group === "relationship" ? "signal" : BAD_WHEN_HIGH.includes(metric.id) ? "warn" : "success";
            const percent = ((metric.value - metric.min) / Math.max(1, metric.max - metric.min)) * 100;
            return <div key={metric.id} className="hitk-companion-state-row">
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
