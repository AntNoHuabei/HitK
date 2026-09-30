// 哪些指标值得被一眼看到。
//
// 24 项指标平铺出来等于没有重点，所以这里只回答一个问题：现在有什么不对劲。
// 后端 MetricView 不带健康区间，所以阈值在前端定；改这里比改组件好，因为
// 分组折叠和"需要留意"两处都吃这套判断。

/** 值越高越不好的指标；不在表里的指标，值高是好事。 */
export const BAD_WHEN_HIGH = new Set([
  "hunger",
  "fatigue",
  "sleepPressure",
  "cognitiveLoad",
  "stress",
  "anxiety",
  "irritability",
  "loneliness",
  "missing",
]);

// 越过中线多远才算"不对劲"。太灵敏会让面板天天喊话，太钝就永远都平稳。
const HIGH_WATER = 55;
const LOW_WATER = 35;

export interface SalientMetric<T> {
  metric: T;
  /** high = 坏方向上偏高，low = 好方向上偏低。 */
  direction: "high" | "low";
  /** 越线幅度，用来排序。 */
  severity: number;
  /** 一句话读数。 */
  read: string;
}

export function salienceOf<T extends { id: string; value: number }>(
  metric: T,
): SalientMetric<T> | null {
  if (BAD_WHEN_HIGH.has(metric.id)) {
    if (metric.value >= HIGH_WATER) {
      return { metric, direction: "high", severity: metric.value - HIGH_WATER, read: "偏高" };
    }
    return null;
  }
  if (metric.value <= LOW_WATER) {
    return { metric, direction: "low", severity: LOW_WATER - metric.value, read: "偏低" };
  }
  return null;
}

/** 最不对劲的前 N 项，按幅度降序。 */
export function notableMetrics<T extends { id: string; value: number }>(
  metrics: T[],
  limit = 4,
): SalientMetric<T>[] {
  return metrics
    .map(salienceOf)
    .filter((item): item is SalientMetric<T> => item !== null)
    .sort((a, b) => b.severity - a.severity)
    .slice(0, limit);
}
