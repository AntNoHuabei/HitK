// 指标配色。
//
// Ant Design 的 CSS 变量不保证在 SVG 属性里被求值，所以这里给死颜色，
// 保证进度条填充路径始终可见。
export type MetricTone = "success" | "warn" | "signal";

export function metricToneColor(tone: MetricTone): string {
  if (tone === "warn") return "#d97706";
  if (tone === "signal") return "#1677ff";
  return "#16a34a";
}
