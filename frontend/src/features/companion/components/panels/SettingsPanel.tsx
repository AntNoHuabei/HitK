// 陪伴设置：人设表单 + 世界开关 + 危险区。
//
// 字段本身不在这里，全部走共享的 CharacterProfileFields，跟新建弹窗同一份。
import { useEffect, useState } from "react";
import {
  App as AntApp,
  Button,
  Flex,
  Form,
  Popconfirm,
  Switch,
  Tooltip,
  Typography,
} from "antd";
import {
  DeleteOutlined,
  PauseCircleOutlined,
  ReloadOutlined,
  SaveOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { errorText, type CompanionProfile } from "../../../../shared/repositories";
import {
  CharacterProfileFields,
  applyFormValues,
  profileToFormValues,
  type CharacterFormValues,
} from "../CharacterProfileFields";

const { Text } = Typography;

export function SettingsPanel({
  profile,
  paused,
  modelOptions,
  activeModelKey,
  reasoningOptions,
  pinStale,
  modelReady,
  deleteDisabled,
  deleteTooltip,
  onPause,
  onTick,
  onReset,
  onDelete,
  onSaveProfile,
}: {
  profile: CompanionProfile;
  paused: boolean;
  modelOptions: { value: string; label: string }[];
  activeModelKey: string;
  reasoningOptions: string[];
  pinStale: boolean;
  modelReady: boolean;
  deleteDisabled: boolean;
  deleteTooltip: string;
  onPause: (paused: boolean) => void;
  onTick: () => void;
  onReset: () => void;
  onDelete: () => void;
  onSaveProfile: (profile: CompanionProfile) => Promise<CompanionProfile>;
}) {
  const [form] = Form.useForm<CharacterFormValues>();
  const { message } = AntApp.useApp();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    form.setFieldsValue(profileToFormValues(profile, activeModelKey));
  }, [activeModelKey, form, profile]);

  const submit = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      await onSaveProfile(applyFormValues(profile, values));
      message.success("已保存");
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setSaving(false);
    }
  };

  const deleteButton = (
    <Button danger icon={<DeleteOutlined />} disabled={deleteDisabled}>
      删除这个角色
    </Button>
  );

  return (
    <div className="hitk-companion-settings">
      <Form form={form} layout="vertical" requiredMark={false}>
        <CharacterProfileFields
          modelOptions={modelOptions}
          reasoningOptions={reasoningOptions}
          pinStale={pinStale}
          modelReady={modelReady}
        />
      </Form>
      <div className="hitk-companion-switch-row">
        <div>
          <Text strong>{`暂停${profile.name}的世界`}</Text>
          <Text type="secondary">暂停期间不推进状态，也不主动联系</Text>
        </div>
        <Switch checked={paused} onChange={onPause} />
      </div>
      <Flex gap={8} wrap>
        <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={() => void submit()}>
          保存设置
        </Button>
        <Button icon={<ThunderboltOutlined />} onClick={onTick}>
          立即推进
        </Button>
        <Popconfirm
          title={`重置${profile.name}的世界？`}
          description="会清空生活、记忆和专用对话，但不会删除 Work/Coding 会话。"
          onConfirm={onReset}
        >
          <Button icon={<ReloadOutlined />}>重置</Button>
        </Popconfirm>
      </Flex>
      <div className="hitk-companion-danger-zone">
        {deleteDisabled ? (
          <Tooltip title={deleteTooltip}>
            <span>{deleteButton}</span>
          </Tooltip>
        ) : (
          <Popconfirm
            title={`删除${profile.name}？`}
            description={`会删除${profile.name}的世界、记忆和专用会话，不可恢复。`}
            onConfirm={onDelete}
          >
            {deleteButton}
          </Popconfirm>
        )}
      </div>
      <Text type="secondary" className="hitk-companion-pause-note">
        <PauseCircleOutlined /> 暂停不会产生离开惩罚；恢复后从当前时间继续。
      </Text>
    </div>
  );
}
