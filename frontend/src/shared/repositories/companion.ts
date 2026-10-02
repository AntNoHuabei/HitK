import { Companion } from "./bindings";
import { list } from "./helpers";
import type {
  CompanionCharacter,
  CompanionDirective,
  CompanionMemory,
  CompanionOverview,
  CompanionProfile,
  CompanionTurnTaskView,
  LifeEvent,
  Thread,
} from "./bindings";

export const companionRepository = {
  overview: (): Promise<CompanionOverview> => Companion.Overview(),

  // 角色管理。所有既有方法都作用于"激活角色",切换与增删走这里。
  listCharacters: async (): Promise<CompanionCharacter[]> =>
    list(await Companion.ListCharacters()),

  activeCharacter: (): Promise<CompanionCharacter> => Companion.ActiveCharacter(),

  createCharacter: (profile: CompanionProfile): Promise<CompanionCharacter> =>
    Companion.CreateCharacter(profile),

  switchCharacter: (id: string): Promise<void> => Companion.SwitchCharacter(id),

  deleteCharacter: (id: string): Promise<void> => Companion.DeleteCharacter(id),

  saveProfile: (profile: CompanionProfile): Promise<CompanionProfile> =>
    Companion.SaveProfile(profile),

  tickNow: (): Promise<void> => Companion.TickNow(),

  setPaused: (paused: boolean): Promise<void> => Companion.SetPaused(paused),

  reset: (): Promise<void> => Companion.Reset(),

  listEvents: async (limit = 40): Promise<LifeEvent[]> =>
    list(await Companion.ListEvents(limit)),

  listMemories: async (limit = 80): Promise<CompanionMemory[]> =>
    list(await Companion.ListMemories(limit)),

  saveMemory: (memory: CompanionMemory): Promise<CompanionMemory> =>
    Companion.SaveMemory(memory),

  deleteMemory: (id: string): Promise<void> => Companion.DeleteMemory(id),

  getThread: (): Promise<Thread> => Companion.GetThread(),

  submitTurn: (
    text: string,
    providerID: string,
    model: string,
    reasoningEffort: string,
  ): Promise<CompanionTurnTaskView> => Companion.SubmitTurn(text, providerID, model, reasoningEffort),

  getTask: (taskID: string): Promise<CompanionTurnTaskView> => Companion.GetTask(taskID),

  listTasks: async (activeOnly = false): Promise<CompanionTurnTaskView[]> =>
    list(await Companion.ListTasks(activeOnly)),

  cancelTask: (taskID: string): Promise<void> => Companion.CancelTask(taskID),

  // 按「让她现在回」：跳过日程可用性这一关，立刻把等待中的这一轮推起来。
  forceReply: (taskID: string): Promise<void> => Companion.ForceReply(taskID),

  // 导演模式：往她的世界里写一件事，不是对她说话。
  submitDirective: (
    text: string,
    providerID: string,
    model: string,
    reasoningEffort: string,
  ): Promise<CompanionDirective> =>
    Companion.SubmitDirective(text, providerID, model, reasoningEffort),

  listDirectives: async (limit = 50): Promise<CompanionDirective[]> =>
    list(await Companion.ListDirectives(limit)),

  revertDirective: (id: string): Promise<void> => Companion.RevertDirective(id),

  interrupt: (): Promise<void> => Companion.Interrupt(),

  running: (): Promise<boolean> => Companion.Running(),

  markRead: (): Promise<void> => Companion.MarkRead(),
};
