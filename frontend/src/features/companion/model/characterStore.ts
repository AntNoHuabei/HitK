// 陪伴角色列表与"唯一激活角色"的前端状态。
//
// 后端保证同时只有一个角色的世界在跑。这里镜像那份注册表,并给侧栏和
// CompanionView 提供同一个数据源,避免两处各自拉列表而对不上。
import { create } from "zustand";
import {
  companionRepository,
  errorText,
  type CompanionCharacter,
  type CompanionProfile,
} from "../../../shared/repositories";

interface CharacterState {
  characters: CompanionCharacter[];
  activeId: string;
  loading: boolean;
  /** 正在切换的目标角色 id;非空时禁止再次切换。 */
  switching: string;
  error: string;
  refresh: () => Promise<void>;
  switchTo: (id: string) => Promise<void>;
  create: (profile: CompanionProfile) => Promise<CompanionCharacter>;
  remove: (id: string) => Promise<void>;
}

function readActive(characters: CompanionCharacter[]): string {
  return characters.find((character) => character.active)?.id ?? "";
}

export const useCharacterStore = create<CharacterState>((set, get) => ({
  characters: [],
  activeId: "",
  loading: true,
  switching: "",
  error: "",

  refresh: async () => {
    try {
      const characters = await companionRepository.listCharacters();
      set({
        characters,
        activeId: readActive(characters),
        loading: false,
        error: "",
      });
    } catch (cause) {
      set({ loading: false, error: errorText(cause) });
    }
  },

  switchTo: async (id) => {
    const state = get();
    // 单飞:切换请求还在路上时忽略后续点击,避免两个 SwitchCharacter 交错。
    if (state.switching || id === state.activeId) return;
    set({ switching: id });
    try {
      await companionRepository.switchCharacter(id);
      const characters = await companionRepository.listCharacters();
      set({ characters, activeId: readActive(characters), switching: "", error: "" });
    } catch (cause) {
      set({ switching: "", error: errorText(cause) });
      throw cause;
    }
  },

  create: async (profile) => {
    const created = await companionRepository.createCharacter(profile);
    const characters = await companionRepository.listCharacters();
    set({ characters, activeId: readActive(characters) });
    return created;
  },

  remove: async (id) => {
    await companionRepository.deleteCharacter(id);
    const characters = await companionRepository.listCharacters();
    set({ characters, activeId: readActive(characters) });
  },
}));
