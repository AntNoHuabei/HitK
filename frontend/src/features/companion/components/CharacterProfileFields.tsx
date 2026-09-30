// 角色人设字段的唯一一份实现。
//
// 创建弹窗和设置面板都渲染这组字段,避免两处各写一遍然后慢慢长歪。字段值放
// 在父级 antd Form 里,这里只负责渲染与校验规则。
import { Button, Flex, Form, Input, Segmented, Select, Switch, Tooltip, Typography } from "antd";
import type { CompanionProfile } from "../../../shared/repositories";

const { Text } = Typography;

/** 头像快捷选项。用户也可以直接手输别的 emoji。 */
export const AVATAR_CHOICES = ["🌙", "☀️", "🌒", "🌸", "🍃", "🔥", "❄️", "🌊"];

export const SCHEDULE_OPTIONS = [
  { value: "relaxed", label: "松弛" },
  { value: "balanced", label: "均衡" },
  { value: "busy", label: "忙碌" },
  { value: "social", label: "社交丰富" },
];

export interface CharacterFormValues {
  name: string;
  avatar: string;
  summary: string;
  traits: string[];
  scheduleTendency: string;
  city: string;
  proactiveEnabled: boolean;
  /** 组合键 provider::model,提交时拆回 provider/model。 */
  modelKey: string;
  reasoningEffort: string;
}

export function profileToFormValues(
  profile: CompanionProfile,
  activeModelKey: string,
): CharacterFormValues {
  return {
    name: profile.name ?? "",
    avatar: profile.avatar ?? "",
    summary: profile.summary ?? "",
    traits: profile.traits ?? [],
    scheduleTendency: profile.scheduleTendency || "balanced",
    city: profile.city ?? "",
    proactiveEnabled: Boolean(profile.proactiveEnabled),
    modelKey: activeModelKey,
    reasoningEffort: profile.reasoningEffort || "off",
  };
}

/** 把表单值合回 profile。provider/model 只在表单里选了可用模型时才落值。 */
export function applyFormValues(
  profile: CompanionProfile,
  values: CharacterFormValues,
): CompanionProfile {
  const next: CompanionProfile = {
    ...profile,
    name: values.name.trim(),
    avatar: values.avatar.trim(),
    summary: values.summary.trim(),
    traits: (values.traits ?? []).map((trait) => trait.trim()).filter(Boolean).slice(0, 6),
    scheduleTendency: values.scheduleTendency,
    city: values.city.trim(),
    proactiveEnabled: values.proactiveEnabled,
    reasoningEffort: values.reasoningEffort || "off",
  };
  if (values.modelKey) {
    const [provider, ...rest] = String(values.modelKey).split("::");
    if (provider && rest.length > 0) {
      next.provider = provider;
      next.model = rest.join("::");
    }
  }
  return next;
}

interface Props {
  modelOptions: { value: string; label: string }[];
  reasoningOptions: string[];
  /** 角色 pin 的模型已不可用,当前跟随模式默认。 */
  pinStale: boolean;
  modelReady: boolean;
}

export function CharacterProfileFields({
  modelOptions,
  reasoningOptions,
  pinStale,
  modelReady,
}: Props) {
  const form = Form.useFormInstance<CharacterFormValues>();
  const avatar = Form.useWatch("avatar", form);

  return (
    <>
      <Form.Item
        name="name"
        label="名字"
        rules={[
          { required: true, whitespace: true, message: "先起个名字" },
          { max: 24, message: "名字不超过 24 个字" },
        ]}
      >
        <Input placeholder="例如：小晴" maxLength={24} />
      </Form.Item>

      <Form.Item label="头像">
        <Flex gap={6} wrap="wrap" align="center">
          {AVATAR_CHOICES.map((emoji) => (
            <Button
              key={emoji}
              type={avatar === emoji ? "primary" : "default"}
              className="hitk-companion-avatar-btn"
              aria-label={`头像 ${emoji}`}
              aria-pressed={avatar === emoji}
              onClick={() => form.setFieldValue("avatar", emoji)}
            >
              {emoji}
            </Button>
          ))}
          <Form.Item name="avatar" noStyle>
            <Input
              className="hitk-companion-avatar-input"
              placeholder="emoji"
              maxLength={4}
            />
          </Form.Item>
        </Flex>
      </Form.Item>

      <Form.Item
        name="summary"
        label="性格与相处方式"
        rules={[
          { required: true, whitespace: true, message: "写清楚 TA 是个什么样的人" },
          { min: 10, message: "至少写 10 个字，太短立不住" },
          { max: 500, message: "不超过 500 字" },
        ]}
      >
        <Input.TextArea
          autoSize={{ minRows: 3, maxRows: 6 }}
          maxLength={500}
          placeholder="TA 怎么说话、怎么对你、亲密时什么风格、边界在哪里"
        />
      </Form.Item>

      <Form.Item
        name="traits"
        label="性格关键词"
        extra="最多 6 个，会出现在人设提示里"
        rules={[
          {
            validator: (_, value: string[] | undefined) =>
              (value ?? []).length > 6
                ? Promise.reject(new Error("最多 6 个关键词"))
                : Promise.resolve(),
          },
        ]}
      >
        <Select
          mode="tags"
          placeholder="温柔、直接、爱开玩笑…"
          maxTagCount={6}
          tokenSeparators={["，", ",", " "]}
        />
      </Form.Item>

      <Form.Item name="scheduleTendency" label="日程倾向">
        <Segmented block options={SCHEDULE_OPTIONS} />
      </Form.Item>

      <Form.Item name="city" label="城市（仅用于城市级天气）">
        <Input placeholder="例如：杭州" maxLength={40} />
      </Form.Item>

      <Form.Item
        name="modelKey"
        label="陪伴模型"
        rules={[{ required: true, message: "选一个可用模型" }]}
        extra={
          pinStale ? (
            <Text type="warning">原模型已不可用，当前跟随默认模型</Text>
          ) : undefined
        }
      >
        <Select
          options={modelOptions}
          placeholder={modelReady ? "选择模型" : "请先在设置中启用模型"}
          disabled={!modelReady}
        />
      </Form.Item>

      {reasoningOptions.length > 0 && (
        <Form.Item name="reasoningEffort" label="思考等级">
          <Segmented
            block
            options={reasoningOptions.map((value) => ({ value, label: reasoningLabel(value) }))}
          />
        </Form.Item>
      )}

      <Form.Item name="proactiveEnabled" label="主动联系" valuePropName="checked">
        <Tooltip title="每次 Tick 由角色根据当下状态决定是否主动发消息">
          <Switch />
        </Tooltip>
      </Form.Item>
    </>
  );
}

export function reasoningLabel(value: string): string {
  return { off: "关闭", low: "低", medium: "中", high: "高" }[value] ?? value;
}
