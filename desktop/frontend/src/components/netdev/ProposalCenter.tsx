import { useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { useToast } from "../../lib/toast";
import { useConfirm } from "../../lib/confirm";
import type { NetDevProposal } from "../../lib/types";
import { STEP_TYPE_LABEL } from "./proposalStepFormat";

// ProposalCenter is the human half of the write path: the agent drafts
// (netdev_propose), and ONLY here can a human approve, execute, or roll back.
// Buttons surface per status; frozen partials show what applied and what did
// not, with the rollback plan visible before anyone presses it.

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
