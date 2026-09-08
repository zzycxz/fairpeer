// toolCards is the per-tool card spec registry (upgrade spec 1-1; first
// dedicated-card batch per FAIRPEER_CODEX_GAP_SPEC Spec-1): tools whose
// result deserves better than the generic args/output JSON card register here,
// and ToolCard swaps the registered body in while keeping its shell (status,
// subject, stat, duration, collapse) shared across every tool. A spec may also
// tweak shell behaviour: forceOpen opens the card by default once it settles,
// noQuiet keeps a read-only card from fading after completion.
//
// This is deliberately per-name, not per-profile: dev/cowork/netdev share the
// ToolCard pipeline, so one registration serves all three layouts.
import { useState } from "react";
import type { ReactNode } from "react";
import { Markdown } from "../components/Markdown";
import { CodeViewer } from "../components/CodeViewer";
import { useT } from "./i18n";
import type { Item } from "./useController";

export type ToolItem = Extract<Item, { kind: "tool" }>;

export interface ToolCardSpec {
  // body replaces the card's default args/output body. Return undefined to
  // keep the default for that state (e.g. while running, before output).
  body?: (item: ToolItem) => ReactNode;
  forceOpen?: boolean;
  noQuiet?: boolean;
}

// splitHeadTail folds long output around a "… +N lines" marker (Spec-1.4).
// Card bodies use tighter budgets than the shell panel: the card shares the
// transcript with everything else.
function splitHeadTail(text: string, head: number, tail: number, fullThreshold: number): { preview: string; total: number; hasMore: boolean } {
  const lines = text.split("\n");
  const total = lines.length;
  if (total <= fullThreshold || total <= head + tail + 1) return { preview: text, total, hasMore: false };
  const hidden = total - head - tail;
  return { preview: [...lines.slice(0, head), `… +${hidden} lines`, ...lines.slice(-tail)].join("\n"), total, hasMore: true };
}

function argField(item: ToolItem, ...keys: string[]): string {
  try {
    const a = JSON.parse(item.args || "{}");
    for (const k of keys) {
      const v = a[k];
      if (typeof v === "string" && v.trim() !== "") return v;
    }
  } catch {
    /* fall through */
  }
  return "";
}

// ShellCardBody (Spec-1.2): agent-initiated bash cards previously dumped the
// args JSON; the command is echoed as a shell line and output folds head+tail
// with a show-all toggle. (!-prefix user shells keep the ToolCard pipeline's
// own isShell branch — this body only replaces the generic one.)
function ShellCardBody({ item }: { item: ToolItem }) {
  const t = useT();
  const [showAll, setShowAll] = useState(false);
  const command = argField(item, "command");
  const output = item.output ?? "";
  const folded = output ? splitHeadTail(output, 8, 12, 60) : null;
  return (
    <div className="toolcard-shell">
      {command && <pre className="toolcard-shell__cmd">$ {command}</pre>}
      {folded && (
        <>
          <CodeViewer value={showAll ? output : folded.preview} maxHeight={showAll ? 800 : 360} />
          {folded.hasMore && !showAll && (
            <button className="tool__showall" onClick={() => setShowAll(true)}>
              {t("tool.showAllLines", { n: folded.total })}
            </button>
          )}
        </>
      )}
      {item.truncated && <div className="tool__note">{t("tool.truncated")}</div>}
      {item.error && <div className="tool__err">{item.error}</div>}
    </div>
  );
}

// OfficeWriteCardBody (Spec-1.2): office writers produce a file, not code —
// show the target path (doc_convert also its source) and the tool's own
// summary output, instead of the raw args JSON.
function OfficeWriteCardBody({ item }: { item: ToolItem }) {
  const path = argField(item, "path", "file_path", "out_path");
  const source = argField(item, "source_path", "path");
  return (
    <div className="toolcard-office">
      {path && (
        <div className="toolcard-office__path">
          {source && source !== path ? `${source} → ${path}` : path}
        </div>
      )}
      {item.output && <CodeViewer value={item.output} maxHeight={200} />}
      {item.error && <div className="tool__err">{item.error}</div>}
    </div>
  );
}

// EmailCardBody (Spec-1.2): recipients + subject up front, the send receipt
// (message id/preview) as the body.
function EmailCardBody({ item }: { item: ToolItem }) {
  let recipients = "";
  let subject = "";
  try {
    const a = JSON.parse(item.args || "{}");
    const to = Array.isArray(a.to) ? a.to.join(", ") : typeof a.to === "string" ? a.to : "";
    recipients = to;
    subject = typeof a.subject === "string" ? a.subject : "";
  } catch {
    /* fall through */
  }
  return (
    <div className="toolcard-office">
      {(recipients || subject) && (
        <div className="toolcard-office__path">
          {recipients && `→ ${recipients}`}
          {recipients && subject ? " — " : ""}
          {subject}
        </div>
      )}
      {item.output && <CodeViewer value={item.output} maxHeight={160} />}
      {item.error && <div className="tool__err">{item.error}</div>}
    </div>
  );
}

// BrowserActionCardBody (Spec-1.2): one card shape for the cowork browser/
// screen/window surface (33 tools) — the action's target (url / selector /
// text / …) up front, the tool's result below, instead of the args JSON dump.
function BrowserActionCardBody({ item }: { item: ToolItem }) {
  const target = argField(
    item,
    "url", "cdp_url", "selector", "css", "text", "keys", "key", "query",
    "path", "file_path", "file", "tab", "window", "title", "target", "script", "expression",
  );
  return (
    <div className="toolcard-office">
      {target && <div className="toolcard-office__path">{target}</div>}
      {item.output && <CodeViewer value={item.output} maxHeight={260} />}
      {item.error && <div className="tool__err">{item.error}</div>}
    </div>
  );
}

// CompleteStepCardBody (NETDEV_OPSTEP_EVIDENCE_SPEC 前端尾巴): the sign-off
// renders as the step + its evidence rows — device references ("device:<name>",
// OpStep 台账验证) show as badges, file citations as plain code.
function CompleteStepCardBody({ item }: { item: ToolItem }) {
  let step = "";
  let evidence: { kind?: string; summary?: string; command?: string; paths?: string[] }[] = [];
  try {
    const a = JSON.parse(item.args || "{}");
    if (typeof a.step === "string") step = a.step;
    if (Array.isArray(a.evidence)) evidence = a.evidence;
  } catch {
    /* fall through */
  }
  return (
    <div className="toolcard-office">
      {step && <div className="toolcard-office__path">✅ {step}</div>}
      {evidence.map((e, i) => (
        <div key={i} className="toolcard-step__row">
          <span className="toolcard-step__kind">{e.kind ?? "?"}</span>
          <span className="toolcard-step__summary">{e.summary ?? ""}</span>
          {(e.paths ?? []).map((p, j) =>
            p.toLowerCase().startsWith("device:") ? (
              <span key={j} className="toolcard-device-badge" title={`OpStep 台账验证：${p.slice(7)}`}>{p.slice(7)}</span>
            ) : (
              <code key={j} className="toolcard-step__path">{p}</code>
            ),
          )}
        </div>
      ))}
      {item.output && <CodeViewer value={item.output} maxHeight={140} />}
      {item.error && <div className="tool__err">{item.error}</div>}
    </div>
  );
}

const browserActionBody = (item: ToolItem) => <BrowserActionCardBody item={item} />;

const registry: Record<string, ToolCardSpec> = {
  // Search results read as links and snippets, not as a JSON args dump.
  web_search: {
    body: (item) => (item.output ? <Markdown text={item.output} /> : undefined),
  },
  // Agent bash cards: shell line + head/tail folded output (Spec-1).
  // !-prefix user shells (isShell) keep ToolCard's own richer live-stream
  // branch — returning undefined defers to it (avoids rendering both).
  bash: { body: (item) => (item.isShell ? undefined : <ShellCardBody item={item} />) },
  // Office writers (Spec-1): target path + tool summary instead of args JSON.
  doc_write: { body: (item) => <OfficeWriteCardBody item={item} /> },
  csv_write: { body: (item) => <OfficeWriteCardBody item={item} /> },
  xlsx_write: { body: (item) => <OfficeWriteCardBody item={item} /> },
  mindmap_create: { body: (item) => <OfficeWriteCardBody item={item} /> },
  doc_convert: { body: (item) => <OfficeWriteCardBody item={item} /> },
  email_send: { body: (item) => <EmailCardBody item={item} /> },
  // 签核卡（Spec-3 / NETDEV_OPSTEP_EVIDENCE_SPEC）：步骤 + 证据行 + device 徽标；
  // noQuiet——签核是审计痕迹，完成后不淡出。
  complete_step: { body: (item) => <CompleteStepCardBody item={item} />, noQuiet: true },
  // Cowork browser/desktop automation (Spec-1.2): one action-card shape for
  // the whole browser_* / screen_* / window_* surface. Names enumerated from
  // the backend registration lists (BrowserTools/ScreenTools/WindowTools).
  browser_open: { body: browserActionBody },
  browser_attach: { body: browserActionBody },
  browser_navigate: { body: browserActionBody },
  browser_tabs: { body: browserActionBody },
  browser_switch_tab: { body: browserActionBody },
  browser_hover: { body: browserActionBody },
  browser_back: { body: browserActionBody },
  browser_forward: { body: browserActionBody },
  browser_click: { body: browserActionBody },
  browser_type: { body: browserActionBody },
  browser_scroll: { body: browserActionBody },
  browser_extract: { body: browserActionBody },
  browser_screenshot: { body: browserActionBody },
  browser_evaluate: { body: browserActionBody },
  browser_snapshot: { body: browserActionBody },
  browser_select_option: { body: browserActionBody },
  browser_upload_file: { body: browserActionBody },
  browser_set_path: { body: browserActionBody },
  browser_wait: { body: browserActionBody },
  browser_keepalive: { body: browserActionBody },
  browser_auto: { body: browserActionBody },
  screenshot: { body: browserActionBody },
  get_ui_tree: { body: browserActionBody },
  screen_click: { body: browserActionBody },
  screen_key: { body: browserActionBody },
  screen_type: { body: browserActionBody },
  screen_scroll: { body: browserActionBody },
  screen_perceive: { body: browserActionBody },
  window_focus: { body: browserActionBody },
  window_maximize: { body: browserActionBody },
  window_restore: { body: browserActionBody },
  window_move: { body: browserActionBody },
  window_close: { body: browserActionBody },
  // Ops evidence stays readable: the command output is the point of the card,
  // so it opens by default and never fades to quiet after completion.
  netdev_exec: { forceOpen: true, noQuiet: true },
  netdev_netconf: { forceOpen: true, noQuiet: true },
  // 弃用别名（discover/nmap/netprobe → probe）：别名调用期间卡片继续工作。
  netdev_discover: { forceOpen: true, noQuiet: true },
  netdev_probe: { forceOpen: true, noQuiet: true },
  netdev_baseline: { forceOpen: true, noQuiet: true },
};

export function toolCardSpec(name: string): ToolCardSpec | undefined {
  return registry[name];
}
