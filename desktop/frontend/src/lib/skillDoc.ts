// skillDoc — SKILL.md ⇄ structured dual-mode parsing for the browser skill
// editor. The generated skills carry a four-section body (何时使用/步骤/
// 注意事项/验证) whose 步骤 section is a markdown table — that table IS the
// workflow. The editor edits the structured form and serializes back.
//
// Guardrail (plan: 防混乱护栏 #1): parsing is only allowed to be LOSSLESS.
// parseSkillDoc reports content it cannot round-trip (unknown frontmatter
// keys, extra sections, non-table prose inside 步骤) via `lossy` — the
// editor then refuses the structured↔source switch and stays in source mode
// instead of silently destroying hand edits.
import type { BrowserConsoleStep, BrowserConsoleStepType } from "./types";

export interface SkillDoc {
  title: string;
  name: string;
  description: string;
  runAs: string;
  allowedTools: string[];
  // {{参数}} → 默认值（空串 = 运行时询问）
  params: Record<string, string>;
  whenToUse: string;
  steps: BrowserConsoleStep[];
  pitfalls: string;
  verification: string;
  // lossy = the source contains content serializeSkillDoc would drop.
  lossy: boolean;
}

const SECTION_ALIASES: Record<string, keyof SkillDoc | "title"> = {
  "何时使用": "whenToUse",
  "when to use": "whenToUse",
  "步骤": "steps",
  "procedure": "steps",
  "steps": "steps",
  "注意事项": "pitfalls",
  "坑": "pitfalls",
  "pitfalls": "pitfalls",
  "验证": "verification",
  "verification": "verification",
};

const KNOWN_FRONTMATTER = new Set(["name", "description", "runas", "allowed-tools", "params"]);

const STEP_TYPES = new Set<string>([
  "navigate", "click", "type", "key", "scroll", "select", "upload", "wait", "extract", "screenshot", "evaluate",
]);

export function parseSkillDoc(content: string): SkillDoc | null {
  const text = content.replace(/\r\n/g, "\n").trim();
  if (!text.startsWith("---")) return null;
  const fmEnd = text.indexOf("\n---", 3);
  if (fmEnd < 0) return null;
  const fm = text.slice(3, fmEnd).trim();
  let body = text.slice(fmEnd + 4).trim();

  const doc: SkillDoc = {
    title: "",
    name: "",
    description: "",
    runAs: "subagent",
    allowedTools: [],
    params: {},
    whenToUse: "",
    steps: [],
    pitfalls: "",
    verification: "",
    lossy: false,
  };

  for (const line of fm.split("\n")) {
    const m = /^([A-Za-z_-]+):\s*(.*)$/.exec(line.trim());
    if (!m) continue;
    const key = m[1].toLowerCase();
    const value = m[2].trim();
    if (!KNOWN_FRONTMATTER.has(key)) {
      doc.lossy = true; // unknown frontmatter key would be dropped
      continue;
    }
    switch (key) {
      case "name":
        doc.name = stripQuotes(value);
        break;
      case "description":
        doc.description = stripQuotes(value);
        break;
      case "runas":
        doc.runAs = stripQuotes(value);
        break;
      case "allowed-tools":
        doc.allowedTools = value.split(",").map((s) => s.trim()).filter(Boolean);
        break;
      case "params":
        for (const pair of value.split(",")) {
          const eq = pair.indexOf("=");
          if (eq > 0) doc.params[pair.slice(0, eq).trim()] = pair.slice(eq + 1).trim();
        }
        break;
    }
  }
  if (!doc.name) return null;

  // H1 title (optional).
  const h1 = /^#\s+(.+)$/m.exec(body);
  if (h1) {
    doc.title = h1[1].trim();
    body = body.replace(h1[0], "").trim();
  }

  // Split into ## sections.
  const sections: { heading: string; body: string }[] = [];
  const parts = body.split(/^##\s+/m);
  for (const part of parts) {
    if (!part.trim()) continue;
    const nl = part.indexOf("\n");
    const heading = (nl < 0 ? part : part.slice(0, nl)).trim().toLowerCase();
    const secBody = nl < 0 ? "" : part.slice(nl + 1).trim();
    sections.push({ heading, body: secBody });
  }

  for (const sec of sections) {
    const field = SECTION_ALIASES[sec.heading];
    if (!field || field === "title") {
      doc.lossy = true; // unknown section would be dropped
      continue;
    }
    if (field === "steps") {
      parseStepsTable(sec.body, doc);
    } else {
      (doc[field] as string) = sec.body;
    }
  }
  return doc;
}

function parseStepsTable(body: string, doc: SkillDoc): void {
  for (const rawLine of body.split("\n")) {
    const line = rawLine.trim();
    if (!line.startsWith("|")) {
      if (line) doc.lossy = true; // non-table prose inside 步骤 would be dropped
      continue;
    }
    const cells = line.split("|").map((c) => c.trim()).filter((_, i, a) => i > 0 && i < a.length);
    if (cells.length < 4) continue;
    const [, op, targetCell, valueCell] = cells;
    if (!op || /^-+$/.test(op) || op === "操作") continue; // header / separator
    if (!STEP_TYPES.has(op.toLowerCase())) {
      doc.lossy = true; // unknown operation would be dropped
      continue;
    }
    const target = stripBackticks(targetCell);
    const step = stepFromRow(op.toLowerCase() as BrowserConsoleStepType, target, valueCell ?? "");
    if (step) doc.steps.push(step);
  }
}

function stepFromRow(type: BrowserConsoleStepType, target: string, value: string): BrowserConsoleStep | null {
  switch (type) {
    case "navigate":
      return { type, url: target };
    case "click":
    case "extract":
      return { type, target };
    case "type":
      return { type, target, text: value };
    case "select":
      return { type, target, value };
    case "key":
      return { type, target: target || undefined, value: value || "enter" };
    case "wait":
      return { type, condition: target || "networkidle", timeout_sec: parseSeconds(value) };
    case "scroll":
      return { type, direction: target || "down", amount: parseInt(value, 10) || 3 };
    case "upload":
      return { type, target, files: value.split(",").map((s) => s.trim()).filter(Boolean) };
    case "screenshot":
      return { type };
    case "evaluate":
      return { type, expression: target };
    default:
      return null;
  }
}

function parseSeconds(v: string): number {
  const m = /(\d+)\s*s?/i.exec(v.trim());
  return m ? parseInt(m[1], 10) : 15;
}

export function serializeSkillDoc(doc: SkillDoc): string {
  const fm: string[] = ["---"];
  fm.push(`name: ${doc.name}`);
  fm.push(`description: ${doc.description.replace(/\n/g, " ")}`);
  fm.push(`runAs: ${doc.runAs || "subagent"}`);
  if (doc.allowedTools.length) fm.push(`allowed-tools: ${doc.allowedTools.join(", ")}`);
  const paramKeys = Object.keys(doc.params);
  if (paramKeys.length) {
    fm.push(`params: ${paramKeys.map((k) => `${k}=${doc.params[k] ?? ""}`).join(", ")}`);
  }
  fm.push("---", "");
  if (doc.title) fm.push(`# ${doc.title}`, "");
  fm.push("## 何时使用", "", doc.whenToUse.trim() || "（待补充）", "");
  fm.push("## 步骤", "", "| # | 操作 | 目标 | 值 |", "|---|------|------|------|");
  doc.steps.forEach((s, i) => {
    fm.push(`| ${i + 1} | ${s.type} | \`${rowTarget(s)}\` | ${rowValue(s)} |`);
  });
  fm.push("", "## 注意事项", "", doc.pitfalls.trim() || "（待补充）", "");
  fm.push("## 验证", "", doc.verification.trim() || "（待补充）", "");
  return fm.join("\n");
}

function rowTarget(s: BrowserConsoleStep): string {
  switch (s.type) {
    case "navigate":
      return s.url ?? "";
    case "wait":
      return s.condition ?? "networkidle";
    case "scroll":
      return s.direction ?? "down";
    case "evaluate":
      return s.expression ?? "";
    case "screenshot":
      return "";
    default:
      return s.target ?? "";
  }
}

function rowValue(s: BrowserConsoleStep): string {
  switch (s.type) {
    case "type":
      return s.text ?? "";
    case "select":
    case "key":
      return s.value ?? "";
    case "wait":
      return `${s.timeout_sec ?? 15}s`;
    case "scroll":
      return String(s.amount ?? 3);
    case "upload":
      return (s.files ?? []).join(", ");
    default:
      return "";
  }
}

// collectParams returns the unique {{name}} references across the doc — the
// editor derives its parameter list from usage, defaults come from params.
export function collectParams(doc: SkillDoc): string[] {
  const refs = new Set<string>();
  const scan = (s?: string) => {
    for (const m of s?.matchAll(/\{\{([^}]+)\}\}/g) ?? []) refs.add(m[1].trim());
  };
  scan(doc.whenToUse);
  scan(doc.pitfalls);
  scan(doc.verification);
  for (const s of doc.steps) {
    scan(s.target);
    scan(s.url);
    scan(s.text);
    scan(s.value);
    scan(s.expression);
    scan(s.condition);
  }
  return [...refs];
}

// substituteParams replaces {{name}} references with the supplied values —
// the trial runner receives final values only. Missing params are replaced
// with "" (the editor asks for them first).
export function substituteParams(steps: BrowserConsoleStep[], params: Record<string, string>): BrowserConsoleStep[] {
  const sub = (s?: string): string | undefined =>
    s === undefined ? undefined : s.replace(/\{\{([^}]+)\}\}/g, (_all, name: string) => params[name.trim()] ?? "");
  return steps.map((s) => ({
    ...s,
    target: sub(s.target),
    url: sub(s.url),
    text: sub(s.text),
    value: sub(s.value),
    expression: sub(s.expression),
    condition: sub(s.condition),
  }));
}

// summarizeStep renders the collapsed row text — DevTools-Recorder style
// "verb + element label" — for step rows and the live recording stream.
export function summarizeStep(step: BrowserConsoleStep): string {
  const label = step.label || step.target || step.url || step.condition || "";
  switch (step.type) {
    case "navigate":
      return `打开 ${step.url ?? ""}`;
    case "click":
      return `点击 ${label}`;
    case "type":
      return `输入 ${step.text ? "…" : ""}（${label}）`;
    case "key":
      return `按键 ${step.value ?? "enter"}`;
    case "scroll":
      return `滚动 ${step.direction ?? "down"} ×${step.amount ?? 3}`;
    case "select":
      return `选择 ${step.value ?? ""}（${label}）`;
    case "upload":
      return `上传 ${(step.files ?? []).length} 个文件`;
    case "wait":
      return `等待 ${step.condition ?? "networkidle"}`;
    case "extract":
      return `提取 ${label || "整页"}`;
    case "screenshot":
      return "截图";
    case "evaluate":
      return "执行脚本";
    default:
      return label;
  }
}

function stripQuotes(v: string): string {
  const t = v.trim();
  if ((t.startsWith(`"`) && t.endsWith(`"`)) || (t.startsWith("'") && t.endsWith("'"))) {
    return t.slice(1, -1);
  }
  return t;
}

function stripBackticks(v: string): string {
  const t = v.trim();
  return t.startsWith("`") && t.endsWith("`") ? t.slice(1, -1) : t;
}

