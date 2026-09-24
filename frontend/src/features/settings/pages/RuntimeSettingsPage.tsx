import { useCallback, useEffect, useState } from "react";
import { App as AntApp, Button, Progress, Spin, Typography } from "antd";
import {
  CheckCircleFilled,
  CloudDownloadOutlined,
  DeleteOutlined,
  StopOutlined,
  WarningFilled,
} from "@ant-design/icons";
import { runtimeRepository } from "../../../shared/repositories";
import type { RuntimeStatus, SkillEnvironmentStatus } from "../../../shared/repositories";
import { humanBytes } from "../../../shared/lib/format";
import { onRuntimeProgress } from "../../../shared/wails/events";

const { Text, Title } = Typography;
const IN_PROGRESS = new Set(["downloading", "verifying", "installing"]);

function stateLabel(state?: string) {
  switch (state) {
    case "ready": return "已安装";
    case "downloading": return "下载中";
    case "verifying": return "校验中";
    case "installing": return "安装中";
    case "failed": return "安装失败";
    case "cancelled": return "已取消";
    default: return "未安装";
  }
}

export function RuntimeSettingsPage() {
  const { modal } = AntApp.useApp();
  const [runtimes, setRuntimes] = useState<RuntimeStatus[]>([]);
  const [documentStatus, setDocumentStatus] = useState<SkillEnvironmentStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const [items, skill] = await Promise.all([
      runtimeRepository.list(),
      runtimeRepository.environment("document"),
    ]);
    setRuntimes(items);
    setDocumentStatus(skill);
  }, []);

  useEffect(() => {
    void load().finally(() => setLoading(false));
  }, [load]);

  useEffect(() => onRuntimeProgress((progress) => {
    if (progress.skill === "document") setDocumentStatus(progress.status);
  }), []);

  const initialize = async () => {
    setBusy(true);
    try {
      setDocumentStatus(await runtimeRepository.prepare("document", true));
    } catch (error) {
      setDocumentStatus((current) => ({
        ...(current ?? { skill: "document", updatedAt: Date.now() }),
        state: "failed",
        error: error instanceof Error ? error.message : String(error),
        updatedAt: Date.now(),
      }));
    } finally {
      setBusy(false);
    }
  };

  const cancel = async () => {
    setBusy(true);
    try {
      await runtimeRepository.cancel("document");
    } finally {
      setBusy(false);
      await load();
    }
  };

  const remove = () => {
    modal.confirm({
      title: "移除文档组件？",
      content: "下次处理 PDF、Office 文档或图片时需要重新下载。",
      okText: "移除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        await runtimeRepository.remove("document");
        await load();
      },
    });
  };

  const state = documentStatus?.state ?? "none";
  const active = IN_PROGRESS.has(state);
  const ready = state === "ready";
  const total = documentStatus?.bytesTotal ?? 0;
  const received = documentStatus?.bytesReceived ?? 0;
  const percent = total > 0 ? Math.min(100, Math.round((received / total) * 100)) : 0;

  return <section className="hitk-settings-panel" aria-labelledby="settings-runtime-title">
    <header className="hitk-settings-page-heading"><div><span className="hitk-settings-section-number">03</span><Title id="settings-runtime-title" level={3}>Skill 运行时</Title></div></header>
    {loading ? <div className="hitk-settings-loading"><Spin /></div> : <>
      <div className="hitk-runtime-list" role="list" aria-label="内置运行时">
        {runtimes.map((runtime, index) => <div className="hitk-runtime-row" role="listitem" key={runtime.name}>
          <span className="hitk-runtime-index">{String(index + 1).padStart(2, "0")}</span>
          <div className="hitk-runtime-identity"><Text strong>{runtime.name}</Text><code>{runtime.version}</code></div>
          <Text className="hitk-runtime-path" title={runtime.path}>{runtime.path}</Text>
          <span className={`hitk-runtime-state ${runtime.verified ? "is-ready" : "is-error"}`} title={runtime.message}>
            {runtime.verified ? <CheckCircleFilled /> : <WarningFilled />}
            {runtime.verified ? "已校验" : runtime.available ? "校验失败" : "未安装"}
          </span>
        </div>)}
      </div>

      <div className="hitk-runtime-skill-row">
        <div>
          <Text strong>Document Skill</Text>
          <Text type="secondary">Docling · PP-OCRv6 · 按需安装</Text>
        </div>
        <span className={`hitk-runtime-state ${ready ? "is-ready" : active ? "is-pending" : "is-error"}`}>
          {ready ? <CheckCircleFilled /> : active ? <CloudDownloadOutlined /> : <WarningFilled />}
          {stateLabel(state)}
        </span>
        {active
          ? <Button type="text" danger icon={<StopOutlined />} disabled={busy} onClick={() => void cancel()}>取消</Button>
          : ready
            ? <Button type="text" danger icon={<DeleteOutlined />} disabled={busy} onClick={remove}>移除</Button>
            : <Button type="text" icon={<CloudDownloadOutlined />} disabled={busy} onClick={() => void initialize()}>{state === "failed" || state === "cancelled" ? "重试" : "初始化"}</Button>}
      </div>

      {active && <div className="hitk-runtime-transfer">
        <Progress percent={percent} showInfo={false} strokeColor="var(--hitk-signal)" />
        <div className="hitk-runtime-transfer-meta">
          <Text>{total > 0 ? `${humanBytes(received)} / ${humanBytes(total)}` : humanBytes(received)}</Text>
          <Text type="secondary">{documentStatus?.speedBytesPerSecond ? `${humanBytes(documentStatus.speedBytesPerSecond)}/s` : "正在连接"}</Text>
        </div>
      </div>}
      {!active && documentStatus?.source && <div className="hitk-runtime-transfer-meta">
        <Text type="secondary">{documentStatus.source}</Text>
        <Text>{documentStatus.componentVersion ? `版本 ${documentStatus.componentVersion}` : ""}{total > 0 ? ` · ${humanBytes(total)}` : ""}</Text>
      </div>}
      {documentStatus?.error && <Text className="hitk-runtime-error">{documentStatus.error}</Text>}
    </>}
  </section>;
}
