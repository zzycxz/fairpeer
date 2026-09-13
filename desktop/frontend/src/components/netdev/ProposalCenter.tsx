import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { useToast } from "../../lib/toast";
import { useConfirm } from "../../lib/confirm";
import type { NetDevProposal } from "../../lib/types";
import { STEP_TYPE_LABEL, stepSummary, k8sRef } from "./proposalStepFormat";

// ProposalCenter is the human half of the write path: the agent drafts
// (netdev_propose), and ONLY here can a human approve, execute, or roll back.
// Buttons surface per status; frozen partials show what applied and what did
// not, with the rollback plan visible before anyone presses it.

// G1-5：状态文案走 i18n（原为硬编码中文表）。
const PROP_STATUS_KEY: Record<string, string> = {
  draft: "ndv.prop.st.draft",
  approved: "ndv.prop.st.approved",
  executing: "ndv.prop.st.executing",
  done: "ndv.prop.st.done",
  partial: "ndv.prop.st.partial",
  failed: "ndv.prop.st.failed",
  watching: "ndv.prop.st.watching",
  closed: "ndv.prop.st.closed",
  rejected: "ndv.prop.st.rejected",
};

// 结构化步骤（§7.1）：类型徽标（labels live in proposalStepFormat）。
export function StepTypeBadge({ type }: { type?: string }) {
  if (type && type !== "cli") {
    return <span className="ndv__badge" style={{ marginLeft: 6 }}>{STEP_TYPE_LABEL[type] ?? type}</span>;
  }
  return null;
}

// ProposalActions: the approve / execute / rollback buttons with their
// confirm dialogs. Shared by the settings 变更中心 and 运维 dock 的 变更
// tab — the human half of the write path lives wherever the human is looking.
export function
 ProposalActions({ p, onDone }: { p: NetDevProposal; onDone: () => void }) {
  const t = useT();
  const { showToast } = useToast();
  const confirmDlg = useConfirm();
  const [busy, setBusy] = useState("");
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");
  const [operator, setOperator] = useState(""); // J5：批准人自报（confirmers 项目必填）
  const act = async (label: string, fn: () => Promise<unknown>) => {
    setBusy(label);
    try {
      await fn();
      await onDone();
    } catch (e) {
      showToast(String(e), "error");
    } finally {
      setBusy("");
    }
  };
  return (
    <span style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
      {p.status === "draft" && (
        <span className="btn btn--primary btn--small" role="button" onClick={() => void act(`approve:${p.id}`, async () => {
          const ok = await confirmDlg({
            title: t("ndv.prop.approveTitle", { id: p.id }),
            message:
              `${p.intent}\n\n` +
              (p.steps ?? []).map((s) => `· ${s.device}: ${(s.commands ?? []).join("; ")}`).join("\n") +
              "\n\n" + t("ndv.prop.approveTail"),
            confirmLabel: t("ndv.prop.approve"),
          });
          if (!ok) return;
          await app.NetDevApproveProposalAs(p.id, true, operator);
        })}>
          {busy === `approve:${p.id}` ? "…" : t("ndv.prop.approve")}
        </span>
      )}
      {p.status === "draft" && (
        <input className="mem-input" style={{ maxWidth: 150 }} placeholder={t("ndv.prop.operatorPh")}
          title={t("ndv.prop.operatorTip")} value={operator} onChange={e => setOperator(e.target.value)} />
      )}
      {p.status === "approved" && (
        <span className="btn btn--primary btn--small" role="button" onClick={() => void act(`exec:${p.id}`, async () => {
          if (!(await confirmDlg({
            title: t("ndv.prop.execTitle", { id: p.id }),
            message: t("ndv.prop.execMsg"),
            confirmLabel: t("ndv.prop.execute"),
          }))) return;
          await app.NetDevExecuteProposal(p.id);
        })}>
          {busy === `exec:${p.id}` ? t("ndv.prop.executing") : t("ndv.prop.execute")}
        </span>
      )}
      {(p.status === "partial" || p.status === "done") && (
        <span className="btn btn--secondary btn--small" role="button" onClick={() => void act(`rb:${p.id}`, async () => {
          if (!(await confirmDlg({
            title: t("ndv.prop.rbTitle", { id: p.id }),
            message: t("ndv.prop.rbMsg"),
            confirmLabel: t("ndv.prop.rollback"),
            danger: false,
          }))) return;
          await app.NetDevRollbackProposal(p.id);
        })}>
          {busy === `rb:${p.id}` ? "…" : p.status === "partial" ? t("ndv.prop.rbExecuted") : t("ndv.prop.rollback")}
        </span>
      )}
      {/* 驳回（§4.1）：人工否决权。draft/approved 可驳，原因内联填写并随
          变更持久化——agent 下一轮读到变更即见被拒原因。 */}
      {(p.status === "draft" || p.status === "approved") && !rejecting && (
        <span className="btn btn--danger btn--small" role="button" onClick={() => { setRejecting(true); setReason(""); }}>
          {t("ndv.prop.rejectEllipsis")}
        </span>
      )}
      {rejecting && (
        <span style={{ display: "inline-flex", gap: 4, alignItems: "center", flexWrap: "wrap" }}>
          <input
            className="mem-input"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t("ndv.prop.rejectPh")}
            style={{ width: 160 }}
            autoFocus
          />
          <span className="btn btn--danger btn--small" role="button" onClick={() => void act(`reject:${p.id}`, async () => {
            setRejecting(false);
            await app.NetDevRejectProposal(p.id, reason.trim() || t("ndv.prop.noReason"));
          })}>{busy === `reject:${p.id}` ? "…" : t("ndv.prop.confirmReject")}</span>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => setRejecting(false)}>{t("common.cancel")}</span>
        </span>
      )}
      {/* 删除（§4.1 列表治理）：仅 draft/已终结态；活跃管线必须留档。 */}
      {(p.status === "draft" || p.status === "rejected" || p.status === "done" || p.status === "failed" || p.status === "closed") && (
        <span className="btn btn--secondary btn--small" role="button" title={t("ndv.prop.delTip")} onClick={() => void act(`del:${p.id}`, async () => {
          if (!(await confirmDlg({
            title: t("ndv.prop.delTitle", { id: p.id }),
            message: t("ndv.prop.delMsg"),
            confirmLabel: t("ndv.prop.delBtn"),
            danger: true,
          }))) return;
          await app.NetDevDeleteProposal(p.id);
        })}>
          {busy === `del:${p.id}` ? "…" : t("ndv.prop.delBtn")}
        </span>
      )}
    </span>
  );
}

export function ProposalCenter() {
  const t = useT();
  const confirmDlg = useConfirm();
  const [items, setItems] = useState<NetDevProposal[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [openID, setOpenID] = useState("");

  const reload = useCallback(async () => {
    try {
      const list = await app.NetDevProposals();
      setItems(list ?? []);
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  const act = useCallback(async (label: string, fn: () => Promise<unknown>) => {
    setBusy(label);
    try {
      await fn();
      await reload();
      setErr("");
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  }, [reload]);

  const approve = (p: NetDevProposal) => act(`approve:${p.id}`, async () => {
    const ok = await confirmDlg({
      title: t("ndv.prop.approveTitle", { id: p.id }),
      message:
        `${p.intent}\n\n` +
        p.steps.map((s) => `· ${stepSummary(s)}`).join("\n") +
        (p.steps.some(s => s.dangerous) ? "\n\n" + t("ndv.prop.dangerousNote") : "") +
        "\n\n" + t("ndv.prop.approveTail"),
      confirmLabel: t("ndv.prop.approve"),
    });
    if (!ok) return;
    await app.NetDevApproveProposal(p.id, true);
  });

  const execute = (p: NetDevProposal) => act(`exec:${p.id}`, async () => {
    if (!(await confirmDlg({
      title: t("ndv.prop.execTitle", { id: p.id }),
      message: t("ndv.prop.execMsg"),
      confirmLabel: t("ndv.prop.execute"),
    }))) return;
    await app.NetDevExecuteProposal(p.id);
  });

  const rollback = (p: NetDevProposal) => act(`rb:${p.id}`, async () => {
    if (!(await confirmDlg({
      title: t("ndv.prop.rbTitle", { id: p.id }),
      message: t("ndv.prop.rbMsg"),
      confirmLabel: t("ndv.prop.rollback"),
      danger: false,
    }))) return;
    await app.NetDevRollbackProposal(p.id);
  });

  return (
    <div>
      <div className="set-label" style={{ margin: "14px 0 6px" }}>{t("ndv.prop.centerTitle", { n: items.length })}</div>
      <div className="mem-hint" style={{ marginBottom: 6 }}>
        {t("ndv.prop.centerHint")}
      </div>
      {err && <div className="banner banner--error" style={{ marginBottom: 6 }}>{err}</div>}
      {items.length === 0 && <div className="mem-hint">{t("ndv.prop.empty")}</div>}
      {items.map(p => (
        <div key={p.id} className="mem-hint" style={{ border: "1px solid var(--border, #333)", borderRadius: 6, padding: 8, marginBottom: 6 }}>
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <span style={{ minWidth: 110, fontWeight: 600 }}>{p.id}</span>
            <span style={{ minWidth: 130 }}>{PROP_STATUS_KEY[p.status] ? t(PROP_STATUS_KEY[p.status] as never) : p.status}</span>
            <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={p.intent}>{p.intent}</span>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setOpenID(openID === p.id ? "" : p.id)}>
              {openID === p.id ? t("ndv.prop.collapse") : t("ndv.prop.details")}
            </span>
            {p.status === "draft" && (
              <span className="btn btn--primary btn--small" role="button" onClick={() => void approve(p)}>
                {busy === `approve:${p.id}` ? "…" : t("ndv.prop.approve")}
              </span>
            )}
            {p.status === "approved" && (
              <span className="btn btn--primary btn--small" role="button" onClick={() => void execute(p)}>
                {busy === `exec:${p.id}` ? t("ndv.prop.executing") : t("ndv.prop.execute")}
              </span>
            )}
            {(p.status === "partial" || p.status === "done") && (
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void rollback(p)}>
                {busy === `rb:${p.id}` ? "…" : p.status === "partial" ? t("ndv.prop.rbExecuted") : t("ndv.prop.rollback")}
              </span>
            )}
          </div>
          {openID === p.id && (
            <div style={{ marginTop: 8, borderTop: "1px solid var(--border, #333)", paddingTop: 8 }}>
              {p.note && <div style={{ marginBottom: 6, color: "var(--text-warn, #e0a800)" }}>{p.note}</div>}
              {p.steps.map((s, i) => (
                <div key={i} style={{ marginBottom: 6 }}>
                  <div>
                    {s.device} — {s.applied ? t("ndv.prop.applied") : s.error ? "❌ " + s.error : t("ndv.prop.notApplied")}
                    <StepTypeBadge type={s.type} />
                    {s.dangerous && <span className="ndv__badge ndv__badge--warn" style={{ marginLeft: 6 }}>{t("ndv.prop.dangerousBadge")}</span>}
                  </div>
                  {(!s.type || s.type === "cli") && (
                    <>
                      <div style={{ marginLeft: 12 }}>{t("ndv.prop.changeLabel")}{(s.commands ?? []).join("；")}</div>
                      <div style={{ marginLeft: 12 }}>{t("ndv.prop.rollbackLabel")}{(s.rollback ?? []).join("；") || t("ndv.prop.noneLabel")}</div>
                    </>
                  )}
                  {s.type === "k8s-apply" && (
                    <div style={{ marginLeft: 12 }}>
                      {t("ndv.prop.changeLabel")}server-side apply — <code>{k8sRef(s.yaml)}</code>
                      <pre style={{ margin: "4px 0", opacity: 0.75, maxHeight: 120, overflow: "auto" }}>{s.yaml}</pre>
                      {t("ndv.prop.k8sRollback")}
                    </div>
                  )}
                  {s.type === "sql-migration" && (
                    <div style={{ marginLeft: 12 }}>
                      <div>{t("ndv.prop.sqlUp")}<pre style={{ margin: "4px 0", opacity: 0.75, maxHeight: 120, overflow: "auto" }}>{s.up_sql}</pre></div>
                      <div>{t("ndv.prop.sqlDown")}{s.down_sql ? "" : " " + t("ndv.prop.sqlDownMissing")}：
                        <pre style={{ margin: "4px 0", opacity: 0.75, maxHeight: 120, overflow: "auto" }}>{s.down_sql || t("ndv.prop.noneLabel")}</pre>
                      </div>
                    </div>
                  )}
                  {(s.type === "file-upload" || s.type === "cert-replace") && (
                    <div style={{ marginLeft: 12 }}>
                      {t("ndv.prop.changeLabel")}{s.local_path} → {s.remote_path}
                      {s.type === "cert-replace" && <>；私钥 {s.key_local_path} → {s.key_remote_path}；reload <code>{s.reload_cmd}</code></>}
                      {s.checksum && <>；sha256 <code>{s.checksum.slice(0, 12)}…</code></>}
                      <div style={{ opacity: 0.75 }}>{t("ndv.prop.fileRollback")}</div>
                    </div>
                  )}
                  {s.backup && <div style={{ marginLeft: 12, opacity: 0.7 }}>{t("ndv.prop.backupArchived", { n: s.backup.length })}</div>}
                </div>
              ))}
              {p.status === "watching" && p.watch_until && (
                <div style={{ marginBottom: 6, color: "var(--text-warn, #e0a800)" }}>
                  {t("ndv.prop.watchUntil", { time: String(p.watch_until).slice(11, 19) })}
                </div>
              )}
              <div style={{ opacity: 0.6 }}>
                {t("ndv.prop.createdAt", { at: p.created_at ? String(p.created_at).slice(0, 19).replace("T", " ") : "-" })}
                {p.approved_at ? " · " + t("ndv.prop.approvedAt", { at: String(p.approved_at).slice(0, 19).replace("T", " ") }) : ""}
                {p.executed_at ? " · " + t("ndv.prop.executedAt", { at: String(p.executed_at).slice(0, 19).replace("T", " ") }) : ""}
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
