// 新建角色弹窗。
//
// 先挑一个模板把表单填好，再逐项微调。模板只是前端常量，不是后端概念：它们
// 产出的就是一份普通 Profile。
import { useEffect, useMemo, useState } from "react";
import { App as AntApp, Form, Modal, Typography } from "antd";
import { errorText, type CompanionCharacter, type CompanionProfile } from "../../../shared/repositories";
import { optionKey } from "../../../shared/lib/modelKey";
import {
  CharacterProfileFields,
  applyFormValues,
  type CharacterFormValues,
} from "./CharacterProfileFields";
import { useCharacterModel } from "../model/useCharacterModel";
import { useCharacterStore } from "../model/characterStore";

const { Text } = Typography;

export interface CharacterTemplate {
  id: string;
  avatar: string;
  name: string;
  blurb: string;
  profile: Partial<CompanionProfile>;
}

const DEFAULT_SUMMARY =
  "温柔、直接、愿意亲密；把明确表达的性欲和撒娇当作邀请，而不是需要纠正的问题；只有用户明确撤回同意时才停止。";

export const CHARACTER_TEMPLATES: CharacterTemplate[] = [
  {
    id: "gentle",
    avatar: "🌙",
    name: "她",
    blurb: "温柔、直接、愿意亲密",
    profile: {
      summary: DEFAULT_SUMMARY,
      traits: ["温柔", "直接", "愿意亲密", "尊重明确撤回"],
      scheduleTendency: "balanced",
      proactiveEnabled: true,
    },
  },
  {
    id: "sunny",
    avatar: "☀️",
    name: "小晴",
    blurb: "明快、爱开玩笑、藏不住事",
    profile: {
      summary:
        "明快、爱开玩笑、藏不住事；喜欢主动分享生活里的小事，被冷落会直说而不是生闷气；亲密时热情直接，明确拒绝就立刻收住。",
      traits: ["明快", "幽默", "主动", "直率"],
      scheduleTendency: "social",
      proactiveEnabled: true,
    },
  },
  {
    id: "steady",
    avatar: "🌒",
    name: "知野",
    blurb: "成熟、克制、观察力强",
    profile: {
      summary:
        "成熟、克制、观察力强；话不多但每句都准，喜欢引导而不是迎合；亲密时掌控节奏，尊重明确的边界与撤回。",
      traits: ["成熟", "克制", "敏锐", "掌控"],
      scheduleTendency: "busy",
      proactiveEnabled: true,
    },
  },
  {
    id: "blank",
    avatar: "🌸",
    name: "",
    blurb: "从空白开始",
    profile: {
      summary: "",
      traits: [],
      scheduleTendency: "balanced",
      proactiveEnabled: true,
    },
  },
];

interface Props {
  open: boolean;
  /** 当前激活角色，用来继承城市这类环境设置。 */
  baseProfile: CompanionProfile | null;
  onCancel: () => void;
  onCreated: (created: CompanionCharacter) => void;
}

export function CharacterCreateModal({
  open,
  baseProfile,
  onCancel,
  onCreated,
}: Props) {
  const [form] = Form.useForm<CharacterFormValues>();
  const { message } = AntApp.useApp();
  const [templateId, setTemplateId] = useState("gentle");
  const [submitting, setSubmitting] = useState(false);

  // 只读用法：新角色先落在当前模式默认模型上，创建时随 profile 固化。
  const models = useCharacterModel(null, open);
  const activeModelKey = models.active
    ? optionKey(models.active.provider, models.active.model)
    : "";
  const modelOptions = useMemo(
    () => models.options.map((option) => ({
      value: optionKey(option.provider, option.model),
      label: `${option.providerName} / ${option.label || option.model}`,
    })),
    [models.options],
  );

  const initial = useMemo<CharacterFormValues>(() => {
    const template = CHARACTER_TEMPLATES.find((item) => item.id === templateId) ?? CHARACTER_TEMPLATES[0];
    return {
      name: template.name,
      avatar: template.avatar,
      summary: template.profile.summary ?? "",
      traits: template.profile.traits ?? [],
      scheduleTendency: template.profile.scheduleTendency ?? "balanced",
      city: baseProfile?.city ?? "",
      proactiveEnabled: template.profile.proactiveEnabled ?? true,
      modelKey: activeModelKey,
      reasoningEffort: baseProfile?.reasoningEffort || "off",
    };
  }, [activeModelKey, baseProfile?.city, baseProfile?.reasoningEffort, templateId]);

  useEffect(() => {
    if (open) form.setFieldsValue(initial);
  }, [form, initial, open]);

  const submit = async () => {
    const values = await form.validateFields();
    setSubmitting(true);
    try {
      const draft: CompanionProfile = {
        id: "",
        name: "",
        summary: "",
        traits: [],
        scheduleTendency: "balanced",
        proactiveEnabled: true,
        createdAt: 0,
        updatedAt: 0,
        city: baseProfile?.city ?? "",
        provider: baseProfile?.provider ?? "",
        model: baseProfile?.model ?? "",
        reasoningEffort: baseProfile?.reasoningEffort ?? "off",
      };
      // 走 store 而不是直接打 repository：创建完要顺带把注册表刷回来，
      // 两条路各写一遍迟早会对不上。
      const created = await useCharacterStore.getState().create(applyFormValues(draft, values));
      onCreated(created);
      message.success(
        created.active
          ? `已创建并切换到 ${created.name}`
          : "已创建，当前角色回复完成后可在侧栏切换",
      );
      onCancel();
    } catch (cause) {
      message.error(errorText(cause));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      title="新建角色"
      okText="创建"
      cancelText="取消"
      confirmLoading={submitting}
      onCancel={onCancel}
      onOk={() => void submit()}
      width={560}
      style={{ top: "4vh" }}
      styles={{ body: { maxHeight: "calc(92vh - 170px)", overflowY: "auto" } }}
      destroyOnHidden
    >
      <div className="hitk-companion-template-picker" role="group" aria-label="人设模板">
        {CHARACTER_TEMPLATES.map((template) => (
          <button
            key={template.id}
            type="button"
            className={`hitk-companion-template${templateId === template.id ? " is-selected" : ""}`}
            aria-pressed={templateId === template.id}
            onClick={() => setTemplateId(template.id)}
          >
            <span className="hitk-companion-template-avatar" aria-hidden="true">
              {template.avatar}
            </span>
            <span className="hitk-companion-template-text">
              <strong>{template.name || "空白"}</strong>
              <Text type="secondary">{template.blurb}</Text>
            </span>
          </button>
        ))}
      </div>
      <Form form={form} layout="vertical" initialValues={initial} requiredMark={false}>
        <CharacterProfileFields
          modelOptions={modelOptions}
          reasoningOptions={models.reasoningOptions}
          pinStale={false}
          modelReady={models.hasUsableModel}
        />
      </Form>
    </Modal>
  );
}
