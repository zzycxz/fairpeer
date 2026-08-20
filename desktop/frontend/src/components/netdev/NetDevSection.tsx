import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { ProposalCenter } from "./ProposalCenter";
import { FindingCenter } from "./FindingCenter";
import { useConfirm } from "../../lib/confirm";
import type { NetDevSettingsView, NetDevAuditEntryView, NetDevSSHImportCandidate } from "../../lib/types";

// NetDevSection is the 运维 settings tab: device/hop inventory (persisted to
// the USER config — the [netdev] section is globally pinned), credentials
// (secret store, never in TOML), scan scopes, and the audit tail. The agent
// itself has no tool to edit any of this — inventory changes are human-only.

const VENDORS = ["huawei", "cisco"];
const OSES: Record<string, string[]> = {
  huawei: ["vrp8", "vrp5"],
  cisco: ["ios", "iosxe"],
};

type EditDevice = NetDevSettingsView["devices"][number];
type EditHop = NetDevSettingsView["hops"][number];

const emptyDevice = (): EditDevice => ({
  name: "", vendor: "huawei", os: "vrp8", model: "", address: "", port: 22,
  via: [], group: "", username: "", passwordEnv: "", passwordSet: false,
  identityFile: "", encoding: "auto", allowTelnet: false, password: "",
});

const emptyHop = (): EditHop => ({
  name: "", host: "", port: 22, user: "", passwordEnv: "", passwordSet: false,
  proxyJump: "", password: "",
});

export function NetDevSection() {
  const confirm = useConfirm();
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [view, setView] = useState<NetDevSettingsView>({ enabled: false, networkName: "", devices: [], hops: [], groups: [], auditRetention: "", scopes: [], guardConfirmEach: false, guardTurnBudget: 0, guardAllowedGroups: [], extraRead: {}, projects: [], presets: [] });
  const [audit, setAudit] = useState<NetDevAuditEntryView[]>([]);
  const [editingDevice, setEditingDevice] = useState<EditDevice | null>(null);
  const [editingHop, setEditingHop] = useState<EditHop | null>(null);
  const [sshCandidates, setSSHCandidates] = useState<NetDevSSHImportCandidate[]>([]);

  const reload = useCallback(async () => {
    try {
      const [v, a] = await Promise.all([app.NetDevSettings(), app.NetDevAuditTail(50)]);
      setView({
        ...v,
        devices: v.devices ?? [],
        hops: v.hops ?? [],
        groups: v.groups ?? [],
        scopes: v.scopes ?? [],
        projects: v.projects ?? [],
        presets: v.presets ?? [],
      });
      setAudit(a ?? []);
      setErr("");
    } catch (e) {
      setErr(String(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  const save = useCallback(async (v: NetDevSettingsView) => {
    setBusy(true);
    try {
      await app.SetNetDevSettings(v);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }, [reload]);

  const patch = (p: Partial<NetDevSettingsView>) => setView(v => ({ ...v, ...p }));

  // First-device flow: connect → TOFU confirm (fingerprint shown verbatim) →
  // trust → retest. Any other failure is reported as text.
  const [testing, setTesting] = useState(false);
  const testConnection = useCallback(async (device: string) => {
    setTesting(true);
    try {
      let r = await app.NetDevTestConnection(device);
      if (r.status === "unknown-host-key") {
        const ok = await confirm({
          title: "UNTRUSTED HOST KEY",
          message: `首次连接 ${device}（${r.host}）\n主机密钥指纹：\n  ${r.keyType}  ${r.fingerprint}\n\n确认信任此密钥？（确认后写入本机 known_hosts）`,
          danger: true
        });
        if (!ok) { setErr("[SYS] KEY REJECTED"); return; }
        if (!r.fingerprint) { setErr("[SYS] INTERNAL ERROR: NO FINGERPRINT"); return; }
        await app.NetDevTrustHostKey(r.fingerprint);
        r = await app.NetDevTestConnection(device);
      }
      setErr(r.status === "ok" ? "[SYS] TARGET VERIFIED (VTY SESSION OPEN)" : `测试失败（${r.status}）：${r.detail ?? ""}`);
    } catch (e) {
      setErr(String(e));
    } finally {
      setTesting(false);
    }
  }, []);

  if (!loaded) return <div className="mem-hint">…</div>;

  return (
    <div>
      {err && <div className="banner banner--error" style={{ marginBottom: 8 }}>{err}</div>}

      <div className="optional-module__controls" style={{ marginBottom: 12 }}>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <input type="checkbox" checked={view.enabled} onChange={e => patch({ enabled: e.target.checked })} />
          启用运维（netdev）能力
        </label>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center", marginLeft: 16 }}>
          网络名称
          <input className="mem-input" style={{ width: 180 }} value={view.networkName ?? ""} placeholder="如：总部生产网" onChange={e => patch({ networkName: e.target.value })} />
        </label>
        <span className="btn btn--primary btn--small" role="button" onClick={() => void save(view)}>{busy ? "保存中…" : "保存"}</span>
      </div>

      <div className="mem-hint" style={{ marginBottom: 4 }}>
        设备清单存于用户全局配置（项目级 fairpeer.toml 注入无效）；密码仅写入加密密钥库，绝不进 TOML。
        诊断手结构性只读：写/危险命令一律拒执行并落审计。
      </div>

      {/* 设备清单 */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>设备（{view.devices.length}）</div>
      <div style={{ display: "flex", gap: 8, marginBottom: 6 }}>
        <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice(emptyDevice())}>+ 添加设备</span>
        <span
          className="btn btn--secondary btn--small" role="button"
          onClick={async () => {
            try {
              const c = await app.NetDevSSHImportCandidates();
              setSSHCandidates(c ?? []);
            } catch (e) { setErr(String(e)); }
          }}
        >从 ~/.ssh/config 导入</span>
      </div>
      {sshCandidates.length > 0 && (
        <div className="mem-hint" style={{ marginBottom: 6 }}>
          {sshCandidates.slice(0, 12).map(c => (
            <span key={c.alias}
              className="btn btn--secondary btn--small" role="button" style={{ marginRight: 6 }}
              onClick={() => setEditingDevice({
                ...emptyDevice(),
                name: c.alias, address: c.host || c.alias, username: c.user || "",
                port: c.port || 22,
              })}
            >{c.alias}</span>
          ))}
        </div>
      )}
      {view.devices.length > 0 && (
        <table className="mem-hint" style={{ width: "100%", borderCollapse: "collapse", marginBottom: 4 }}>
          <thead>
            <tr style={{ textAlign: "left" }}><th>名称</th><th>厂商/OS</th><th>地址</th><th>路由</th><th>凭证</th><th /></tr>
          </thead>
          <tbody>
            {view.devices.map(d => (
              <tr key={d.name}>
                <td>{d.name}</td>
                <td>{d.vendor}/{d.os}</td>
                <td>{d.address}{d.port && d.port !== 22 ? `:${d.port}` : ""}</td>
                <td>{(d.via ?? []).join("→") || "直连"}</td>
                <td>{d.passwordSet ? "✓ 已设" : "✗ 未设"}</td>
                <td>
                  <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice({ ...d, password: "" })}>编辑</span>{" "}
                  <span className="btn btn--secondary btn--small" role="button" title="删除"
                    onClick={async () => { if (await confirm({ title: "DELETE DEVICE", message: `删除设备 ${d.name}？（不影响已存凭证）`, danger: true })) void save({ ...view, devices: view.devices.filter(x => x.name !== d.name) }); }}>×</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {/* 跳板 */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>跳板/堡垒机（{view.hops.length}）</div>
      <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop(emptyHop())}>+ 添加跳板</span>
      {view.hops.map(h => (
        <div key={h.name} className="mem-hint" style={{ display: "flex", gap: 8, marginTop: 4 }}>
          <span style={{ minWidth: 120 }}>{h.name} → {h.host}{h.proxyJump ? `（经 ${h.proxyJump}）` : ""}</span>
          <span>{h.passwordSet ? "✓ 凭证已设" : "✗ 未设"}</span>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop({ ...h, password: "" })}>编辑</span>
          <span className="btn btn--secondary btn--small" role="button" title="删除"
            onClick={async () => { if (await confirm({ title: "DELETE HOP", message: `删除跳板 ${h.name}？`, danger: true })) void save({ ...view, hops: view.hops.filter(x => x.name !== h.name) }); }}>×</span>
        </div>
      ))}

      {/* 探测范围 */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>探测范围白名单（CIDR，逗号分隔）</div>
      <input
        className="mem-input" style={{ width: "100%" }}
        value={(view.scopes ?? []).join(", ")}
        placeholder="例：10.30.0.0/16, 10.31.0.0/16"
        onChange={e => patch({ scopes: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
      />

      {/* 护栏 —— 控制到每次询问/每次工具调用 */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>护栏（控制到每一次询问与每一条工具命令）</div>
      <div style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <input
            type="checkbox"
            checked={!!view.guardConfirmEach}
            onChange={e => patch({ guardConfirmEach: e.target.checked })}
          />
          每条命令确认：netdev_exec / netdev_netconf 执行前弹审批卡（优先级压过全自动模式，"记住允许"也无效）
        </label>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          每轮命令预算
          <input
            className="mem-input" style={{ width: 70 }} type="number" min={0}
            value={view.guardTurnBudget ?? 0}
            onChange={e => patch({ guardTurnBudget: Math.max(0, Number(e.target.value) || 0) })}
          />
          条（0 = 不限；每次你发送消息预算重置，超出后 agent 收到提醒并停下汇总）
        </label>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          设备组作用域
          <input
            className="mem-input" style={{ width: "50%" }}
            value={(view.guardAllowedGroups ?? []).join(", ")}
            placeholder="留空 = 全部组；例：核心, 汇聚"
            onChange={e => patch({ guardAllowedGroups: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
          />
        </label>
        <div style={{ opacity: 0.6, fontSize: 11.5 }}>
          作用域非空时，范围外的设备对 AI 完全不可见（netdev_devices 列表也过滤）——在第一个 token 花出去之前就完成控制。脱敏提醒与拒绝提醒默认常开，无需配置。
        </div>
      </div>

      {/* 读表扩展 —— 用户教知识，模型不能自我声明（B-1） */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>读表扩展（用户教会 AI 识别更多只读命令）</div>
      {["huawei", "cisco", "zte"].map(vendor => {
        const list = view.extraRead?.[vendor] ?? [];
        return (
          <div key={vendor} style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
            <span style={{ minWidth: 60, fontWeight: 600 }}>{vendor}</span>
            {list.map(cmd => (
              <span key={cmd} className="btn btn--secondary btn--small" role="button" title="点击移除"
                onClick={() => patch({ extraRead: { ...view.extraRead, [vendor]: list.filter(c => c !== cmd) } })}>
                {cmd} ×
              </span>
            ))}
            <span className="btn btn--secondary btn--small" role="button"
              onClick={() => {
                const cmd = prompt(`${vendor} 要加入读表的只读命令（单行）：`)?.trim();
                if (!cmd) return;
                patch({ extraRead: { ...view.extraRead, [vendor]: [...list, cmd] } });
              }}>+ 添加</span>
          </div>
        );
      })}
      <div style={{ opacity: 0.6, fontSize: 11.5 }}>
        对话中被拒绝的未知命令也会在设备卡上出现「允许此命令」一键加入。扩展只让更多命令「可读」，永远不可能放开写操作。
      </div>

      {/* 项目 —— 站点级作用域（标题栏切换器） */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>项目（站点/机房——标题栏可快速切换范围）</div>
      {(view.projects ?? []).map((p, i) => (
        <div key={p.name + i} style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
          <input
            className="mem-input" style={{ width: 120 }}
            value={p.name}
            onChange={e => { const projects = [...(view.projects ?? [])]; projects[i] = { ...p, name: e.target.value }; patch({ projects }); }}
          />
          <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>
            {(view.groups ?? []).map(g => (
              <span
                key={g}
                className="btn btn--secondary btn--small"
                role="button"
                style={(p.groups ?? []).includes(g) ? { borderColor: "var(--accent, #7ab8ff)", color: "var(--accent, #7ab8ff)" } : { opacity: 0.55 }}
                onClick={() => {
                  const projects = [...(view.projects ?? [])];
                  const gs = new Set(p.groups ?? []);
                  if (gs.has(g)) gs.delete(g); else gs.add(g);
                  projects[i] = { ...p, groups: [...gs] };
                  patch({ projects });
                }}
              >{g}</span>
            ))}
            {(view.groups ?? []).length === 0 && <span style={{ opacity: 0.55, fontSize: 11.5 }}>先在设备编辑里建立分组</span>}
          </span>
          <input
            className="mem-input" style={{ width: 200 }} placeholder="备注（悬停可见）"
            value={p.note ?? ""}
            onChange={e => { const projects = [...(view.projects ?? [])]; projects[i] = { ...p, note: e.target.value }; patch({ projects }); }}
          />
          <span className="btn btn--secondary btn--small" role="button"
            onClick={() => patch({ projects: (view.projects ?? []).filter((_, j) => j !== i) })}>删除</span>
        </div>
      ))}
      <div>
        <span className="btn btn--secondary btn--small" role="button"
          onClick={() => patch({ projects: [...(view.projects ?? []), { name: "新项目", groups: [], note: "" }] })}>+ 新建项目</span>
      </div>

      {/* 诊断命令组合 */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>诊断命令组合（设备卡一键逐条执行，走密封路径）</div>
      {(view.presets ?? []).map((p, i) => (
        <div key={p.name + i} style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
          <input
            className="mem-input" style={{ width: 130 }}
            value={p.name}
            onChange={e => { const presets = [...(view.presets ?? [])]; presets[i] = { ...p, name: e.target.value }; patch({ presets }); }}
          />
          <input
            className="mem-input" style={{ flex: 1, minWidth: 240 }}
            value={(p.commands ?? []).join("; ")}
            placeholder="命令用分号分隔，如 display ospf peer; display ospf lsdb"
            onChange={e => { const presets = [...(view.presets ?? [])]; presets[i] = { ...p, commands: e.target.value.split(/[;；]/).map(c => c.trim()).filter(Boolean) }; patch({ presets }); }}
          />
          <span className="btn btn--secondary btn--small" role="button"
            onClick={() => patch({ presets: (view.presets ?? []).filter((_, j) => j !== i) })}>删除</span>
        </div>
      ))}
      <div>
        <span className="btn btn--secondary btn--small" role="button"
          onClick={() => patch({ presets: [...(view.presets ?? []), { name: "新组合", commands: [], vendors: [] }] })}>+ 新建组合</span>
      </div>

      {/* 巡检 */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>巡检</div>
      <span
        className="btn btn--secondary btn--small" role="button"
        onClick={async () => {
          try {
            setErr("巡检中…");
            const f = await app.NetDevRunInspection();
            setErr(f ? `[SYS] INSPECTION COMPLETE: ${f.title}` : "[SYS] INSPECTION COMPLETE");
            await reload();
          } catch (e) { setErr(String(e)); }
        }}
      >立即巡检（全部设备，只读电池，结果存 Finding）</span>

      <FindingCenter />

      <ProposalCenter />

      {/* 审计 */}
      <div className="set-label" style={{ margin: "14px 0 6px" }}>最近审计（{audit.length}）</div>
      <div className="mem-hint" style={{ maxHeight: 200, overflowY: "auto" }}>
        {audit.length === 0 && <div>暂无记录。诊断命令执行后此处可见（命令/分类/结果）。</div>}
        {audit.slice().reverse().map((e, i) => (
          <div key={i} style={{ display: "flex", gap: 8 }}>
            <span style={{ minWidth: 96, opacity: 0.7 }}>{e.time}</span>
            <span style={{ minWidth: 90 }}>{e.device}</span>
            <span style={{ minWidth: 64 }} className={e.class === "read" ? "" : "banner--error"}>{e.class}</span>
            <span style={{ minWidth: 80, opacity: 0.7 }}>{e.status}</span>
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>{e.command}</span>
          </div>
        ))}
      </div>

      {/* 设备编辑表单 */}
      {editingDevice && (
        <Modal title={view.devices.some(d => d.name === editingDevice.name) ? "编辑设备" : "添加设备"} onClose={() => setEditingDevice(null)}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label="名称 *"><input className="mem-input" value={editingDevice.name} onChange={e => setEditingDevice({ ...editingDevice, name: e.target.value })} /></Field>
            <Field label="厂商">
              <select className="mem-select" value={editingDevice.vendor} onChange={e => setEditingDevice({ ...editingDevice, vendor: e.target.value, os: (OSES[e.target.value] ?? [""])[0] })}>
                {VENDORS.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            </Field>
            <Field label="OS">
              <select className="mem-select" value={editingDevice.os} onChange={e => setEditingDevice({ ...editingDevice, os: e.target.value })}>
                {(OSES[editingDevice.vendor] ?? []).map(o => <option key={o} value={o}>{o}</option>)}
              </select>
            </Field>
            <Field label="型号"><input className="mem-input" value={editingDevice.model} onChange={e => setEditingDevice({ ...editingDevice, model: e.target.value })} /></Field>
            <Field label="地址 *"><input className="mem-input" value={editingDevice.address} onChange={e => setEditingDevice({ ...editingDevice, address: e.target.value })} /></Field>
            <Field label="端口"><input className="mem-input" type="number" value={editingDevice.port} onChange={e => setEditingDevice({ ...editingDevice, port: Number(e.target.value) || 22 })} /></Field>
            <Field label="登录用户"><input className="mem-input" value={editingDevice.username} onChange={e => setEditingDevice({ ...editingDevice, username: e.target.value })} /></Field>
            <Field label={editingDevice.passwordSet ? "密码（留空=保持不变）" : "密码"}>
              <input className="mem-input" type="password" value={editingDevice.password} onChange={e => setEditingDevice({ ...editingDevice, password: e.target.value })} />
            </Field>
            <Field label="路由 via（跳板名，逗号分隔）">
              <input className="mem-input" value={(editingDevice.via ?? []).join(",")}
                onChange={e => setEditingDevice({ ...editingDevice, via: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })} />
            </Field>
            <Field label="编码">
              <select className="mem-select" value={editingDevice.encoding || "auto"} onChange={e => setEditingDevice({ ...editingDevice, encoding: e.target.value })}>
                {["auto", "utf-8", "gbk"].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span
              className="btn btn--secondary btn--small" role="button"
              onClick={() => { if (editingDevice.name.trim()) void testConnection(editingDevice.name); }}
              title="连接 → 主机密钥确认（首次） → CLI 会话验证"
            >{testing ? "测试中…" : "测试连接"}</span>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingDevice.name.trim() || !editingDevice.address.trim()) { setErr("名称和地址必填"); return; }
                const exists = view.devices.some(d => d.name === editingDevice.name);
                const devices = exists ? view.devices.map(d => d.name === editingDevice.name ? editingDevice : d) : [...view.devices, editingDevice];
                setEditingDevice(null);
                void save({ ...view, devices });
              }}
            >保存设备</span>
          </div>
        </Modal>
      )}

      {/* 跳板编辑表单 */}
      {editingHop && (
        <Modal title={view.hops.some(h => h.name === editingHop.name) ? "编辑跳板" : "添加跳板"} onClose={() => setEditingHop(null)}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label="名称 *"><input className="mem-input" value={editingHop.name} onChange={e => setEditingHop({ ...editingHop, name: e.target.value })} /></Field>
            <Field label="地址 *"><input className="mem-input" value={editingHop.host} onChange={e => setEditingHop({ ...editingHop, host: e.target.value })} /></Field>
            <Field label="端口"><input className="mem-input" type="number" value={editingHop.port} onChange={e => setEditingHop({ ...editingHop, port: Number(e.target.value) || 22 })} /></Field>
            <Field label="用户"><input className="mem-input" value={editingHop.user} onChange={e => setEditingHop({ ...editingHop, user: e.target.value })} /></Field>
            <Field label={editingHop.passwordSet ? "密码（留空=保持不变）" : "密码"}>
              <input className="mem-input" type="password" value={editingHop.password} onChange={e => setEditingHop({ ...editingHop, password: e.target.value })} />
            </Field>
            <Field label="上级跳板（可选，名称）"><input className="mem-input" value={editingHop.proxyJump} onChange={e => setEditingHop({ ...editingHop, proxyJump: e.target.value })} /></Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingHop.name.trim() || !editingHop.host.trim()) { setErr("名称和地址必填"); return; }
                const exists = view.hops.some(h => h.name === editingHop.name);
                const hops = exists ? view.hops.map(h => h.name === editingHop.name ? editingHop : h) : [...view.hops, editingHop];
                setEditingHop(null);
                void save({ ...view, hops });
              }}
            >保存跳板</span>
          </div>
        </Modal>
      )}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="set-label" style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <span>{label}</span>
      {children}
    </label>
  );
}

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
