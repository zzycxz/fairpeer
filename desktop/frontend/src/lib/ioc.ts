// IOC 批量导入解析（NETDEV_SPEC_V2 §4.6「外部 feed 导入」来源的实现）：
// 用户自备威胁情报清单（CERT 通报 / 厂商通告 / 兄弟单位共享），每行一个
// 粘贴进案例台账，类型自动识别；行内「#」后为备注，「#」开头的行跳过。

export interface ParsedIOC {
  value: string;
  type: string; // ip | domain | hash | keyword
  note: string;
}

// detectIOCType — heuristics, in priority order: IPv4/IPv6 → hash（32/64
// 位十六进制，md5/sha256）→ domain（含点分隔的字母数字段）→ keyword。
export function detectIOCType(v: string): string {
  if (/^\d{1,3}(\.\d{1,3}){3}(:\d+)?$/.test(v)) return "ip"; // v4（可带 :port）
  if (/^[0-9a-f]{0,4}(:[0-9a-f]{0,4}){2,7}$/i.test(v)) return "ip"; // IPv6（含 ::）
  if (/^[0-9a-f]{32}$|^[0-9a-f]{64}$/i.test(v)) return "hash";
  if (/^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9-]{2,})+$/i.test(v)) return "domain";
  return "keyword";
}

export function parseIOCList(text: string): ParsedIOC[] {
  const out: ParsedIOC[] = [];
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const hash = line.indexOf("#");
    const value = (hash > 0 ? line.slice(0, hash) : line).trim();
    if (!value) continue;
    out.push({ value, type: detectIOCType(value), note: hash > 0 ? line.slice(hash + 1).trim() : "" });
  }
  return out;
}
