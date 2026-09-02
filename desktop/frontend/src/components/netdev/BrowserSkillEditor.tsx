// BrowserSkillEditor — structured ⇄ source editor for browser skills
// (drafts from recording AND existing skills from the user skills dir).
//
// Guardrail (防混乱护栏 #1): the source mode is an escape hatch. Switching
// back to structured mode parses the edited SKILL.md; when parsing fails or
// content would be lost (unknown sections/frontmatter), the editor REFUSES
// the switch with a notice and stays in source mode — hand edits are never
// silently destroyed. Saving validates via the backend either way.
//
// Trial runs honor the human breakpoint (人工断点): a "human" step parks the
// run and this editor surfaces the waiting banner — the user operates the
// browser (SMS code, login, scan) and signals 继续 (or the step's auto-detect
// condition releases itself).
import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  Code2,
  Hand,
  Loader2,
  Play,
  Plus,
  Save,
  Send,
  Square,
  X,
} from "lucide-react";
import { app, onBrowserTrial } from "../../lib/bridge";
import { useConfirm } from "../../lib/confirm";
import { useT, t as tt } from "../../lib/i18n";
import {
  collectParams,
  parseSkillDoc,
  serializeSkillDoc,
  summarizeStep,
  type SkillDoc,
} from "../../lib/skillDoc";
import { summarizeRecordEvent } from "../../lib/recordTrace";
import type { BrowserConsoleRecordEvent, BrowserConsoleStep, BrowserConsoleStepType } from "../../lib/types";

// Step palette: grouped for discoverability — the editor's friendly face. The
// same taxonomy drives the per-row type select (as optgroups) and the add-step
// popover (with one-line descriptions).
const STEP_CATEGORIES: { labelKey: string; steps: { value: BrowserConsoleStepType; labelKey: string; descKey: string }[] }[] = [
  {
    labelKey: "brc.catBasic",
    steps: [
      { value: "navigate", labelKey: "ndv.bse.stNavigate", descKey: "brc.sdNavigate" },
      { value: "back", labelKey: "ndv.bse.stBack", descKey: "brc.sdBack" },
      { value: "forward", labelKey: "ndv.bse.stForward", descKey: "brc.sdForward" },
      { value: "click", labelKey: "ndv.bse.stClick", descKey: "brc.sdClick" },
      { value: "hover", labelKey: "ndv.bse.stHover", descKey: "brc.sdHover" },
      { value: "type", labelKey: "ndv.bse.stType", descKey: "brc.sdType" },
      { value: "key", labelKey: "ndv.bse.stKey", descKey: "brc.sdKey" },
      { value: "select", labelKey: "ndv.bse.stSelect", descKey: "brc.sdSelect" },
      { value: "upload", labelKey: "ndv.bse.stUpload", descKey: "brc.sdUpload" },
      { value: "scroll", labelKey: "ndv.bse.stScroll", descKey: "brc.sdScroll" },
    ],
  },
  {
    labelKey: "brc.catRead",
    steps: [
      { value: "extract", labelKey: "ndv.bse.stExtract", descKey: "brc.sdExtract" },
      { value: "screenshot", labelKey: "ndv.bse.stScreenshot", descKey: "brc.sdScreenshot" },
      { value: "evaluate", labelKey: "ndv.bse.stEvaluate", descKey: "brc.sdEvaluate" },
    ],
  },
  {
    labelKey: "brc.catWait",
    steps: [
      { value: "wait", labelKey: "ndv.bse.stWait", descKey: "brc.sdWait" },
    ],
  },
  {
    labelKey: "brc.catHuman",
    steps: [
      { value: "human", labelKey: "ndv.bse.stHuman", descKey: "brc.sdHuman" },
      { value: "ask", labelKey: "ndv.bse.stAsk", descKey: "brc.sdAsk" },
    ],
  },
];

// defaultStep seeds sensible fields per type so a freshly added step is
// immediately runnable/editable (never an empty mystery row).
function defaultStep(type: BrowserConsoleStepType): BrowserConsoleStep {
  switch (type) {
    case "navigate":
      return { type, url: "" };
    case "back":
    case "forward":
    case "screenshot":
      return { type };
    case "hover":
      return { type, target: "" };
    case "type":
      return { type, target: "", text: "" };
    case "key":
      return { type, value: "enter" };
    case "scroll":
      return { type, direction: "down", amount: 3 };
    case "select":
      return { type, target: "", value: "" };
    case "upload":
      return { type, target: "", files: [] };
    case "wait":
      return { type, condition: "networkidle", timeout_sec: 15 };
    case "extract":
      return { type, target: "" };
    case "evaluate":
      return { type, expression: "" };
    case "human":
      return { type, text: "", condition: "", timeout_sec: 600 };
    case "ask":
      return { type, text: "", target: "", timeout_sec: 600 };
    default:
      return { type, target: "" };
  }
}

// pickIdentity carries over fields that still make sense when a row is
// retyped (click→type keeps the target; human⇄ask keep the prompt text), so
// switching a step's type doesn't blank what the user already entered.
function pickIdentity(s: BrowserConsoleStep, next: BrowserConsoleStepType): Partial<BrowserConsoleStep> {
  const keep: Partial<BrowserConsoleStep> = {};
  if (s.text && (next === "human" || next === "ask" || next === "type" || next === "select")) keep.text = s.text;
  if (s.target && next !== "human" && next !== "navigate" && next !== "wait" && next !== "screenshot") keep.target = s.target;
  if (s.condition && (next === "wait" || next === "human")) keep.condition = s.condition;
  if (s.url && next === "navigate") keep.url = s.url;
  if (s.value === "table" && next === "extract") keep.value = "table";
  return keep;
}

export function BrowserSkillEditor({
  initialContent,
  trace,
  autoRun = false,
  onCancel,
  onSaved,
}: {
  initialContent: string;
  // Raw recording (kept + dropped events) for the 原始对照 disclosure — the
  // review half of the filter-transparency guardrail. Empty for skills
  // opened from the list (no recording context).
  trace?: BrowserConsoleRecordEvent[];
  // Start the trial run immediately once parsed (the skills tab's 试运行).
  autoRun?: boolean;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const t = useT();
  const confirm = useConfirm();
  // Prose/protocol skills (runAs:inline, e.g. 值守循环) don't round-trip the
  // step table — start them in source mode with a friendly banner instead of
  // a misleading empty structured form.
  const [mode, setMode] = useState<"structured" | "source">(() =>
    parseSkillDoc(initialContent)?.lossy ? "source" : "structured",
  );
  const [doc, setDoc] = useState<SkillDoc | null>(() => parseSkillDoc(initialContent));
  const [source, setSource] = useState(initialContent);
  const [switchNotice, setSwitchNotice] = useState("");
  const [saveBusy, setSaveBusy] = useState(false);
  const [error, setError] = useState("");
  const proseSkill = mode === "source" && (!doc || doc.lossy);
  // Add-step palette (click-outside closes; also closes after each pick).
  const [paletteOpen, setPaletteOpen] = useState(false);
  const paletteRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!paletteOpen) return;
    const onDown = (e: MouseEvent) => {
      if (paletteRef.current && !paletteRef.current.contains(e.target as Node)) setPaletteOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [paletteOpen]);

  // params: defaults editable in a small map (keys derived from {{refs}}).
  const paramKeys = useMemo(() => (doc ? collectParams(doc) : []), [doc]);
  const [paramDefaults, setParamDefaults] = useState<Record<string, string>>({});
  useEffect(() => {
    if (doc) setParamDefaults((prev) => ({ ...doc.params, ...prev }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doc?.name]);

  // --- trial run ---
  const [trialRunning, setTrialRunning] = useState(false);
  const [trialStates, setTrialStates] = useState<Record<number, { status: string; output?: string; error?: string }>>({});
  // The parked human/ask step: null while running free; while set, the banner
  // shows the prompt (and an input box for ask steps — the reply travels back
  // via TrialResume and binds to the step's parameter for later steps).
  const [humanWaiting, setHumanWaiting] = useState<{ index: number; prompt: string; awaitReply: boolean; bind: string } | null>(null);
  const [replyDraft, setReplyDraft] = useState("");
  useEffect(
    () =>
      onBrowserTrial((st) => {
        if (st.index >= 0) {
          setTrialStates((prev) => ({ ...prev, [st.index]: { status: st.status, output: st.output, error: st.error } }));
          if (st.status === "waiting") {
            setHumanWaiting({ index: st.index, prompt: st.output || "", awaitReply: Boolean(st.await_reply), bind: st.bind || "" });
            setReplyDraft("");
          } else if (st.status === "done" || st.status === "failed") {
            setHumanWaiting((w) => (w && w.index === st.index ? null : w));
          }
        } else if (st.status === "done" || st.status === "failed") {
          setTrialRunning(false);
          setHumanWaiting(null);
          if (st.status === "failed" && st.error) setError(`${t("brc.trialFailed")}: ${st.error}`);
        }
      }),
    [t],
  );

  const sendReply = (resume: boolean) => {
    if (!resume) {
      void app.BrowserConsoleTrialAbort().catch((err) => setError(err instanceof Error ? err.message : String(err)));
      return;
    }
    const reply = replyDraft.trim();
    setHumanWaiting(null);
    void app.BrowserConsoleTrialResume(reply).catch((err) => setError(err instanceof Error ? err.message : String(err)));
  };

  const startTrial = async () => {
    if (!doc || doc.steps.length === 0) return;
    setError("");
    setTrialStates({});
    setHumanWaiting(null);
    setTrialRunning(true);
    try {
      // Steps travel with {{参数}} refs intact; the runner substitutes at run
      // time so ask-step replies (and the defaults below) feed later steps.
      await app.BrowserConsoleTrialRun(doc.steps, paramDefaults);
    } catch (err) {
      setTrialRunning(false);
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  // autoRun (skills tab ▶): fire the trial once the doc is parsed and this
  // editor actually mounted. Skipped when doc is null (unparseable source).
  useEffect(() => {
    if (autoRun && doc && doc.steps.length > 0) {
      void startTrial();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // --- mode switching with the lossless guardrail ---
  const toSource = () => {
    if (doc) setSource(serializeSkillDoc(doc));
    setMode("source");
    setSwitchNotice("");
  };
  const toStructured = () => {
    const parsed = parseSkillDoc(source);
    if (!parsed || parsed.lossy) {
      // Guardrail: refuse the switch, stay in source mode.
      setSwitchNotice(t("brc.sourceGuard"));
      return;
    }
    setDoc(parsed);
    setSwitchNotice("");
    setMode("structured");
  };

  // --- save ---
  const save = async (overwrite: boolean) => {
    const content = mode === "structured" && doc ? serializeSkillDoc(doc) : source;
    setSaveBusy(true);
    setError("");
    try {
      if (mode === "structured" && doc) {
        // Persist the current param defaults into the frontmatter.
        const withParams: SkillDoc = { ...doc, params: paramDefaults };
        await app.BrowserConsoleSaveSkill(serializeSkillDoc(withParams), overwrite);
      } else {
        await app.BrowserConsoleSaveSkill(content, overwrite);
      }
      onSaved();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (msg.includes("已存在同名技能")) {
        const ok = await confirm({
          title: t("brc.overwriteTitle"),
          message: msg,
          danger: true,
          confirmLabel: t("brc.overwriteConfirm"),
        });
        if (ok) await save(true);
      } else {
        setError(msg);
      }
    } finally {
      setSaveBusy(false);
    }
  };

  const stepOps = {
    update: (i: number, patch: Partial<BrowserConsoleStep>) => {
      setDoc((d) => (d ? { ...d, steps: d.steps.map((s, j) => (j === i ? { ...s, ...patch } : s)) } : d));
    },
    move: (i: number, delta: -1 | 1) => {
      setDoc((d) => {
        if (!d) return d;
        const j = i + delta;
        if (j < 0 || j >= d.steps.length) return d;
        const steps = [...d.steps];
        [steps[i], steps[j]] = [steps[j], steps[i]];
        return { ...d, steps };
      });
    },
    remove: (i: number) => {
      setDoc((d) => (d ? { ...d, steps: d.steps.filter((_, j) => j !== i) } : d));
    },
    insert: (i: number, type: BrowserConsoleStepType = "click") => {
      setDoc((d) => {
        if (!d) return d;
        const steps = [...d.steps];
        steps.splice(i, 0, defaultStep(type));
        return { ...d, steps };
      });
    },
  };

  return (
    <div className="ndv__card ndv-brc ndv-brc-editor" style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
      <div className="ndv-brc-editor__bar">
        <button type="button" className="btn btn--secondary btn--small" onClick={onCancel}>
          ‹ {t("brc.back")}
        </button>
        <div className="ndv-brc-editor__modes">
          <button
            type="button"
            className={`ndv-brc__subtab${mode === "structured" ? " ndv-brc__subtab--active" : ""}`}
            onClick={toStructured}
            disabled={mode === "structured"}
          >
            {t("brc.modeStructured")}
          </button>
          <button
            type="button"
            className={`ndv-brc__subtab${mode === "source" ? " ndv-brc__subtab--active" : ""}`}
            onClick={toSource}
            disabled={mode === "source"}
          >
            <Code2 size={12} />
            <span>{t("brc.modeSource")}</span>
          </button>
        </div>
      </div>

      {switchNotice && <div className="banner banner--error ndv-brc__error">{switchNotice}</div>}
      {error && <div className="banner banner--error ndv-brc__error">{error}</div>}

      {mode === "source" ? (
        <div className="ndv-brc-editor__source-wrap" style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
          {proseSkill && (
            <div className="banner banner--warn ndv-brc-editor__prose-hint">{t("brc.proseSkillHint")}</div>
          )}
          <textarea
            className="ndv-brc-editor__source mem-input"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            spellCheck={false}
          />
        </div>
      ) : doc ? (
        <div className="ndv-brc__body ndv-brc-editor__body">
          <div className="ndv-brc__field">
            <span className="ndv-brc__field-label">{t("brc.skillName")}</span>
            <input className="mem-input" value={doc.name} onChange={(e) => setDoc({ ...doc, name: e.target.value })} />
          </div>
          <div className="ndv-brc__field">
            <span className="ndv-brc__field-label">{t("brc.skillDesc")}</span>
            <input className="mem-input" value={doc.description} onChange={(e) => setDoc({ ...doc, description: e.target.value })} />
          </div>

          <label className="ndv-brc__check ndv-brc-editor__exec-toggle" title={t("brc.executorHint")}>
            <input
              type="checkbox"
              checked={doc.executor === "browser-flow"}
              onChange={(e) => setDoc({ ...doc, executor: e.target.checked ? "browser-flow" : "" })}
            />
            <span>{t("brc.executorToggle")}</span>
          </label>

          {paramKeys.length > 0 && (
            <div className="ndv-brc__section">
              <div className="ndv-brc__section-head">
                <span>{t("brc.params")}</span>
                <span className="ndv-brc__hint-inline">{t("brc.paramsHint")}</span>
              </div>
              {paramKeys.map((k) => (
                <div key={k} className="ndv-brc__field ndv-brc__field--inline">
                  <span className="ndv-brc__param-key">{`{{${k}}}`}</span>
                  <input
                    className="mem-input"
                    value={paramDefaults[k] ?? ""}
                    onChange={(e) => setParamDefaults((prev) => ({ ...prev, [k]: e.target.value }))}
                    placeholder={t("brc.paramDefault")}
                  />
                </div>
              ))}
            </div>
          )}

          <div className="ndv-brc__section ndv-brc-editor__steps">
            <div className="ndv-brc__section-head">
              <span>{t("brc.steps")}</span>
            </div>
            {doc.steps.map((s, i) => (
              <div key={i} className={`ndv-brc-editor__step${trialStates[i] ? ` ndv-brc-editor__step--${trialStates[i].status}` : ""}`}>
                <div className="ndv-brc-editor__step-head">
                  <span className="ndv-brc__step-n">{i + 1}</span>
                  <span className="ndv-brc__step-text">{summarizeStep(s)}</span>
                  <span className={`ndv-brc-editor__trial ndv-brc-editor__trial--${trialStates[i]?.status ?? "idle"}`}>
                    {trialStates[i]?.status === "running" ? (
                      <Loader2 size={11} className="composer-phase__spin" />
                    ) : trialStates[i]?.status === "waiting" ? (
                      <Hand size={11} />
                    ) : trialStates[i]?.status === "done" ? (
                      "✓"
                    ) : trialStates[i]?.status === "failed" ? (
                      "✕"
                    ) : (
                      ""
                    )}
                  </span>
                  <button type="button" className="ndv-brc-editor__step-btn" onClick={() => stepOps.move(i, -1)} title={t("brc.moveUp")}>
                    <ArrowUp size={11} />
                  </button>
                  <button type="button" className="ndv-brc-editor__step-btn" onClick={() => stepOps.move(i, 1)} title={t("brc.moveDown")}>
                    <ArrowDown size={11} />
                  </button>
                  <button type="button" className="ndv-brc-editor__step-btn" onClick={() => stepOps.remove(i)} title={t("brc.deleteStep")}>
                    <X size={11} />
                  </button>
                </div>
                <div className="ndv-brc-editor__step-form">
                  <select
                    className="mem-select"
                    value={s.type}
                    onChange={(e) => stepOps.update(i, { ...defaultStep(e.target.value as BrowserConsoleStepType), ...pickIdentity(s, e.target.value as BrowserConsoleStepType) })}
                  >
                    {STEP_CATEGORIES.map((cat) => (
                      <optgroup key={cat.labelKey} label={t(cat.labelKey as never)}>
                        {cat.steps.map((opt) => (
                          <option key={opt.value} value={opt.value}>{t(opt.labelKey as never)}</option>
                        ))}
                      </optgroup>
                    ))}
                  </select>
                  {stepFields(s, (patch) => stepOps.update(i, patch))}
                </div>
                {trialStates[i]?.error && <div className="ndv-brc-editor__step-error">{trialStates[i]?.error}</div>}
                {trialStates[i]?.output && <div className="ndv-brc-editor__step-output">{trialStates[i]?.output}</div>}
                <button type="button" className="ndv-brc-editor__insert" onClick={() => stepOps.insert(i + 1)} title={t("brc.insertStep")}>
                  <Plus size={11} />
                </button>
              </div>
            ))}
            <button type="button" className="btn btn--secondary btn--small" onClick={() => stepOps.insert(doc.steps.length)}>
              <Plus size={11} />
              <span>{t("brc.addStep")}</span>
            </button>
            <div className="ndv-brc-editor__palette-wrap">
              <button
                type="button"
                className="btn btn--secondary btn--small"
                onClick={() => setPaletteOpen((v) => !v)}
                disabled={paletteOpen}
              >
                <ChevronDown size={11} />
                <span>{t("brc.addStepTyped")}</span>
              </button>
              {paletteOpen && (
                <div className="ndv-brc-editor__palette" ref={paletteRef}>
                  {STEP_CATEGORIES.map((cat) => (
                    <div key={cat.labelKey} className="ndv-brc-editor__palette-group">
                      <div className="ndv-brc-editor__palette-cat">{t(cat.labelKey as never)}</div>
                      {cat.steps.map((opt) => (
                        <button
                          key={opt.value}
                          type="button"
                          className="ndv-brc-editor__palette-item"
                          title={t(opt.descKey as never)}
                          onClick={() => {
                            stepOps.insert(doc.steps.length, opt.value);
                            setPaletteOpen(false);
                          }}
                        >
                          <span className="ndv-brc-editor__palette-name">{t(opt.labelKey as never)}</span>
                          <span className="ndv-brc-editor__palette-desc">{t(opt.descKey as never)}</span>
                        </button>
                      ))}
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>

          {trace && trace.length > 0 && (
            <Fold title={t("brc.rawTrace")}>
              <div className="ndv-brc__dropped-list">
                {trace
                  .filter((ev) => ev.type !== "effect")
                  .map((ev, i) => (
                    <div key={i} className="ndv-brc__step-row">
                      <span className="ndv-brc__step-text">{summarizeRecordEvent(ev)}</span>
                    </div>
                  ))}
              </div>
            </Fold>
          )}

          {(["whenToUse", "pitfalls", "verification"] as const).map((field) => (
            <Fold key={field} title={t(`brc.sec_${field}`)}>
              <textarea
                className="ndv-brc-editor__sec mem-input"
                value={doc[field]}
                onChange={(e) => setDoc({ ...doc, [field]: e.target.value })}
                rows={2}
              />
            </Fold>
          ))}
        </div>
      ) : (
        <div className="ndv-brc__hint">{t("brc.sourceGuardInitial")}</div>
      )}

      {/* Human/ask banner: the parked trial waits on the user. A human step
          waits for manual browser operation then 继续; an ask step shows an
          input box — the reply travels back via TrialResume and binds to the
          step's parameter for later steps. */}
      {humanWaiting && (
        <div className="ndv-brc-editor__humanwait">
          <Hand size={14} className="ndv-brc-editor__humanwait-icon" />
          <div className="ndv-brc-editor__humanwait-body">
            <span className="ndv-brc-editor__humanwait-title">
              {t("brc.humanWaiting")} · {t("brc.stepN", { n: humanWaiting.index + 1 })}
              {humanWaiting.awaitReply && humanWaiting.bind ? ` · {{${humanWaiting.bind}}}` : ""}
            </span>
            <span className="ndv-brc-editor__humanwait-prompt">{humanWaiting.prompt}</span>
          </div>
          {humanWaiting.awaitReply && (
            <input
              className="mem-input ndv-brc-editor__humanwait-input"
              value={replyDraft}
              onChange={(e) => setReplyDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") sendReply(true);
              }}
              placeholder={t("brc.askPlaceholder")}
              autoFocus
            />
          )}
          <button
            type="button"
            className="btn btn--primary btn--small"
            onClick={() => sendReply(true)}
          >
            {humanWaiting.awaitReply ? <Send size={11} /> : <Play size={11} />}
            <span>{humanWaiting.awaitReply ? t("brc.askSend") : t("brc.humanResume")}</span>
          </button>
          <button
            type="button"
            className="btn btn--danger btn--small"
            onClick={() => sendReply(false)}
          >
            <Square size={11} />
            <span>{t("brc.humanAbort")}</span>
          </button>
        </div>
      )}

      <div className="ndv-brc-editor__footer">
        <button type="button" className="btn btn--secondary btn--small" onClick={() => void startTrial()} disabled={trialRunning || !doc || doc.steps.length === 0}>
          {trialRunning ? <Loader2 size={11} className="composer-phase__spin" /> : <Play size={11} />}
          <span>{t("brc.trialRun")}</span>
        </button>
        <div style={{ flex: 1 }} />
        <button type="button" className="btn btn--secondary btn--small" onClick={onCancel}>
          {t("brc.discard")}
        </button>
        <button type="button" className="btn btn--primary btn--small" onClick={() => void save(false)} disabled={saveBusy}>
          {saveBusy ? <Loader2 size={11} className="composer-phase__spin" /> : <Save size={11} />}
          <span>{t("brc.saveSkill")}</span>
        </button>
      </div>
    </div>
  );
}

// stepFields renders the type-specific inputs for one step row.
function stepFields(step: BrowserConsoleStep, update: (patch: Partial<BrowserConsoleStep>) => void) {
  const t = tt; // module-level translator (non-reactive is fine for placeholders)
  switch (step.type) {
    case "navigate":
      return <input className="mem-input" value={step.url ?? ""} placeholder="https://…" onChange={(e) => update({ url: e.target.value })} />;
    case "type":
      return (
        <>
          <input className="mem-input" value={step.target ?? ""} placeholder={t("ndv.bse.phTargetMulti")} spellCheck={false} onChange={(e) => update({ target: e.target.value })} />
          <input className="mem-input" value={step.text ?? ""} placeholder={t("ndv.bse.phText")} onChange={(e) => update({ text: e.target.value })} />
        </>
      );
    case "key":
      return (
        <select className="mem-select" value={step.value ?? "enter"} onChange={(e) => update({ value: e.target.value })}>
          <option value="enter">enter</option>
          <option value="tab">tab</option>
          <option value="escape">escape</option>
        </select>
      );
    case "scroll":
      return (
        <>
          <select className="mem-select" value={step.direction ?? "down"} onChange={(e) => update({ direction: e.target.value })}>
            <option value="down">down</option>
            <option value="up">up</option>
          </select>
          <input className="mem-input" type="number" min={1} value={step.amount ?? 3} onChange={(e) => update({ amount: parseInt(e.target.value, 10) || 1 })} />
        </>
      );
    case "wait":
      return (
        <>
          <input className="mem-input" value={step.condition ?? "networkidle"} placeholder="networkidle" onChange={(e) => update({ condition: e.target.value })} />
          <input className="mem-input" type="number" min={1} value={step.timeout_sec ?? 15} onChange={(e) => update({ timeout_sec: parseInt(e.target.value, 10) || 15 })} />
        </>
      );
    case "select":
      return (
        <>
          <input className="mem-input" value={step.target ?? ""} placeholder="CSS" onChange={(e) => update({ target: e.target.value })} />
          <input className="mem-input" value={step.value ?? ""} placeholder={t("ndv.bse.phValue")} onChange={(e) => update({ value: e.target.value })} />
        </>
      );
    case "upload":
      return <input className="mem-input" value={(step.files ?? []).join(", ")} placeholder={t("ndv.bse.phFiles")} onChange={(e) => update({ files: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })} />;
    case "extract":
      return (
        <>
          <input className="mem-input" value={step.target ?? ""} placeholder={t("ndv.bse.phCss")} onChange={(e) => update({ target: e.target.value })} />
          <label className="ndv-brc__check" title={t("brc.extractTableHint")}>
            <input
              type="checkbox"
              checked={step.value === "table"}
              onChange={(e) => update({ value: e.target.checked ? "table" : undefined })}
            />
            <span>{t("brc.extractTable")}</span>
          </label>
        </>
      );
    case "back":
    case "forward":
      return <span className="ndv-brc-editor__stepform-note">{t("brc.noFields")}</span>;
    case "human":
      return (
        <>
          <input
            className="mem-input"
            value={step.text ?? ""}
            placeholder={t("ndv.bse.phHumanPrompt")}
            onChange={(e) => update({ text: e.target.value })}
          />
          <input
            className="mem-input"
            value={step.condition ?? ""}
            placeholder={t("ndv.bse.phHumanCond")}
            spellCheck={false}
            onChange={(e) => update({ condition: e.target.value })}
          />
        </>
      );
    case "ask":
      return (
        <>
          <input
            className="mem-input"
            value={step.text ?? ""}
            placeholder={t("ndv.bse.phAskPrompt")}
            onChange={(e) => update({ text: e.target.value })}
          />
          <input
            className="mem-input"
            value={step.target ?? ""}
            placeholder={t("ndv.bse.phAskBind")}
            spellCheck={false}
            onChange={(e) => update({ target: e.target.value })}
          />
        </>
      );
    case "evaluate":
      return <input className="mem-input" value={step.expression ?? ""} placeholder={t("ndv.bse.phJs")} onChange={(e) => update({ expression: e.target.value })} />;
    default:
      return <input className="mem-input" value={step.target ?? ""} placeholder={t("ndv.bse.phTargetMulti")} spellCheck={false} onChange={(e) => update({ target: e.target.value })} />;
  }
}

function Fold({ title, children }: { title: string; children: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="ndv-brc__section">
      <button type="button" className="ndv-brc__dropped-toggle" onClick={() => setOpen((v) => !v)}>
        <ChevronDown size={11} className={open ? "" : "ndv-brc__chev-closed"} />
        <span>{title}</span>
      </button>
      {open && children}
    </div>
  );
}
