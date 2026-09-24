import { useCallback, useEffect, useState } from "react";
import { Button, Modal, Progress, Typography } from "antd";
import { CloudDownloadOutlined, StopOutlined } from "@ant-design/icons";
import { runtimeRepository } from "../../../shared/repositories";
import type { SkillEnvironmentStatus } from "../../../shared/repositories";
import { humanBytes } from "../../../shared/lib/format";
import { onRuntimeProgress } from "../../../shared/wails/events";

const { Text, Paragraph } = Typography;

const IN_PROGRESS = new Set(["downloading", "verifying", "installing"]);

export function RuntimeInitializationModal() {
  const [status, setStatus] = useState<SkillEnvironmentStatus | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => onRuntimeProgress((progress) => {
    setStatus(progress.status);
  }), []);

  const start = useCallback(async () => {
    setBusy(true);
    try {
      const skill = status?.skill || "document";
      setStatus(skill === "document"
        ? await runtimeRepository.prepare(skill, true)
        : await runtimeRepository.prepareResolved(skill, true));
    } catch (error) {
      setStatus((current) => ({
        ...(current ?? { skill: "document", updatedAt: Date.now() }),
        state: "failed",
        error: error instanceof Error ? error.message : String(error),
        updatedAt: Date.now(),
      }));
    } finally {
      setBusy(false);
    }
  }, [status?.skill]);

  const cancel = useCallback(async () => {
    setBusy(true);
    try {
      await runtimeRepository.cancel(status?.skill || "document");
      setStatus(null);
    } finally {
      setBusy(false);
    }
  }, [status?.skill]);

  if (!status || status.state === "ready" || status.state === "none") return null;

  const active = IN_PROGRESS.has(status.state);
  const waiting = status.state === "approval_required";
  const failed = status.state === "failed" || status.state === "cancelled";
  const total = status.bytesTotal ?? 0;
  const received = status.bytesReceived ?? 0;
  const percent = total > 0 ? Math.min(100, Math.round((received / total) * 100)) : 0;

  return <Modal
    open
    centered
    closable={!waiting && !active}
    maskClosable={false}
    keyboard={false}
    title={waiting ? "初始化 Skill" : active ? "正在初始化 Skill" : "Skill 初始化失败"}
    onCancel={() => void cancel()}
    footer={[
      waiting && <Button key="cancel" disabled={busy} onClick={() => void cancel()}>暂不下载</Button>,
      active && <Button key="cancel" danger icon={<StopOutlined />} loading={busy} onClick={() => void cancel()}>取消下载</Button>,
      failed && <Button key="close" onClick={() => setStatus(null)}>关闭</Button>,
      (waiting || failed) && <Button key="start" type="primary" icon={<CloudDownloadOutlined />} loading={busy} onClick={() => void start()}>{failed ? "重试" : "下载并继续"}</Button>,
    ].filter(Boolean)}
  >
    <Paragraph>
      {waiting && `运行 ${status.skill} 需要准备隔离依赖。确认后只在 HitK 管理目录中安装，不会修改系统 Python 或 Node。`}
      {active && "下载和安装期间可以继续使用其他功能，取消不会影响已下载的临时数据。"}
      {failed && "组件尚未安装完成。可以重试，已下载的部分会继续使用。"}
    </Paragraph>
    {active && <>
      <Progress percent={percent} showInfo={false} strokeColor="var(--hitk-signal)" />
      <div className="hitk-runtime-transfer-meta">
        <Text>{total > 0 ? `${humanBytes(received)} / ${humanBytes(total)}` : humanBytes(received)}</Text>
        <Text type="secondary">{status.speedBytesPerSecond ? `${humanBytes(status.speedBytesPerSecond)}/s` : "正在连接"}</Text>
      </div>
    </>}
    {waiting && <div className="hitk-runtime-transfer-meta">
      <Text type="secondary">{status.source || "ModelScope"}</Text>
      <Text>{status.componentVersion ? `版本 ${status.componentVersion}` : ""}{total > 0 ? ` · ${humanBytes(total)}` : ""}</Text>
    </div>}
    {failed && status.error && <Text className="hitk-runtime-error">{status.error}</Text>}
  </Modal>;
}
