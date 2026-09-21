// 数据与导出:工作区、数据目录与运行参数。
import { useCallback, useEffect, useState } from "react";
import { App as AntApp, Button, InputNumber, Space, Typography } from "antd";
import { FolderOpenOutlined } from "@ant-design/icons";
import { appRepository, errorText, settingsRepository } from "../../../shared/repositories";
import { DEFAULT_MAX_STEPS, MAX_STEPS_CEILING } from "../../../shared/lib/limits";
import type { AppSettingsLite } from "../../../shared/types/settings";

const { Title, Text, Paragraph } = Typography;

export function DataSettingsPage() {
  const { message } = AntApp.useApp();
  const [settings, setSettings] = useState<AppSettingsLite | null>(null);
  const [maxSteps, setMaxSteps] = useState(DEFAULT_MAX_STEPS);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const next = await settingsRepository.get();
    setSettings(next);
    setMaxSteps(next.maxSteps || DEFAULT_MAX_STEPS);
  }, []);

  useEffect(() => {
    void load().catch(() => undefined);
  }, [load]);

  const save = async () => {
    if (!settings) return;
    setBusy(true);
    try {
      await settingsRepository.saveOptions(
        maxSteps,
        settings.contextLimit,
        settings.embeddingVendor,
        settings.embeddingModel,
      );
      await load();
      message.success("已保存");
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const chooseWorkspace = async () => {
    const dir = await appRepository.pickDirectory("选择工作区");
    if (!dir) return;
    try {
      await appRepository.setWorkspace(dir);
      await load();
      message.success("工作区已切换");
    } catch (err) {
      message.error(errorText(err));
    }
  };

  return (
    <section className="hitk-settings-panel" aria-labelledby="settings-data-title">
      <header className="hitk-settings-page-heading">
        <div>
          <span className="hitk-settings-section-number">04</span>
          <Title id="settings-data-title" level={3}>
            数据与导出
          </Title>
        </div>
      </header>

      <div className="hitk-settings-data-band">
        <span className="hitk-settings-data-index">工作区</span>
        <div className="hitk-settings-data-copy">
          <Text strong>{settings?.workspace || "未设置"}</Text>
          <Paragraph type="secondary" style={{ margin: "4px 0 10px" }}>
            Coding 模式下读写文件与执行命令的根目录。读操作限于此目录内，写操作仍
            会按审批策略向你确认。
          </Paragraph>
          <Space wrap>
            <Button icon={<FolderOpenOutlined />} onClick={() => void chooseWorkspace()}>
              更换工作区
            </Button>
            {settings?.workspace && (
              <Button onClick={() => void appRepository.reveal(settings.workspace)}>在文件管理器中打开</Button>
            )}
          </Space>
        </div>
      </div>

      <div className="hitk-settings-data-band">
        <span className="hitk-settings-data-index">运行</span>
        <div className="hitk-settings-data-copy">
          <Text strong>单轮最大工具步数</Text>
          <Paragraph type="secondary" style={{ margin: "4px 0 10px" }}>
            一轮对话里模型最多可以连续调用多少次工具。调高适合多跳检索，调低可以
            更快拿到结论。
          </Paragraph>
          <Space>
            <InputNumber
              min={1}
              max={MAX_STEPS_CEILING}
              value={maxSteps}
              onChange={(value) => setMaxSteps(value ?? DEFAULT_MAX_STEPS)}
            />
            <Button type="primary" loading={busy} onClick={() => void save()}>
              保存
            </Button>
          </Space>
        </div>
      </div>

      <div className="hitk-settings-data-band">
        <span className="hitk-settings-data-index">位置</span>
        <div className="hitk-settings-data-copy">
          <Text strong>数据目录</Text>
          <Paragraph type="secondary" style={{ margin: "4px 0 10px" }}>
            会话记录、知识库索引与配置都保存在这里。整体拷走这个目录即为完整备份。
          </Paragraph>
          {settings && (
            <Space wrap>
              <Text code>{`%APPDATA%/HitK`}</Text>
              <Button onClick={() => void appRepository.reveal(settings.workspace)} disabled>
                打开数据目录
              </Button>
            </Space>
          )}
        </div>
      </div>
    </section>
  );
}
