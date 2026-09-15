import { useCallback, useEffect, useMemo, useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { getActiveProject } from "../../lib/netdevProjectStore";
import type { netdev } from "../../../wailsjs/go/models";
import type { NetDevCutoverRun } from "../../lib/types";

type Tpl = netdev.RunbookTemplate;
type ApplyResult = netdev.RunbookApplyResult;

// RunbookTplPanel — 割接 runbook 模板库的产品入口（F1a/F1b/F1c，轮3审查补
// UI 断路）。挂在割接创建视图顶部：模板列表（场景筛选，按活动项目类型联动
// 缺省——G-P2）→ 变量表单 → preview（dry-run 分类标注）→ apply（落 draft
// 提案 + 组装 run 定义）。apply 产出的 run 进「待启动」列表（localStorage
// 会话暂存）：提案批齐后一键 NetDevCutoverStart——模板从不代批。

const LS_KEY = "fairpeer.netdev.pendingRuns";

type PendingRun = {
  key: string;
  name: string;
  createdAt: number;
  proposalIds: string[];
  run: NetDevCutoverRun;
};

function loadPending(): PendingRun[] {
  try {
    return JSON.parse(localStorage.getItem(LS_KEY) ?? "[]") as PendingRun[];
  } catch {
    return [];
  }
}

function savePending(list: PendingRun[]) {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify(list));
  } catch { /* private mode — 待启动列表退化为会话内 */ }
}

// G-P2：项目类型 → 场景缺省筛选（"all"=全部）。
function scenarioForProjectType(type?: string): string {
  switch (type) {
    case "netdev": return "network-cutover";
    case "aicompute": return "model-deploy";
    case "blueteam": return "model-ops";
    default: return "all";
  }
}

export default function RunbookTplPanel({ onChanged, onCreated, devices }: {
  onChanged?: () => void;
  onCreated?: (id: string) => void;
  devices?: { name: string; vendor: string }[];
}) {
  const t = useT();
  const [tpls, setTpls] = useState<Tpl[]>([]);
  const [scenario, setScenario] = useState("all");
  const [scenarioTouched, setScenarioTouched] = useState(false);
  const [sel, setSel] = useState<Tpl | null>(null);
  const [vars, setVars] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<NetDevCutoverRun | null>(null);
  const [notes, setNotes] = useState<string[]>([]);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [pending, setPending] = useState<PendingRun[]>(loadPending);
  const [proposals, setProposals] = useState<Set<string>>(new Set());
  // E4/E7 部署建议校验（轮3审查 P2：binding 无 UI 消费的断路修）。
  const [pcDevice, setPcDevice] = useState("");
  const [pcModel, setPcModel] = useState("");
  const [pcQuant, setPcQuant] = useState("");
  const [pcTp, setPcTp] = useState("8");
  const [pcOut, setPcOut] = useState<string[]>([]);
  const [pcBusy, setPcBusy] = useState(false);

  const reload = useCallback(async () => {
    try {
      setTpls((await app.NetDevRunbookTplList()) ?? []);
    } catch { /* 空库降级为内置骨架（后端 List 已合并内置） */ }
    try {
      const ps = await app.NetDevProposals();
      setProposals(new Set((ps ?? []).filter(p => p.status === "approved").map(p => p.id)));
    } catch { /* 提案状态仅影响待启动启动按钮可用性 */ }
  }, []);
  useEffect(() => { void reload(); }, [reload]);
  useEffect(() => { savePending(pending); }, [pending]);

  // G-P2 联动：项目定义就绪后按活动项目 type 设一次缺省筛选（用户手选优先）。
  useEffect(() => {
    if (scenarioTouched || tpls.length === 0) return;
    (async () => {
      try {
        const s = await app.NetDevSettings();
        const active = getActiveProject();
        const type = (s.projects ?? []).find(p => p.name === active?.name)?.type;
        setScenario(scenarioForProjectType(type));
      } catch { /* 缺省失败=保持 all */ }
    })();
  }, [tpls.length, scenarioTouched]);

  const visible = useMemo(
    () => tpls.filter(x => scenario === "all" || x.scenario === scenario),
    [tpls, scenario],
  );
  const scenarios = useMemo(() => {
    const set = new Set<string>(["all"]);
    tpls.forEach(x => x.scenario && set.add(x.scenario));
    return [...set];
  }, [tpls]);

  const pick = (x: Tpl) => {
    setSel(x);
    setVars(Object.fromEntries((x.vars ?? []).map(v => [v, ""])));
    setPreview(null);
    setNotes([]);
    setErr("");
    setMsg("");
  };

  const doPreview = async () => {
    if (!sel) return;
    setBusy("preview");
    setErr("");
    try {
      const p = await app.NetDevRunbookTplPreview(sel.id, vars);
      setPreview((p.run as unknown as NetDevCutoverRun) ?? null);
      setNotes((p.notes as string[]) ?? []);
      // 逐步分类标注进 notes 展示（preview.steps 的 class/notes）。
      for (const st of (p.steps ?? []) as Array<{ label?: string; notes?: string[] }>) {
        const nn = st.notes;
        if (nn && nn.length) setNotes(n => [...n, `${st.label ?? ""}: ${nn.join("；")}`]);
      }
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  };

  const doApply = async () => {
    if (!sel) return;
    setBusy("apply");
    setErr("");
    try {
      const res: ApplyResult = await app.NetDevRunbookTplApply(sel.id, vars, "");
      const ids = (res.proposals ?? []).map(p => p.id);
      setPending(list => [{
        key: `${sel.id}-${Date.now()}`,
        name: res.run?.name || sel.name,
        createdAt: Date.now(),
        proposalIds: ids,
        run: res.run!,
      }, ...list].slice(0, 20));
      setMsg(t("ndv.rbp.applied", { n: String(ids.length) }));
      setPreview(null);
      setSel(null);
      onChanged?.();
      void reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  };

  const startPending = async (p: PendingRun) => {
    setBusy(`start:${p.key}`);
    setErr("");
    try {
      const created = await app.NetDevCutoverStart(p.run);
      setPending(list => list.filter(x => x.key !== p.key));
      onChanged?.();
      onCreated?.(created.id); // 跳到运行中的割接视图（与手动创建同语义）
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  };

  const dropPending = (key: string) => setPending(list => list.filter(x => x.key !== key));

  const runProfileCheck = async () => {
    setPcBusy(true);
    setErr("");
    try {
      setPcOut(await app.NetDevProfileCheck(pcDevice, pcModel, pcQuant, Number(pcTp) || 0));
    } catch (e) {
      setErr(String(e));
    } finally {
      setPcBusy(false);
    }
  };

  return (
    <div className="ndv__card" style={{ marginBottom: 12 }}>
      <div className="ndv__card-title">{t("ndv.rbp.title")}</div>
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap", margin: "6px 0" }}>
        {scenarios.map(sc => (
          <span key={sc} className="btn btn--secondary btn--small" role="button"
            style={scenario === sc ? { borderColor: "var(--accent, #7ab8ff)", color: "var(--accent, #7ab8ff)" } : { opacity: 0.6 }}
            onClick={() => { setScenarioTouched(true); setScenario(sc); }}>
            {sc === "all" ? t("ndv.rbp.scAll") : sc}
          </span>
        ))}
        <span className="ndv__meta" style={{ marginLeft: "auto", alignSelf: "center" }}>{t("ndv.rbp.hint")}</span>
      </div>

      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        {visible.map(x => (
          <span key={x.id} className="btn btn--secondary btn--small" role="button"
            style={sel?.id === x.id ? { borderColor: "var(--accent, #7ab8ff)", color: "var(--accent, #7ab8ff)" } : {}}
            title={x.notes || x.id}
            onClick={() => pick(x)}>
            {x.name}
          </span>
        ))}
      </div>

      {sel && (
        <div style={{ marginTop: 8, padding: "8px 10px", border: "1px solid var(--border, #333)" }}>
          <div className="ndv__meta" style={{ marginBottom: 6 }}>
            {t("ndv.rbp.varsHint", { name: sel.name })}{sel.window_min ? ` · ${t("ndv.rbp.window", { n: String(sel.window_min) })}` : ""}
          </div>
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
            {(sel.vars ?? []).map((v: string) => (
              <input key={v} className="mem-input" style={{ width: 180 }} placeholder={v}
                value={vars[v] ?? ""} onChange={e => setVars({ ...vars, [v]: e.target.value })} />
            ))}
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void doPreview()}>{busy === "preview" ? "…" : t("ndv.rbp.preview")}</span>
            <span className="btn btn--primary btn--small" role="button" onClick={() => void doApply()}>{busy === "apply" ? "…" : t("ndv.rbp.apply")}</span>
          </div>
          {preview && (
            <div className="ndv__meta" style={{ marginTop: 6 }}>
              <b>{t("ndv.rbp.previewTitle")}</b>
              {(preview.steps ?? []).map((st, i) => (
                <div key={i}>
                  {i + 1}. {st.label}{st.proposal_id ? ` [${t("ndv.rbp.proposalStep")}]` : ""}{st.decision_point ? ` [${t("ndv.rbp.decision")}]` : ""}{st.gate ? ` ${t("ndv.rbp.gate")}: ${st.gate.command} → ${st.gate.expect}` : ""}
                </div>
              ))}
            </div>
          )}
          {notes.length > 0 && (
            <div className="ndv__meta" style={{ marginTop: 4 }}>
              {notes.map((n, i) => <div key={i}>· {n}</div>)}
            </div>
          )}
        </div>
      )}

      {pending.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <div className="ndv__group-label">{t("ndv.rbp.pendingTitle")}</div>
          {pending.map(pr => {
            const allApproved = pr.proposalIds.every(id => proposals.has(id));
            return (
              <div key={pr.key} style={{ display: "flex", gap: 8, alignItems: "center", padding: "3px 0" }}>
                <span>{pr.name}</span>
                <span className="ndv__meta">{t("ndv.rbp.proposalsN", { n: String(pr.proposalIds.length) })}</span>
                {!allApproved && <span className="ndv__meta" style={{ color: "var(--warn)" }}>{t("ndv.rbp.waitApproval")}</span>}
                <span style={{ marginLeft: "auto" }} />
                {allApproved && (
                  <span className="btn btn--primary btn--small" role="button" onClick={() => void startPending(pr)}>
                    {busy === `start:${pr.key}` ? "…" : t("ndv.rbp.start")}
                  </span>
                )}
                <span className="btn btn--secondary btn--small" role="button" title={t("common.delete")} onClick={() => dropPending(pr.key)}>×</span>
              </div>
            );
          })}
        </div>
      )}

      {pending.length > 0 && <div style={{ marginTop: 6 }} />}

      <div style={{ marginTop: 8, padding: "8px 10px", border: "1px solid var(--border, #333)" }}>
        <div className="ndv__group-label">{t("ndv.rbp.pcTitle")}</div>
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
          <select className="mem-input" style={{ width: 130 }} value={pcDevice} onChange={e => setPcDevice(e.target.value)}>
            <option value="">{t("ndv.rbp.pcDevice")}</option>
            {(devices ?? []).map(d => <option key={d.name} value={d.name}>{d.name}</option>)}
          </select>
          <input className="mem-input" style={{ width: 180 }} placeholder={t("ndv.rbp.pcModel")} value={pcModel} onChange={e => setPcModel(e.target.value)} />
          <input className="mem-input" style={{ width: 90 }} placeholder={t("ndv.rbp.pcQuant")} value={pcQuant} onChange={e => setPcQuant(e.target.value)} />
          <input className="mem-input" style={{ width: 60 }} placeholder="TP" value={pcTp} onChange={e => setPcTp(e.target.value)} />
          <span className="btn btn--secondary btn--small" role="button" onClick={() => void runProfileCheck()}>
            {pcBusy ? "…" : t("ndv.rbp.pcRun")}
          </span>
        </div>
        {pcOut.length > 0 && (
          <div className="ndv__meta" style={{ marginTop: 6 }}>
            {pcOut.map((l, i) => <div key={i}>· {l}</div>)}
          </div>
        )}
      </div>

      {msg && <div className="ndv__meta" style={{ color: "var(--ok, #4caf50)", marginTop: 4 }}>{msg}</div>}
      {err && <div className="ndv__meta" style={{ color: "var(--err)", marginTop: 4 }}>{err}</div>}
    </div>
  );
}
