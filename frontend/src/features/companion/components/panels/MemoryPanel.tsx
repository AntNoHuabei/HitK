// 长期记忆列表。
import { Button, Empty, Flex, Popconfirm, Tag, Tooltip, Typography } from "antd";
import { DeleteOutlined, EditOutlined, PlusOutlined } from "@ant-design/icons";
import type { CompanionMemory } from "../../../../shared/repositories";

const { Text } = Typography;

const KIND_LABELS: Record<string, string> = {
  user_fact: "用户",
  relationship: "关系",
  life_event: "事件",
  promise: "约定",
};

export function MemoryPanel({
  memories,
  characterName,
  onAdd,
  onEdit,
  onDelete,
}: {
  memories: CompanionMemory[];
  characterName: string;
  onAdd: () => void;
  onEdit: (memory: CompanionMemory) => void;
  onDelete: (id: string) => void;
}) {
  return (
    <div className="hitk-companion-memory-panel">
      <Flex justify="space-between" align="center">
        <Text type="secondary">{characterName}当前记得的事</Text>
        <Button size="small" icon={<PlusOutlined />} onClick={onAdd}>新增</Button>
      </Flex>
      {memories.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有长期记忆" />
      ) : (
        <div className="hitk-companion-memory-list">
          {memories.map((memory) => (
            <article key={memory.id} className="hitk-companion-memory-item">
              <div>
                <Tag bordered={false}>{KIND_LABELS[memory.kind] ?? "记忆"}</Tag>
                <Text>{memory.content}</Text>
                <Text type="secondary" className="hitk-companion-memory-strength">
                  强度 {memory.strength.toFixed(1)} · 召回 {memory.accessCount}
                </Text>
              </div>
              <Flex gap={2}>
                <Tooltip title="编辑">
                  <Button type="text" size="small" icon={<EditOutlined />} onClick={() => onEdit(memory)} />
                </Tooltip>
                <Popconfirm title="删除这条记忆？" onConfirm={() => onDelete(memory.id)}>
                  <Tooltip title="删除">
                    <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                  </Tooltip>
                </Popconfirm>
              </Flex>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}
