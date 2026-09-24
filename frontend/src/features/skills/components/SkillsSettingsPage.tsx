import { useCallback, useEffect, useState } from "react";
import {
  App as AntApp,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  CloudDownloadOutlined,
  FolderOpenOutlined,
  ReloadOutlined,
  RollbackOutlined,
  SearchOutlined,
  DeleteOutlined,
} from "@ant-design/icons";
import { appRepository, runtimeRepository } from "../../../shared/repositories";
import type {
  DependencyCandidate,
  InstallRequest,
  ResolvedPlan,
  SkillSummary,
} from "../../../shared/repositories";
import { onRuntimeProgress } from "../../../shared/wails/events";

const { Text, Paragraph } = Typography;

function formatDependency(dependency: DependencyCandidate): string {
  const version = dependency.version ? ` ${dependency.version}` : "";
  return `${dependency.packageName || dependency.importName}${version}`;
}

export function SkillsSettingsPage() {
  const { message, modal } = AntApp.useApp();
  const [skills, setSkills] = useState<SkillSummary[]>([]);
  const [states, setStates] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [plan, setPlan] = useState<ResolvedPlan | null>(null);
  const [installOpen, setInstallOpen] = useState(false);
  const [form] = Form.useForm<InstallRequest>();

  const load = useCallback(async () => {
    const items = await runtimeRepository.listSkills();
    setSkills(items);
  }, []);

  useEffect(() => {
    void load().finally(() => setLoading(false));
  }, [load]);

  useEffect(() => onRuntimeProgress((progress) => {
    setStates((current) => ({ ...current, [progress.skill]: progress.status.state }));
  }), []);

  const resolve = async (skill: SkillSummary) => {
    setBusy(`resolve:${skill.id}`);
    try {
      setPlan(await runtimeRepository.resolveSkill(skill.id));
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
      try {
        setPlan(await runtimeRepository.skillPlan(skill.id));
      } catch {
        // The original resolution error is the useful one.
      }
    } finally {
      setBusy("");
    }
  };

  const prepare = async (skill: SkillSummary) => {
    setBusy(`prepare:${skill.id}`);
    try {
      const current = await runtimeRepository.skillPlan(skill.id).catch(() => null);
      const dependencies = (current?.dependencies?.length ?? 0) + (current?.unresolved?.length ?? 0);
      if (skill.kind !== "prompt" && dependencies > 0) {
        const confirmed = await new Promise<boolean>((resolveConfirm) => {
          modal.confirm({
            title: `初始化 ${skill.displayName || skill.name}？`,
            content: "将在 HitK 隔离目录中锁定并安装依赖，安装网络只用于这一步。",
            okText: "初始化",
            cancelText: "取消",
            onOk: () => resolveConfirm(true),
            onCancel: () => resolveConfirm(false),
          });
        });
        if (!confirmed) return;
      }
      const status = await runtimeRepository.prepareResolved(skill.id, true);
      setStates((value) => ({ ...value, [skill.id]: status.state }));
      if (status.state === "ready") message.success("Skill 环境已就绪");
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy("");
    }
  };

  const install = async (values: InstallRequest) => {
    setBusy("install");
    try {
      await runtimeRepository.install(values);
      setInstallOpen(false);
      form.resetFields();
      await load();
      message.success("Skill 已导入");
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy("");
    }
  };

  return <section className="hitk-settings-panel" aria-labelledby="settings-skills-title">
    <header className="hitk-settings-page-heading">
      <div>
        <span className="hitk-settings-section-number">04</span>
        <Typography.Title id="settings-skills-title" level={3}>Skills</Typography.Title>
      </div>
      <Space>
        <Button icon={<ReloadOutlined />} onClick={() => void runtimeRepository.reloadSkills().then(load)}>重新扫描</Button>
        <Button type="primary" icon={<CloudDownloadOutlined />} onClick={() => setInstallOpen(true)}>导入 Skill</Button>
      </Space>
    </header>

    <Table<SkillSummary>
      rowKey="id"
      loading={loading}
      dataSource={skills}
      pagination={false}
      size="small"
      locale={{ emptyText: "没有发现 Skill" }}
      columns={[
        {
          title: "Skill",
          dataIndex: "name",
          render: (_, skill) => <div className="hitk-skill-identity">
            <Text strong>{skill.displayName || skill.name}</Text>
            <Text type="secondary" title={skill.sourceUrl || undefined}>
              {skill.id} · {skill.source} · {skill.kind}{skill.publisher ? ` · ${skill.publisher}` : ""}
            </Text>
          </div>,
        },
        {
          title: "版本",
          dataIndex: "version",
          width: 120,
          render: (value: string | undefined) => value || "内置",
        },
        {
          title: "状态",
          width: 140,
          render: (_, skill) => {
            const state = states[skill.id] || skill.state || "discovered";
            return <Tag color={state === "ready" ? "green" : state === "failed" ? "red" : "default"}>{state}</Tag>;
          },
        },
        {
          title: "操作",
          width: 150,
          align: "right",
          render: (_, skill) => <Space size={2}>
            <Tooltip title="解析依赖计划">
              <Button
                type="text"
                aria-label={`解析 ${skill.id}`}
                icon={<SearchOutlined />}
                loading={busy === `resolve:${skill.id}`}
                onClick={() => void resolve(skill)}
              />
            </Tooltip>
            {skill.kind !== "prompt" && <Tooltip title="初始化隔离环境">
              <Button
                type="text"
                aria-label={`初始化 ${skill.id}`}
                icon={<CloudDownloadOutlined />}
                loading={busy === `prepare:${skill.id}`}
                onClick={() => void prepare(skill)}
              />
            </Tooltip>}
            <Tooltip title="回滚上一版本">
              <Button
                type="text"
                aria-label={`回滚 ${skill.id}`}
                icon={<RollbackOutlined />}
                disabled={skill.source !== "skillhub"}
                onClick={() => void runtimeRepository.rollbackSkill(skill.id).then(load).catch((error) => message.error(error instanceof Error ? error.message : String(error)))}
              />
            </Tooltip>
            <Tooltip title="清除上一版本">
              <Button
                type="text"
                aria-label={`清理 ${skill.id}`}
                icon={<DeleteOutlined />}
                disabled={skill.source !== "skillhub"}
                onClick={() => void runtimeRepository.pruneSkill(skill.id).then(() => message.success("已清理上一版本")).catch((error) => message.error(error instanceof Error ? error.message : String(error)))}
              />
            </Tooltip>
          </Space>,
        },
      ]}
    />

    <Modal
      open={plan !== null}
      title={plan ? `${plan.analysis.kind} 依赖计划` : "依赖计划"}
      footer={<Button onClick={() => setPlan(null)}>关闭</Button>}
      onCancel={() => setPlan(null)}
    >
      {plan && <>
        <Paragraph>
          Runtime: <Text code>{plan.runtime?.kind || "none"} {plan.runtime?.constraint || ""}</Text>
          {" · "}Environment: <Text code>{plan.environment || "none"}</Text>
          {" · "}Plan: <Text code>{plan.planHash || "pending"}</Text>
        </Paragraph>
        <Text strong>已解析依赖</Text>
        <div className="hitk-skill-dependencies">
          {(plan.dependencies ?? []).length > 0
            ? plan.dependencies?.map((dependency, index) => <div key={`${dependency.packageName}-${index}`}>
              <Text code>{formatDependency(dependency)}</Text>
              <Text type="secondary">{dependency.source} · {dependency.confidence ?? 0}%</Text>
            </div>)
            : <Text type="secondary">无需安装第三方依赖</Text>}
        </div>
        {(plan.unresolved?.length ?? 0) > 0 && <>
          <Text strong>待确认依赖</Text>
          <div className="hitk-skill-dependencies">
            {plan.unresolved?.map((dependency, index) => <div key={`${dependency.importName}-${index}`}>
              <Text code>{dependency.importName || dependency.packageName}</Text>
              <Text type="secondary">无法可靠映射，需要人工补充</Text>
            </div>)}
          </div>
        </>}
      </>}
    </Modal>

    <Modal
      open={installOpen}
      title="导入 Skill"
      okText="导入"
      confirmLoading={busy === "install"}
      onCancel={() => setInstallOpen(false)}
      onOk={() => form.submit()}
    >
      <Form<InstallRequest>
        form={form}
        layout="vertical"
        initialValues={{ source: "local" }}
        onFinish={(values) => void install(values)}
      >
        <Form.Item name="id" label="Skill ID" rules={[{ required: true }]}>
          <Input placeholder="例如 pdf-tools" />
        </Form.Item>
        <Form.Item name="source" label="来源" rules={[{ required: true }]}>
          <Select options={[
            { value: "local", label: "本地目录" },
            { value: "zip", label: "ZIP URL" },
            { value: "git", label: "Git URL" },
          ]} />
        </Form.Item>
        <Form.Item noStyle shouldUpdate={(previous, current) => previous.source !== current.source}>
          {({ getFieldValue }) => getFieldValue("source") === "local"
            ? <Form.Item name="path" label="目录" rules={[{ required: true }]}>
              <Input
                addonAfter={<FolderOpenOutlined onClick={async () => {
                  const path = await appRepository.pickDirectory("选择 Skill 目录");
                  if (path) form.setFieldValue("path", path);
                }} />}
              />
            </Form.Item>
            : <Form.Item name="url" label="URL" rules={[{ required: true }]}>
              <Input placeholder="https://..." />
            </Form.Item>}
        </Form.Item>
        <Form.Item name="version" label="版本">
          <Input placeholder="可选" />
        </Form.Item>
        <Form.Item name="sha256" label="SHA256">
          <Input placeholder="可选；ZIP 强烈建议填写" />
        </Form.Item>
      </Form>
    </Modal>
  </section>;
}
