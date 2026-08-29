import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { useToast } from "../../lib/toast";
import { useConfirm } from "../../lib/confirm";
import type { NetDevProposal } from "../../lib/types";
import { STEP_TYPE_LABEL, stepSummary, k8sRef } from "./proposalStepFormat";

// ProposalCenter is the human half of the write path: the agent drafts
// (netdev_propose), and ONLY here can a human approve, execute, or roll back.
// Buttons surface per status; frozen partials show what applied and what did
// not, with the rollback plan visible before anyone presses it.

const STATUS_LABEL: Record<string, string> = {
  draft: "草稿（待审阅）",
  approved: "已批准（待执行）",
  executing: "执行中…",
  done: "已完成",
  partial: "⚠ 部分执行（已冻结）",
  failed: "🔴 回滚失败（需人工处理）",
  watching: "👁 观察期（§7.1）",
  closed: "已关闭",
};

// 结构化步骤（§7.1）：类型徽标（labels live in proposalStepFormat）。
export function StepTypeBadge({ type }: { type?: string }) {
  if (type && type !== "cli") {
    return <span className="ndv__badge" style={{ marginLeft: 6 }}>{STEP_TYPE_LABEL[type] ?? type}</span>;
  }
  return null;
}

// ProposalActions: the approve / execute / rollback buttons with their
// confirm dialogs. Shared by the settings 提案中心 and the 运维 dock's 提案
// tab — the human half of the write path lives wherever the human is looking.
export function
 ProposalActions({ p, onDone }: { p: NetDevProposal; onDone: () => void }) {
  const { showToast } = useToast();
  const confirmDlg = useConfirm();
  const [busy, setBusy] = useState("");
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
            title: `批准提案 ${p.id}`,
            message:
              `${p.intent}\n\n` +
              (p.steps ?? []).map((s) => `· ${s.device}: ${(s.commands ?? []).join("; ")}`).join("\n") +
              "\n\n回滚计划已随提案起草，批准后仍需手动点击执行。",
            confirmLabel: "批准",
          });
          if (!ok) return;
          await app.NetDevApproveProposal(p.id, true);
        })}>
          {busy === `approve:${p.id}` ? "…" : "批准"}
        </span>
      )}
      {p.status === "approved" && (
        <span className="btn btn--primary btn--small" role="button" onClick={() => void act(`exec:${p.id}`, async () => {
          if (!(await confirmDlg({
            title: `执行提案 ${p.id}`,
            message: "将逐台下发（先备份），任一台失败即冻结。",
            confirmLabel: "执行",
          }))) return;
          await app.NetDevExecuteProposal(p.id);
        })}>
          {busy === `exec:${p.id}` ? "执行中…" : "执行"}
        </span>
      )}
      {(p.status === "partial" || p.status === "done") && (
        <span className="btn btn--secondary btn--small" role="button" onClick={() => void act(`rb:${p.id}`, async () => {
          if (!(await confirmDlg({
            title: `回滚提案 ${p.id}`,
            message: "按已起草的回滚计划回滚已执行步骤。",
            confirmLabel: "回滚",
            danger: false,
          }))) return;
          await app.NetDevRollbackProposal(p.id);
        })}>
          {busy === `rb:${p.id}` ? "…" : p.status === "partial" ? "回滚已执行" : "回滚"}
        </span>
      )}
    </span>
  );
}

export function ProposalCenter() {
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
      title: `批准提案 ${p.id}`,
      message:
        `${p.intent}\n\n` +
        p.steps.map((s) => `· ${stepSummary(s)}`).join("\n") +
        (p.steps.some(s => s.dangerous) ? "\n\n⚠ 含危险动词步骤——已强制二次确认。" : "") +
        "\n\n回滚计划已随提案起草，批准后仍需手动点击执行。",
      confirmLabel: "批准",
    });
    if (!ok) return;
    await app.NetDevApproveProposal(p.id, true);
  });

  const execute = (p: NetDevProposal) => act(`exec:${p.id}`, async () => {
    if (!(await confirmDlg({
      title: `执行提案 ${p.id}`,
      message: "将逐台下发（先备份），任一台失败即冻结。",
      confirmLabel: "执行",
    }))) return;
    await app.NetDevExecuteProposal(p.id);
  });

  const rollback = (p: NetDevProposal) => act(`rb:${p.id}`, async () => {
    if (!(await confirmDlg({
      title: `回滚提案 ${p.id}`,
      message: "按已起草的回滚计划回滚已执行步骤。",
      confirmLabel: "回滚",
      danger: false,
    }))) return;
    await app.NetDevRollbackProposal(p.id);
  });

  return (
    <div>
      <div className="set-label" style={{ margin: "14px 0 6px" }}>提案中心（{items.length}）</div>
      <div className="mem-hint" style={{ marginBottom: 6 }}>
        agent 只能起草（netdev_propose）；批准、执行、回滚只在此处由人操作。每步的回滚计划在详情中可见。
      </div>
      {err && <div className="banner banner--error" style={{ marginBottom: 6 }}>{err}</div>}
      {items.length === 0 && <div className="mem-hint">暂无提案。对话中让 agent 用 netdev_propose 起草。</div>}
      {items.map(p => (
        <div key={p.id} className="mem-hint" style={{ border: "1px solid var(--border, #333)", borderRadius: 6, padding: 8, marginBottom: 6 }}>
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <span style={{ minWidth: 110, fontWeight: 600 }}>{p.id}</span>
            <span style={{ minWidth: 130 }}>{STATUS_LABEL[p.status] ?? p.status}</span>
            <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={p.intent}>{p.intent}</span>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setOpenID(openID === p.id ? "" : p.id)}>
              {openID === p.id ? "收起" : "详情"}
            </span>
            {p.status === "draft" && (
              <span className="btn btn--primary btn--small" role="button" onClick={() => void approve(p)}>
                {busy === `approve:${p.id}` ? "…" : "批准"}
              </span>
            )}
            {p.status === "approved" && (
              <span className="btn btn--primary btn--small" role="button" onClick={() => void execute(p)}>
                {busy === `exec:${p.id}` ? "执行中…" : "执行"}
              </span>
            )}
            {(p.status === "partial" || p.status === "done") && (
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void rollback(p)}>
                {busy === `rb:${p.id}` ? "…" : p.status === "partial" ? "回滚已执行" : "回滚"}
              </span>
            )}
          </div>
          {openID === p.id && (
            <div style={{ marginTop: 8, borderTop: "1px solid var(--border, #333)", paddingTop: 8 }}>
              {p.note && <div style={{ marginBottom: 6, color: "var(--text-warn, #e0a800)" }}>{p.note}</div>}
              {p.steps.map((s, i) => (
                <div key={i} style={{ marginBottom: 6 }}>
                  <div>
                    {s.device} — {s.applied ? "✅ 已下发" : s.error ? "❌ " + s.error : "⬜ 未执行"}
                    <StepTypeBadge type={s.type} />
                    {s.dangerous && <span className="ndv__badge ndv__badge--warn" style={{ marginLeft: 6 }}>⚠ 危险动词 · 已强制二次确认</span>}
                  </div>
                  {(!s.type || s.type === "cli") && (
                    <>
                      <div style={{ marginLeft: 12 }}>变更：{(s.commands ?? []).join("；")}</div>
                      <div style={{ marginLeft: 12 }}>回滚：{(s.rollback ?? []).join("；") || "（无）"}</div>
                    </>
                  )}
                  {s.type === "k8s-apply" && (
                    <div style={{ marginLeft: 12 }}>
                      变更：server-side apply — <code>{k8sRef(s.yaml)}</code>
                      <pre style={{ margin: "4px 0", opacity: 0.75, maxHeight: 120, overflow: "auto" }}>{s.yaml}</pre>
                      回滚依据：apply 前的 live 对象备份（resourceVersion 钉住）
                    </div>
                  )}
                  {s.type === "sql-migration" && (
                    <div style={{ marginLeft: 12 }}>
                      <div>变更（Up）：<pre style={{ margin: "4px 0", opacity: 0.75, maxHeight: 120, overflow: "auto" }}>{s.up_sql}</pre></div>
                      <div>回滚（Down{s.down_sql ? "" : " ⚠ 缺失——该类型不可提交"}）：
                        <pre style={{ margin: "4px 0", opacity: 0.75, maxHeight: 120, overflow: "auto" }}>{s.down_sql || "（无）"}</pre>
                      </div>
                    </div>
                  )}
                  {(s.type === "file-upload" || s.type === "cert-replace") && (
                    <div style={{ marginLeft: 12 }}>
                      变更：{s.local_path} → {s.remote_path}
                      {s.type === "cert-replace" && <>；私钥 {s.key_local_path} → {s.key_remote_path}；reload <code>{s.reload_cmd}</code></>}
                      {s.checksum && <>；sha256 <code>{s.checksum.slice(0, 12)}…</code></>}
                      <div style={{ opacity: 0.75 }}>回滚依据：目标现文件备份（上传前自动抓取）</div>
                    </div>
                  )}
                  {s.backup && <div style={{ marginLeft: 12, opacity: 0.7 }}>备份已存档（{s.backup.length} 字符）</div>}
                </div>
              ))}
              {p.status === "watching" && p.watch_until && (
                <div style={{ marginBottom: 6, color: "var(--text-warn, #e0a800)" }}>
                  👁 观察期至 {String(p.watch_until).slice(11, 19)}（§7.1：劣化触发 Finding + 一键回滚提案）
                </div>
              )}
              <div style={{ opacity: 0.6 }}>
                创建 {p.created_at ? String(p.created_at).slice(0, 19).replace("T", " ") : "-"}
                {p.approved_at ? ` · 批准 ${String(p.approved_at).slice(0, 19).replace("T", " ")}` : ""}
                {p.executed_at ? ` · 执行 ${String(p.executed_at).slice(0, 19).replace("T", " ")}` : ""}
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
