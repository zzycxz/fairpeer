// Pure logic tests for skillDoc (SKILL.md ⇄ structured round-trip, the
// lossy guardrail, parameter collection/substitution).
//
// Run: tsx src/__tests__/skill-doc.test.ts

import {
  collectParams,
  parseSkillDoc,
  serializeSkillDoc,
  substituteParams,
  summarizeStep,
} from "../lib/skillDoc";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

const sample = [
  "---",
  "name: portal-patrol",
  "description: 登录运维门户并导出巡检报表。",
  "runAs: subagent",
  "allowed-tools: browser_open, browser_navigate, browser_click, browser_type, browser_wait, browser_extract",
  "params: username=admin",
  "---",
  "",
  "# 门户巡检",
  "",
  "## 何时使用",
  "",
  "需要导出巡检报表时使用。参数 username 为登录名。",
  "",
  "## 步骤",
  "",
  "| # | 操作 | 目标 | 值 |",
  "|---|------|------|------|",
  "| 1 | navigate | `https://ops.portal.local/` | |",
  "| 2 | type | `#user` | {{username}} |",
  "| 3 | type | `#pwd` | {{密码}} |",
  "| 4 | click | `button[type=submit]` | |",
  "| 5 | wait | `networkidle` | 10s |",
  "| 6 | extract | `table.results` | |",
  "",
  "## 注意事项",
  "",
  "密码运行时询问。",
  "",
  "## 验证",
  "",
  "提取结果非空。",
  "",
].join("\n");

console.log("\nparseSkillDoc");
const doc = parseSkillDoc(sample);
eq(doc !== null, true, "parses a well-formed skill");
if (doc) {
  eq(doc.name, "portal-patrol", "name");
  eq(doc.title, "门户巡检", "h1 title");
  eq(doc.params.username, "admin", "frontmatter params");
  eq(doc.allowedTools.length, 6, "allowed-tools list");
  eq(doc.steps.length, 6, "six steps");
  eq(doc.steps[0].type, "navigate", "step 1 type");
  eq(doc.steps[0].url, "https://ops.portal.local/", "step 1 url");
  eq(doc.steps[1].text, "{{username}}", "type step carries value cell as text");
  eq(doc.steps[4].condition, "networkidle", "wait condition");
  eq(doc.steps[4].timeout_sec, 10, "wait timeout parsed from 值");
  eq(doc.lossy, false, "well-formed skill round-trips losslessly");
}

console.log("\nround-trip");
if (doc) {
  const again = parseSkillDoc(serializeSkillDoc(doc));
  eq(again !== null && again.steps.length, 6, "serialized skill re-parses");
  eq(again?.steps[1].text, "{{username}}", "type text survives round-trip");
  eq(again?.steps[4].timeout_sec, 10, "wait timeout survives round-trip");
  eq(again?.lossy, false, "round-trip is lossless");
}

console.log("\nguardrail (lossy detection)");
eq(parseSkillDoc("no frontmatter here"), null, "missing frontmatter → null");
const lossy = parseSkillDoc(sample.replace("## 注意事项", "## 自定义段落"));
eq(lossy?.lossy, true, "unknown section flagged lossy");
const lossyKeys = parseSkillDoc(sample.replace("runAs: subagent\n", "runAs: subagent\nversion: 2\n"));
eq(lossyKeys?.lossy, true, "unknown frontmatter key flagged lossy");

console.log("\nparams");
if (doc) {
  eq(collectParams(doc).sort(), ["username", "密码"].sort(), "collects unique {{refs}}");
  const substituted = substituteParams(doc.steps, { username: "ops", 密码: "hunter2" });
  eq(substituted[1].text, "ops", "substitutes typed value");
  eq(substituted[2].text, "hunter2", "substitutes password value");
  const missing = substituteParams(doc.steps, {});
  eq(missing[1].text, "", "missing params substitute to empty string");
}

console.log("\nsummarizeStep");
eq(summarizeStep({ type: "navigate", url: "https://x/" }), "打开 https://x/", "navigate summary");
eq(summarizeStep({ type: "click", target: "#go", label: "登录" }), "点击 登录", "click prefers label");
eq(summarizeStep({ type: "wait", condition: "networkidle", timeout_sec: 15 }), "等待 networkidle", "wait summary");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
