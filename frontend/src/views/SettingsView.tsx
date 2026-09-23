// 设置视图:左侧分区导航 + 右侧分区内容。
import { useState } from "react";
import { SettingsNavigation } from "../features/settings/components/SettingsNavigation";
import type { SettingsSection } from "../features/settings/components/SettingsNavigation";
import { ModelsSettingsPage } from "../features/settings/pages/ModelsSettingsPage";
import { KnowledgeSettingsPage } from "../features/settings/pages/KnowledgeSettingsPage";
import { AppearanceSettingsPage } from "../features/settings/pages/AppearanceSettingsPage";
import { DataSettingsPage } from "../features/settings/pages/DataSettingsPage";
import { AboutSettingsPage } from "../features/settings/pages/AboutSettingsPage";
import { RuntimeSettingsPage } from "../features/settings/pages/RuntimeSettingsPage";
import { SplitView } from "../shared/components/SplitView";

export default function SettingsView() {
  const [section, setSection] = useState<SettingsSection>("models");

  return (
    <SplitView
      className="hitk-settings-layout"
      mainClassName="hitk-settings-main"
      sidebar={<SettingsNavigation active={section} onChange={setSection} />}
    >
        {section === "models" && <ModelsSettingsPage />}
        {section === "knowledge" && <KnowledgeSettingsPage />}
        {section === "runtime" && <RuntimeSettingsPage />}
        {section === "appearance" && <AppearanceSettingsPage />}
        {section === "data" && <DataSettingsPage />}
        {section === "about" && <AboutSettingsPage />}
    </SplitView>
  );
}
