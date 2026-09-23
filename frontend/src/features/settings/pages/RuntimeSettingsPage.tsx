import { useCallback, useEffect, useState } from "react";
import { Button, Spin, Typography } from "antd";
import { CheckCircleFilled, ReloadOutlined, WarningFilled } from "@ant-design/icons";
import { runtimeRepository } from "../../../shared/repositories";
import type { RuntimeStatus, SkillEnvironmentStatus } from "../../../shared/repositories";

const { Text, Title } = Typography;

export function RuntimeSettingsPage() {
  const [runtimes, setRuntimes] = useState<RuntimeStatus[]>([]);
  const [documentStatus, setDocumentStatus] = useState<SkillEnvironmentStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [repairing, setRepairing] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [items, skill] = await Promise.all([runtimeRepository.list(), runtimeRepository.environment("document")]);
      setRuntimes(items);
      setDocumentStatus(skill);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const repair = async () => {
    setRepairing(true);
    try {
      setDocumentStatus(await runtimeRepository.repair("document", false));
      setRuntimes(await runtimeRepository.list());
    } catch (error) {
      setDocumentStatus({
        skill: "document",
        state: "failed",
        error: error instanceof Error ? error.message : String(error),
        updatedAt: Date.now(),
      });
    } finally {
      setRepairing(false);
    }
  };

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
        <div><Text strong>Document Skill</Text><Text type="secondary">Docling · PP-OCRv6 · CPU</Text></div>
        <span className={`hitk-runtime-state ${documentStatus?.state === "ready" ? "is-ready" : "is-error"}`}>{documentStatus?.state === "ready" ? <CheckCircleFilled /> : <WarningFilled />}{documentStatus?.state === "ready" ? "可用" : "不可用"}</span>
        <Button type="text" icon={<ReloadOutlined spin={repairing} />} disabled={repairing} onClick={() => void repair()}>重新校验</Button>
      </div>
      {documentStatus?.error && <Text className="hitk-runtime-error">{documentStatus.error}</Text>}
    </>}
  </section>;
}
