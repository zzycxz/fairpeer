// BrowserConsolePanel — the 运维 dock's 「浏览器」 tab: a two-sub-tab console
// for manually driving the console browser session and recording runs into
// skills. Sub-tab pills follow the ZCode right-dock tab language (transparent
// inactive / soft-pill active); the driven browser is the external window,
// the collapsible preview reads the shared mirror stream.
import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type Dispatch, type SetStateAction } from "react";
import {
  ChevronDown,
  CircleStop,
  Globe,
  Loader2,
  MousePointerClick,
  RefreshCw,
  Sparkles,
  Square,
  Upload,
} from "lucide-react";
import { app, onBrowserRecord } from "../../lib/bridge";
import { browserMirrorSnapshot, subscribeBrowserMirror } from "../../lib/browserMirror";
import { useT, type Translator } from "../../lib/i18n";
import type {
  BrowserConsoleElement,
  BrowserConsoleRecordEvent,
  BrowserConsoleSkill,
  BrowserConsoleState,
  BrowserSkillDraft,
} from "../../lib/types";
import { summarizeRecordEvent } from "../../lib/recordTrace";
import { BrowserSkillEditor } from "./BrowserSkillEditor";

type SubTab = "interact" | "record";

const LOG_CAP = 50;
const LIVE_CAP = 200;

export function BrowserConsolePanel({ onInsertComposer }: { onInsertComposer?: (text: string) => void }) {
  const t = useT();
  const [subTab, setSubTab] = useState<SubTab>("interact");

  // --- shared session state ---
  const [state, setState] = useState<BrowserConsoleState | null>(null);
  const [busy, setBusy] = useState<string>("");
  const [error, setError] = useState("");
  const [log, setLog] = useState<string[]>([]);
  const [previewOpen, setPreviewOpen] = useState(true);
  const mirror = useMirror();

  const appendLog = useCallback((line: string) => {
    setLog((prev) => [...prev.slice(-(LOG_CAP - 1)), line]);
  }, []);

  const refreshState = useCallback(async () => {
    try {
      setState(await app.BrowserConsoleState());
    } catch {
      setState({ open: false, session_id: "", browser: "", attached: false, url: "" });
    }
  }, []);

  useEffect(() => {
    void refreshState();
  }, [refreshState]);

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

  const startRecord = () =>
    void runAction("record", async () => {
      setLive([]);
      setDropped([]);
      await app.BrowserConsoleRecordStart();
      setRecording(true);
      appendLog("● 开始录制");
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
        appendLog(`■ 录制结束：${filtered.kept.length} 步，过滤 ${filtered.dropped.length} 条`);
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

  // --- render ---
  if (draft) {
    return (
      <BrowserSkillEditor
        initialContent={draft.content}
        trace={rawTrace}
        onCancel={() => setDraft(null)}
        onSaved={() => {
          setDraft(null);
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
        </div>
        <span className={`ndv-brc__dot${state?.open ? " ndv-brc__dot--on" : ""}`} title={state?.browser ?? ""} />
      </div>

      {/* session bar */}
      <div className="ndv-brc__session">
        <span className="ndv-brc__session-label">
          {state?.open ? `${state.browser}${state.attached ? " · attach" : ""}` : t("brc.sessionClosed")}
        </span>
        {state?.open ? (
          <button type="button" className="btn btn--secondary btn--small" onClick={() => void runAction("close", () => app.BrowserConsoleClose(), t("brc.closed"))}>
            {t("brc.closeBrowser")}
          </button>
        ) : (
          <button type="button" className="btn btn--primary btn--small" onClick={() => void runAction("open", async () => { await app.BrowserConsoleOpen("", ""); }, t("brc.opened"))}>
            {busy === "open" ? <Loader2 size={12} className="composer-phase__spin" /> : t("brc.openBrowser")}
          </button>
        )}
      </div>

      {error && <div className="banner banner--error ndv-brc__error">{error}</div>}

      {/* mirror preview (collapsible) */}
      <button type="button" className="ndv-brc__preview-toggle" onClick={() => setPreviewOpen((v) => !v)}>
        <ChevronDown size={12} className={previewOpen ? "" : "ndv-brc__chev-closed"} />
        <span>{t("brc.preview")}</span>
      </button>
      {previewOpen && (
        <div className="ndv-brc__preview">
          {mirror.image ? (
            <img className="ndv-brc__preview-img" src={mirror.image} alt="" />
          ) : (
            <div className="ndv-brc__preview-empty">{t("brc.previewEmpty")}</div>
          )}
        </div>
      )}

      {subTab === "interact" ? (
        <InteractSub
          {...{ url, setUrl, elements, setElements, target, setTarget, text, setText, submitEnter, setSubmitEnter, extractResult, setExtractResult, busy, runAction, t, onInsertComposer }}
        />
      ) : (
        <RecordSub
          {...{
            recording, live, dropped, showDropped, setShowDropped, generating, skills, t,
            busy, startRecord, stopRecord, setDraft,
          }}
        />
      )}

      {/* op log */}
      {log.length > 0 && (
        <div className="ndv-brc__log">
          {log.slice(-8).map((line, i) => (
            <div key={i} className="ndv-brc__log-line">{line}</div>
          ))}
        </div>
      )}
    </div>
  );
}

// useMirror subscribes to the shared browser mirror store (no local state).
function useMirror() {
  return useSyncExternalStore(subscribeBrowserMirror, browserMirrorSnapshot);
}

// formatElapsed renders the recording clock as m:ss.
function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
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
  t: Translator;
}) {
  const { url, setUrl, elements, setElements, target, setTarget, text, setText, submitEnter, setSubmitEnter, extractResult, setExtractResult, busy, runAction, onInsertComposer, t } = props;
  return (
    <div className="ndv-brc__body">
      <form
        className="ndv-brc__urlbar"
        onSubmit={(e) => {
          e.preventDefault();
          if (url) void runAction("nav", () => app.BrowserConsoleNavigate(url));
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
        <div className="ndv-brc__section-head">
          <span>{t("brc.elements")}</span>
          <button
            type="button"
            className="btn btn--secondary btn--small"
            aria-label={t("brc.refreshElements")}
            title={t("brc.refreshElements")}
            onClick={() => void runAction("elements", async () => {
              setElements(await app.BrowserConsoleElements());
            }, undefined)}
          >
            <RefreshCw size={11} />
          </button>
        </div>
        <div className="ndv-brc__elements">
          {elements.length === 0 && <div className="ndv-brc__hint">{t("brc.elementsEmpty")}</div>}
          {elements.map((el) => (
            <button
              type="button"
              key={el.ref}
              className={`ndv-brc__element${target === el.ref ? " ndv-brc__element--sel" : ""}`}
              onClick={() => setTarget(el.ref)}
              title={`${el.role} ${el.name}`}
            >
              <span className="ndv-brc__element-role">{el.role}</span>
              <span className="ndv-brc__element-name">{el.name || el.ref}</span>
              {el.value ? <span className="ndv-brc__element-value">{el.value}</span> : null}
            </button>
          ))}
        </div>
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
            onClick={() => void runAction("type", async () => {
              const out = await app.BrowserConsoleType(target, text);
              if (submitEnter) await app.BrowserConsoleKey("enter");
              return out;
            })}
          >
            {t("brc.typeBtn")}
          </button>
          <button
            type="button"
            className="btn btn--secondary btn--small"
            disabled={!target || busy === "click"}
            onClick={() => void runAction("click", () => app.BrowserConsoleClick(target))}
          >
            {t("brc.clickBtn")}
          </button>
        </div>
      </div>

      <div className="ndv-brc__section">
        <ExtractBox {...{ extractResult, setExtractResult, target, busy, runAction, onInsertComposer, t }} />
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
  t: Translator;
}) {
  const { extractResult, setExtractResult, target, busy, runAction, onInsertComposer, t } = props;
  const [selector, setSelector] = useState("");
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
          onClick={() => void runAction("extract", async () => {
            setExtractResult(await app.BrowserConsoleExtract(selector || target));
          })}
        >
          {busy === "extract" ? <Loader2 size={12} className="composer-phase__spin" /> : t("brc.extractBtn")}
        </button>
      </div>
      {extractResult && <pre className="ndv-brc__extract-result">{extractResult.slice(0, 4000)}</pre>}
    </>
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
  skills: BrowserConsoleSkill[];
  t: Translator;
  busy: string;
  startRecord: () => void;
  stopRecord: () => void;
  setDraft: (d: BrowserSkillDraft | null) => void;
}) {
  const { recording, live, dropped, showDropped, setShowDropped, generating, skills, t, busy, startRecord, stopRecord, setDraft } = props;
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

      <div className="ndv-brc__section">
        <div className="ndv-brc__section-head">
          <span>{t("brc.savedSkills")}</span>
          <span className="ndv-brc__hint-inline">/{t("brc.slashHint")}</span>
        </div>
        {skills.length === 0 && <div className="ndv-brc__hint">{t("brc.noSkills")}</div>}
        {skills.map((s) => (
          <div key={s.name} className="ndv-brc__skill-row">
            <span className="ndv-brc__skill-name">{s.name}</span>
            <span className="ndv-brc__skill-desc">{s.description}</span>
            <button
              type="button"
              className="btn btn--secondary btn--small"
              title={t("brc.editSkill")}
              onClick={() =>
                void (async () => {
                  try {
                    const content = await app.BrowserConsoleReadSkill(s.name);
                    setDraft({ name: s.name, content, fallback: false });
                  } catch (err) {
                    window.console?.error?.(err);
                  }
                })()
              }
            >
              ✎
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
