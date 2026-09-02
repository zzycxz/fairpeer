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
import { SKILL_TEMPLATE_GROUPS } from "../lib/skillTemplates";

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

console.log("\nhuman breakpoint (人工断点)");
const humanSample = [
  "---",
  "name: sms-login",
  "description: 短信验证码登录。",
  "runAs: subagent",
  "---",
  "",
  "# 短信登录",
  "",
  "## 何时使用",
  "",
  "需要人工完成验证码登录时。",
  "",
  "## 步骤",
  "",
  "| # | 操作 | 目标 | 值 |",
  "|---|------|------|------|",
  "| 1 | navigate | `https://portal.local/login` | |",
  "| 2 | human | `url:/home` | 请在浏览器中输入短信验证码并提交 |",
  "| 3 | wait | `networkidle` | 10s |",
  "",
  "## 注意事项",
  "",
  "无。",
  "",
  "## 验证",
  "",
  "地址栏进入 /home。",
  "",
].join("\n");
const hdoc = parseSkillDoc(humanSample);
eq(hdoc !== null, true, "human-step skill parses");
if (hdoc) {
  eq(hdoc.lossy, false, "human step is not lossy");
  eq(hdoc.steps[1].type, "human", "step 2 is a human breakpoint");
  eq(hdoc.steps[1].condition, "url:/home", "condition rides the target column");
  eq(hdoc.steps[1].text, "请在浏览器中输入短信验证码并提交", "prompt rides the value column");
  eq(hdoc.steps[1].timeout_sec, 600, "human timeout defaults to 10 minutes");
  const hagain = parseSkillDoc(serializeSkillDoc(hdoc));
  eq(hagain?.steps[1].type, "human", "human type survives round-trip");
  eq(hagain?.steps[1].condition, "url:/home", "condition survives round-trip");
  eq(hagain?.steps[1].text, "请在浏览器中输入短信验证码并提交", "prompt survives round-trip");
  eq(hagain?.lossy, false, "human round-trip is lossless");
}
eq(
  summarizeStep({ type: "human", condition: "url:/home", text: "输入短信验证码" }),
  "等待人工（自动检测 url:/home）：输入短信验证码",
  "human summary",
);

console.log("\nask step (运行时询问)");
const askSample = [
  "---",
  "name: ask-flow",
  "description: 询问后填报。",
  "runAs: subagent",
  "---",
  "",
  "# 询问填报",
  "",
  "## 何时使用",
  "",
  "需要运行时数据。",
  "",
  "## 步骤",
  "",
  "| # | 操作 | 目标 | 值 |",
  "|---|------|------|------|",
  "| 1 | ask | 工单号 | 请输入要提交的工单号： |",
  "| 2 | type | `#order-no` | {{工单号}} |",
  "",
  "## 注意事项",
  "",
  "无。",
  "",
  "## 验证",
  "",
  "提交成功。",
  "",
].join("\n");
const adoc = parseSkillDoc(askSample);
eq(adoc !== null, true, "ask-step skill parses");
if (adoc) {
  eq(adoc.lossy, false, "ask step is not lossy");
  eq(adoc.steps[0].type, "ask", "step 1 is an ask");
  eq(adoc.steps[0].target, "工单号", "bind name rides the target column");
  eq(adoc.steps[0].text, "请输入要提交的工单号：", "question rides the value column");
  const aagain = parseSkillDoc(serializeSkillDoc(adoc));
  eq(aagain?.steps[0].type, "ask", "ask type survives round-trip");
  eq(aagain?.steps[0].target, "工单号", "bind survives round-trip");
  eq(aagain?.steps[0].text, "请输入要提交的工单号：", "question survives round-trip");
  eq(aagain?.lossy, false, "ask round-trip is lossless");
  eq(collectParams(adoc).includes("工单号"), true, "{{工单号}} ref collected for the params list");
}
eq(summarizeStep({ type: "ask", target: "工单号", text: "要提交哪个工单号？" }), "询问 {{工单号}}：要提交哪个工单号？", "ask summary");

console.log("\nhistory nav + table extract steps");
const navSample = [
  "---",
  "name: browser-nav-demo",
  "description: 后退与表格提取演示。",
  "runAs: subagent",
  "---",
  "",
  "# 演示",
  "",
  "## 何时使用",
  "",
  "演示用。",
  "",
  "## 步骤",
  "",
  "| # | 操作 | 目标 | 值 |",
  "|---|------|------|------|",
  "| 1 | navigate | `https://a/` |  |",
  "| 2 | click | `a.detail` |  |",
  "| 3 | back |  |  |",
  "| 4 | forward |  |  |",
  "| 5 | extract | `table.logs` | table |",
  "",
  "## 注意事项",
  "",
  "无。",
  "",
  "## 验证",
  "",
  "无。",
  "",
].join("\n");
const ndoc = parseSkillDoc(navSample);
eq(ndoc !== null, true, "nav demo parses");
if (ndoc) {
  eq(ndoc.lossy, false, "back/forward/extract-table rows are lossless");
  eq(ndoc.steps[2].type, "back", "back step parses");
  eq(ndoc.steps[3].type, "forward", "forward step parses");
  eq(ndoc.steps[4].value, "table", "extract table flag rides the value column");
  const nagain = parseSkillDoc(serializeSkillDoc(ndoc));
  eq(nagain?.steps[2].type, "back", "back survives round-trip");
  eq(nagain?.steps[4].value, "table", "table flag survives round-trip");
  eq(summarizeStep({ type: "back" }), "后退一页", "back summary");
  eq(summarizeStep({ type: "extract", target: "table.logs", value: "table" }), "提取表格 table.logs", "table extract summary");
  const hDoc = parseSkillDoc(navSample.replace("| 3 | back |  |  |", "| 3 | hover | `nav.menu;;text=设置` |  |"));
  eq(hDoc?.steps[2].type, "hover", "hover row parses");
  eq(hDoc?.steps[2].target, "nav.menu;;text=设置", "hover carries the anchor chain");
  eq(parseSkillDoc(serializeSkillDoc(hDoc!))?.steps[2].type, "hover", "hover round-trips");
  eq(summarizeStep({ type: "hover", target: "nav.menu" }), "悬停 nav.menu", "hover summary");
}

console.log("\nskill template library guard (运维技能库)");
// Every table-form template must parse losslessly (the structured editor
// opens them editable); every prose template must at least carry a valid
// frontmatter name (the editor opens them in source mode). A template that
// breaks the parser fails here, not in the user's face.
// Names must carry the browser- domain prefix: saved skills are GLOBAL
// (invokable via /name anywhere), bare names like data-export would collide
// with future non-browser skills — and the kernel's builtins set the
// convention (browser-auto, desktop-auto, email-auto).
for (const group of SKILL_TEMPLATE_GROUPS) {
  for (const tpl of group.templates) {
    const doc = parseSkillDoc(tpl.build());
    eq(doc !== null, true, `${tpl.id}: parses`);
    if (!doc) continue;
    eq(doc.name.startsWith("browser-"), true, `${tpl.id}: name carries the browser- prefix (${doc.name})`);
    if (tpl.form === "table") {
      eq(doc.lossy, false, `${tpl.id}: table template is lossless`);
      eq(doc.steps.length > 0, true, `${tpl.id}: has steps`);
      // The four concrete site templates ship deterministic execution (the
      // kernel step runner); blank stays opt-in via the editor toggle.
      eq(tpl.id === "blank" ? doc.executor === "" : doc.executor === "browser-flow", true, `${tpl.id}: executor flag`);
    } else {
      eq(doc.runAs, "inline", `${tpl.id}: prose template is runAs:inline`);
    }
  }
}
eq(SKILL_TEMPLATE_GROUPS.reduce((n, g) => n + g.templates.length, 0) >= 7, true, "library ships at least 7 specialized skills");

console.log("\nmulti-anchor targets (多锚定位)");
// `;;` chains ride the target column verbatim (no `|` so the table cell
// survives) and render as a readable chain in summaries.
const maDoc = parseSkillDoc(
  [
    "---",
    "name: browser-anchor-demo",
    "description: 多锚演示。",
    "runAs: subagent",
    "executor: browser-flow",
    "---",
    "",
    "# 多锚",
    "",
    "## 何时使用",
    "",
    "演示。",
    "",
    "## 步骤",
    "",
    "| # | 操作 | 目标 | 值 |",
    "|---|------|------|------|",
    "| 1 | click | `#kw;;text=百度一下` |  |",
    "| 2 | type | `input[name=wd];;text=搜索` | {{词}} |",
    "",
    "## 注意事项",
    "",
    "无。",
    "",
    "## 验证",
    "",
    "无。",
    "",
  ].join("\n"),
);
eq(maDoc !== null && maDoc.lossy, false, "multi-anchor table is lossless");
eq(maDoc?.steps[0].target, "#kw;;text=百度一下", "anchor chain rides the target cell");
eq(maDoc?.executor, "browser-flow", "executor frontmatter round-trips");
const maAgain = parseSkillDoc(serializeSkillDoc(maDoc!));
eq(maAgain?.steps[0].target, "#kw;;text=百度一下", "anchor chain survives round-trip");
eq(summarizeStep({ type: "click", target: "#kw;;text=百度一下" }), "点击 #kw→「百度一下」", "multi-anchor summary");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
