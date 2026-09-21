// UI 通用的格式化工具。

/** formatDate 把毫秒时间戳渲染成 YYYY-MM-DD。 */
export function formatDate(ms: number): string {
  if (!ms) return "-";
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(
    d.getDate(),
  ).padStart(2, "0")}`;
}

/** relativeTime 渲染「刚刚 / 5 分钟前 / 3 天前 / 日期」。 */
export function relativeTime(ms: number): string {
  if (!ms) return "";
  const diff = Date.now() - ms;
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (diff < minute) return "刚刚";
  if (diff < hour) return `${Math.floor(diff / minute)} 分钟前`;
  if (diff < day) return `${Math.floor(diff / hour)} 小时前`;
  if (diff < 7 * day) return `${Math.floor(diff / day)} 天前`;
  return formatDate(ms).slice(5);
}

/** humanBytes 把字节数渲染成可读单位。 */
export function humanBytes(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

/**
 * docAccent 给一份文档挑一个短标签与颜色。
 *
 * 文档的「类型」有三种来源(kind / mime / 扩展名),逐个判断会把逻辑散到各处,
 * 所以统一在这里裁决。
 */
export function docAccent(doc: { mime?: string; source?: string; kind?: string }): {
  label: string;
  color: string;
} {
  if (doc.kind === "url") return { label: "WEB", color: "#4c8dff" };
  if (doc.kind === "text") return { label: "TXT", color: "#8c8c8c" };
  const ext = (doc.source ?? "").split(".").pop()?.toLowerCase() ?? "";
  switch (ext) {
    case "md":
    case "markdown":
    case "mdx":
      return { label: "MD", color: "#4c8dff" };
    case "pdf":
      return { label: "PDF", color: "#e5484d" };
    case "docx":
      return { label: "DOC", color: "#2b6cd4" };
    case "csv":
    case "tsv":
      return { label: "CSV", color: "#1f9d55" };
    case "json":
    case "jsonl":
      return { label: "JSON", color: "#c77d1a" };
    case "yaml":
    case "yml":
      return { label: "YML", color: "#8250df" };
    case "html":
    case "htm":
      return { label: "HTML", color: "#e34f26" };
  }
  return { label: ext ? ext.slice(0, 4).toUpperCase() : "TXT", color: "#6b7280" };
}

/** shortSource 把长路径压成「末两级」,列表里更好读。 */
export function shortSource(source: string): string {
  if (!source) return "";
  if (/^https?:\/\//i.test(source)) {
    try {
      const url = new URL(source);
      return url.host + (url.pathname === "/" ? "" : url.pathname);
    } catch {
      return source;
    }
  }
  const parts = source.replace(/\\/g, "/").split("/").filter(Boolean);
  return parts.length <= 2 ? parts.join("/") : parts.slice(-2).join("/");
}
