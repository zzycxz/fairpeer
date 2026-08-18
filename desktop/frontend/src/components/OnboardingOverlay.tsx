import { useCallback, useEffect, useRef, useState } from "react";
import logo from "../assets/logo.png";
import { useT, type Translator } from "../lib/i18n";
import { app } from "../lib/bridge";
import type { ProviderTemplate } from "../lib/types";
import { ANCHORED_POPOVER_CLOSE_MS, AnchoredPopover } from "./AnchoredPopover";
import { Check, ChevronsUpDown, Loader2, RefreshCw } from "lucide-react";

// Three-step first-run wizard: pick vendor → paste key → pick default model.
// Replaces the old single-key-input overlay (which assumed a built-in provider
// that no longer exists after WP-3.0).
type Step = "vendor" | "key" | "model";

export function OnboardingOverlay({ onComplete }: { onComplete: () => void }) {
  const t = useT();
  const [step, setStep] = useState<Step>("vendor");
  const [templates, setTemplates] = useState<ProviderTemplate[]>([]);
  const [syncing, setSyncing] = useState(false);
  const [selected, setSelected] = useState<ProviderTemplate | null>(null);
  // The API key is entered in step 2 and consumed in step 3 (SetupProvider).
  // Lifted here so both steps share it without a global.
  const [apiKey, setApiKey] = useState("");
  const [loadErr, setLoadErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    app.GetProviderTemplates()
      .then((ts) => { if (!cancelled) setTemplates(ts); })
      .catch((e) => { if (!cancelled) setLoadErr(String(e)); });
    return () => { cancelled = true; };
  }, []);

  return (
    <div className="onboarding">
      <div className="onboarding__card onboarding__card--wide">
        <img src={logo} className="onboarding__logo" alt="FairPeer" draggable={false} />
        <div className="onboarding__title">{t("onboarding.title")}</div>

        {loadErr && <div className="onboarding__error" role="alert">{loadErr}</div>}

        {step === "vendor" && (
          <VendorStep
            direct={templates.filter((t) => t.category === "direct")}
            aggregators={templates.filter((t) => t.category === "aggregator")}
            locals={templates.filter((t) => t.category === "local")}
            onPick={(tpl) => {
              setSelected(tpl);
              if (tpl.local) {
                // Keyless local endpoint (Ollama / llama.cpp) — no key step.
                setApiKey("");
                setStep("model");
              } else {
                setStep("key");
              }
            }}
            syncing={syncing}
            onSync={async () => {
              setSyncing(true);
              try {
                await app.RefreshRegistry();
                const fresh = await app.GetProviderTemplates();
                setTemplates(fresh);
              } catch (e) { console.error(e); }
              setSyncing(false);
            }}
            t={t}
          />
        )}

        {step === "key" && selected && (
          <KeyStep
            template={selected}
            apiKey={apiKey}
            onApiKeyChange={setApiKey}
            onBack={() => setStep("vendor")}
            onConnected={() => setStep("model")}
            t={t}
          />
        )}

        {step === "model" && selected && (
          <ModelStep
            template={selected}
            apiKey={apiKey}
            onDone={onComplete}
            t={t}
          />
        )}

        <button type="button" className="onboarding__skip" onClick={onComplete}>
          {t("onboarding.skip")}
        </button>
      </div>
    </div>
  );
}

// ── Step 1: vendor grid ───────────────────────────────────────────────────
export function VendorStep({ direct, aggregators, locals, onPick, syncing, onSync, t }: {
  direct: ProviderTemplate[];
  aggregators: ProviderTemplate[];
  locals: ProviderTemplate[];
  onPick: (tpl: ProviderTemplate) => void;
  syncing: boolean;
  onSync: () => void;
  t: Translator;
}) {
  const all = [...direct, ...aggregators, ...locals];
  const [value, setValue] = useState("");
  const selected = all.find((x) => x.name === value) ?? null;

  const [open, setOpen] = useState(false);
  const [closing, setClosing] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const closeTimerRef = useRef<number | null>(null);

  const closeMenu = useCallback(() => {
    if (closeTimerRef.current !== null) return;
    setClosing(true);
    window.requestAnimationFrame(() => setOpen(false));
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    closeTimerRef.current = window.setTimeout(() => {
      closeTimerRef.current = null;
      setClosing(false);
    }, reduceMotion ? 0 : ANCHORED_POPOVER_CLOSE_MS);
  }, []);

  useEffect(() => {
    return () => {
      if (closeTimerRef.current !== null) window.clearTimeout(closeTimerRef.current);
    };
  }, []);

  return (
    <div className="onboarding__step">
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div className="onboarding__tag">{t("onboarding.step1Hint")}</div>
        <button 
          type="button" 
          className="btn btn--small" 
          style={{ background: "transparent", border: "none", color: "var(--text-muted)", cursor: "pointer", display: "flex", alignItems: "center", gap: "4px", fontSize: "12px" }}
          onClick={onSync}
          disabled={syncing}
        >
          {syncing ? <Loader2 size={12} className="onboarding__spinner" /> : <RefreshCw size={12} />}
          {t("settings.registryCheckUpdate") || "同步模型"}
        </button>
      </div>

      <div className="onboarding__dropdown">
        <button
          ref={triggerRef}
          type="button"
          className="onboarding__dropdown-trigger"
          aria-expanded={open && !closing}
          onClick={() => (open || closing ? closeMenu() : setOpen(true))}
        >
          <span className="onboarding__dropdown-label">
            {selected ? selected.displayName : t("onboarding.selectPlaceholder")}
          </span>
          <ChevronsUpDown size={14} className="onboarding__dropdown-icon" />
        </button>

        <AnchoredPopover
          open={open}
          closing={closing}
          anchorRef={triggerRef}
          onClose={closeMenu}
          className="onboarding__menu onboarding__menu--portal"
          style={{ width: triggerRef.current?.getBoundingClientRect().width ?? 280 }}
        >
          <div className="onboarding__menu-list" role="listbox">
            <div className="onboarding__menu-group">{t("onboarding.categoryDirect")}</div>
            {direct.map((tpl) => (
              <button
                key={tpl.name}
                type="button"
                role="option"
                aria-selected={value === tpl.name}
                className={`onboarding__menu-item ${value === tpl.name ? "onboarding__menu-item--selected" : ""}`}
                onClick={() => {
                  setValue(tpl.name);
                  closeMenu();
                }}
              >
                <span className="onboarding__menu-item-label">{tpl.displayName}</span>
                {value === tpl.name && <Check size={14} className="onboarding__menu-item-check" />}
              </button>
            ))}
            
            {aggregators.length > 0 && (
              <>
                <div className="onboarding__menu-group">{t("onboarding.categoryAggregator")}</div>
                {aggregators.map((tpl) => (
                  <button
                    key={tpl.name}
                    type="button"
                    role="option"
                    aria-selected={value === tpl.name}
                    className={`onboarding__menu-item ${value === tpl.name ? "onboarding__menu-item--selected" : ""}`}
                    onClick={() => {
                      setValue(tpl.name);
                      closeMenu();
                    }}
                  >
                    <span className="onboarding__menu-item-label">{tpl.displayName}</span>
                    {value === tpl.name && <Check size={14} className="onboarding__menu-item-check" />}
                  </button>
                ))}
              </>
            )}

            {locals.length > 0 && (
              <>
                <div className="onboarding__menu-group">{t("onboarding.categoryLocal")}</div>
                {locals.map((tpl) => (
                  <button
                    key={tpl.name}
                    type="button"
                    role="option"
                    aria-selected={value === tpl.name}
                    className={`onboarding__menu-item ${value === tpl.name ? "onboarding__menu-item--selected" : ""}`}
                    onClick={() => {
                      setValue(tpl.name);
                      closeMenu();
                    }}
                  >
                    <span className="onboarding__menu-item-label">{tpl.displayName}</span>
                    {value === tpl.name && <Check size={14} className="onboarding__menu-item-check" />}
                  </button>
                ))}
              </>
            )}
          </div>
        </AnchoredPopover>
      </div>

      {selected && (
        <div className="onboarding__vendor-info">
          <div className="onboarding__endpoint">{selected.baseUrl}</div>
          {selected.local && <span className="onboarding__vendor-badge">{t("onboarding.badgeLocal")}</span>}
          {selected.vision && <span className="onboarding__vendor-badge">{t("onboarding.badgeVision")}</span>}
          {selected.codingOnly && <span className="onboarding__vendor-badge">{t("onboarding.badgeCoding")}</span>}
        </div>
      )}

      <div className="onboarding__actions">
        <button
          type="button"
          className="onboarding__submit"
          onClick={() => selected && onPick(selected)}
          disabled={!selected}
        >
          {t("onboarding.next")}
        </button>
      </div>
    </div>
  );
}

// ── Step 2: paste API key ─────────────────────────────────────────────────
export function KeyStep({ template, apiKey, onApiKeyChange, onBack, onConnected, t }: {
  template: ProviderTemplate;
  apiKey: string;
  onApiKeyChange: (v: string) => void;
  onBack: () => void;
  onConnected: () => void;
  t: Translator;
}) {
  const [state, setState] = useState<"idle" | "validating" | "error">("idle");
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => { inputRef.current?.focus(); }, []);

  const submit = useCallback(async () => {
    const key = apiKey.trim();
    if (!key) {
      setError(t("onboarding.error.empty"));
      setState("error");
      return;
    }
    setState("validating");
    setError(null);
    try {
      await app.ProbeVendorKey(template.baseUrl, key);
      onConnected();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (/invalid|401|403|unauthorized/i.test(msg)) {
        setError(t("onboarding.error.invalid"));
      } else if (/network|unreachable|timeout|dial/i.test(msg)) {
        setError(t("onboarding.error.network"));
      } else {
        setError(msg || t("onboarding.error.unknown"));
      }
      setState("error");
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [apiKey, template, onConnected, t]);

  return (
    <div className="onboarding__step">
      <div className="onboarding__vendor-picked">{template.displayName}</div>
      <div className="onboarding__endpoint">{template.baseUrl}</div>

      <label className="onboarding__label" htmlFor="onboarding-key">
        {t("onboarding.inputLabel")}
      </label>
      <input
        id="onboarding-key"
        ref={inputRef}
        className="onboarding__input"
        type="password"
        autoComplete="off"
        spellCheck={false}
        placeholder={t("onboarding.inputPlaceholder")}
        value={apiKey}
        onChange={(e) => { onApiKeyChange(e.target.value); if (state === "error") setState("idle"); }}
        onKeyDown={(e) => { if (e.key === "Enter" && state !== "validating") { e.preventDefault(); void submit(); } }}
        disabled={state === "validating"}
      />

      {state === "error" && error && (
        <div className="onboarding__error" role="alert">{error}</div>
      )}

      <div className="onboarding__actions">
        <button type="button" className="onboarding__back" onClick={onBack} disabled={state === "validating"}>
          ← {t("onboarding.back")}
        </button>
        <button type="button" className="onboarding__submit" onClick={() => void submit()} disabled={state === "validating"}>
          {state === "validating" ? (<><span className="onboarding__spinner" />{t("onboarding.validating")}</>) : t("onboarding.connect")}
        </button>
      </div>

      {template.docUrl && (
        <a className="onboarding__doclink" href={template.docUrl} target="_blank" rel="noreferrer">
          {t("onboarding.getKey")} →
        </a>
      )}
    </div>
  );
}

// ── Step 3: pick default model ────────────────────────────────────────────
// Shows the template's preset model list (with role badges). Real-time model
// discovery happens later in Settings; here we use the curated preset so the
// user gets a working default in one shot. SetupProvider persists key + provider
// + default in one atomic call.
export function ModelStep({ template, apiKey, onDone, t }: {
  template: ProviderTemplate;
  apiKey: string;
  onDone: () => void;
  t: Translator;
}) {
  const [defaultPick, setDefaultPick] = useState<string>(template.defaultModel);
  const [visionPick, setVisionPick] = useState<string>(template.visionModel || template.defaultModel);
  const [fastPick, setFastPick] = useState<string>(template.fastModel || "follow");
  const [voicePick, setVoicePick] = useState<string>("none");
  const [state, setState] = useState<"ready" | "saving" | "error">("ready");
  const [error, setError] = useState<string | null>(null);

  // Local templates (Ollama, llama.cpp): list the models actually installed on
  // the running server via GET /v1/models — no key required. Falls back to the
  // preset list when the server is unreachable.
  const [fetchedModels, setFetchedModels] = useState<string[] | null>(null);
  const [fetchFailed, setFetchFailed] = useState(false);
  useEffect(() => {
    if (!template.local) return;
    let cancelled = false;
    app.FetchProviderModels({
      name: template.name,
      builtIn: false,
      added: false,
      kind: template.kind,
      baseUrl: template.baseUrl,
      models: [],
      modelsUrl: "",
      default: "",
      apiKeyEnv: template.apiKeyEnv,
      keySet: false,
      contextWindow: 0,
      reasoningProtocol: "",
      supportedEfforts: [],
      defaultEffort: "",
    })
      .then((ms) => { if (!cancelled && ms.length > 0) setFetchedModels(ms); })
      .catch(() => { if (!cancelled) setFetchFailed(true); });
    return () => { cancelled = true; };
  }, [template]);

  const models = fetchedModels ?? (template.models.length > 0 ? template.models : [template.defaultModel]);

  // Once the live list lands, retarget picks that no longer exist in it.
  useEffect(() => {
    if (!fetchedModels || fetchedModels.length === 0) return;
    if (!fetchedModels.includes(defaultPick)) setDefaultPick(fetchedModels[0]);
    if (visionPick !== "none" && !fetchedModels.includes(visionPick)) setVisionPick("none");
    if (fastPick !== "follow" && !fetchedModels.includes(fastPick)) setFastPick("follow");
  }, [fetchedModels, defaultPick, visionPick, fastPick]);

  const finish = useCallback(async () => {
    setState("saving");
    setError(null);
    try {
      await app.SetupProvider(template, apiKey, defaultPick, visionPick, fastPick, voicePick);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setState("error");
    }
  }, [template, apiKey, defaultPick, visionPick, fastPick, voicePick, onDone]);

  return (
    <div className="onboarding__step">
      <div className="onboarding__tag">{t("onboarding.step3Hint")}</div>
      {template.local && fetchFailed && (
        <div className="onboarding__endpoint" role="status" style={{ marginTop: "0.5rem" }}>
          {t("onboarding.localFetchFailed")}
        </div>
      )}
      <div className="onboarding__model-selectors" style={{ display: "flex", flexDirection: "column", gap: "1rem", margin: "1.5rem 0" }}>
        <div>
          <label className="set-label" style={{ display: "block", marginBottom: "0.25rem" }}>{t("settings.defaultModel")}</label>
          <select className="mem-select" style={{ width: "100%" }} value={defaultPick} onChange={(e) => setDefaultPick(e.target.value)}>
            {models.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>
        
        <div>
          <label className="set-label" style={{ display: "block", marginBottom: "0.25rem" }}>{t("settings.screenshotVlmLabel") || "图片识别模型"}</label>
          <select className="mem-select" style={{ width: "100%" }} value={visionPick} onChange={(e) => setVisionPick(e.target.value)}>
            <option value="none">{t("settings.screenshotVlmNone") || "未配置"}</option>
            {models.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>

        <div>
          <label className="set-label" style={{ display: "block", marginBottom: "0.25rem" }}>{t("settings.voiceModelLabel") || "语音识别模型"}</label>
          <select className="mem-select" style={{ width: "100%" }} value={voicePick} onChange={(e) => setVoicePick(e.target.value)}>
            <option value="none">{t("settings.voiceModelNone") || "未配置"}</option>
            {models.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>
        
        <div>
          <label className="set-label" style={{ display: "block", marginBottom: "0.25rem" }}>{t("settings.fastTaskModel") || "迅捷任务模型"}</label>
          <select className="mem-select" style={{ width: "100%" }} value={fastPick} onChange={(e) => setFastPick(e.target.value)}>
            <option value="follow">{t("settings.fastTaskNone") || "跟随默认"}</option>
            {models.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>
      </div>

      {state === "error" && error && <div className="onboarding__error" role="alert">{error}</div>}

      <div className="onboarding__actions">
        <button type="button" className="onboarding__submit" onClick={() => void finish()} disabled={state === "saving"}>
          {state === "saving" ? (<><span className="onboarding__spinner" />{t("onboarding.saving")}</>) : t("onboarding.finish")}
        </button>
      </div>
    </div>
  );
}
