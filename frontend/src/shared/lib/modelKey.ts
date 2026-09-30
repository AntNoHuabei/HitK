// 模型在扁平列表里的唯一键。
//
// 放 shared 是因为 chat 和陪伴两边都要拿它比对、传值，谁都不该为了一个小
// helper 去 import 对方的 controller。
export function optionKey(provider: string, model: string): string {
  return `${provider}::${model}`;
}
