// 顶栏上的一根小进度条（精力 / 压力 / 想念）。
import { Progress, Tooltip } from "antd";
import { metricToneColor } from "../../model/metricTone";

export function StatusMeter({
  label,
  value,
  tone = "normal",
}: {
  label: string;
  value: number;
  tone?: "normal" | "warn" | "signal";
}) {
  const stroke = metricToneColor(tone === "warn" ? "warn" : tone === "signal" ? "signal" : "success");
  return (
    <Tooltip title={`${label} ${value}/100`}>
      <span className="hitk-companion-meter">
        <span>{label}</span>
        <Progress
          percent={value}
          showInfo={false}
          size="small"
          strokeColor={stroke}
          trailColor="color-mix(in srgb, var(--hitk-divider) 72%, transparent)"
        />
      </span>
    </Tooltip>
  );
}
