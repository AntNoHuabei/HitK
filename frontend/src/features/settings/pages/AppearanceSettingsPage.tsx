import { Typography } from "antd";
import { CheckOutlined } from "@ant-design/icons";
import { HITK_THEMES, useHitKTheme } from "../../../theme/ThemeContext";

const { Title } = Typography;

/** 外观与主题:从主题列表里挑一套,热切换并持久化。 */
export function AppearanceSettingsPage() {
  const { themeId, setTheme } = useHitKTheme();
  return (
    <section className="hitk-settings-panel" aria-labelledby="settings-appearance-title">
      <header className="hitk-settings-page-heading">
        <div>
          <span className="hitk-settings-section-number">03</span>
          <Title id="settings-appearance-title" level={3}>
            外观与主题
          </Title>
        </div>
      </header>
      <div className="hitk-settings-appearance-list">
        {HITK_THEMES.map((theme, index) => {
          const selected = themeId === theme.id;
          return (
            <button
              type="button"
              className={`hitk-settings-appearance-row ${selected ? "is-active" : ""}`}
              aria-pressed={selected}
              key={theme.id}
              onClick={() => void setTheme(theme.id)}
            >
              <span className="hitk-settings-theme-index">{String(index + 1).padStart(2, "0")}</span>
              <span
                className="hitk-settings-theme-swatch"
                style={{ backgroundColor: theme.vars["sidebar-bg"], borderColor: theme.vars.divider }}
              >
                <span style={{ backgroundColor: theme.vars.signal }} />
              </span>
              <span className="hitk-settings-theme-copy">
                <strong>{theme.name}</strong>
                <small>{theme.desc}</small>
              </span>
              <span className="hitk-settings-theme-mode">{theme.dark ? "深色" : "浅色"}</span>
              <span className="hitk-settings-theme-check" aria-hidden>
                {selected && <CheckOutlined />}
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
}
