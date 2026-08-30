// locale-parity.test.ts — WORKSPACE_GIT_SPEC F2 的防回归闸（2026-08-30 运维
// i18n 半修事故的护栏）：en/zh 键集必须完全一致；zh 值不得是「看起来没翻译的
// 英文句子」（>15 字符、小写开头、不含任何 CJK 字符）。合法的纯 ASCII 值
// （URL/格式占位/品牌名/mock 演示串）走白名单。
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const enSrc = readFileSync(join(here, "../locales/en.ts"), "utf-8");
const zhSrc = readFileSync(join(here, "../locales/zh.ts"), "utf-8");

function keys(src: string): Set<string> {
  return new Set([...src.matchAll(/"([A-Za-z0-9_.]+)":\s*"/g)].map((m) => m[1]));
}
function entries(src: string): [string, string][] {
  return [...src.matchAll(/"([A-Za-z0-9_.]+)":\s*"((?:[^"\\]|\\.)*)"/g)].map((m) => [m[1], m[2]] as [string, string]);
}

// zh 里允许保持纯 ASCII 的键：mock 演示串、预览占位、URL/账号格式示例。
// 白名单按完整键名列出，新增条目必须在 PR 里给出理由。
const ASCII_OK = new Set([
  "preview.addressPlaceholder",
  "mock.sessionFixLogin",
  "mock.sessionRefactor",
  "mock.sessionReadme",
  "mock.sessionPlugin",
  "ndv.wiz.phWebhook",
  "ndv.wiz.phBot",
]);

describe("locale parity (F2 guard)", () => {
  it("en and zh expose the same key set", () => {
    const en = keys(enSrc);
    const zh = keys(zhSrc);
    expect([...zh].filter((k) => !en.has(k))).toEqual([]);
    expect([...en].filter((k) => !zh.has(k))).toEqual([]);
  });

  it("zh values are not untranslated English sentences", () => {
    const offenders = entries(zhSrc).filter(([k, v]) => {
      if (ASCII_OK.has(k)) return false;
      if (!v || /\p{Script=Han}/u.test(v)) return false;
      return v.length > 15 && /^[a-z]/.test(v);
    });
    expect(offenders.map(([k]) => k)).toEqual([]);
  });
});
