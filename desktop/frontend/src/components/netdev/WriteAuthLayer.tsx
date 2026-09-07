import { useEffect, useRef, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { app, onNetdevWriteApproval } from "../../lib/bridge";
import type { NetDevWriteApproval } from "../../lib/types";

// WriteAuthLayer — WRITE_AUTHZ_SPEC 的运维界面浮层（P1 前端必需件）：
//
//   1. confirm 档写命令审批卡（"netdev:write-approval" 事件）：
//      Manager 审批通道的 UI 侧——批准/拒绝回填 NetDevResolveWriteApproval。
//      这张卡不经过 permission 系统，完全访问模式跳不过（spec §6②）。
//   2. TOML 放宽拦截横幅：挂载时拉一次 NetDevWriteTierState，未确认的放宽
//      被降级为只读——横幅说明并指路运维设置（spec §5.3）。
//
// 超时由 Go 侧兜底（120s 按拒绝处理），前端只管渲染与回填。

const TIER_LABEL: Record<string, string> = { sealed: "🔒 sealed", confirm: "🔐 confirm", auto: "⚡ auto" };

interface PendingCard extends NetDevWriteApproval {}

export function WriteAuthLayer() {
  const { t } = useI18n();
  const [pending, setPending] = useState<PendingCard[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);
  const decided = useRef(new Set<string>());

  useEffect(() => {
    const off = onNetdevWriteApproval((req) => {
      if (decided.current.has(req.id)) return;
      setPending((prev) => (prev.some((p) => p.id === req.id) ? prev : [...prev, req]));
    });
    return off;
  }, []);

  useEffect(() => {
    let alive = true;
    app.NetDevWriteTierState()
      .then((st) => { if (alive && st?.warnings?.length) setWarnings(st.warnings); })
      .catch(() => { /* backend away → no banner */ });
    return () => { alive = false; };
  }, []);

  const resolve = (id: string, approved: boolean) => {
    decided.current.add(id);
    setPending((prev) => prev.filter((p) => p.id !== id));
    app.NetDevResolveWriteApproval(id, approved, "").catch(() => { /* timed out */ });
  };

  return (
    <>
      {warnings.length > 0 && (
        <div className="ndv-writeclamp-banner" role="alert">
          <div className="ndv-writeclamp-banner__title">{t("ndv.writeClamp.title")}</div>
          {warnings.map((w, i) => (
            <div key={i} className="ndv-writeclamp-banner__row">{w}</div>
          ))}
          <div className="ndv-writeclamp-banner__hint">{t("ndv.writeClamp.hint")}</div>
        </div>
      )}
      {pending.map((p) => (
        <div key={p.id} className="ndv-writecard" role="dialog" aria-modal="false">
          <div className="ndv-writecard__title">{t("ndv.writeCard.title")}</div>
          <div className="ndv-writecard__meta">
            <span className="ndv-writecard__device">{p.device}</span>
            <span className="ndv-writecard__tier">🔐 {t("ndv.writeCard.tierConfirm")}</span>
          </div>
          <pre className="ndv-writecard__cmd">{p.command}</pre>
          <div className="ndv-writecard__note">{t("ndv.writeCard.sandwichNote")}</div>
          <div className="ndv-writecard__actions">
            <button className="btn btn--small" onClick={() => resolve(p.id, true)}>{t("ndv.writeCard.approve")}</button>
            <button className="btn btn--secondary btn--small" onClick={() => resolve(p.id, false)}>{t("ndv.writeCard.decline")}</button>
          </div>
        </div>
      ))}
    </>
  );
}

export function writeTierLabel(tier: string | undefined): string {
  return TIER_LABEL[tier || "sealed"] ?? "🔒 sealed";
}
