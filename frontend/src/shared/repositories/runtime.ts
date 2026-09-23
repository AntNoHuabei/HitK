import { Runtime } from "./bindings";
import type { RuntimeStatus, SkillEnvironmentStatus } from "./bindings";

export const runtimeRepository = {
  list: async (): Promise<RuntimeStatus[]> => (await Runtime.ListRuntimes()) ?? [],
  environment: (name: string): Promise<SkillEnvironmentStatus> => Runtime.GetSkillEnvironment(name),
  prepare: (name: string, allowNetwork = false): Promise<SkillEnvironmentStatus> => Runtime.PrepareSkillEnvironment(name, allowNetwork),
  repair: (name: string, allowNetwork = false): Promise<SkillEnvironmentStatus> => Runtime.RepairSkillEnvironment(name, allowNetwork),
  remove: (name: string): Promise<void> => Runtime.RemoveSkillEnvironment(name),
};
