// BrowserConsolePanel — 运维 dock 的「浏览器」 tab: a three-sub-tab console
// for manually driving the console browser session, recording runs into
// skills, and managing/editing/running those skills. Sub-tab pills follow the
// ZCode right-dock tab language (transparent inactive / soft-pill active);
// the driven browser is the external window, the collapsible preview reads
// the shared mirror stream.
import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type Dispatch, type SetStateAction } from "react";
import {
  BookMarked,
  ChevronDown,
  ChevronsDown,
  Download,
  FileWarning,
  Monitor,
  CircleStop,
  Globe,
  HeartPulse,
  Loader2,
  MousePointerClick,
  Plus,
  Radar,
  RefreshCw,
  Sparkles,
  Square,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { app, onBrowserRecord, onBrowserWatch } from "../../lib/bridge";
import { parseSkillDoc, summarizeStep } from "../../lib/skillDoc";
import { useConfirm } from "../../lib/confirm";
import { useT, type Translator } from "../../lib/i18n";
import { Markdown } from "../Markdown";
import type {
  BrowserConsoleElement,
  BrowserConsoleStep,
  BrowserConsoleTab,
  BrowserConsoleRecordEvent,
  BrowserConsoleSkill,
  BrowserConsoleState,
  BrowserConsoleWatchConfig,
  BrowserConsoleWatchRound,
  BrowserConsoleWatchState,
  BrowserSkillDraft,
  RecentChatView,
} from "../../lib/types";
import { summarizeRecordEvent } from "../../lib/recordTrace";
import { browserMirrorSnapshot, subscribeBrowserMirror } from "../../lib/browserMirror";
import { SKILL_TEMPLATE_GROUPS } from "../../lib/skillTemplates";
import { BrowserSkillEditor } from "./BrowserSkillEditor";

type SubTab = "interact" | "record" | "skills" | "watch";

const LOG_CAP = 50;

// useMirror subscribes to the shared browser mirror store; the sidebar
// preview prefers the CONSOLE session's own frame (per-session buckets)
// over the any-session aggregate.
function useMirror() {
  return useSyncExternalStore(subscribeBrowserMirror, browserMirrorSnapshot);
}
const LIVE_CAP = 200;

export function BrowserConsolePanel({ onInsertComposer }: { onInsertComposer?: (text: string) => void }) {
  const t = useT();
  const [subTab, setSubTab] = useState<SubTab>("interact");
  // Watch live-dot feed for the 巡检 pill (the tab itself subscribes again —
  // the store is event-driven, two subscriptions are cheap).
  const watchFeed = useBrowserWatch();

  // --- shared session state ---
  const [state, setState] = useState<BrowserConsoleState | null>(null);
  const [busy, setBusy] = useState<string>("");
  const [error, setError] = useState("");
  // Run-feedback buffer: no longer rendered (the 动作记录 section shows the
  // structured steps only), but kept as a bounded diagnostic trail writers
  // can still push into.
  const [, setLog] = useState<string[]>([]);
  // 浏览器观察窗 (center viewer): tabs + big mirror bound together — the
  // sidebar keeps only a launcher. Polling gives near-live view of manual
  // changes in the controlled browser (frames otherwise push after actions).
  // 结构化动作历史：面板每个操作记一步，「记录为技能」一键转 SKILL.md。
  const [history, setHistory] = useState<BrowserConsoleStep[]>([]);
  // Drag-reorder state for the history rows (HTML5 DnD — no lib needed).
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [overIdx, setOverIdx] = useState<number | null>(null);
  const dropOn = (i: number) => {
    if (dragIdx !== null && dragIdx !== i) {
      setHistory((prev) => {
        const next = [...prev];
        const [moved] = next.splice(dragIdx, 1);
        next.splice(i, 0, moved);
        return next;
      });
    }
    setDragIdx(null);
    setOverIdx(null);
  };
  // Inline preview (sidebar): invisible until the console session has a
  // frame, then unfolds once — the launcher beside it opens the big bench.
  const [previewOpen, setPreviewOpen] = useState(false);
  const mirror = useMirror();
  const recordStep = useCallback((step: BrowserConsoleStep) => {
    setHistory((prev) => [...prev.slice(-199), step]);
  }, []);

  const appendLog = useCallback((line: string) => {
    setLog((prev) => [...prev.slice(-(LOG_CAP - 1)), line]);
  }, []);

  // 提交为发现（浏览器→发现 生命周期桥，对应日志侧的「转为发现」）：
  // 把当前页（网址+备注）立案进发现中心，参与告警生命周期/通知/晨报。
  const [filing, setFiling] = useState(false);
  const [fileSeverity, setFileSeverity] = useState("warning");
  const [fileNote, setFileNote] = useState("");
  const [fileBusy, setFileBusy] = useState(false);
  const fileFinding = useCallback(async () => {
    const u = state?.url;
    if (!u) return;
    setFileBusy(true);
    try {
      await app.NetDevBrowserFinding(u, fileSeverity, fileNote);
      appendLog(t("brc.fileFindingOk"));
      setFiling(false);
      setFileNote("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setFileBusy(false);
    }
  }, [state?.url, fileSeverity, fileNote, appendLog, t]);

  const [tabs, setTabs] = useState<BrowserConsoleTab[]>([]);

  const refreshState = useCallback(async () => {
    try {
      const [st, tb] = await Promise.all([
        app.BrowserConsoleState(),
        app.BrowserConsoleTabs().catch(() => [] as BrowserConsoleTab[]),
      ]);
      setState(st);
      setTabs(st.open ? tb : []);
    } catch {
      setState({
        open: false, session_id: "", browser: "", attached: false, url: "",
        keep_alive: false, keep_alive_mode: "", keep_alive_url: "", keep_alive_last: 0, keep_alive_err: "",
      });
    }
  }, []);

  useEffect(() => {
    void refreshState();
  }, [refreshState]);
  // One preview at a time: while the center browser workbench is the active
  // bench, the inline mirror disappears entirely (not a collapsed duplicate).
  const [benchActive, setBenchActive] = useState(
    () => new URLSearchParams(window.location.search).get("bench") === "browser",
  );
  useEffect(() => {
    const onBench = (e: Event) => setBenchActive((e as CustomEvent<string>).detail === "browser");
    window.addEventListener("fairpeer:netdev-bench-changed", onBench);
    return () => window.removeEventListener("fairpeer:netdev-bench-changed", onBench);
  }, []);

  const previewImg = (state?.session_id && mirror.sessions[state.session_id]?.image) || mirror.image || "";
  const hadFrameRef = useRef(false);
  if (previewImg) hadFrameRef.current = true;
  useEffect(() => {
    if (previewImg && !previewOpen) setPreviewOpen(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [Boolean(previewImg)]);

  // --- element list lifecycle -------------------------------------------------------
  // Refs (and even scan anchors) belong to the page they were captured on.
  // Tab switches already broadcast fairpeer:browser-console-changed; the URL
  // watchdog below covers every OTHER navigation — URL bar, a click that
  // jumps, back/forward, or manual browsing in the controlled browser.
  const lastElementsUrlRef = useRef("");
  const [scanSummary, setScanSummary] = useState("");
  // Silent re-capture: watchdog / tab-switch / post-navigate — no busy flash,
  // no log noise. The manual button uses the runAction variant for feedback.
  const refreshElementsSilent = useCallback(() => {
    setTarget("");
    setScanSummary("");
    void app.BrowserConsoleElements().then((res) => {
      setElements(res.elements);
      if (res.note) setScanSummary(res.note);
      void app.BrowserConsoleState().then((st) => {
        lastElementsUrlRef.current = st.url;
      }).catch(() => undefined);
    }).catch(() => undefined);
  }, []);

  // The center workbench broadcasts tab switches — refresh state + elements
  // so the dock stays in sync (refs die with the old page).
  useEffect(() => {
    // The workbench dispatches this on tab switches (detail: 1-based index
    // + title). Switching tabs is a real flow step — refs die with the old
    // page and skills must replay the switch — so it records into the
    // structured history, unlike selection/highlight bookkeeping.
    const onConsoleChanged = (e: Event) => {
      const detail = (e as CustomEvent<{ index: number; title: string }>).detail;
      if (detail && detail.index > 0) {
        // Target = the tab TITLE (indexes drift as tabs open/close); the
        // 1-based index rides along as a note for humans.
        recordStep({ type: "switch_tab", target: detail.title || String(detail.index), text: `第 ${detail.index} 个页卡` });
        appendLog(`已切换到页卡 ${detail.index}${detail.title ? `「${detail.title}」` : ""}`);
      }
      setTarget("");
      void refreshState();
      refreshElementsSilent();
    };
    window.addEventListener("fairpeer:browser-console-changed", onConsoleChanged);
    return () => window.removeEventListener("fairpeer:browser-console-changed", onConsoleChanged);
  }, [refreshState, refreshElementsSilent, recordStep, appendLog]);

  // URL watchdog: poll the live page URL; when it moves on from the page the
  // element list was captured on, re-capture automatically. 3s is enough —
  // the alternative (stale refs that error on click) is far worse UX.
  useEffect(() => {
    if (!state?.open) return;
    const timer = window.setInterval(() => {
      void app.BrowserConsoleState().then((st) => {
        if (!st.open || !st.url || st.url === lastElementsUrlRef.current) return;
        if (lastElementsUrlRef.current === "") {
          lastElementsUrlRef.current = st.url; // baseline before any capture
          return;
        }
        refreshElementsSilent();
      }).catch(() => undefined);
    }, 3000);
    return () => window.clearInterval(timer);
  }, [state?.open, refreshElementsSilent]);

  // While keep-alive is armed, poll the session state so the 上次刷新 clock
  // in the status line advances without user interaction.
  useEffect(() => {
    if (!state?.keep_alive) return;
    const timer = window.setInterval(() => void refreshState(), 30_000);
    return () => window.clearInterval(timer);
  }, [state?.keep_alive, refreshState]);

  const runAction = useCallback(
    async (id: string, fn: () => Promise<unknown>, successText?: string) => {
      setError("");
      setBusy(id);
      try {
        const out = await fn();
        if (successText || out) appendLog(successText || String(out));
        await refreshState();
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        setError(msg);
        appendLog(`✕ ${msg}`);
      } finally {
        setBusy("");
      }
    },
    [appendLog, refreshState],
  );

  // --- interact sub-tab ---
  const [url, setUrl] = useState("");
  const [elements, setElements] = useState<BrowserConsoleElement[]>([]);
  const [target, setTarget] = useState("");
  const [text, setText] = useState("");
  const [submitEnter, setSubmitEnter] = useState(false);
  const [extractResult, setExtractResult] = useState("");

  // Manual refresh (the button): same re-capture, through runAction so the
  // button spins and errors surface in the banner.
  const refreshElements = useCallback(() => {
    setTarget("");
    setScanSummary("");
    void runAction("elements", async () => {
      const res = await app.BrowserConsoleElements();
      setElements(res.elements);
      if (res.note) setScanSummary(res.note);
      void app.BrowserConsoleState().then((st) => {
        lastElementsUrlRef.current = st.url;
      }).catch(() => undefined);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runAction]);

  // Stale-ref recovery for HIGHLIGHT clicks: the page re-rendered without a
  // URL change (SPA refresh, polling table), so the ref died mid-list. Refs
  // are opaque, but (role, name) identity survives — re-capture once and
  // re-highlight the fresh ref. Highlight is safe to auto-retry (unlike
  // click: a wrong hit is immediately visible, not a destructive action).
  const recoverStaleHighlight = useCallback((el: BrowserConsoleElement, err: unknown) => {
    const msg = err instanceof Error ? err.message : String(err);
    void (async () => {
      try {
        const res = await app.BrowserConsoleElements();
        const els = res.elements;
        setElements(els);
        void app.BrowserConsoleState().then((st) => {
          lastElementsUrlRef.current = st.url;
        }).catch(() => undefined);
        const match = els.find((x) => x.role === el.role && x.name === el.name && x.ref !== el.ref);
        if (match) {
          setTarget(match.ref);
          await app.BrowserConsoleHighlight(match.ref, 0);
          // Silent self-heal by design: the 动作记录 should carry ACTIONS,
          // not bookkeeping about ref recovery.
          return;
        }
        setError(msg);
        appendLog(`✕ ${msg}`);
      } catch {
        setError(msg);
        appendLog(`✕ ${msg}`);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appendLog]);

  // Deep scan (长页面/瀑布流) — panel-owned like the rest of the element
  // lifecycle: 12 screens keeps the worst case ~4-6s; the summary line
  // reports the stop reason so a deeper feed is an informed re-run.
  const runDeepScan = useCallback(() => {
    setTarget("");
    setScanSummary(t("brc.scanning"));
    void runAction("scan", async () => {
      const res = await app.BrowserConsoleDeepScan(12);
      setElements(res.elements.map((e) => ({ ref: e.selector, role: e.role, name: e.name })));
      void app.BrowserConsoleState().then((st) => {
        lastElementsUrlRef.current = st.url;
      }).catch(() => undefined);
      const stopText =
        res.stop === "bottom" ? t("brc.scanStop.bottom")
          : res.stop === "cap" ? t("brc.scanStop.cap")
            : res.stop === "no-new" ? t("brc.scanStop.no-new")
              : t("brc.scanStop.max-scrolls");
      const summary = t("brc.scanSummary", { screens: res.screens, n: res.elements.length, stop: stopText });
      setScanSummary(summary);
      return summary;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runAction]);

  // --- record sub-tab ---
  const [recording, setRecording] = useState(false);
  const [live, setLive] = useState<BrowserConsoleRecordEvent[]>([]);
  const [dropped, setDropped] = useState<BrowserConsoleRecordEvent[]>([]);
  const [showDropped, setShowDropped] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [skills, setSkills] = useState<BrowserConsoleSkill[]>([]);

  useEffect(() => onBrowserRecord((ev) => {
    setLive((prev) => (ev.type === "effect" ? prev : [...prev.slice(-(LIVE_CAP - 1)), ev]));
  }), []);

  const refreshSkills = useCallback(async () => {
    try {
      setSkills(await app.BrowserConsoleListSkills());
    } catch { /* listed empty on error */ }
  }, []);

  useEffect(() => {
    void refreshSkills();
  }, [refreshSkills]);

  // Re-list skills each time the tab is entered (external edits / deletions
  // from outside the panel stay visible).
  useEffect(() => {
    if (subTab === "skills") void refreshSkills();
  }, [subTab, refreshSkills]);

  const startRecord = () =>
    void runAction("record", async () => {
      setLive([]);
      setDropped([]);
      await app.BrowserConsoleRecordStart();
      setRecording(true);
      appendLog(t("ndv.brc.recStart"));
    });

  const stopRecord = () =>
    void (async () => {
      setBusy("record");
      try {
        const events = await app.BrowserConsoleRecordStop();
        setRecording(false);
        const filtered = await app.BrowserConsoleFilterTrace(events);
        setDropped(filtered.dropped);
        setLive(filtered.kept);
        // Raw trace (kept + dropped) travels to the editor as the 原始对照
        // so the review can verify nothing meaningful was filtered away.
        setRawTrace(events);
        appendLog(t("ndv.brc.recEnd", { kept: filtered.kept.length, dropped: filtered.dropped.length }));
        await generateSkill(filtered.kept);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
        setRecording(false);
      } finally {
        setBusy("");
      }
    })();

  const generateSkill = async (events: BrowserConsoleRecordEvent[]) => {
    if (events.length === 0) {
      setError(t("brc.emptyTrace"));
      return;
    }
    setGenerating(true);
    try {
      const draft: BrowserSkillDraft = await app.BrowserConsoleGenerateSkill("", events);
      setDraft(draft);
      if (draft.fallback && draft.detail) appendLog(`⚠ ${draft.detail}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setGenerating(false);
    }
  };

  const [draft, setDraft] = useState<BrowserSkillDraft | null>(null);
  const [rawTrace, setRawTrace] = useState<BrowserConsoleRecordEvent[]>([]);
  // Run-after-open: the skills tab's 试运行 opens the editor and immediately
  // starts the trial (the waiting-on-human banner lives in the editor).
  const [autoRun, setAutoRun] = useState(false);
  const openEditor = useCallback((d: BrowserSkillDraft, run = false) => {
    setAutoRun(run);
    setDraft(d);
  }, []);

  // --- render ---
  if (draft) {
    return (
      <BrowserSkillEditor
        initialContent={draft.content}
        trace={rawTrace}
        autoRun={autoRun}
        onCancel={() => {
          setDraft(null);
          setAutoRun(false);
        }}
        onSaved={() => {
          setDraft(null);
          setAutoRun(false);
          void refreshSkills();
        }}
      />
    );
  }

  return (
    <div className="ndv__card ndv-brc" style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
      {/* sub-tab pills (ZCode style) + session dot */}
      <div className="ndv-brc__subtabs">
        <div className="ndv-brc__subtab-group">
          <button
            type="button"
            className={`ndv-brc__subtab${subTab === "interact" ? " ndv-brc__subtab--active" : ""}`}
            onClick={() => setSubTab("interact")}
          >
            <MousePointerClick size={12} />
            <span>{t("brc.subInteract")}</span>
          </button>
          <button
            type="button"
            className={`ndv-brc__subtab${subTab === "record" ? " ndv-brc__subtab--active" : ""}`}
            onClick={() => setSubTab("record")}
          >
            <CircleStop size={12} />
            <span>{t("brc.subRecord")}</span>
          </button>
          <button
            type="button"
            className={`ndv-brc__subtab${subTab === "skills" ? " ndv-brc__subtab--active" : ""}`}
            onClick={() => setSubTab("skills")}
          >
            <BookMarked size={12} />
            <span>{t("brc.subSkills")}</span>
          </button>
          <button
            type="button"
            className={`ndv-brc__subtab${subTab === "watch" ? " ndv-brc__subtab--active" : ""}`}
            onClick={() => setSubTab("watch")}
            title={watchFeed.active ? t("brc.watchNext", { t: watchFeed.next_at ? formatWatchTime(watchFeed.next_at) : "" }) : t("brc.watchHint")}
          >
            <Radar size={12} />
            <span>{t("brc.subWatch")}</span>
            {watchFeed.active && <span className="ndv-brc__subtab-live" />}
          </button>
        </div>
        <span className={`ndv-brc__dot${state?.open ? " ndv-brc__dot--on" : ""}`} title={state?.browser ?? ""} />
      </div>

      {/* session bar */}
      <div className="ndv-brc__session">
        <span className="ndv-brc__session-label">
          {state?.open ? `${state.browser}${state.attached ? " · attach" : ""}` : t("brc.sessionClosed")}
        </span>
        {state?.open ? (
          <>
            <KeepAliveToggle state={state} busy={busy} runAction={runAction} t={t} />
            <button type="button" className="btn btn--secondary btn--small" title={t("brc.fileFindingTip")} onClick={() => { setFiling(v => !v); setError(""); }}>
              <FileWarning size={12} />
              <span>{t("brc.fileFinding")}</span>
            </button>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void runAction("close", () => app.BrowserConsoleClose(), t("brc.closed"))}>
              {t("brc.closeBrowser")}
            </button>
          </>
        ) : (
          <button type="button" className="btn btn--primary btn--small" onClick={() => void runAction("open", async () => { await app.BrowserConsoleOpen("", ""); }, t("brc.opened"))}>
            {busy === "open" ? <Loader2 size={12} className="composer-phase__spin" /> : t("brc.openBrowser")}
          </button>
        )}
      </div>
      {state?.open && state.keep_alive && <KeepAliveStatus state={state} runAction={runAction} t={t} />}

      {/* 提交为发现：严重级 + 备注 + 当前网址确认 */}
      {filing && state?.open && (
        <div className="ndv-brc__session" style={{ flexDirection: "column", alignItems: "stretch", gap: 4 }}>
          <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
            <span className="ndv-brc__session-label">{t("brc.fileFindingTitle")}</span>
            <select className="mem-input" style={{ width: 96, fontSize: 11 }} value={fileSeverity} onChange={e => setFileSeverity(e.target.value)}>
              <option value="critical">critical</option>
              <option value="warning">warning</option>
              <option value="info">info</option>
            </select>
            <input className="mem-input" style={{ flex: 1, minWidth: 0, fontSize: 11 }}
              placeholder={t("brc.fileFindingPh")} value={fileNote}
              onChange={e => setFileNote(e.target.value)}
              onKeyDown={e => { if (e.key === "Enter") void fileFinding(); }} />
            <button type="button" className="btn btn--primary btn--small" disabled={fileBusy} onClick={() => void fileFinding()}>
              {fileBusy ? "…" : t("brc.fileFindingGo")}
            </button>
          </div>
          <div style={{ fontSize: 11, opacity: 0.6, wordBreak: "break-all" }}>{state.url}</div>
        </div>
      )}

      {error && <div className="banner banner--error ndv-brc__error">{error}</div>}

      {/* inline mirror (console session) — appears with the first frame */}
      {state?.open && previewImg && !benchActive && (
        <>
          <button type="button" className="ndv-brc__preview-toggle" onClick={() => setPreviewOpen((v) => !v)}>
            <ChevronDown size={12} className={previewOpen ? "" : "ndv-brc__chev-closed"} />
            <span>{t("brc.preview")}</span>
          </button>
          {previewOpen && (
            <div className="ndv-brc__preview">
              <img className="ndv-brc__preview-img" src={previewImg} alt="" />
            </div>
          )}
        </>
      )}

      {/* bench launcher — the tabs/mirror/elements live in the center
          浏览器工作台 (same mechanism as logs/sec/dash); this opens it. */}
      {state?.open && (
        <button
          type="button"
          className="ndv-brc__view-launch"
          onClick={() => window.dispatchEvent(new CustomEvent("fairpeer:netdev-bench", { detail: "browser" }))}
          title={t("brc.viewerHint")}
        >
          <Monitor size={12} />
          <span className="ndv-brc__view-launch-title">{t("brc.viewerTitle")}</span>
          <span className="ndv-brc__view-launch-n">{tabs.length}</span>
        </button>
      )}



      {subTab === "interact" ? (
        <InteractSub
          {...{ url, setUrl, elements, setElements, target, setTarget, text, setText, submitEnter, setSubmitEnter, extractResult, setExtractResult, busy, runAction, t, onInsertComposer, onAction: recordStep, onError: (msg: string) => { setError(msg); appendLog(`✕ ${msg}`); }, scanSummary, onRefreshElements: refreshElements, onScan: runDeepScan, onStaleRef: recoverStaleHighlight }}
        />

      ) : subTab === "record" ? (
        <RecordSub
          {...{
            recording, live, dropped, showDropped, setShowDropped, generating, t,
            busy, startRecord, stopRecord, setDraft,
          }}
        />
      ) : subTab === "watch" ? (
        <WatchSub {...{ skills, t, busy, runAction }} />
      ) : (
        <SkillsSub {...{ skills, t, busy, runAction, refreshSkills, openEditor, onWatchStarted: () => setSubTab("watch") }} />
      )}

      {/* op log (动作记录) — every panel action, plus 记录为技能 turning the
          structured history into a SKILL.md draft for the skills tab editor.
          Hidden on the skills sub-tab: that page's space belongs to skill
          configuration, not the action trail. */}
      {subTab !== "skills" && history.length > 0 && (
        <div className="ndv-brc__log">
          <div className="ndv-brc__log-head">
            <span>{t("brc.opLog")}</span>
            {history.length > 0 && (
              <>
                <span className="ndv-brc__elements-count">{history.length}</span>
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  title={t("brc.logToSkillHint")}
                  onClick={() =>
                    openEditor({ name: "", content: historySkillContent(history), fallback: false }, false)
                  }
                >
                  <Sparkles size={11} />
                  <span>{t("brc.logToSkill")}</span>
                </button>
                <button
                  type="button"
                  className="btn btn--secondary btn--small ndv-brc__skill-del"
                  title={t("brc.logClear")}
                  onClick={() => setHistory([])}
                >
                  <Trash2 size={11} />
                </button>
              </>
            )}
          </div>
          {/* Structured steps — the skill's raw material. Deletable per
              row: a mis-click mid-run shouldn't poison the whole draft;
              prune here, then 记录为技能 hands the survivors to the editor
              for orchestration. */}
          {history.map((st, i) => (
            <div
              key={i}
              className={`ndv-brc__hist-row${overIdx === i && dragIdx !== null && dragIdx !== i ? " ndv-brc__hist-row--over" : ""}`}
              draggable
              title={t("brc.histDrag")}
              onDragStart={() => setDragIdx(i)}
              onDragEnd={() => { setDragIdx(null); setOverIdx(null); }}
              onDragOver={(e) => { e.preventDefault(); setOverIdx(i); }}
              onDrop={(e) => { e.preventDefault(); dropOn(i); }}
            >
              <span className="ndv-brc__hist-n">{i + 1}</span>
              <span className="ndv-brc__hist-label">{summarizeStep(st)}</span>
              <button
                type="button"
                className="ndv-brc__hist-del"
                title={t("brc.histDel")}
                onClick={() => setHistory((prev) => prev.filter((_, j) => j !== i))}
              >
                <X size={10} />
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// useMirror subscribes to the shared browser mirror store (no local state).
// formatElapsed renders the recording clock as m:ss.
function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

// ElementPicker — the 元素 section: a bounded, filterable list of the page's
// interactive elements. Fetching can return ~200 rows; without a filter and
// display cap the raw dump drowned the panel (and pre-scroll-fix, pushed it
// over the card). Show matched rows up to a cap, always report the count.
const ELEMENT_CAP = 50;

function ElementPicker(props: {
  elements: BrowserConsoleElement[];
  target: string;
  onPick: (ref: string) => void;
  onRefresh: () => void;
  onScan: () => void;
  scanSummary: string;
  busy: string;
  onError?: (msg: string) => void;
  // Invoked when highlighting a REF row fails — the panel re-captures and
  // re-highlights by (role, name); anchors (CSS/text=) go to onError instead.
  onStaleRef?: (el: BrowserConsoleElement, err: unknown) => void;
  t: Translator;
}) {
  const { elements, target, onPick, onRefresh, onScan, scanSummary, busy, onError, onStaleRef, t } = props;
  const [filter, setFilter] = useState("");
  // Hover flashes a short outline on the page element; click lights it
  // longer. Hover stays best-effort silent (banners on every row would be
  // noise); the CLICK mark reports failures — a stale ref otherwise dies
  // silently and the preview just doesn't frame, which reads as "broken".
  const hoverTimerRef = useRef<number>(0);
  const flash = (ref: string, ms: number) => {
    void app.BrowserConsoleHighlight(ref, ms).catch(() => undefined);
  };
  // stableTarget: the row's computed CSS with a text fallback anchor - picking
  // fills the target with THIS, so the recorded step survives session changes
  // (the bare ref dies with the snapshot that minted it).
  const stableTarget = (el: BrowserConsoleElement): string => {
    if (el.css && el.name) return el.css + ';;text=' + el.name;
    if (el.css) return el.css;
    // No unique CSS computed (deep non-unique nesting): a text anchor still
    // survives session changes — far better than the transient ref.
    if (el.name) return 'text=' + el.name;
    return el.ref;
  };
  const q = filter.trim().toLowerCase();
  const matched = q
    ? elements.filter((el) =>
        `${el.role} ${el.name} ${el.ref} ${el.value ?? ""}`.toLowerCase().includes(q))
    : elements;
  const shown = matched.slice(0, ELEMENT_CAP);
  return (
    <>
      <div className="ndv-brc__section-head">
        <span>{t("brc.elements")}</span>
        <span className="ndv-brc__elements-count">
          {q ? `${shown.length}/${matched.length}` : elements.length}
        </span>
        <div className="ndv-brc__elements-tools">
          <input
            className="mem-input ndv-brc__elements-filter"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t("brc.filterElements")}
            spellCheck={false}
          />
          <button
            type="button"
            className="btn btn--secondary btn--small"
            aria-label={t("brc.deepScan")}
            title={t("brc.deepScan")}
            onClick={onScan}
            disabled={busy === "scan"}
          >
            {busy === "scan" ? <Loader2 size={11} className="composer-phase__spin" /> : <ChevronsDown size={11} />}
          </button>
          <button
            type="button"
            className="btn btn--secondary btn--small"
            aria-label={t("brc.refreshElements")}
            title={t("brc.refreshElements")}
            onClick={onRefresh}
            disabled={busy === "elements"}
          >
            <RefreshCw size={11} />
          </button>
        </div>
      </div>
      {scanSummary && <div className="ndv-brc__hint">{scanSummary}</div>}
      <div className="ndv-brc__elements">
        {elements.length === 0 && <div className="ndv-brc__hint">{t("brc.elementsEmpty")}</div>}
        {shown.map((el) => (
          <button
            type="button"
            key={el.ref}
            className={`ndv-brc__element${target === el.ref ? " ndv-brc__element--sel" : ""}`}
            onMouseEnter={() => {
              window.clearTimeout(hoverTimerRef.current);
              hoverTimerRef.current = window.setTimeout(() => flash(el.ref, 500), 150);
            }}
            onMouseLeave={() => window.clearTimeout(hoverTimerRef.current)}
            onClick={() => {
              // Persistent mark (kernel toggles off when re-clicked); the
              // highlight pushes a mirror frame so the preview shows it at once.
              void app.BrowserConsoleHighlight(el.ref, 0).catch((err) => {
                if (/^e\d+$/.test(el.ref)) {
                  onStaleRef?.(el, err);
                  return;
                }
                const msg = err instanceof Error ? err.message : String(err);
                onError?.(msg);
              });
              onPick(stableTarget(el));
            }}
            title={`${el.role} ${el.name}`}
          >
            <span className="ndv-brc__element-role">{el.role}</span>
            <span className="ndv-brc__element-name">{el.name || el.ref}</span>
            {el.value ? <span className="ndv-brc__element-value">{el.value}</span> : null}
            {el.css ? <span className="ndv-brc__element-css" title={el.css}>{el.css}</span> : null}
          </button>
        ))}
        {matched.length > shown.length && (
          <div className="ndv-brc__hint">{t("brc.elementsCapped", { n: matched.length - shown.length })}</div>
        )}
      </div>
    </>
  );
}

// --- interact sub-tab -----------------------------------------------------------------

function InteractSub(props: {
  url: string;
  setUrl: (v: string) => void;
  elements: BrowserConsoleElement[];
  setElements: (v: BrowserConsoleElement[]) => void;
  target: string;
  setTarget: (v: string) => void;
  text: string;
  setText: (v: string) => void;
  submitEnter: boolean;
  setSubmitEnter: (v: boolean) => void;
  extractResult: string;
  setExtractResult: (v: string) => void;
  busy: string;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
  onInsertComposer?: (text: string) => void;
  onAction: (step: BrowserConsoleStep) => void;
  onError?: (msg: string) => void;
  // Element-list lifecycle is panel-owned (URL watchdog) — InteractSub
  // consumes: the scan summary line + the two refresh entry points.
  scanSummary: string;
  onRefreshElements: () => void;
  onScan: () => void;
  onStaleRef?: (el: BrowserConsoleElement, err: unknown) => void;
  t: Translator;
}) {
  const { url, setUrl, elements, target, setTarget, text, setText, submitEnter, setSubmitEnter, extractResult, setExtractResult, busy, runAction, onInsertComposer, onAction, onError, scanSummary, onRefreshElements, onScan, onStaleRef, t } = props;
  return (
    <div className="ndv-brc__body">
      <form
        className="ndv-brc__urlbar"
        onSubmit={(e) => {
          e.preventDefault();
          if (url) {
            onAction({ type: "navigate", url });
            void runAction("nav", () => app.BrowserConsoleNavigate(url))
              .then(() => onRefreshElements());
          }
        }}
      >
        <input
          className="mem-input ndv-brc__url"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://…"
          spellCheck={false}
        />
        <button
          type="submit"
          className="btn btn--secondary btn--small"
          disabled={!url || busy === "nav"}
          aria-label={t("brc.go")}
          title={t("brc.go")}
        >
          {busy === "nav" ? <Loader2 size={12} className="composer-phase__spin" /> : <Globe size={12} />}
        </button>
      </form>

      <div className="ndv-brc__section">
        <ElementPicker
          elements={elements}
          target={target}
          onPick={setTarget}
          onRefresh={onRefreshElements}
          onScan={onScan}
          scanSummary={scanSummary}
          busy={busy}
          onError={onError}
          onStaleRef={onStaleRef}
          t={t}
        />
      </div>

      <div className="ndv-brc__section">
        <div className="ndv-brc__field">
          <span className="ndv-brc__field-label">{t("brc.target")}</span>
          <input className="mem-input" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="e12 / CSS" spellCheck={false} />
        </div>
        <div className="ndv-brc__field">
          <span className="ndv-brc__field-label">{t("brc.text")}</span>
          <input className="mem-input" value={text} onChange={(e) => setText(e.target.value)} placeholder={t("brc.textPlaceholder")} />
        </div>
        <div className="ndv-brc__actions">
          <label className="ndv-brc__check">
            <input type="checkbox" checked={submitEnter} onChange={(e) => setSubmitEnter(e.target.checked)} />
            <span>↵</span>
          </label>
          <button
            type="button"
            className="btn btn--secondary btn--small"
            disabled={!target || !text || busy === "type"}
            onClick={() => {
              onAction({ type: "type", target, text });
              if (submitEnter) onAction({ type: "key", value: "enter" });
              void runAction("type", async () => {
                const out = await app.BrowserConsoleType(target, text);
                if (submitEnter) await app.BrowserConsoleKey("enter");
                return out;
              });
            }}
          >
            {t("brc.typeBtn")}
          </button>
          <button
            type="button"
            className="btn btn--secondary btn--small"
            disabled={!target || busy === "click"}
            onClick={() => {
              onAction({ type: "click", target });
              void runAction("click", () => app.BrowserConsoleClick(target));
            }}
          >
            {t("brc.clickBtn")}
          </button>
          <button
            type="button"
            className="btn btn--secondary btn--small"
            title={t("brc.hoverBtnHint")}
            disabled={!target || busy === "hover"}
            onClick={() => {
              onAction({ type: "hover", target });
              void runAction("hover", () => app.BrowserConsoleHover(target));
            }}
          >
            {t("brc.hoverBtn")}
          </button>
        </div>
      </div>

      <div className="ndv-brc__section">
        <ExtractBox {...{ extractResult, setExtractResult, target, busy, runAction, onInsertComposer, onAction, t }} />
      </div>
    </div>
  );
}

function ExtractBox(props: {
  extractResult: string;
  setExtractResult: (v: string) => void;
  target: string;
  busy: string;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
  onInsertComposer?: (text: string) => void;
  onAction: (step: BrowserConsoleStep) => void;
  t: Translator;
}) {
  const { extractResult, setExtractResult, target, busy, runAction, onInsertComposer, onAction, t } = props;
  const [selector, setSelector] = useState("");
  const [format, setFormat] = useState("");
  return (
    <>
      <div className="ndv-brc__section-head">
        <span>{t("brc.extract")}</span>
        <div className="ndv-brc__section-tools">
          {extractResult && (
            <>
              <button type="button" className="btn btn--secondary btn--small" onClick={() => void navigator.clipboard?.writeText(extractResult)}>
                {t("brc.copy")}
              </button>
              <button
                type="button"
                className="btn btn--secondary btn--small"
                disabled={!onInsertComposer}
                onClick={() => {
                  // Bounded excerpt into the chat composer — the netdev
                  // "交给 AI" convention (LogPanel's sendToAI pattern).
                  onInsertComposer?.(extractResult.slice(0, 1200));
                }}
                title={t("brc.sendToAIHint")}
              >
                <Upload size={11} />
              </button>
            </>
          )}
        </div>
      </div>
      <div className="ndv-brc__extract-form">
        <select
          className="mem-input ndv-brc__extract-format"
          value={format}
          onChange={(e) => setFormat(e.target.value)}
          title={t("brc.extractFormatHint")}
        >
          <option value="">{t("brc.fmtText")}</option>
          <option value="markdown">{t("brc.fmtMarkdown")}</option>
          <option value="table">{t("brc.fmtTable")}</option>
        </select>
        <input
          className="mem-input"
          value={selector}
          onChange={(e) => setSelector(e.target.value)}
          placeholder={t("brc.extractPlaceholder")}
          spellCheck={false}
        />
        <button
          type="button"
          className="btn btn--secondary btn--small"
          disabled={busy === "extract"}
          onClick={() => {
            onAction({ type: "extract", target: selector || target, value: format || undefined });
            void runAction("extract", async () => {
              const out = await app.BrowserConsoleExtract(selector || target, format);
              setExtractResult(out);
              // The extracted CONTENT stays in the 提取 box for downstream
              // use — the log only carries a length so the trail shows the
              // step happened without dumping the payload.
              return `提取完成（${out.length} 字符）`;
            });
          }}
        >
          {busy === "extract" ? <Loader2 size={12} className="composer-phase__spin" /> : t("brc.extractBtn")}
        </button>
      </div>
      {extractResult && <pre className="ndv-brc__extract-result">{extractResult.slice(0, 4000)}</pre>}
    </>
  );
}

// --- keep-alive (会话保活) ------------------------------------------------------------

// formatKaTime renders the last-refresh stamp as HH:MM:SS local time.
function formatKaTime(unixMillis: number): string {
  if (!unixMillis) return "–";
  const d = new Date(unixMillis);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`;
}

const KA_MODES = ["ping", "navigate", "local"] as const;
const KA_INTERVALS = [1, 5, 15, 30, 60] as const; // minutes

// KeepAliveToggle is the session-bar switch. Arming uses the current status
// line settings (mode/interval/URL persist there while armed).
function KeepAliveToggle(props: {
  state: BrowserConsoleState;
  busy: string;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
  t: Translator;
}) {
  const { state, busy, runAction, t } = props;
  if (!state.keep_alive) {
    return (
      <button
        type="button"
        className="btn btn--secondary btn--small"
        title={t("brc.kaHint")}
        onClick={() => void runAction("ka", () => app.BrowserConsoleSetKeepAlive(true, 300, "ping", ""), t("brc.kaOn"))}
      >
        {busy === "ka" ? <Loader2 size={12} className="composer-phase__spin" /> : <HeartPulse size={12} />}
        <span>{t("brc.keepAlive")}</span>
      </button>
    );
  }
  return (
    <button
      type="button"
      className="btn btn--secondary btn--small ndv-brc__ka-btn--on"
      title={t("brc.kaStopHint")}
      onClick={() => void runAction("ka", () => app.BrowserConsoleSetKeepAlive(false, 0, "", ""), t("brc.kaOff"))}
    >
      <HeartPulse size={12} className="ndv-brc__ka-pulse" />
      <span>{t("brc.kaStop")}</span>
    </button>
  );
}

// KeepAliveStatus is the armed status line: mode/interval/URL selectors that
// re-arm in place (the kernel swaps settings without dropping the loop), the
// last-refresh clock, and any heartbeat error.
function KeepAliveStatus(props: {
  state: BrowserConsoleState;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
  t: Translator;
}) {
  const { state, runAction, t } = props;
  const [mode, setMode] = useState<string>(state.keep_alive_mode || "ping");
  const [minutes, setMinutes] = useState<number>(5);
  const [url, setUrl] = useState(state.keep_alive_url || "");
  const rearm = (m: string, min: number, u: string) =>
    void runAction("ka", () => app.BrowserConsoleSetKeepAlive(true, min * 60, m, u), t("brc.kaOn"));
  return (
    <div className="ndv-brc__ka">
      <span className="ndv-brc__ka-clock">
        <HeartPulse size={11} className="ndv-brc__ka-pulse" />
        {t("brc.kaActive")} · {t("brc.kaLast")} {formatKaTime(state.keep_alive_last)}
      </span>
      <select className="mem-select ndv-brc__ka-select" value={mode} onChange={(e) => { setMode(e.target.value); rearm(e.target.value, minutes, url); }}>
        {KA_MODES.map((m) => (
          <option key={m} value={m}>{t(`brc.kaMode_${m}` as never)}</option>
        ))}
      </select>
      <select className="mem-select ndv-brc__ka-select" value={minutes} onChange={(e) => { const v = parseInt(e.target.value, 10); setMinutes(v); rearm(mode, v, url); }}>
        {KA_INTERVALS.map((m) => (
          <option key={m} value={m}>{t("brc.kaEvery", { n: m })}</option>
        ))}
      </select>
      {mode === "navigate" && (
        <input
          className="mem-input ndv-brc__ka-url"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          onBlur={() => rearm(mode, minutes, url)}
          placeholder={t("brc.kaUrlPlaceholder")}
          spellCheck={false}
        />
      )}
      {state.keep_alive_err && <span className="ndv-brc__ka-err" title={state.keep_alive_err}>{state.keep_alive_err}</span>}
    </div>
  );
}

// historySkillContent turns the panel's structured action history into a
// SKILL.md draft — targets ride verbatim (refs are transient, the editor's
// review pass swaps them for CSS/text anchors).
function historySkillContent(steps: BrowserConsoleStep[]): string {
  const rows = steps
    .map((st, i) => {
      const target = st.type === "navigate" ? (st.url ?? "") : st.target ?? "";
      const value = st.type === "type" || st.type === "switch_tab" ? (st.text ?? "") : st.value ?? "";
      return `| ${i + 1} | ${st.type} | \`${target}\` | ${value} |`;
    })
    .join("\n");
  return [
    "---",
    "name: browser-actions",
    "description: 浏览器操作流程（面板动作记录生成），可直接调用；描述与选择器可逐步打磨。",
    "runAs: subagent",
    "executor: browser-flow",
    "domain: browser-ops",
    "draft: true",
    "allowed-tools: browser_open, browser_navigate, browser_click, browser_type, browser_wait, browser_extract",
    "---",
    "",
    "# 面板动作技能",
    "",
    "## 何时使用",
    "",
    "（说明何时用这个流程。**参数化**：每次会变的内容改成 {{参数名}}——比如输入步骤的固定文字改成 {{问题}}，调用时以 问题=xxx 传入；对话调用 /技能名 问题=xxx，面板试运行会弹输入框）",
    "",
    "## 步骤",
    "",
    "| # | 操作 | 目标 | 值 |",
    "|---|------|------|------|",
    rows,
    "",
    "## 注意事项",
    "",
    "- 由面板动作记录生成；快照编号（eN）是瞬时值，改用 CSS 选择器或 text= 可见文字锚（目标支持回退链：选择器;;text=文字）",
    "- 输入类步骤记录的是当时的固定文字——需要每次可变就改成 {{参数名}}",
    "- 提取结果一般需要二次加工（整理/改写）后再使用；把 extract 放在最后一步，输出落在执行报告里便于复制；AI 生成的富文本块可用 Markdown 提取保留结构",
    "",
    "## 验证",
    "",
    "（如何确认执行成功——通常看最后一步提取到的内容或页面跳转结果）",
    "",
  ].join("\n");
}

// --- standing watch (定时巡检: poll → download → AI judge) --------------------

// mergeWatchRounds folds one round event into the list keyed by started_at:
// "running" snapshots and the final record share the key, later snapshots
// replace earlier ones instead of stacking duplicates.
function mergeWatchRounds(rounds: BrowserConsoleWatchRound[] | undefined, r: BrowserConsoleWatchRound): BrowserConsoleWatchRound[] {
  const list = [...(rounds ?? [])];
  const i = list.findIndex((x) => x.started_at === r.started_at);
  if (i >= 0) list[i] = r;
  else list.unshift(r);
  return list;
}

// useBrowserWatch mirrors the Go-side watch: initial state fetch + live
// "browser:watch" subscription. State events replace wholesale; round events
// merge into the tail.
function useBrowserWatch(): BrowserConsoleWatchState {
  const [state, setState] = useState<BrowserConsoleWatchState>({ active: false });
  useEffect(() => {
    app.BrowserConsoleWatchState().then(setState).catch(() => undefined);
    return onBrowserWatch((ev) => {
      if (ev.type === "state" && ev.state) {
        setState(ev.state);
      } else if (ev.type === "round" && ev.round) {
        setState((prev) => ({ ...prev, rounds: mergeWatchRounds(prev.rounds, ev.round as BrowserConsoleWatchRound) }));
      }
    });
  }, []);
  return state;
}

// WATCH_INTERVALS are the offered poll intervals (minutes). The Go side clamps
// to >=60s; rounds overlap-skip when the export tail outlasts the interval.
const WATCH_INTERVALS = [1, 3, 5, 10, 15, 30];

// WATCH_EVENTS are the notification triggers: which round outcomes reach the
// configured channels. "compromised" is the 安全负责人 default — only confirmed
// 失陷主机 (per the triage verdict) push out.
const WATCH_EVENTS: { value: string; labelKey: string }[] = [
  { value: "compromised", labelKey: "brc.watchEvCompromised" },
  { value: "attention", labelKey: "brc.watchEvAttention" },
  { value: "always", labelKey: "brc.watchEvAlways" },
  { value: "never", labelKey: "brc.watchEvNever" },
];

// composeImDest mirrors TaskForm's dest composition: QQ group/channel chats
// need the chatType segment so gw.Push routes to the right URL;
// feishu/weixin route by platform:chatId alone. Manual input passes through.
function composeImDest(platform: string, chat: RecentChatView | undefined, manualDest: string) {
  if (chat) {
    if (platform === "qq" && chat.chatType && chat.chatType !== "dm") {
      return `${platform}:${chat.chatType}:${chat.chatId}`;
    }
    return `${platform}:${chat.chatId}`;
  }
  return manualDest;
}

// WATCH_ANALYSES are the post-download treatments — the watch is
// skill-agnostic: SIEM alerts get the triage prompt, anything else the
// generic table analysis, "none" just downloads.
const WATCH_ANALYSES: { value: string; labelKey: string }[] = [
  { value: "alerts", labelKey: "brc.watchAnAlerts" },
  { value: "generic", labelKey: "brc.watchAnGeneric" },
  { value: "none", labelKey: "brc.watchAnNone" },
];

// WatchFormFields — the watch's editable configuration, shared by the skill
// expand area (fixed skill) and the 巡检 sub-tab (skill dropdown). Delivery
// targets read fairpeer's own config: IM recipients from the bot's recent
// chats, email senders from the configured SMTP accounts — dropdowns first,
// manual input as fallback.
function WatchFormFields(props: {
  skill: string;
  t: Translator;
  busy: string;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
  onStarted?: () => void;
}) {
  const { skill, t, busy, runAction, onStarted } = props;
  const [intervalMin, setIntervalMin] = useState(5);
  const [anchorMin, setAnchorMin] = useState("");
  const [analysis, setAnalysis] = useState("alerts");
  const [onEvent, setOnEvent] = useState("compromised");
  const [imPick, setImPick] = useState(""); // selected chatId from the picker ("" = manual)
  const [imManual, setImManual] = useState("");
  const [emailAccount, setEmailAccount] = useState("");
  const [email, setEmail] = useState("");
  const [systemNotify, setSystemNotify] = useState(true);
  const [chats, setChats] = useState<RecentChatView[]>([]);
  const [emailAccounts, setEmailAccounts] = useState<{ name: string; default: boolean }[]>([]);
  useEffect(() => {
    // IM recipients: the bot gateway's recent chats; email senders: the
    // configured SMTP accounts — same pickers the scheduled-task form uses.
    app
      .ListRecentBotChats()
      .then(setChats)
      .catch(() => undefined);
    app
      .Settings()
      .then((sv) => {
        setEmailAccounts((sv.cowork?.emailAccounts ?? []).map((a) => ({ name: a.name, default: a.default })));
      })
      .catch(() => undefined);
  }, []);
  const pickedChat = chats.find((c) => c.chatId === imPick);
  const imDest = imPick ? composeImDest(pickedChat?.platform ?? "", pickedChat, imManual) : imManual.trim();
  const start = async () => {
    const cfg: BrowserConsoleWatchConfig = {
      skill,
      interval_sec: intervalMin * 60,
      anchor_min: anchorMin.trim(),
      analysis: analysis as BrowserConsoleWatchConfig["analysis"],
      notify: {
        on_event: onEvent as BrowserConsoleWatchConfig["notify"]["on_event"],
        im_chat_id: imDest,
        im_chat_name: pickedChat ? pickedChat.userName || pickedChat.chatId : "",
        email: email.trim(),
        email_account: emailAccount,
        system: systemNotify,
      },
    };
    await runAction("watch-start", async () => {
      await app.BrowserConsoleWatchStart(cfg);
    });
    onStarted?.();
  };
  const notifyOpen = analysis !== "none";
  return (
    <>
      <div className="ndv-brc-watch__row">
        <select className="mem-select" value={intervalMin} onChange={(e) => setIntervalMin(parseInt(e.target.value, 10))} title={t("brc.watchInterval")}>
          {WATCH_INTERVALS.map((m) => (
            <option key={m} value={m}>
              {m} min
            </option>
          ))}
        </select>
        <input
          className="mem-input ndv-brc-watch__anchor"
          value={anchorMin}
          onChange={(e) => setAnchorMin(e.target.value)}
          placeholder={t("brc.watchAnchorPh")}
          title={t("brc.watchAnchorHint")}
        />
        <button type="button" className="btn btn--primary btn--small" disabled={busy === "watch-start"} title={t("brc.watchHint")} onClick={() => void start()}>
          {busy === "watch-start" ? <Loader2 size={11} className="composer-phase__spin" /> : <Sparkles size={11} />}
          <span>{t("brc.watchStart")}</span>
        </button>
      </div>

      <details className="ndv-brc-watch__notify" open>
        <summary>{t("brc.watchNotify")}</summary>
        <div className="ndv-brc-watch__grid">
          <label className="ndv-brc-watch__cell">
            <span className="ndv-brc-watch__label">{t("brc.watchAnalysisKind")}</span>
            <select className="mem-select" value={analysis} onChange={(e) => setAnalysis(e.target.value)} title={t("brc.watchAnalysisHint")}>
              {WATCH_ANALYSES.map((a) => (
                <option key={a.value} value={a.value}>
                  {t(a.labelKey as never)}
                </option>
              ))}
            </select>
          </label>
          {notifyOpen && (
            <label className="ndv-brc-watch__cell">
              <span className="ndv-brc-watch__label">{t("brc.watchTrigger")}</span>
              <select className="mem-select" value={onEvent} onChange={(e) => setOnEvent(e.target.value)}>
                {WATCH_EVENTS.map((e) => (
                  <option key={e.value} value={e.value}>
                    {t(e.labelKey as never)}
                  </option>
                ))}
              </select>
            </label>
          )}
          {notifyOpen && (
            <label className="ndv-brc-watch__cell">
              <span className="ndv-brc-watch__label">{t("brc.watchIMTarget")}</span>
              {chats.length > 0 ? (
                <select className="mem-select" value={imPick} onChange={(e) => setImPick(e.target.value)} title={t("brc.watchIMHint")}>
                  <option value="">{t("brc.watchIMPickManual")}</option>
                  {chats.map((c) => (
                    <option key={`${c.platform}:${c.chatId}`} value={c.chatId}>
                      {c.userName || c.chatId}（{c.platform}）
                    </option>
                  ))}
                </select>
              ) : (
                <input className="mem-input" value={imManual} onChange={(e) => setImManual(e.target.value)} placeholder={t("brc.watchIMPh")} title={t("brc.watchIMHint")} />
              )}
              {chats.length > 0 && !imPick && (
                <input className="mem-input" value={imManual} onChange={(e) => setImManual(e.target.value)} placeholder={t("brc.watchIMPh")} title={t("brc.watchIMHint")} />
              )}
            </label>
          )}
          {notifyOpen && (
            <label className="ndv-brc-watch__cell">
              <span className="ndv-brc-watch__label">{t("brc.watchEmail")}</span>
              {emailAccounts.length > 0 ? (
                <select className="mem-select" value={emailAccount} onChange={(e) => setEmailAccount(e.target.value)} title={t("brc.watchMailAccountHint")}>
                  {emailAccounts.map((a) => (
                    <option key={a.name} value={a.name}>
                      {a.name}
                      {a.default ? ` (${t("brc.watchMailDefault")})` : ""}
                    </option>
                  ))}
                </select>
              ) : (
                <span className="ndv-brc-watch__none">{t("brc.watchNoMailAccount")}</span>
              )}
              <input className="mem-input" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t("brc.watchMailToPh")} />
            </label>
          )}
          {notifyOpen && (
            <label className="ndv-brc-watch__cell ndv-brc-watch__cell--check">
              <span className="ndv-brc-watch__label">{t("brc.watchSys")}</span>
              <label className="ndv-brc__check">
                <input type="checkbox" checked={systemNotify} onChange={(e) => setSystemNotify(e.target.checked)} />
                <span>{t("brc.watchSysOn")}</span>
              </label>
            </label>
          )}
        </div>
      </details>
      <div className="ndv-brc-watch__hint">{t("brc.watchHint")}</div>
    </>
  );
}

// SkillWatchControls — the per-skill slice rendered inside the expanded skill
// config: a summary of the ACTIVE watch (or the form when none). Starting a
// watch on another skill replaces it; the config persists and auto-resumes on
// app restart.
function SkillWatchControls(props: {
  skill: string;
  t: Translator;
  state: BrowserConsoleWatchState;
  busy: string;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
  onWatchStarted?: () => void;
}) {
  const { skill, t, state, busy, runAction, onWatchStarted } = props;
  const mine = state.active && state.skill === skill;
  const other = state.active && state.skill !== skill;
  const notify = state.notify;
  return (
    <div className="ndv-brc-watch">
      <div className="ndv-brc-watch__row">
        <span className="ndv-brc-watch__label">{t("brc.watch")}</span>
        {mine && state.anchor && <span className="ndv-brc-watch__next">{t("brc.watchAnchor", { t: formatWatchTime(state.anchor) })}</span>}
        {mine && state.next_at && <span className="ndv-brc-watch__next">{t("brc.watchNext", { t: formatWatchTime(state.next_at) })}</span>}
        {other && <span className="ndv-brc-watch__other">{t("brc.watchOnOther", { name: state.skill ?? "" })}</span>}
      </div>
      {!mine && <WatchFormFields skill={skill} t={t} busy={busy} runAction={runAction} onStarted={onWatchStarted} />}
      {(mine || other) && (
        <div className="ndv-brc-watch__row">
          <button
            type="button"
            className="btn btn--danger btn--small"
            disabled={busy === "watch-stop"}
            onClick={() =>
              void runAction("watch-stop", async () => {
                await app.BrowserConsoleWatchStop();
              })
            }
          >
            {busy === "watch-stop" ? <Loader2 size={11} className="composer-phase__spin" /> : <CircleStop size={11} />}
            <span>{t("brc.watchStop")}</span>
          </button>
          {mine && (
            <span className="ndv-brc-watch__next">
              {t((WATCH_ANALYSES.find((x) => x.value === (state.analysis || "alerts")) ?? WATCH_ANALYSES[0]).labelKey as never)}
              {state.analysis !== "none" &&
                ` · ${t((WATCH_EVENTS.find((e) => e.value === (notify?.on_event || "never")) ?? WATCH_EVENTS[3]).labelKey as never)}`}
              {notify?.im_chat_id ? ` · IM ${notify.im_chat_name || notify.im_chat_id}` : ""}
              {notify?.email ? ` · ${notify.email_account ? notify.email_account + " → " : ""}${notify.email}` : ""}
              {notify?.system ? " · sys" : ""}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

function formatWatchTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`;
}

// WatchRoundsLog — the watch's round history: per-round header (time range,
// status, download, rows) with an expandable AI triage report and step detail.
function WatchRoundsLog(props: { t: Translator; state: BrowserConsoleWatchState }) {
  const { t, state } = props;
  const [open, setOpen] = useState<string>("");
  const rounds = state.rounds ?? [];
  const generic = state.analysis === "generic";
  if (!state.active && rounds.length === 0) return null;
  const statusKey = (s: string) =>
    s === "done" ? "brc.watchRoundDone" : s === "failed" ? "brc.watchRoundFailed" : s === "skipped" ? "brc.watchRoundSkipped" : "brc.watchRoundRunning";
  return (
    <div className="ndv-brc__section">
      <div className="ndv-brc__section-head">
        <span>
          {t("brc.watchRounds")}
          {state.active && <span className="ndv-brc-watch__live">● {t("brc.watchActive")}</span>}
        </span>
      </div>
      {rounds.length === 0 && <div className="ndv-brc__hint">{t("brc.watchNoRounds")}</div>}
      {rounds.map((r) => {
        const key = r.started_at;
        const expanded = open === key;
        return (
          <div key={key} className={`ndv-brc-watch-round ndv-brc-watch-round--${r.status}`}>
            <button type="button" className="ndv-brc-watch-round__head" onClick={() => setOpen(expanded ? "" : key)}>
              <span className={`ndv-brc-watch-round__status ndv-brc-watch-round__status--${r.status}`}>{t(statusKey(r.status) as never)}</span>
              {r.compromised_hosts && r.compromised_hosts.length > 0 && (
                <span className="ndv-brc-watch-round__compromised" title={r.compromised_hosts.join("、")}>
                  {generic
                    ? t("brc.watchFindings", { n: r.compromised_hosts.length })
                    : t("brc.watchCompromised", { n: r.compromised_hosts.length })}
                </span>
              )}
              {!r.compromised_hosts?.length && !!r.attention_count && r.attention_count > 0 && (
                <span className="ndv-brc-watch-round__attn">
                  {generic ? t("brc.watchAttnItems", { n: r.attention_count }) : t("brc.watchAttnCount", { n: r.attention_count })}
                  {r.severity ? ` · ${r.severity}` : ""}
                </span>
              )}
              <span className="ndv-brc-watch-round__time">{formatWatchTime(r.started_at)}</span>
              {r.time_range && <span className="ndv-brc-watch-round__range" title={r.time_range}>{r.time_range}</span>}
              {r.download_name && <span className="ndv-brc-watch-round__file">{r.download_name}{(r.rows ?? 0) > 0 ? ` · ${r.rows} ${t("brc.watchRows")}` : ""}</span>}
              {r.notified && r.notified.length > 0 && <span className="ndv-brc-watch-round__notified">{t("brc.watchNotified")}: {r.notified.join("/")}</span>}
              {r.skipped && <span className="ndv-brc-watch-round__note">{t("brc.watchBusy")}</span>}
              {!r.download_name && r.note && <span className="ndv-brc-watch-round__note">{t("brc.watchNoDownload")}</span>}
              <ChevronDown size={11} className={expanded ? "" : "ndv-brc__chev-closed"} />
            </button>
            {r.error && <div className="ndv-brc-watch-round__error">{r.error}</div>}
            {r.notify_error && <div className="ndv-brc-watch-round__error">{t("brc.watchNotifyError")}: {r.notify_error}</div>}
            {expanded && (
              <div className="ndv-brc-watch-round__body">
                {r.compromised_hosts && r.compromised_hosts.length > 0 && (
                  <div className="ndv-brc-watch-round__compromised-list">
                    {generic ? t("brc.watchFindingsList") : t("brc.watchCompromisedList")}：{r.compromised_hosts.join("、")}
                  </div>
                )}
                {r.analysis && (
                  <div className="ndv-brc-watch-round__analysis">
                    <div className="ndv-brc-watch-round__analysis-title">{t("brc.watchAnalysisKind")}</div>
                    <Markdown text={r.analysis} />
                  </div>
                )}
                {r.steps && r.steps.length > 0 && (
                  <details className="ndv-brc-watch-round__steps">
                    <summary>{t("brc.watchSteps")}</summary>
                    {r.steps.map((s) => (
                      <div key={s.index} className={`ndv-brc-watch-round__step ndv-brc-watch-round__step--${s.status}`}>
                        <span className="ndv-brc__step-n">{s.index + 1}</span>
                        <span className="ndv-brc__step-text">{s.type}</span>
                        {s.error && <span className="ndv-brc-watch-round__step-err">{s.error}</span>}
                        {!s.error && s.output && <span className="ndv-brc-watch-round__step-out">{s.output}</span>}
                      </div>
                    ))}
                  </details>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

// --- watch sub-tab -----------------------------------------------------------

// WatchSub — the 巡检 sub-tab: the watch's first-class home. Active status
// header (skill/alignment/next round/last verdict), the configuration form
// with a skill picker, and the rounds log — no digging through skill expand
// areas. The form body is shared with the per-skill controls.
function WatchSub(props: {
  skills: BrowserConsoleSkill[];
  t: Translator;
  busy: string;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
}) {
  const { skills, t, busy, runAction } = props;
  const state = useBrowserWatch();
  const watchSkills = skills.filter((s) => s.browser);
  const [picked, setPicked] = useState(
    state.active && watchSkills.some((s) => s.name === state.skill) ? (state.skill as string) : (watchSkills[0]?.name ?? ""),
  );
  const last = state.last_round;
  return (
    <div className="ndv-brc__body">
      {/* active status header */}
      {state.active && (
        <div className="ndv-brc-watch-status">
          <span className="ndv-brc-watch__live">● {t("brc.watchActive")}</span>
          <span className="ndv-brc-watch-status__skill">{state.skill}</span>
          {state.interval_sec && <span className="ndv-brc-watch__next">{Math.round(state.interval_sec / 60)} min</span>}
          {state.anchor && <span className="ndv-brc-watch__next">{t("brc.watchAnchor", { t: formatWatchTime(state.anchor) })}</span>}
          {state.next_at && <span className="ndv-brc-watch__next">{t("brc.watchNext", { t: formatWatchTime(state.next_at) })}</span>}
          {last?.compromised_hosts?.length ? (
            <span className="ndv-brc-watch-round__compromised" title={last.compromised_hosts.join("、")}>
              {t("brc.watchCompromised", { n: last.compromised_hosts.length })}
            </span>
          ) : last?.attention_count ? (
            <span className="ndv-brc-watch-round__attn">{t("brc.watchAttnCount", { n: last.attention_count })}</span>
          ) : null}
          <div style={{ flex: 1 }} />
          <button
            type="button"
            className="btn btn--danger btn--small"
            disabled={busy === "watch-stop"}
            onClick={() =>
              void runAction("watch-stop", async () => {
                await app.BrowserConsoleWatchStop();
              })
            }
          >
            {busy === "watch-stop" ? <Loader2 size={11} className="composer-phase__spin" /> : <CircleStop size={11} />}
            <span>{t("brc.watchStop")}</span>
          </button>
        </div>
      )}

      {/* configuration (skill picker + shared form) */}
      {!state.active && (
        <div className="ndv-brc__section">
          <div className="ndv-brc__section-head">
            <span>{t("brc.watch")}</span>
          </div>
          {watchSkills.length === 0 ? (
            <div className="ndv-brc__hint">
              {t("brc.watchNoSkill")}
              <div className="ndv-brc__hint-sub">{t("brc.watchNoSkillHint")}</div>
            </div>
          ) : (
            <div className="ndv-brc-watch">
              <div className="ndv-brc-watch__row">
                <span className="ndv-brc-watch__label">{t("brc.watchPickSkill")}</span>
                <select className="mem-select ndv-brc-watch__skill" value={picked} onChange={(e) => setPicked(e.target.value)}>
                  {watchSkills.map((s) => (
                    <option key={s.name} value={s.name}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </div>
              <WatchFormFields skill={picked} t={t} busy={busy} runAction={runAction} />
            </div>
          )}
        </div>
      )}

      <WatchRoundsLog t={t} state={state} />
    </div>
  );
}

// --- skills sub-tab --------------------------------------------------------------------

function SkillsSub(props: {
  skills: BrowserConsoleSkill[];
  t: Translator;
  busy: string;
  runAction: (id: string, fn: () => Promise<unknown>, successText?: string) => Promise<void>;
  refreshSkills: () => Promise<void>;
  openEditor: (draft: BrowserSkillDraft, autoRun: boolean) => void;
  onWatchStarted: () => void;
}) {
  const { skills, t, busy, runAction, refreshSkills, openEditor, onWatchStarted } = props;
  const confirm = useConfirm();
  const [editError, setEditError] = useState("");
  const watch = useBrowserWatch();
  // Template gallery popover: 新建技能 opens the picker instead of jumping
  // straight into a blank table — real workflows differ, the descriptions
  // say which shape fits.
  const [galleryOpen, setGalleryOpen] = useState(false);
  const importInputRef = useRef<HTMLInputElement | null>(null);
  const galleryRef = useRef<HTMLDivElement | null>(null);
  // Inline config expansion: click a skill's name to unfold its
  // configuration (executor / params / step outline) without leaving the
  // list — the 技能 page's space belongs to skill config, not action trails.
  const [expanded, setExpanded] = useState("");
  const [cfgCache, setCfgCache] = useState<Record<string, string>>({});
  const toggleSkill = async (name: string) => {
    if (expanded === name) {
      setExpanded("");
      return;
    }
    setExpanded(name);
    if (!cfgCache[name]) {
      try {
        const c = await app.BrowserConsoleReadSkill(name);
        setCfgCache((prev) => ({ ...prev, [name]: c }));
      } catch { /* expand simply shows nothing on read failure */ }
    }
  };
  useEffect(() => {
    if (!galleryOpen) return;
    const onDown = (e: MouseEvent) => {
      if (galleryRef.current && !galleryRef.current.contains(e.target as Node)) setGalleryOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [galleryOpen]);

  const openSkill = async (name: string, autoRun: boolean) => {
    setEditError("");
    try {
      const content = await app.BrowserConsoleReadSkill(name);
      openEditor({ name, content, fallback: false }, autoRun);
    } catch (err) {
      setEditError(err instanceof Error ? err.message : String(err));
    }
  };

  // Wake flips draft:true off — the skill enters the model index and chat
  // invocation unlocks; panel trial runs worked all along.
  const wakeSkill = async (name: string) => {
    setEditError("");
    try {
      await app.BrowserConsoleWakeSkill(name);
      await refreshSkills();
    } catch (err) {
      setEditError(err instanceof Error ? err.message : String(err));
    }
  };

  const deleteSkill = async (name: string) => {
    const ok = await confirm({
      title: t("brc.deleteSkillTitle"),
      message: t("brc.deleteSkillConfirm", { name }),
      danger: true,
      confirmLabel: t("brc.deleteSkillConfirmBtn"),
    });
    if (!ok) return;
    await runAction("del-skill", async () => {
      await app.BrowserConsoleDeleteSkill(name);
      await refreshSkills();
      return t("brc.skillDeleted", { name });
    });
  };

  return (
    <div className="ndv-brc__body">
      <div className="ndv-brc__record-bar">
        <span className="ndv-brc__rec-label">{t("brc.skillsHint")}</span>
        <button
          type="button"
          className="btn btn--secondary btn--small"
          title={t("brc.importSkillHint")}
          onClick={() => importInputRef.current?.click()}
        >
          <Upload size={11} />
          <span>{t("brc.importSkill")}</span>
        </button>
        <div className="ndv-brc__gallery-wrap" ref={galleryRef}>
          <button
            type="button"
            className="btn btn--primary btn--small"
            onClick={() => setGalleryOpen((v) => !v)}
            disabled={galleryOpen}
          >
            <Plus size={11} />
            <span>{t("brc.newSkill")}</span>
            <ChevronDown size={11} />
          </button>
          {galleryOpen && (
            <div className="ndv-brc__gallery">
              {SKILL_TEMPLATE_GROUPS.map((group) => (
                <div key={group.id} className="ndv-brc__gallery-group">
                  <div className="ndv-brc__gallery-cat">{t(group.labelKey as never)}</div>
                  {group.templates.map((tpl) => (
                    <button
                      key={tpl.id}
                      type="button"
                      className="ndv-brc__gallery-item"
                      onClick={() => {
                        setGalleryOpen(false);
                        openEditor({ name: "", content: tpl.build(), fallback: false }, false);
                      }}
                    >
                      <span className="ndv-brc__gallery-title">
                        {t(tpl.titleKey as never)}
                        <span className="ndv-brc__gallery-form">{tpl.form === "prose" ? t("brc.tplFormProse") : t("brc.tplFormTable")}</span>
                      </span>
                      <span className="ndv-brc__gallery-desc">{t(tpl.descKey as never)}</span>
                    </button>
                  ))}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
      <input
        ref={importInputRef}
        type="file"
        accept=".md,.markdown,text/markdown"
        style={{ display: "none" }}
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (!file) return;
          void file.text().then((content) => {
            openEditor({ name: "", content, fallback: false }, false);
          }).catch(() => undefined);
        }}
      />
      {editError && <div className="banner banner--error ndv-brc__error">{editError}</div>}

      <div className="ndv-brc__section">
        <div className="ndv-brc__section-head">
          <span>{t("brc.savedSkills")}</span>
          <span className="ndv-brc__hint-inline">/{t("brc.slashHint")}</span>
        </div>
        {skills.length === 0 && (
          <div className="ndv-brc__hint">
            {t("brc.noSkills")}
            <div className="ndv-brc__hint-sub">{t("brc.noSkillsHint")}</div>
          </div>
        )}
        {skills.map((s) => (
          <div key={s.name} className="ndv-brc__skill-item">
            <div className="ndv-brc__skill-row">
              <button
                type="button"
                className="ndv-brc__skill-chev"
                title={t("brc.skillCfgToggle")}
                onClick={() => void toggleSkill(s.name)}
              >
                <ChevronDown size={11} className={expanded === s.name ? "" : "ndv-brc__chev-closed"} />
              </button>
              <span className="ndv-brc__skill-name" role="button" title={t("brc.skillCfgToggle")} onClick={() => void toggleSkill(s.name)}>{s.name}</span>
              {s.draft && <span className="ndv-brc__skill-draft" title={t("brc.draftHint")}>{t("brc.draftBadge")}</span>}
              <span className="ndv-brc__skill-desc">{s.description}</span>
            {s.draft ? (
              <button
                type="button"
                className="btn btn--secondary btn--small ndv-brc__skill-wake"
                title={t("brc.wakeHint")}
                disabled={busy === "wake"}
                onClick={() => void wakeSkill(s.name)}
              >
                {t("brc.wakeBtn")}
              </button>
            ) : (
              <button
                type="button"
                className="btn btn--secondary btn--small"
                title={t("brc.runSkill")}
                disabled={busy === "run-skill"}
                onClick={() => void openSkill(s.name, true)}
              >
                ▶
              </button>
            )}
            <button
              type="button"
              className="btn btn--secondary btn--small"
              title={t("brc.exportSkill")}
              onClick={() =>
                void (async () => {
                  try {
                    const content = await app.BrowserConsoleReadSkill(s.name);
                    const blob = new Blob([content], { type: "text/markdown;charset=utf-8" });
                    const a = document.createElement("a");
                    a.href = URL.createObjectURL(blob);
                    a.download = `${s.name}.md`;
                    a.click();
                    URL.revokeObjectURL(a.href);
                  } catch (err) {
                    setEditError(err instanceof Error ? err.message : String(err));
                  }
                })()
              }
            >
              <Download size={11} />
            </button>
            <button
              type="button"
              className="btn btn--secondary btn--small"
              title={t("brc.editSkill")}
              onClick={() => void openSkill(s.name, false)}
            >
              ✎
            </button>
            <button
              type="button"
              className="btn btn--secondary btn--small ndv-brc__skill-del"
              title={t("brc.deleteSkill")}
              onClick={() => void deleteSkill(s.name)}
            >
              <Trash2 size={11} />            </button>
          </div>
          {expanded === s.name && (
            <>
              <SkillConfigPreview content={cfgCache[s.name] ?? ""} t={t} />
              {s.browser && <SkillWatchControls skill={s.name} t={t} state={watch} busy={busy} runAction={runAction} onWatchStarted={onWatchStarted} />}
            </>
          )}
          </div>
        ))}
      </div>
    </div>
  );
}

// SkillConfigPreview unfolds one skill's configuration inline: frontmatter
// chips (executor / runAs / params) plus a step outline. Prose skills (no
// step table) show their first body lines instead.
function SkillConfigPreview(props: { content: string; t: Translator }) {
  const { content, t } = props;
  if (!content) {
    return <div className="ndv-brc__skill-cfg ndv-brc__hint">{t("brc.skillCfgLoading")}</div>;
  }
  const doc = parseSkillDoc(content);
  if (doc && doc.steps.length > 0) {
    const shown = doc.steps.slice(0, 6);
    return (
      <div className="ndv-brc__skill-cfg">
        <div className="ndv-brc__skill-cfg-meta">
          <span className="ndv-brc__skill-cfg-chip">{doc.executor === "browser-flow" ? t("brc.skillCfgDeterministic") : t("brc.skillCfgInline")}</span>
          {Object.keys(doc.params).length > 0 && (
            <span className="ndv-brc__skill-cfg-chip">{t("brc.skillCfgParams", { names: Object.keys(doc.params).join(", ") })}</span>
          )}
        </div>
        {shown.map((st, i) => (
          <div key={i} className="ndv-brc__skill-cfg-step">{i + 1}. {summarizeStep(st)}</div>
        ))}
        {doc.steps.length > shown.length && (
          <div className="ndv-brc__hint">{t("brc.skillCfgMore", { n: doc.steps.length - shown.length })}</div>
        )}
      </div>
    );
  }
  const lines = content.split("\n").map((l) => l.trim()).filter((l) => l && !l.startsWith("---")).slice(0, 3);
  return (
    <div className="ndv-brc__skill-cfg">
      {lines.map((l, i) => (
        <div key={i} className="ndv-brc__skill-cfg-step">{l}</div>
      ))}
    </div>
  );
}

// --- record sub-tab --------------------------------------------------------------------

function RecordSub(props: {
  recording: boolean;
  live: BrowserConsoleRecordEvent[];
  dropped: BrowserConsoleRecordEvent[];
  showDropped: boolean;
  setShowDropped: Dispatch<SetStateAction<boolean>>;
  generating: boolean;
  t: Translator;
  busy: string;
  startRecord: () => void;
  stopRecord: () => void;
  setDraft: (d: BrowserSkillDraft | null) => void;
}) {
  const { recording, live, dropped, showDropped, setShowDropped, generating, t, busy, startRecord, stopRecord, setDraft } = props;
  const [skillName, setSkillName] = useState("");
  const [regenError, setRegenError] = useState("");
  // Elapsed recording clock (mm:ss), ticking only while recording.
  const [elapsed, setElapsed] = useState(0);
  const recordStartRef = useRef<number>(0);
  useEffect(() => {
    if (!recording) return;
    recordStartRef.current = Date.now();
    setElapsed(0);
    const timer = window.setInterval(() => {
      setElapsed(Math.floor((Date.now() - recordStartRef.current) / 1000));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [recording]);
  return (
    <div className="ndv-brc__body">
      <div className="ndv-brc__record-bar">
        {recording ? (
          <>
            <span className="ndv-brc__rec-dot" aria-hidden="true" />
            <span className="ndv-brc__rec-count">{formatElapsed(elapsed)}</span>
            <span className="ndv-brc__rec-label">{t("brc.recording")} · {live.length}</span>
            <button type="button" className="btn btn--danger btn--small" onClick={stopRecord} disabled={busy === "record"}>
              {busy === "record" ? <Loader2 size={12} className="composer-phase__spin" /> : <Square size={11} />}
              <span>{t("brc.stopRecord")}</span>
            </button>
          </>
        ) : (
          <>
            <span className="ndv-brc__rec-label">{t("brc.recordHint")}</span>
            <button
              type="button"
              className="btn btn--primary btn--small"
              onClick={startRecord}
              disabled={busy === "record"}
            >
              <CircleStop size={11} />
              <span>{t("brc.startRecord")}</span>
            </button>
          </>
        )}
      </div>

      <div className="ndv-brc__steps">
        {live.length === 0 && <div className="ndv-brc__hint">{t("brc.recordEmpty")}</div>}
        {live.map((ev, i) => (
          <div key={i} className="ndv-brc__step-row">
            <span className="ndv-brc__step-n">{i + 1}</span>
            <span className="ndv-brc__step-text">{summarizeRecordEvent(ev)}</span>
          </div>
        ))}
        {recording && live.length > 0 && (
          <div className="ndv-brc__hint">{t("brc.recordTail")}</div>
        )}
      </div>

      {dropped.length > 0 && (
        <div className="ndv-brc__dropped">
          <button type="button" className="ndv-brc__dropped-toggle" onClick={() => setShowDropped((v) => !v)}>
            <ChevronDown size={11} className={showDropped ? "" : "ndv-brc__chev-closed"} />
            <span>{t("brc.filtered", { n: dropped.length })}</span>
          </button>
          {showDropped && (
            <div className="ndv-brc__dropped-list">
              {dropped.map((ev, i) => (
                <div key={i} className="ndv-brc__step-row ndv-brc__step-row--dim">
                  <span className="ndv-brc__step-text">{summarizeRecordEvent(ev)}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {generating && (
        <div className="ndv-brc__generating">
          <Sparkles size={12} className="composer-phase__spin" />
          <span>{t("brc.generating")}</span>
        </div>
      )}

      {!recording && live.length > 0 && !generating && (
        <div className="ndv-brc__regen">
          <input
            className="mem-input"
            value={skillName}
            onChange={(e) => setSkillName(e.target.value)}
            placeholder={t("brc.skillNamePlaceholder")}
          />
          <button
            type="button"
            className="btn btn--secondary btn--small"
            onClick={() =>
              void (async () => {
                setRegenError("");
                try {
                  setDraft(await app.BrowserConsoleGenerateSkill(skillName, live));
                } catch (err) {
                  setRegenError(err instanceof Error ? err.message : String(err));
                }
              })()
            }
          >
            {t("brc.regen")}
          </button>
        </div>
      )}
      {regenError && <div className="banner banner--error ndv-brc__error">{regenError}</div>}
    </div>
  );
}
