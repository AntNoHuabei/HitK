// 检索面板:在 Agentic Search 之外,给用户一个直接验证「库里的资料能不能被找到」
// 的入口。检索走的是 agent 用的同一套混合检索,所以这里命中什么,agent 就能找到什么。
import { useState } from "react";
import { Button, Empty, Flex, Input, Spin, Typography } from "antd";
import { SearchOutlined } from "@ant-design/icons";
import type { SearchHitLite } from "../../../shared/types/knowledge";

const { Text } = Typography;

interface Props {
  hits: SearchHitLite[];
  query: string;
  searching: boolean;
  disabled: boolean;
  onSearch: (query: string) => void;
}

export function SearchPanel({ hits, query, searching, disabled, onSearch }: Props) {
  const [value, setValue] = useState(query);

  const submit = () => {
    if (!value.trim()) return;
    onSearch(value);
  };

  return (
    <>
      <div className="hitk-kb-searchbar">
        <Input
          allowClear
          size="middle"
          placeholder="输入问题或关键词,看看当前知识库能命中的原文"
          prefix={<SearchOutlined />}
          value={value}
          disabled={disabled}
          onChange={(event) => setValue(event.target.value)}
          onPressEnter={submit}
        />
        <Button type="primary" loading={searching} disabled={disabled} onClick={submit}>
          检索
        </Button>
      </div>

      <div className="hitk-kb-body">
        {searching ? (
          <Flex justify="center" style={{ padding: 40 }}>
            <Spin />
          </Flex>
        ) : hits.length === 0 ? (
          <div className="hitk-kb-empty">
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                query
                  ? `没有找到与「${query}」相关的内容`
                  : "输入关键词试搜一次，命中的就是 agent 能引用的原文"
              }
            />
          </div>
        ) : (
          <>
            <Text type="secondary" style={{ fontSize: 12 }}>
              「{query}」命中 {hits.length} 段
            </Text>
            <div style={{ marginTop: 10 }}>
              {hits.map((hit) => (
                <div key={hit.chunkId} className="hitk-kb-hit">
                  <div className="hitk-kb-hit-head">
                    <span className="hitk-kb-hit-title" title={hit.docTitle}>
                      {hit.docTitle}
                    </span>
                    {hit.heading && <span className="hitk-kb-hit-heading">{hit.heading}</span>}
                    <span className="hitk-kb-hit-score">
                      {hit.score.toFixed(3)} · 词 {hit.lexical.toFixed(2)} · 向量{" "}
                      {hit.vector.toFixed(2)}
                    </span>
                  </div>
                  <div className="hitk-kb-hit-snippet">{hit.snippet}</div>
                  <div className="hitk-kb-hit-source">
                    {hit.collection} · {hit.source}
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </>
  );
}
