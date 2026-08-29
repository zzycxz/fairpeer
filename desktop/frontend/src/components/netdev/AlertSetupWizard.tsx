import { useState } from "react";
import { app } from "../../lib/bridge";
import type { NetDevSettingsView } from "../../lib/types";

// AlertSetupWizard — 告警接入的场景向导（手册页卡「场景引导」的旗舰场景）：
// 五步把「设备出事 → 群里收到消息」整条链路接通并当场验证。
//   ① 开轮询（健康数据是告警的地基）② 挑告警规则（预设三条）③ 配一个通知出口
//   ④ 发送测试（当场收到才算通）⑤ 完成（跳健康页卡看效果）。
// 向导直接写运维设置（app.SetNetDevSettings），保存后调用方 reload。

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
  { key: "unreachable", name: "设备不可达", metric: "reachable", op: "==", value: 0, severity: "critical" },
  { key: "ifdown", name: "接口掉线", metric: "if_down_count", op: ">=", value: 1, severity: "warning" },
  { key: "flap", name: "链路抖动（动态）", metric: "flap_count", op: ">=", value: 3, severity: "warning" },
];

export function AlertSetupWizard({ settings, onClose, onSaved, onOpenSettings, onFinish }: Props) {
  const [step, setStep] = useState(0);
  const [poll, setPoll] = useState<number>(settings.pollIntervalSeconds || 60);
  const [picked, setPicked] = useState<string[]>(["unreachable", "ifdown"]);
  const [outlet, setOutlet] = useState<"webhook" | "bot" | "">(
    (settings.notifyWebhook ? "webhook" : settings.notifyBotDest ? "bot" : "webhook"));
  const [webhook, setWebhook] = useState(settings.notifyWebhook ?? "");
  const [botDest, setBotDest] = useState(settings.notifyBotDest ?? "");
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

  const steps = ["开启轮询", "挑告警规则", "通知出口", "验证", "完成"];

  return (
    <Modal title={`告警接入向导（${step + 1}/5 · ${steps[step]}）`} onClose={onClose}>
      {step === 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint">告警的地基是 SNMP 健康轮询（可达性/接口状态）。设备编辑里配过 SNMP 团体字的设备才会被轮询——目前有 {snmpDevices.length} 台。</div>
          <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
            轮询间隔（秒）
            <input className="mem-input" type="number" style={{ width: 80 }} value={poll}
              onChange={e => setPoll(Math.max(10, Number(e.target.value) || 60))} />
          </label>
          {snmpDevices.length === 0 && (
            <div className="mem-hint" style={{ color: "var(--warn)" }}>
              还没有设备配 SNMP——先去 设置 → 运维 的设备编辑里给至少一台设备填「SNMP（健康轮询）v2c + 团体字」。
              <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: 8 }} onClick={() => onOpenSettings("netdev")}>去设置</span>
            </div>
          )}
        </div>
      )}
      {step === 1 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <div className="mem-hint">命中即生成「发现」并推送；条件清除自动标记已恢复。预设规则（可多选）：</div>
          {RULE_PRESETS.map(r => (
            <label key={r.key} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12 }}>
              <input type="checkbox" checked={picked.includes(r.key)}
                onChange={e => setPicked(p => e.target.checked ? [...p, r.key] : p.filter(x => x !== r.key))} />
              <span style={{ fontWeight: 600 }}>{r.name}</span>
              <span style={{ opacity: 0.7 }}>{r.metric} {r.op} {r.value} · {r.severity}</span>
            </label>
          ))}
        </div>
      )}
      {step === 2 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint">出事时消息发到哪？三选一即可（之后可在 设置 → 通知出口 加更多）：</div>
          <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12 }}>
            <input type="radio" checked={outlet === "webhook"} onChange={() => setOutlet("webhook")} />
            群机器人 Webhook（飞书/钉钉/企微建自定义机器人，复制地址）
          </label>
          {outlet === "webhook" && (
            <input className="mem-input" style={{ width: "100%" }} value={webhook} onChange={e => setWebhook(e.target.value)}
              placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/…" />
          )}
          <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12 }}>
            <input type="radio" checked={outlet === "bot"} onChange={() => setOutlet("bot")} />
            内嵌 IM 网关直推（需已在 设置 → Bot 登录）
          </label>
          {outlet === "bot" && (
            <input className="mem-input" style={{ width: "100%" }} value={botDest} onChange={e => setBotDest(e.target.value)}
              placeholder="feishu:oc_xxx / weixin:wxid_xxx" />
          )}
        </div>
      )}
      {step === 3 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint">保存配置并发送一条测试消息——群里/邮箱当场收到才算接通。</div>
          {err && <div className="banner banner--error">{err}</div>}
          <span className="btn btn--primary btn--small" role="button" onClick={() => void saveAndTest()}>{busy ? "发送中…" : "保存并发送测试"}</span>
        </div>
      )}
      {step === 4 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="mem-hint" style={{ color: testOk ? "var(--ok)" : "var(--warn)" }}>
            {testOk ? "✓ 测试消息已发出——请确认收到。「健康」页卡现在开始积累数据，动态阈值（抖动/偏离基线）会随历史自动生效。"
                    : "配置已保存。测试消息未确认发出——检查通知出口后可用 设置 → 通知出口 → 发送测试 重试。"}
          </div>
          <div className="mem-hint">后续可调：设置 → 运维（告警规则/轮询间隔/通知出口/每日早报推送时间）。</div>
        </div>
      )}
      <div style={{ marginTop: 12, display: "flex", gap: 8, justifyContent: "flex-end" }}>
        {step > 0 && step < 4 && <span className="btn btn--secondary btn--small" role="button" onClick={() => setStep(s => s - 1)}>上一步</span>}
        {step < 3 && <span className="btn btn--primary btn--small" role="button" onClick={() => setStep(s => s + 1)}>下一步</span>}
        {step === 4 && <span className="btn btn--primary btn--small" role="button" onClick={() => { onFinish(); onClose(); }}>打开健康页卡</span>}
        {step !== 4 && <span className="btn btn--secondary btn--small" role="button" onClick={onClose}>{step === 3 ? "取消" : "稍后再说"}</span>}
      </div>
    </Modal>
  );
}
