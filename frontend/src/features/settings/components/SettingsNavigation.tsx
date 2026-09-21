import { Button } from "antd";
import type { ReactNode } from "react";
import {
  BgColorsOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
  InfoCircleOutlined,
} from "@ant-design/icons";

export type SettingsSection =
  | "models"
  | "knowledge"
  | "appearance"
  | "data"
  | "about";

const items = [
  { key: "models", icon: <CloudServerOutlined />, label: "模型服务商" },
  { key: "knowledge", icon: <DatabaseOutlined />, label: "知识库" },
  { key: "appearance", icon: <BgColorsOutlined />, label: "外观与主题" },
  { key: "data", icon: <FolderOpenOutlined />, label: "数据与导出" },
  { key: "about", icon: <InfoCircleOutlined />, label: "关于" },
] satisfies Array<{ key: SettingsSection; icon: ReactNode; label: string }>;

/** SettingsNavigation 是设置页左侧的分区导航。 */
export function SettingsNavigation({
  active,
  onChange,
}: {
  active: SettingsSection;
  onChange: (section: SettingsSection) => void;
}) {
  return (
    <aside className="hitk-settings-nav" aria-label="设置分类">
      <nav className="hitk-settings-nav-list">
        {items.map((item, index) => (
          <Button
            type="text"
            key={item.key}
            className={`hitk-settings-nav-item ${active === item.key ? "is-active" : ""}`}
            aria-current={active === item.key ? "page" : undefined}
            onClick={() => onChange(item.key)}
          >
            <span className="hitk-settings-nav-index">{String(index + 1).padStart(2, "0")}</span>
            <span className="hitk-settings-nav-icon">{item.icon}</span>
            <span className="hitk-settings-nav-label">{item.label}</span>
          </Button>
        ))}
      </nav>
    </aside>
  );
}
