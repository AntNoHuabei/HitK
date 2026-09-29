// 服务商配置弹窗。
//
// 内置服务商在目录里有预设模型,用户只需填 Key 再逐个开关;自定义服务商没有
// 目录,所以必须"获取模型列表"或手输模型名——这就是两类服务商在界面上的差别。
import { useEffect, useMemo, useState } from "react";
import {
  App as AntApp,
  Button,
  Checkbox,
  Flex,
  Form,
  Input,
  Modal,
  Radio,
  Select,
  Space,
  Switch,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { DeleteOutlined, PlusOutlined, ReloadOutlined } from "@ant-design/icons";
import { errorText, modelRepository } from "../../../shared/repositories";
import type {
  CatalogModelLite,
  CatalogProviderLite,
  DiscoveredModelLite,
  ProviderInputLite,
  ProviderLite,
  ProviderModelLite,
} from "../../../shared/types/settings";

// 自定义服务商在表单里的哨兵值。提交时必须转成空 catalogId 才走后端的自定义分支,
// 所以这里不能直接用空串:antd 的 required 校验会把空串判成“未填”。
const CUSTOM_KIND = "custom";

/** 自定义服务商要选一个线协议族;内置服务商的协议由目录决定。 */
const WIRE_KINDS = [
  { value: "openai", label: "OpenAI 兼容" },
  { value: "anthropic", label: "Anthropic Messages" },
  { value: "gemini", label: "Google Gemini" },
  { value: "ollama", label: "Ollama（本地）" },
];

interface Props {
  open: boolean;
  /** 内置目录,用于"服务商类型"下拉与预设模型。 */
  catalog: CatalogProviderLite[];
  /** 编辑时传入现有服务商;新增时为 null。 */
  existing: ProviderLite | null;
  onCancel: () => void;
  onSaved: () => void;
}

interface FormValues {
  catalogId: string;
  wireKind: string;
  name: string;
  baseUrl: string;
  apiKey: string;
}

export function ProviderFormModal({ open, catalog, existing, onCancel, onSaved }: Props) {
  const [form] = Form.useForm<FormValues>();
  const { message } = AntApp.useApp();
  const [enabled, setEnabled] = useState<ProviderModelLite[]>([]);
  const [current, setCurrent] = useState("");
  const [remote, setRemote] = useState<DiscoveredModelLite[]>([]);
  const [customName, setCustomName] = useState("");
  const [fetching, setFetching] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  const catalogId = Form.useWatch("catalogId", form) ?? CUSTOM_KIND;
  const isCustom = catalogId === CUSTOM_KIND;
  const entry = useMemo(() => catalog.find((item) => item.kind === catalogId), [catalog, catalogId]);
  const presets = useMemo(
    () => (entry?.models ?? []).filter((model) => !model.embedding),
    [entry],
  );

  // 目录自带一条“自定义”项,这里去重后统一由 CUSTOM_KIND 表达,下拉里只留一个入口。
  const typeOptions = useMemo(
    () => [
      ...catalog
        .filter((item) => item.kind !== CUSTOM_KIND)
        .map((item) => ({ value: item.kind, label: item.label })),
      { value: CUSTOM_KIND, label: "自定义（OpenAI 兼容）" },
    ],
    [catalog],
  );

  // 打开时初始化:编辑回填已启用模型,新增用首个目录项的默认模型预置一个。
  useEffect(() => {
    if (!open) return;
    setRemote([]);
    setCustomName("");
    if (existing) {
      form.setFieldsValue({
        catalogId: existing.custom ? CUSTOM_KIND : existing.kind,
        wireKind: existing.custom ? wireKindOf(existing) : "openai",
        name: existing.name,
        baseUrl: existing.baseUrl,
        apiKey: "",
      });
      setEnabled(existing.models);
      setCurrent(existing.model);
      return;
    }
    const first = catalog[0];
    form.setFieldsValue({
      catalogId: first?.kind ?? CUSTOM_KIND,
      wireKind: "openai",
      name: first?.label ?? "",
      baseUrl: first?.baseUrl ?? "",
      apiKey: "",
    });
    const preset = first?.models.find((model) => !model.embedding);
    setEnabled(preset ? [toEnabled(preset)] : []);
    setCurrent(preset?.id ?? "");
    // catalog 只在首次加载后变化,重新打开时不应覆盖用户的当前输入。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, existing]);

  const applyCatalog = (kind: string) => {
    const found = catalog.find((item) => item.kind === kind);
    if (!found) {
      form.setFieldsValue({ name: "自定义服务商", baseUrl: "", apiKey: "" });
      setEnabled([]);
      setCurrent("");
      setRemote([]);
      return;
    }
    form.setFieldsValue({ name: found.label, baseUrl: found.baseUrl, apiKey: "" });
    const preset = found.models.find((model) => !model.embedding);
    setEnabled(preset ? [toEnabled(preset)] : []);
    setCurrent(preset?.id ?? "");
    setRemote([]);
  };

  /** currentInput 组装一份提交用的配置,保存与测试共用。 */
  const currentInput = (): ProviderInputLite => {
    const v = form.getFieldsValue(true) as FormValues;
    return {
      id: existing?.id ?? "",
      // 后端以空 catalogId 判定“自定义服务商”,哨兵值不能原样提交。
      catalogId: isCustom ? "" : (v.catalogId ?? ""),
      name: (v.name ?? "").trim(),
      kind: isCustom ? v.wireKind : "",
      baseUrl: (v.baseUrl ?? "").trim(),
      apiKey: (v.apiKey ?? "").trim(),
      model: current,
      models: enabled,
    };
  };

  const togglePreset = (preset: CatalogModelLite, on: boolean) => {
    if (!on) {
      const rest = enabled.filter((model) => model.model !== preset.id);
      if (rest.length === 0) {
        message.warning("至少保留一个可用模型");
        return;
      }
      setEnabled(rest);
      if (current === preset.id) setCurrent(rest[0].model);
      return;
    }
    setEnabled((prev) =>
      prev.some((model) => model.model === preset.id) ? prev : [...prev, toEnabled(preset)],
    );
    if (!current) setCurrent(preset.id);
  };

  const addModel = (id: string, label: string, custom: boolean, vision: boolean) => {
    const name = id.trim();
    if (!name) {
      message.warning("请先选择或输入模型名");
      return;
    }
    if (enabled.some((model) => model.model === name)) {
      message.warning("该模型已启用");
      return;
    }
    setEnabled((prev) => [
      ...prev,
      { model: name, label: label || name, custom, reasoning: false, vision, tools: true, context: 0 },
    ]);
    if (!current) setCurrent(name);
    setCustomName("");
  };

  const removeModel = (model: string) => {
    const rest = enabled.filter((item) => item.model !== model);
    if (rest.length === 0) {
      message.warning("至少保留一个可用模型");
      return;
    }
    setEnabled(rest);
    if (current === model) setCurrent(rest[0].model);
  };

  const fetchModels = async () => {
    setFetching(true);
    try {
      const discovered = await modelRepository.discoverModels(currentInput());
      setRemote(discovered);
      message.success(`获取到 ${discovered.length} 个模型`);
    } catch (err) {
      message.error(`获取模型列表失败:${errorText(err)}`);
    } finally {
      setFetching(false);
    }
  };

  const testConnection = async () => {
    setTesting(true);
    try {
      const result = await modelRepository.testProvider(currentInput());
      if (result.ok) {
        message.success(`连接成功（${result.latencyMs}ms）：${result.reply || result.message}`);
      } else {
        message.error(`连接失败:${result.message}`);
      }
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setTesting(false);
    }
  };

  const save = async () => {
    try {
      const values = await form.validateFields();
      if (enabled.length === 0) {
        message.warning("请至少启用一个模型");
        return;
      }
      setSaving(true);
      await modelRepository.saveProvider({ ...currentInput(), ...values });
      message.success("已保存服务商配置");
      onSaved();
    } catch (err) {
      // antd 的表单校验失败不是错误,只有后端拒绝才需要提示。
      if (err && typeof err === "object" && "errorFields" in err) return;
      message.error(errorText(err));
    } finally {
      setSaving(false);
    }
  };

  const presetIDs = new Set(presets.map((preset) => preset.id));
  const enabledIDs = new Set(enabled.map((model) => model.model));
  const extras = enabled.filter((model) => !presetIDs.has(model.model));
  const addableRemote = remote.filter(
    (model) => !enabledIDs.has(model.id) && !presetIDs.has(model.id),
  );

  return (
    <Modal
      open={open}
      title={existing ? "编辑模型服务商" : "配置模型服务商"}
      width={640}
      destroyOnHidden
      onCancel={onCancel}
      style={{ top: "4vh" }}
      styles={{ body: { maxHeight: "calc(92vh - 170px)", overflowY: "auto" } }}
      footer={
        <Space>
          <Button onClick={onCancel}>取消</Button>
          <Button loading={testing} onClick={() => void testConnection()}>
            测试连接
          </Button>
          <Button type="primary" loading={saving} onClick={() => void save()}>
            保存
          </Button>
        </Space>
      }
    >
      <Form<FormValues> form={form} layout="vertical">
        <Form.Item
          label="服务商类型"
          name="catalogId"
          rules={[{ required: true, message: "请选择服务商类型" }]}
        >
          <Select
            onChange={(value) => applyCatalog(String(value))}
            popupMatchSelectWidth={false}
            options={typeOptions}
          />
        </Form.Item>

        {isCustom && (
          <Form.Item
            label="接口类型"
            name="wireKind"
            rules={[{ required: true, message: "请选择接口类型" }]}
          >
            <Select options={WIRE_KINDS} />
          </Form.Item>
        )}

        <Form.Item label="显示名称" name="name" rules={[{ required: true, message: "请输入名称" }]}>
          <Input placeholder="例如：DeepSeek 主账号" />
        </Form.Item>

        <Form.Item
          label="API Base URL"
          name="baseUrl"
          rules={[{ required: true, message: "请输入接口地址" }]}
        >
          <Input placeholder="接口地址，切换服务商时自动填充" onChange={() => setRemote([])} />
        </Form.Item>

        <Form.Item
          label="API Key"
          name="apiKey"
          extra={entry?.apiKeyUrl ? `申请地址：${entry.apiKeyUrl}` : undefined}
        >
          <Input.Password
            placeholder={existing?.hasKey ? "留空则沿用已保存的 Key" : "sk-…"}
            autoComplete="off"
          />
        </Form.Item>

        <Form.Item
          label={
            <Space size={8}>
              <span>启用模型</span>
              <Button
                type="link"
                size="small"
                icon={<ReloadOutlined />}
                loading={fetching}
                onClick={() => void fetchModels()}
              >
                获取模型列表
              </Button>
            </Space>
          }
          tooltip="内置模型用开关启用，勾选右侧的圆点把它设为该服务商的当前模型。"
        >
          <Flex vertical gap={6}>
            {presets.length === 0 && extras.length === 0 && (
              <Typography.Text type="secondary">
                该服务商没有预设模型，请点“获取模型列表”或直接输入模型名。
              </Typography.Text>
            )}

            {presets.map((preset) => {
              const on = enabledIDs.has(preset.id);
              return (
                <Flex key={preset.id} align="center" justify="space-between" gap={8}>
                  <Space size={6} wrap>
                    <Switch size="small" checked={on} onChange={(next) => togglePreset(preset, next)} />
                    <Typography.Text style={{ fontSize: 13 }}>{preset.label}</Typography.Text>
                    {preset.reasoning && <Tag style={{ fontSize: 11, marginInlineEnd: 0 }}>思考</Tag>}
                    {preset.vision && (
                      <Tag color="blue" style={{ fontSize: 11, marginInlineEnd: 0 }}>
                        支持图片
                      </Tag>
                    )}
                  </Space>
                  {on && (
                    <Radio
                      checked={current === preset.id}
                      onChange={() => setCurrent(preset.id)}
                      aria-label={`把 ${preset.label} 设为当前模型`}
                    >
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        {current === preset.id ? "当前" : "设为当前"}
                      </Typography.Text>
                    </Radio>
                  )}
                </Flex>
              );
            })}

            {extras.map((model) => (
              <Flex key={model.model} align="center" justify="space-between" gap={8}>
                <Space size={6} wrap>
                  <Tag color="orange" style={{ fontSize: 11, marginInlineEnd: 0 }}>
                    {model.custom ? "自定义" : "目录外"}
                  </Tag>
                  <Typography.Text style={{ fontSize: 13 }}>{model.label || model.model}</Typography.Text>
                  <Checkbox
                    checked={model.vision}
                    onChange={(event) =>
                      setEnabled((prev) =>
                        prev.map((item) =>
                          item.model === model.model ? { ...item, vision: event.target.checked } : item,
                        ),
                      )
                    }
                  >
                    图片输入
                  </Checkbox>
                </Space>
                <Space size={2}>
                  <Radio
                    checked={current === model.model}
                    onChange={() => setCurrent(model.model)}
                    aria-label={`把 ${model.model} 设为当前模型`}
                  >
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                      {current === model.model ? "当前" : "设为当前"}
                    </Typography.Text>
                  </Radio>
                  <Tooltip title="移除">
                    <Button
                      type="text"
                      size="small"
                      icon={<DeleteOutlined />}
                      aria-label={`移除 ${model.model}`}
                      onClick={() => removeModel(model.model)}
                    />
                  </Tooltip>
                </Space>
              </Flex>
            ))}

            {addableRemote.length > 0 && (
              <Select
                value={undefined}
                placeholder="选择服务商返回的模型…"
                showSearch
                popupMatchSelectWidth={false}
                style={{ width: "100%" }}
                notFoundContent="没有更多可启用的模型"
                onChange={(value) => {
                  const found = addableRemote.find((model) => model.id === value);
                  addModel(String(value), found?.label ?? "", true, found?.vision ?? false);
                }}
                options={addableRemote.map((model) => ({
                  value: model.id,
                  label: [
                    model.label && model.label !== model.id ? `${model.id} · ${model.label}` : model.id,
                    model.vision ? "支持图片" : "",
                  ]
                    .filter(Boolean)
                    .join(" "),
                }))}
              />
            )}

            <Space.Compact block>
              <Input
                value={customName}
                placeholder="输入自定义模型 ID"
                onChange={(event) => setCustomName(event.target.value)}
                onPressEnter={() => addModel(customName, "", true, false)}
              />
              <Tooltip title="添加自定义模型">
                <Button
                  icon={<PlusOutlined />}
                  aria-label="添加自定义模型"
                  onClick={() => addModel(customName, "", true, false)}
                />
              </Tooltip>
            </Space.Compact>
          </Flex>
        </Form.Item>
      </Form>
    </Modal>
  );
}

function toEnabled(preset: CatalogModelLite): ProviderModelLite {
  return {
    model: preset.id,
    label: preset.label,
    custom: false,
    reasoning: preset.reasoning,
    vision: preset.vision,
    tools: preset.tools,
    context: preset.context,
  };
}

function wireKindOf(provider: ProviderLite): string {
  return WIRE_KINDS.some((kind) => kind.value === provider.kind) ? provider.kind : "openai";
}
