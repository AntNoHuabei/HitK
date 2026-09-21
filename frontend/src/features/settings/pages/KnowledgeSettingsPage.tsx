// 知识库设置:检索用的嵌入模型与全量重建索引。
//
// 集合与文档的增删在知识库视图里做,这里只放"检索怎么算"这类全局开关 —— 它们
// 影响的是索引本身,改完必须重建。
import { useCallback, useEffect, useState } from "react";
import { App as AntApp, Button, Select, Space, Tag, Typography } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { errorText, knowledgeRepository, modelRepository, settingsRepository } from "../../../shared/repositories";
import type { ModelOptionLite } from "../../../shared/types/settings";
import type { KnowledgeStatsLite } from "../../../shared/types/knowledge";

const { Title, Text, Paragraph } = Typography;

/** 离线哈希向量不需要联网也不需要 Key,是零配置的默认值。 */
const OFFLINE_EMBEDDER = "";

export function KnowledgeSettingsPage() {
  const { message } = AntApp.useApp();
  const [stats, setStats] = useState<KnowledgeStatsLite | null>(null);
  const [embedder, setEmbedder] = useState("");
  const [options, setOptions] = useState<ModelOptionLite[]>([]);
  const [selected, setSelected] = useState(OFFLINE_EMBEDDER);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const [nextStats, settings, nextOptions, current] = await Promise.all([
      knowledgeRepository.stats(),
      settingsRepository.get(),
      modelRepository.modelOptions(),
      knowledgeRepository.embedderId(),
    ]);
    setStats(nextStats);
    setEmbedder(current);
    setOptions(nextOptions);
    setSelected(
      settings.embeddingVendor && settings.embeddingModel
        ? `${settings.embeddingVendor}::${settings.embeddingModel}`
        : OFFLINE_EMBEDDER,
    );
  }, []);

  useEffect(() => {
    void load().catch(() => undefined);
  }, [load]);

  const applyEmbedder = async (value: string) => {
    setSelected(value);
    const [vendor, ...model] = value.split("::");
    setBusy(true);
    try {
      await settingsRepository.saveOptions(0, 0, vendor, model.join("::"));
      message.success("嵌入模型已更新，请重建索引让已有文档生效");
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const reindex = async () => {
    setBusy(true);
    try {
      await knowledgeRepository.reindex();
      await load();
      message.success("索引已重建");
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="hitk-settings-panel" aria-labelledby="settings-knowledge-title">
      <header className="hitk-settings-page-heading">
        <div>
          <span className="hitk-settings-section-number">02</span>
          <Title id="settings-knowledge-title" level={3}>
            知识库
          </Title>
        </div>
      </header>

      <div className="hitk-settings-data-band">
        <span className="hitk-settings-data-index">统计</span>
        <div className="hitk-settings-data-copy">
          {stats ? (
            <Space size={6} wrap>
              <Tag>{stats.collections} 个集合</Tag>
              <Tag>{stats.documents} 篇文档</Tag>
              <Tag>{stats.chunks} 个分块</Tag>
              <Tag color={stats.indexed ? "green" : "orange"}>
                {stats.indexed ? "索引就绪" : "索引未建立"}
              </Tag>
            </Space>
          ) : (
            <Text type="secondary">读取中…</Text>
          )}
          <Paragraph type="secondary" style={{ margin: "8px 0 0" }}>
            当前生效的嵌入模型：<Text code>{embedder || "离线哈希向量"}</Text>
          </Paragraph>
        </div>
      </div>

      <div className="hitk-settings-data-band">
        <span className="hitk-settings-data-index">检索</span>
        <div className="hitk-settings-data-copy">
          <Text strong>嵌入模型</Text>
          <Paragraph type="secondary" style={{ margin: "4px 0 10px" }}>
            默认使用内置的离线哈希向量：零配置、不联网，适合本地资料。换成厂商的
            embedding 模型可以提升语义召回，但需要 Key，并且换完要重建索引。
          </Paragraph>
          <Space wrap>
            <Select
              style={{ minWidth: 320 }}
              value={selected}
              loading={busy}
              onChange={(value) => void applyEmbedder(String(value))}
              options={[
                { value: OFFLINE_EMBEDDER, label: "离线哈希向量（默认）" },
                ...options.map((option) => ({
                  value: `${option.provider}::${option.model}`,
                  label: `${option.providerName} / ${option.label || option.model}`,
                })),
              ]}
            />
            <Button icon={<ReloadOutlined />} loading={busy} onClick={() => void reindex()}>
              重建索引
            </Button>
          </Space>
        </div>
      </div>
    </section>
  );
}
