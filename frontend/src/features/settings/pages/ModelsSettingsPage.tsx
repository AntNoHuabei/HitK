// 模型服务商设置页。
//
// 布局沿用 BlankMind:左侧分区导航在父级,这里是分区内容 —— 顶部一行"模式默认
// 模型",下面是每个服务商一张卡片,卡片里是该服务商的可用模型开关。
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { App as AntApp, Button, Empty, Popconfirm, Select, Spin, Switch, Tooltip, Typography } from "antd";
import { CheckOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from "@ant-design/icons";
import { errorText, modelRepository } from "../../../shared/repositories";
import type { ModeDefaultsLite, ModelOptionLite, ProviderLite } from "../../../shared/types/settings";
import { useProviderController } from "../providerController";
import { ProviderFormModal } from "../components/ProviderFormModal";

const { Title, Text } = Typography;

/** 模式默认模型的下拉值用的组合键。 */
const keyOf = (provider: string, model: string) => `${provider}::${model}`;

export function ModelsSettingsPage() {
  const { message } = AntApp.useApp();
  const controller = useProviderController();
  const [editing, setEditing] = useState<ProviderLite | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [defaults, setDefaults] = useState<ModeDefaultsLite | null>(null);
  const [options, setOptions] = useState<ModelOptionLite[]>([]);

  const loadDefaults = useCallback(async () => {
    const [nextDefaults, nextOptions] = await Promise.all([
      modelRepository.modeDefaults(),
      modelRepository.modelOptions(),
    ]);
    setDefaults(nextDefaults);
    setOptions(nextOptions);
  }, []);

  useEffect(() => {
    void loadDefaults().catch(() => undefined);
  }, [loadDefaults, controller.providers]);

  useEffect(() => {
    if (controller.error) message.error(`加载失败:${errorText(controller.error)}`);
  }, [controller.error, message]);

  const run = async (action: () => Promise<void>, success?: string) => {
    try {
      await action();
      await loadDefaults();
      if (success) message.success(success);
    } catch (err) {
      message.error(errorText(err));
    }
  };

  const profileOptions = useMemo(
    () =>
      options.map((option) => ({
        value: keyOf(option.provider, option.model),
        label: `${option.providerName} / ${option.label || option.model}`,
      })),
    [options],
  );

  const enabledOf = (provider: ProviderLite, model: string) =>
    provider.models.some((item) => item.model === model);

  /** 每一行:一个目录预设模型,或一个目录外的已启用模型。 */
  const renderModelRow = (
    provider: ProviderLite,
    model: string,
    label: string,
    custom: boolean,
    vision: boolean,
  ): ReactNode => {
    const enabled = enabledOf(provider, model);
    const current = enabled && provider.model === model;
    return (
      <div className="hitk-settings-model-row" key={model}>
        <div className="hitk-settings-model-main">
          <Switch
            size="small"
            checked={enabled}
            aria-label={`${enabled ? "停用" : "启用"} ${label}`}
            onChange={(value) =>
              void run(() =>
                value
                  ? controller.enableModel(provider.id, model, label, custom)
                  : controller.disableModel(provider.id, model),
              )
            }
          />
          <div className="hitk-settings-model-label">
            <Text ellipsis={{ tooltip: label }}>{label}</Text>
            {custom && <span className="hitk-settings-model-kind">自定义</span>}
            {vision && <span className="hitk-settings-model-kind">支持图片</span>}
          </div>
        </div>
        <div className="hitk-settings-model-state">
          {enabled &&
            (current ? (
              <span className="hitk-settings-current-model">
                <CheckOutlined /> 当前
              </span>
            ) : (
              <Button
                type="text"
                size="small"
                onClick={() => void run(() => controller.setCurrent(provider.id, model))}
              >
                设为当前
              </Button>
            ))}
        </div>
      </div>
    );
  };

  /** 该服务商的可用模型 = 内置目录预设 + 用户已启用的目录外模型。 */
  const renderProviderModels = (provider: ProviderLite) => {
    const entry = controller.catalogFor(provider);
    const rows: ReactNode[] = [];
    const enabledByID = new Map(provider.models.map((item) => [item.model, item]));
    for (const preset of entry?.models ?? []) {
      if (preset.embedding) continue;
      const enabledEntry = enabledByID.get(preset.id);
      rows.push(
        renderModelRow(
          provider,
          preset.id,
          preset.label,
          false,
          enabledEntry?.vision ?? preset.vision,
        ),
      );
    }
    for (const model of provider.models) {
      if (entry?.models.some((preset) => preset.id === model.model)) continue;
      rows.push(renderModelRow(provider, model.model, model.label || model.model, model.custom, model.vision));
    }
    if (rows.length === 0) {
      rows.push(
        <Text type="secondary" key="empty" className="hitk-settings-model-empty">
          该服务商没有预设模型，点“编辑”获取模型列表或手动添加。
        </Text>,
      );
    }
    return (
      <div className="hitk-settings-model-list">
        <div className="hitk-settings-model-list-head">
          <span>可用模型</span>
          <span>使用状态</span>
        </div>
        {rows}
      </div>
    );
  };

  return (
    <section className="hitk-settings-panel" aria-labelledby="settings-models-title">
      <header className="hitk-settings-page-heading">
        <div>
          <span className="hitk-settings-section-number">01</span>
          <Title id="settings-models-title" level={3}>
            模型服务商
          </Title>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => {
            setEditing(null);
            setModalOpen(true);
          }}
        >
          添加服务商
        </Button>
      </header>

      {defaults && profileOptions.length > 0 && (
        <div className="hitk-settings-profile-defaults">
          <div>
            <Text strong>模式默认模型</Text>
            <Text type="secondary">新建对话会分别继承这两项设置</Text>
          </div>
          {(["work", "coding"] as const).map((profile) => (
            <label key={profile}>
              <span>{profile === "work" ? "Work" : "Coding"}</span>
              <Select
                aria-label={`${profile} 默认模型`}
                style={{ minWidth: 260 }}
                placeholder="未设置，选择启用中的模型"
                // 空的选择必须传 undefined:antd 会把不在选项里的 value 原样显示出来,
                // 于是「未设置」会变成字面的 `::`。
                value={
                  defaults[profile].provider
                    ? keyOf(defaults[profile].provider, defaults[profile].model)
                    : undefined
                }
                options={profileOptions}
                onChange={(value) => {
                  const [provider, ...model] = String(value).split("::");
                  void run(
                    () => modelRepository.setModeDefault(profile, provider, model.join("::")),
                    `${profile === "work" ? "Work" : "Coding"} 默认模型已更新`,
                  );
                }}
              />
            </label>
          ))}
        </div>
      )}

      {controller.loading ? (
        <div className="hitk-settings-loading">
          <Spin />
        </div>
      ) : controller.providers.length === 0 ? (
        <div className="hitk-settings-empty">
          <Empty description="还没有配置模型服务商">
            <Button
              type="primary"
              onClick={() => {
                setEditing(null);
                setModalOpen(true);
              }}
            >
              立即配置
            </Button>
          </Empty>
        </div>
      ) : (
        <div className="hitk-settings-provider-list">
          {controller.providers.map((provider, index) => (
            <article className="hitk-settings-provider" key={provider.id}>
              <div className="hitk-settings-provider-index">{String(index + 1).padStart(2, "0")}</div>
              <div className="hitk-settings-provider-body">
                <header className="hitk-settings-provider-head">
                  <div className="hitk-settings-provider-identity">
                    <Text strong ellipsis={{ tooltip: provider.name }}>
                      {provider.name}
                    </Text>
                    {provider.custom && <span className="hitk-settings-provider-default">自定义</span>}
                    {!provider.ready && <span className="hitk-settings-model-kind">未就绪</span>}
                  </div>
                  <dl className="hitk-settings-provider-meta">
                    <div>
                      <dt>当前模型</dt>
                      <dd title={provider.model}>{provider.model || "未设置"}</dd>
                    </div>
                    <div>
                      <dt>服务地址</dt>
                      <dd title={provider.baseUrl}>{provider.baseUrl}</dd>
                    </div>
                  </dl>
                  <div className="hitk-settings-provider-actions">
                    <Tooltip title="编辑服务商">
                      <Button
                        type="text"
                        icon={<EditOutlined />}
                        aria-label={`编辑 ${provider.name}`}
                        onClick={() => {
                          setEditing(provider);
                          setModalOpen(true);
                        }}
                      />
                    </Tooltip>
                    <Popconfirm
                      title={`删除 ${provider.name}？`}
                      description="已启用的模型与模式默认选择会一并移除。"
                      onConfirm={() =>
                        void run(() => controller.deleteProvider(provider.id), "已删除")
                      }
                    >
                      <Tooltip title="删除服务商">
                        <Button
                          type="text"
                          danger
                          icon={<DeleteOutlined />}
                          aria-label={`删除 ${provider.name}`}
                        />
                      </Tooltip>
                    </Popconfirm>
                  </div>
                </header>
                {renderProviderModels(provider)}
              </div>
            </article>
          ))}
        </div>
      )}

      <ProviderFormModal
        open={modalOpen}
        catalog={controller.catalog}
        existing={editing}
        onCancel={() => setModalOpen(false)}
        onSaved={() => {
          setModalOpen(false);
          void controller.reload().catch((err) => message.error(errorText(err)));
          void loadDefaults().catch(() => undefined);
        }}
      />
    </section>
  );
}
