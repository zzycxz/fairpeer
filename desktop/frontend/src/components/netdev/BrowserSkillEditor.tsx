// BrowserSkillEditor — structured ⇄ source editor for browser skills
// (drafts from recording AND existing skills from the user skills dir).
//
// Guardrail (防混乱护栏 #1): the source mode is an escape hatch. Switching
// back to structured mode parses the edited SKILL.md; when parsing fails or
// content would be lost (unknown sections/frontmatter), the editor REFUSES
// the switch with a notice and stays in source mode — hand edits are never
// silently destroyed. Saving validates via the backend either way.
import { useEffect, useMemo, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  Code2,
  Loader2,
  Play,
  Plus,
  Save,
  X,
} from "lucide-react";
import { app, onBrowserTrial } from "../../lib/bridge";
import { useConfirm } from "../../lib/confirm";
import { useT } from "../../lib/i18n";
import {
  collectParams,
  parseSkillDoc,
  serializeSkillDoc,
  substituteParams,
  summarizeStep,
  type SkillDoc,
} from "../../lib/skillDoc";
import { summarizeRecordEvent } from "../../lib/recordTrace";
import type { BrowserConsoleRecordEvent, BrowserConsoleStep, BrowserConsoleStepType } from "../../lib/types";

const STEP_TYPES: { value: BrowserConsoleStepType; label: string }[] = [
  { value: "navigate", label: "navigate 打开" },
  { value: "click", label: "click 点击" },
  { value: "type", label: "type 输入" },
  { value: "key", label: "key 按键" },
  { value: "scroll", label: "scroll 滚动" },
  { value: "select", label: "select 选择" },
  { value: "upload", label: "upload 上传" },
  { value: "wait", label: "wait 等待" },
  { value: "extract", label: "extract 提取" },
  { value: "screenshot", label: "screenshot 截图" },
  { value: "evaluate", label: "evaluate 脚本" },
];

export function BrowserSkillEditor({
  initialContent,
  trace,
  onCancel,
  onSaved,
}: {
  initialContent: string;
  // Raw recording (kept + dropped events) for the 原始对照 disclosure — the
  // review half of the filter-transparency guardrail. Empty for skills
  // opened from the list (no recording context).
  trace?: BrowserConsoleRecordEvent[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const t = useT();
  const confirm = useConfirm();
  const [mode, setMode] = useState<"structured" | "source">("structured");
  const [doc, setDoc] = useState<SkillDoc | null>(() => parseSkillDoc(initialContent));
  const [source, setSource] = useState(initialContent);
  const [switchNotice, setSwitchNotice] = useState("");
  const [saveBusy, setSaveBusy] = useState(false);
  const [error, setError] = useState("");

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
  useEffect(
    () =>
      onBrowserTrial((st) => {
        if (st.index >= 0) {
          setTrialStates((prev) => ({ ...prev, [st.index]: { status: st.status, output: st.output, error: st.error } }));
        } else if (st.status === "done" || st.status === "failed") {
          setTrialRunning(false);
          if (st.status === "failed" && st.error) setError(`${t("brc.trialFailed")}: ${st.error}`);
        }
      }),
    [t],
  );

  const startTrial = async () => {
    if (!doc || doc.steps.length === 0) return;
    setError("");
    setTrialStates({});
    setTrialRunning(true);
    try {
      await app.BrowserConsoleTrialRun(substituteParams(doc.steps, paramDefaults));
    } catch (err) {
      setTrialRunning(false);
      setError(err instanceof Error ? err.message : String(err));
    }
  };

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
    insert: (i: number) => {
      setDoc((d) => {
        if (!d) return d;
        const steps = [...d.steps];
        steps.splice(i, 0, { type: "click", target: "" });
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
        <textarea
          className="ndv-brc-editor__source mem-input"
          value={source}
          onChange={(e) => setSource(e.target.value)}
          spellCheck={false}
        />
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
                    {trialStates[i]?.status === "running" ? <Loader2 size={11} className="composer-phase__spin" /> : trialStates[i]?.status === "done" ? "✓" : trialStates[i]?.status === "failed" ? "✕" : ""}
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
                    onChange={(e) => stepOps.update(i, { type: e.target.value as BrowserConsoleStepType })}
                  >
                    {STEP_TYPES.map((opt) => (
                      <option key={opt.value} value={opt.value}>{opt.label}</option>
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
  switch (step.type) {
    case "navigate":
      return <input className="mem-input" value={step.url ?? ""} placeholder="https://…" onChange={(e) => update({ url: e.target.value })} />;
    case "type":
      return (
        <>
          <input className="mem-input" value={step.target ?? ""} placeholder="CSS" onChange={(e) => update({ target: e.target.value })} />
          <input className="mem-input" value={step.text ?? ""} placeholder="{{参数}} / 文本" onChange={(e) => update({ text: e.target.value })} />
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
          <input className="mem-input" value={step.value ?? ""} placeholder="值" onChange={(e) => update({ value: e.target.value })} />
        </>
      );
    case "upload":
      return <input className="mem-input" value={(step.files ?? []).join(", ")} placeholder="文件路径（逗号分隔）" onChange={(e) => update({ files: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })} />;
    case "extract":
      return <input className="mem-input" value={step.target ?? ""} placeholder="CSS（空=整页）" onChange={(e) => update({ target: e.target.value })} />;
    case "evaluate":
      return <input className="mem-input" value={step.expression ?? ""} placeholder="JS 表达式" onChange={(e) => update({ expression: e.target.value })} />;
    default:
      return <input className="mem-input" value={step.target ?? ""} placeholder="CSS" onChange={(e) => update({ target: e.target.value })} />;
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
