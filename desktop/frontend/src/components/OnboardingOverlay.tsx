import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import logo from "../assets/logo.png";
import { useT, type Translator } from "../lib/i18n";
import { app } from "../lib/bridge";
import type { ProviderTemplate } from "../lib/types";

// Three-step first-run wizard: pick vendor → paste key → pick default model.
// Replaces the old single-key-input overlay (which assumed a built-in provider
// that no longer exists after WP-3.0).
type Step = "vendor" | "key" | "model";

export function OnboardingOverlay({ onComplete }: { onComplete: () => void }) {
  const t = useT();
  const [step, setStep] = useState<Step>("vendor");
  const [templates, setTemplates] = useState<ProviderTemplate[]>([]);
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

  const direct = useMemo(() => templates.filter((x) => x.category === "direct"), [templates]);
  const aggregators = useMemo(() => templates.filter((x) => x.category === "aggregator"), [templates]);

  return (
    <div className="onboarding">
      <div className="onboarding__card onboarding__card--wide">
        <img src={logo} className="onboarding__logo" alt="FairPeer" draggable={false} />
        <div className="onboarding__title">{t("onboarding.title")}</div>

        {loadErr && <div className="onboarding__error" role="alert">{loadErr}</div>}

        {step === "vendor" && (
          <VendorStep
            direct={direct}
            aggregators={aggregators}
            onPick={(tpl) => { setSelected(tpl); setStep("key"); }}
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
function VendorStep({ direct, aggregators, onPick, t }: {
  direct: ProviderTemplate[];
  aggregators: ProviderTemplate[];
  onPick: (tpl: ProviderTemplate) => void;
  t: Translator;
}) {
  return (
    <div className="onboarding__step">
      <div className="onboarding__tag">{t("onboarding.step1Hint")}</div>

      <div className="onboarding__vendor-group">
        <div className="onboarding__vendor-group-title">{t("onboarding.categoryDirect")}</div>
        <div className="onboarding__vendor-grid">
          {direct.map((tpl) => (
            <button key={tpl.name} type="button" className="onboarding__vendor-card" onClick={() => onPick(tpl)}>
              <div className="onboarding__vendor-name">{tpl.displayName}</div>
              {tpl.vision && <span className="onboarding__vendor-badge">{t("onboarding.badgeVision")}</span>}
            </button>
          ))}
        </div>
      </div>

      {aggregators.length > 0 && (
        <div className="onboarding__vendor-group">
          <div className="onboarding__vendor-group-title">{t("onboarding.categoryAggregator")}</div>
          <div className="onboarding__vendor-grid">
            {aggregators.map((tpl) => (
              <button key={tpl.name} type="button" className="onboarding__vendor-card onboarding__vendor-card--agg" onClick={() => onPick(tpl)}>
                <div className="onboarding__vendor-name">{tpl.displayName}</div>
                {tpl.codingOnly && <span className="onboarding__vendor-badge">{t("onboarding.badgeCoding")}</span>}
              </button>
            ))}
          </div>
          <div className="onboarding__vendor-note">{t("onboarding.aggregatorNote")}</div>
        </div>
      )}
    </div>
  );
}

// ── Step 2: paste API key ─────────────────────────────────────────────────
function KeyStep({ template, apiKey, onApiKeyChange, onBack, onConnected, t }: {
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
function ModelStep({ template, apiKey, onDone, t }: {
  template: ProviderTemplate;
  apiKey: string;
  onDone: () => void;
  t: Translator;
}) {
  const [pick, setPick] = useState<string>(template.defaultModel);
  const [state, setState] = useState<"ready" | "saving" | "error">("ready");
  const [error, setError] = useState<string | null>(null);

  const models = template.models.length > 0 ? template.models : [template.defaultModel];

  const finish = useCallback(async () => {
    setState("saving");
    setError(null);
    try {
      await app.SetupProvider(template, apiKey, pick);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setState("error");
    }
  }, [template, apiKey, pick, onDone]);

  return (
    <div className="onboarding__step">
      <div className="onboarding__tag">{t("onboarding.step3Hint")}</div>
      <div className="onboarding__model-list">
        {models.map((m) => (
          <label key={m} className={`onboarding__model-item ${pick === m ? "onboarding__model-item--selected" : ""}`}>
            <input type="radio" name="default-model" value={m} checked={pick === m} onChange={() => setPick(m)} />
            <span className="onboarding__model-name">{m}</span>
            {m === template.defaultModel && <span className="onboarding__model-role">{t("onboarding.roleDefault")}</span>}
            {m === template.visionModel && m !== template.defaultModel && <span className="onboarding__model-role">{t("onboarding.roleVision")}</span>}
            {m === template.fastModel && m !== template.defaultModel && <span className="onboarding__model-role">{t("onboarding.roleFast")}</span>}
          </label>
        ))}
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
