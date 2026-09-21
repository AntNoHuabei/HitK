import type { ReactNode } from "react";

interface SplitViewProps {
  sidebar: ReactNode;
  children: ReactNode;
  className?: string;
  mainClassName?: string;
}

/** Shared application split: navigation on the left, one owned content surface on the right. */
export function SplitView({ sidebar, children, className = "", mainClassName = "" }: SplitViewProps) {
  return (
    <div className={`hitk-split-view ${className}`.trim()}>
      {sidebar}
      <div className={`hitk-split-main ${mainClassName}`.trim()}>{children}</div>
    </div>
  );
}
