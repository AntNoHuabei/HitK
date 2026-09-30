// 新增/编辑一条长期记忆。
import { useEffect, useState } from "react";
import { Flex, Input, Modal, Select } from "antd";
import type { CompanionMemory } from "../../../../shared/repositories";

export function MemoryEditor({
  open,
  memory,
  characterName,
  onCancel,
  onSave,
}: {
  open: boolean;
  memory: CompanionMemory | null;
  characterName: string;
  onCancel: () => void;
  onSave: (memory: CompanionMemory) => void;
}) {
  const [draft, setDraft] = useState<CompanionMemory | null>(memory);
  useEffect(() => setDraft(memory), [memory]);
  return (
    <Modal
      open={open}
      title={memory?.id ? "编辑记忆" : "新增记忆"}
      okText="保存"
      onCancel={onCancel}
      onOk={() => draft && onSave(draft)}
    >
      {draft && (
        <Flex vertical gap={12}>
          <Select
            value={draft.kind}
            onChange={(kind) => setDraft({ ...draft, kind })}
            options={[
              { value: "user_fact", label: "关于用户的事实" },
              { value: "relationship", label: "关系记忆" },
              { value: "life_event", label: "对话事件" },
              { value: "promise", label: "承诺与约定" },
            ]}
          />
          <Input.TextArea
            value={draft.content}
            autoSize={{ minRows: 3, maxRows: 8 }}
            maxLength={300}
            placeholder={`${characterName}应该记住什么？`}
            onChange={(event) => setDraft({ ...draft, content: event.target.value })}
          />
          <Select
            value={draft.importance}
            onChange={(importance) => setDraft({ ...draft, importance })}
            options={[3, 5, 7, 9].map((value) => ({ value, label: `重要度 ${value}` }))}
          />
        </Flex>
      )}
    </Modal>
  );
}
