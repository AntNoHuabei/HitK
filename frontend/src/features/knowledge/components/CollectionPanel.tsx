// 知识库集合列表(左栏)。
//
// 集合是检索的天然范围边界,所以选中集合既改变右侧列表也改变检索范围,这里是
// 唯一的切换入口。
import { useEffect, useState } from "react";
import { App as AntApp, Button, Dropdown, Input, Modal, Typography } from "antd";
import {
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  MoreOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import type { CollectionLite } from "../../../shared/types/knowledge";

const { Text } = Typography;

interface Props {
  collections: CollectionLite[];
  activeId: string;
  onSelect: (id: string) => void;
  onCreate: (name: string, description: string) => Promise<void>;
  onUpdate: (id: string, name: string, description: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}

export function CollectionPanel({
  collections,
  activeId,
  onSelect,
  onCreate,
  onUpdate,
  onDelete,
}: Props) {
  const { message, modal } = AntApp.useApp();
  const [editing, setEditing] = useState<CollectionLite | null>(null);
  const [creating, setCreating] = useState(false);

  const remove = (collection: CollectionLite) => {
    modal.confirm({
      title: `删除知识库「${collection.name}」?`,
      content: `其中 ${collection.docCount} 份文档与 ${collection.chunkCount} 个分块会一并删除,不可恢复。`,
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: async () => {
        try {
          await onDelete(collection.id);
          void message.success("已删除");
        } catch (err) {
          void message.error(String(err));
        }
      },
    });
  };

  return (
    <div className="hitk-kb-collections">
      <div className="hitk-kb-collections-head">
        <span className="hitk-kb-collections-label">知识库</span>
        <Button
          type="text"
          size="small"
          aria-label="新建知识库"
          icon={<PlusOutlined />}
          onClick={() => setCreating(true)}
        />
      </div>

      <div className="hitk-kb-collection-list">
        {collections.length === 0 ? (
          <Text type="secondary" style={{ fontSize: 12, padding: "6px 4px" }}>
            还没有知识库
          </Text>
        ) : (
          collections.map((collection) => {
            const active = collection.id === activeId;
            return (
              <Dropdown
                key={collection.id}
                trigger={["contextMenu"]}
                menu={{
                  items: [
                    { key: "edit", label: "重命名 / 改简介", icon: <EditOutlined /> },
                    { key: "delete", label: "删除", icon: <DeleteOutlined />, danger: true },
                  ],
                  onClick: ({ key }) => {
                    if (key === "edit") setEditing(collection);
                    if (key === "delete") remove(collection);
                  },
                }}
              >
                <div
                  className={`hitk-kb-collection ${active ? "is-active" : ""}`}
                  role="button"
                  tabIndex={0}
                  onClick={() => onSelect(collection.id)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") onSelect(collection.id);
                  }}
                >
                  <span
                    className="hitk-kb-collection-dot"
                    style={{ background: collection.color || "var(--hitk-signal, #1677ff)" }}
                  />
                  <span className="hitk-kb-collection-body">
                    <span className="hitk-kb-collection-name">{collection.name}</span>
                    <span className="hitk-kb-collection-meta">
                      {collection.docCount} 文档 · {collection.chunkCount} 分块
                    </span>
                  </span>
                  <Dropdown
                    trigger={["click"]}
                    menu={{
                      items: [
                        { key: "edit", label: "重命名 / 改简介", icon: <EditOutlined /> },
                        { key: "delete", label: "删除", icon: <DeleteOutlined />, danger: true },
                      ],
                      onClick: ({ key, domEvent }) => {
                        domEvent.stopPropagation();
                        if (key === "edit") setEditing(collection);
                        if (key === "delete") remove(collection);
                      },
                    }}
                  >
                    <Button
                      type="text"
                      size="small"
                      aria-label="知识库操作"
                      icon={<MoreOutlined />}
                      onClick={(event) => event.stopPropagation()}
                    />
                  </Dropdown>
                </div>
              </Dropdown>
            );
          })
        )}
      </div>

      <CollectionEditor
        open={creating}
        title="新建知识库"
        onClose={() => setCreating(false)}
        onSubmit={async (name, description) => {
          await onCreate(name, description);
          setCreating(false);
        }}
      />
      <CollectionEditor
        open={editing !== null}
        title="编辑知识库"
        initial={editing}
        onClose={() => setEditing(null)}
        onSubmit={async (name, description) => {
          if (editing) await onUpdate(editing.id, name, description);
          setEditing(null);
        }}
      />
    </div>
  );
}

function CollectionEditor({
  open,
  title,
  initial,
  onClose,
  onSubmit,
}: {
  open: boolean;
  title: string;
  initial?: CollectionLite | null;
  onClose: () => void;
  onSubmit: (name: string, description: string) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [saving, setSaving] = useState(false);

  // 每次打开都把受控字段重置为目标值,避免上一次的输入残留到下一次。
  useEffect(() => {
    if (!open) return;
    setName(initial?.name ?? "");
    setDescription(initial?.description ?? "");
  }, [open, initial]);

  const submit = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      await onSubmit(name.trim(), description.trim());
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title={title}
      okText="保存"
      cancelText="取消"
      confirmLoading={saving}
      onCancel={onClose}
      onOk={() => void submit()}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        <Input
          placeholder="名称,例如：产品文档"
          maxLength={40}
          value={name}
          onChange={(event) => setName(event.target.value)}
          onPressEnter={() => void submit()}
          prefix={<DatabaseOutlined />}
        />
        <Input.TextArea
          placeholder="简介（可选）"
          rows={2}
          maxLength={160}
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
      </div>
    </Modal>
  );
}
