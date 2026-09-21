// 关于:版本与运行环境。
import { useCallback, useEffect, useState } from "react";
import { Descriptions, Typography } from "antd";
import { appRepository } from "../../../shared/repositories";
import type { Info } from "../../../shared/repositories";

const { Title, Paragraph } = Typography;

export function AboutSettingsPage() {
  const [info, setInfo] = useState<Info | null>(null);

  const load = useCallback(async () => {
    setInfo(await appRepository.info());
  }, []);

  useEffect(() => {
    void load().catch(() => undefined);
  }, [load]);

  return (
    <section className="hitk-settings-panel" aria-labelledby="settings-about-title">
      <header className="hitk-settings-page-heading">
        <div>
          <span className="hitk-settings-section-number">05</span>
          <Title id="settings-about-title" level={3}>
            关于
          </Title>
        </div>
      </header>

      <div className="hitk-settings-data-band">
        <span className="hitk-settings-data-index">HitK</span>
        <div className="hitk-settings-data-copy">
          <Paragraph type="secondary" style={{ margin: "0 0 12px" }}>
            本地优先的 Agentic Search 知识库。资料、索引与会话都留在本机，模型走你
            自己配置的服务商。
          </Paragraph>
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="版本">{info?.version ?? "—"}</Descriptions.Item>
            <Descriptions.Item label="运行环境">
              {info ? `${info.os} / ${info.arch} · ${info.goVersion}` : "—"}
            </Descriptions.Item>
            <Descriptions.Item label="数据目录">{info?.dataDir ?? "—"}</Descriptions.Item>
            <Descriptions.Item label="会话数">{info?.threadCount ?? 0}</Descriptions.Item>
            <Descriptions.Item label="知识库">
              {info ? `${info.kbStats.documents} 篇文档 / ${info.kbStats.chunks} 个分块` : "—"}
            </Descriptions.Item>
          </Descriptions>
        </div>
      </div>
    </section>
  );
}
