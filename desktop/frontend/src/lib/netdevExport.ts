import { app } from "./bridge";

// netdevExport — C3.2 导出体系的统一落盘通道（completion-spec §4.2）：
// 复用会话导出的 PickExportFile/SaveExportFile 桥，全部为纯前端文本。
// 返回落盘路径（空 = 用户取消），调用方 toast 提示。
export async function exportTextFile(defaultFilename: string, text: string, mime = "text/plain"): Promise<string> {
  const path = await app.PickExportFile(defaultFilename, mime);
  if (!path) return "";
  await app.SaveExportFile(path, text, false);
  return path;
}
