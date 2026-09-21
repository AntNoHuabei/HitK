// Markdown 渲染。
//
// 只做一件事:把文本变成 .hitk-md 容器里的节点。样式全部来自 chat.css,组件
// 不内联任何排版规则,这样换主题时对话区的排版自动跟随。
import { memo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

interface Props {
  children: string;
  className?: string;
}

export const Markdown = memo(function Markdown({ children, className }: Props) {
  return (
    <div className={className ? `hitk-md ${className}` : "hitk-md"}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
    </div>
  );
});

/** 纯文本(fenced code)渲染,用于工具输出这类不该再解析的内容。 */
export function PlainText({ children }: { children: string }) {
  return <div className="hitk-tool-detail">{children}</div>;
}
