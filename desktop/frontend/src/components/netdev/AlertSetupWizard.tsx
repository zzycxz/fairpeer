import { useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import type { NetDevSettingsView } from "../../lib/types";

// AlertSetupWizard — 告警接入的场景向导（手册页卡「场景引导」的旗舰场景）：
// 五步把「设备出事 → 群里收到消息」整条链路接通并当场验证。
//   ① 开轮询（健康数据是告警的地基）② 挑告警规则（预设三条）③ 配一个通知出口
//   ④ 发送测试（当场收到才算通）⑤ 完成（跳健康页卡看效果）。
// 向导直接写入运维设置（app.SetNetDevSettings），保存后调用方 reload。

type Props = {
  settings: NetDevSettingsView;
  onClose: () => void;
  onSaved: () => void;
  onOpenSettings: (tab: string) => void;
  onFinish: () => void; // 完成时的动作（如打开健康页卡）
};

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div
      role="dialog" aria-modal="true"
      style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.45)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50 }}
      onClick={e => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div style={{ background: "var(--bg-elevated, #23272e)", borderRadius: 8, padding: 16, minWidth: 520, maxWidth: 680, maxHeight: "80vh", overflowY: "auto" }}>
        <div className="set-label" style={{ marginBottom: 10 }}>{title}</div>
        {children}
      </div>
    </div>
  );
}

const RULE_PRESETS = [
  { key: "unreachable", name: "ndv.wiz.pUnreachable", metric: "reachable", op: "==", value: 0, severity: "critical" },
  { key: "ifdown", name: "ndv.wiz.pIfdown", metric: "if_down_count", op: ">=", value: 1, severity: "warning" },
  { key: "flap", name: "ndv.wiz.pFlap", metric: "flap_count", op: ">=", value: 3, severity: "warning" },
  { key: "reboot", name: "ndv.wiz.pReboot", metric: "uptime_reset", op: "==", value: 1, severity: "warning" },
  { key: "drift", name: "ndv.wiz.pDrift", metric: "if_down_above_p90", op: "==", value: 1, severity: "info" },
];

export function AlertSetupWizard({ settings, onClose, onSaved, onOpenSettings, onFinish }: Props) {
  const t = useT();
  const [step, setStep] = useState(0);
  const [poll, setPoll] = useState<number>(settings.pollIntervalSeconds || 60);
  const [picked, setPicked] = useState<string[]>(["unreachable", "ifdown"]);
  const [outlet, setOutlet] = useState<"webhook" | "bot" | "smtp">(
    (settings.notifyWebhook ? "webhook" : settings.notifyBotDest ? "bot" : settings.notifySMTPHost ? "smtp" : "webhook"));
  const [webhook, setWebhook] = useState(settings.notifyWebhook ?? "");
  const [botDest, setBotDest] = useState(settings.notifyBotDest ?? "");
  // SMTP 出口（completion-spec §4.7）：向导收录第三选项——只填投递必需项，
  // 完整参数（端口/用户/密码）仍在 设置 → 通知出口。
  const [smtpHost, setSmtpHost] = useState(settings.notifySMTPHost ?? "");
  const [smtpFrom, setSmtpFrom] = useState(settings.notifySMTPFrom ?? "");
  const [smtpTo, setSmtpTo] = useState((settings.notifySMTPTo ?? []).join(", "));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [testOk, setTestOk] = useState(false);

  const snmpDevices = (settings.devices ?? []).filter(d => d.snmpVersion || d.snmpCommunitySet);

  const saveAndTest = async () => {
    setBusy(true); setErr("");
    try {
      const rules = RULE_PRESETS.filter(r => picked.includes(r.key)).map(r => ({
        name: r.name, metric: r.metric, op: r.op, value: r.value, severity: r.severity, enabled: true,
      }));
      const merged = [...(settings.alertRules ?? []).filter(r => !rules.some(n => n.name === r.name)), ...rules];
      await app.SetNetDevSettings({
        ...settings,
        pollIntervalSeconds: poll > 0 ? poll : 60,
        alertRules: merged,
        notifyWebhook: outlet === "webhook" ? webhook.trim() : settings.notifyWebhook,
        notifyBotDest: outlet === "bot" ? botDest.trim() : settings.notifyBotDest,
        ...(outlet === "smtp" ? {
          notifySMTPHost: smtpHost.trim(),
          notifySMTPFrom: smtpFrom.trim(),
          notifySMTPTo: smtpTo.split(/[,，]/).map(x => x.trim()).filter(Boolean),
        } : {}),
      });
      onSaved();
      await app.NetDevNotifyTest();
      setTestOk(true);
      setStep(4);
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  };

  // C3.7 步骤校验：空出口/零规则不能下一步——过去能一路空走到发测试。
  const outletValid =
    outlet === "webhook" ? webhook.trim().length > 0 :
    outlet === "bot" ? botDest.trim().length > 0 :
    smtpHost.trim().length > 0 && smtpFrom.trim().length > 0 && smtpTo.trim().length > 0;
  const stepValid = step === 1 ? picked.length > 0 : step === 2 ? outletValid : true;

  const steps = [t("ndv.wiz.s1"), t("ndv.wiz.s2"), t("ndv.wiz.s3"), t("ndv.wiz.s4"), t("ndv.wiz.s5")];

  return (
    <Modal title={t("ndv.wiz.title", { n: step + 1, s: steps[step] })} onClose={onClose}>
      {step === 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint">{t("ndv.wiz.step0Hint", { n: snmpDevices.length })}</div>
          <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
            {t("ndv.wiz.pollInterval")}
            <input className="mem-input" type="number" style={{ width: 80 }} value={poll}
              onChange={e => setPoll(Math.max(10, Number(e.target.value) || 60))} />
          </label>
          {snmpDevices.length === 0 && (
            <div className="mem-hint" style={{ color: "var(--warn)" }}>
              {t("ndv.wiz.noSnmp")}
              <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: 8 }} onClick={() => onOpenSettings("netdev")}>{t("ndv.goSettings")}</span>
            </div>
          )}
        </div>
      )}
      {step === 1 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <div className="mem-hint">{t("ndv.wiz.step1Hint")}</div>
          {RULE_PRESETS.map(r => (
            <label key={r.key} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12 }}>
              <input type="checkbox" checked={picked.includes(r.key)}
                onChange={e => setPicked(p => e.target.checked ? [...p, r.key] : p.filter(x => x !== r.key))} />
              <span style={{ fontWeight: 600 }}>{t(r.name as never)}</span>
              <span style={{ opacity: 0.7 }}>{r.metric} {r.op} {r.value} · {r.severity}</span>
            </label>
          ))}
        </div>
      )}
      {step === 2 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint">{t("ndv.wiz.step2Hint")}</div>
          <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12 }}>
            <input type="radio" checked={outlet === "webhook"} onChange={() => setOutlet("webhook")} />
            {t("ndv.wiz.webhookOpt")}
          </label>
          {outlet === "webhook" && (
            <input className="mem-input" style={{ width: "100%" }} value={webhook} onChange={e => setWebhook(e.target.value)}
              placeholder={t("ndv.wiz.phWebhook")} />
          )}
          <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12 }}>
            <input type="radio" checked={outlet === "bot"} onChange={() => setOutlet("bot")} />
            {t("ndv.wiz.botOpt")}
          </label>
          {outlet === "bot" && (
            <input className="mem-input" style={{ width: "100%" }} value={botDest} onChange={e => setBotDest(e.target.value)}
              placeholder={t("ndv.wiz.phBot")} />
          )}
          <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12 }}>
            <input type="radio" checked={outlet === "smtp"} onChange={() => setOutlet("smtp")} />
            {t("ndv.wiz.smtpOpt")}
          </label>
          {outlet === "smtp" && (
            <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              <input className="mem-input" style={{ width: "100%" }} value={smtpHost} onChange={e => setSmtpHost(e.target.value)}
                placeholder={t("ndv.wiz.phSmtpHost")} />
              <div style={{ display: "flex", gap: 4 }}>
                <input className="mem-input" style={{ flex: 1 }} value={smtpFrom} onChange={e => setSmtpFrom(e.target.value)} placeholder={t("ndv.wiz.phSmtpFrom")} />
                <input className="mem-input" style={{ flex: 2 }} value={smtpTo} onChange={e => setSmtpTo(e.target.value)} placeholder={t("ndv.wiz.phSmtpTo")} />
              </div>
            </div>
          )}
        </div>
      )}
      {step === 3 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint">{t("ndv.wiz.step3Hint")}</div>
          {err && <div className="banner banner--error">{err}</div>}
          <span className="btn btn--primary btn--small" role="button" onClick={() => void saveAndTest()}>{busy ? t("ndv.wiz.sending") : t("ndv.wiz.saveTest")}</span>
        </div>
      )}
      {step === 4 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint" style={{ color: testOk ? "var(--ok)" : "var(--warn)" }}>
            {testOk ? t("ndv.wiz.okMsg") : t("ndv.wiz.testFail")}
          </div>
          <div className="mem-hint">{t("ndv.wiz.laterTune")}</div>
        </div>
      )}
      {step === 1 && picked.length === 0 && <div className="mem-hint" style={{ color: "var(--warn)" }}>{t("ndv.wiz.needRule")}</div>}
      {step === 2 && !outletValid && <div className="mem-hint" style={{ color: "var(--warn)" }}>{t("ndv.wiz.needOutlet", { what: outlet === "webhook" ? t("ndv.wiz.needWebhook") : outlet === "bot" ? t("ndv.wiz.needBot") : t("ndv.wiz.needSmtp") })}</div>}
      <div style={{ marginTop: 12, display: "flex", gap: 8, justifyContent: "flex-end" }}>
        {step > 0 && step < 4 && <span className="btn btn--secondary btn--small" role="button" onClick={() => setStep(s => s - 1)}>{t("ndv.wiz.prev")}</span>}
        {step < 3 && <span className="btn btn--primary btn--small" role="button" style={stepValid ? undefined : { opacity: 0.5 }} onClick={() => { if (stepValid) setStep(s => s + 1); }}>{t("ndv.wiz.next")}</span>}
        {step === 4 && <span className="btn btn--primary btn--small" role="button" onClick={() => { onFinish(); onClose(); }}>{t("ndv.wiz.openHealth")}</span>}
        {step !== 4 && <span className="btn btn--secondary btn--small" role="button" onClick={onClose}>{step === 3 ? t("common.cancel") : t("ndv.wiz.later")}</span>}
      </div>
    </Modal>
  );
}
