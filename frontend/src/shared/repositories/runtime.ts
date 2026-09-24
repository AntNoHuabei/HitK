import { Runtime } from "./bindings";
import type {
  RuntimeStatus,
  SkillEnvironmentStatus,
  InstallRequest,
  ResolvedPlan,
  SkillHubFeed,
  SkillSummary,
} from "./bindings";

export const runtimeRepository = {
  list: async (): Promise<RuntimeStatus[]> => (await Runtime.ListRuntimes()) ?? [],
  environment: (name: string): Promise<SkillEnvironmentStatus> => Runtime.GetSkillEnvironment(name),
  prepare: (name: string, allowNetwork = false): Promise<SkillEnvironmentStatus> => Runtime.PrepareSkillEnvironment(name, allowNetwork),
  repair: (name: string, allowNetwork = false): Promise<SkillEnvironmentStatus> => Runtime.RepairSkillEnvironment(name, allowNetwork),
  cancel: (name: string): Promise<void> => Runtime.CancelSkillEnvironment(name),
  remove: (name: string): Promise<void> => Runtime.RemoveSkillEnvironment(name),
  listSkills: async (): Promise<SkillSummary[]> => (await Runtime.ListSkills()) ?? [],
  reloadSkills: (): Promise<void> => Runtime.ReloadSkills(),
  resolveSkill: (name: string): Promise<ResolvedPlan> => Runtime.ResolveSkill(name),
  skillPlan: (name: string): Promise<ResolvedPlan> => Runtime.GetSkillPlan(name),
  prepareResolved: (name: string, allowNetwork = false): Promise<SkillEnvironmentStatus> => Runtime.PrepareResolvedSkill(name, allowNetwork),
  install: (request: InstallRequest): Promise<string> => Runtime.InstallSkill(request),
  skillHubFeed: (url: string, allowNetwork = false): Promise<SkillHubFeed> => Runtime.ResolveSkillHubFeed(url, allowNetwork),
  rollbackSkill: (name: string): Promise<void> => Runtime.RollbackSkill(name),
  pruneSkill: (name: string): Promise<void> => Runtime.PruneSkill(name),
};
