// repository 层的公共工具。

/**
 * toError 把 Wails 的任意拒绝归一化成 Error。
 *
 * Wails 拒绝时抛的是字符串而不是 Error,而且对话框、连接测试这类调用本身就
 * 会经常失败。在这里统一,组件就不必记住这两件事。
 */
export function toError(err: unknown): Error {
  if (err instanceof Error) return err;
  if (typeof err === "string") return new Error(err);
  if (err && typeof err === "object" && "message" in err) {
    return new Error(String((err as { message: unknown }).message));
  }
  return new Error("发生未知错误");
}

/** errorText 渲染任意拒绝信息。 */
export function errorText(err: unknown): string {
  return toError(err).message;
}

/**
 * list 把 Go 的 nil slice 兜成空数组。
 *
 * 过桥后 nil slice 就是 `null`(json.Marshal(nil slice) → null),生成类型也
 * 如实标成 `T[] | null`。所有列表型返回值都经过这里,调用方拿到的永远是数组。
 */
export function list<T>(items: T[] | null | undefined): T[] {
  return items ?? [];
}

/** text 把可选的字符串字段兜成空串。 */
export function text(value: string | null | undefined): string {
  return value ?? "";
}

/**
 * stringMap 把 Go 的 map[string]string 归一化成不含 undefined 的记录。
 *
 * 生成类型里索引签名的值是 `string | undefined`(键可能不存在),直接当
 * `Record<string, string>` 用会让每个取值点都要判空。
 */
export function stringMap(
  value: Record<string, string | undefined> | null | undefined,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, item] of Object.entries(value ?? {})) {
    if (typeof item === "string") out[key] = item;
  }
  return out;
}
