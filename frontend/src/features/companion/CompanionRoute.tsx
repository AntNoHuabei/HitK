// 陪伴视图的路由入口。
//
// 切角色时靠 key 把整棵视图重建一遍，直接复用视图自带的加载态，省掉一套
// 自己写的换人过渡。包这一层是为了让外壳只认识"陪伴路由"，不必知道角色
// 注册表的存在。
import { CompanionView } from "./CompanionView";
import { useCharacterStore } from "./model/characterStore";

export function CompanionRoute() {
  const activeId = useCharacterStore((state) => state.activeId);
  return <CompanionView key={activeId} />;
}
