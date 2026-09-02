import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// guides-drift（UI_GUIDANCE_SPEC G1-2）：手册页签的 ?raw 嵌入副本必须与
// 仓库 docs/ 原件逐字节一致——docs 更新后忘了 cp 进 src/guides/ 即测试红，
// 防止应用内手册腐化。修复方式：cp docs/<file> desktop/frontend/src/guides/。

const DOCS = ["NETDEV_USAGE.md", "NETDEV_HELP.md", "browser-ops-guide.md"];
const repoRoot = resolve(__dirname, "../../../../");

describe("in-app manual guides stay in sync with docs/", () => {
  for (const file of DOCS) {
    it(`${file} matches the bundled copy`, () => {
      const original = readFileSync(resolve(repoRoot, "docs", file), "utf8");
      const bundled = readFileSync(resolve(repoRoot, "desktop/frontend/src/guides", file), "utf8");
      expect(bundled).toBe(original);
    });
  }
});
