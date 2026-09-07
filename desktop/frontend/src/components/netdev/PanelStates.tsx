import { AlertTriangle, RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import { useI18n } from "../../lib/i18n";

// PanelErrorState / EmptyState（SCENARIO_SPEC G1-1/G1-4）：运维面板的错误态
// 与空态共享组件。错误=文案+重试（拉取失败不再与"无数据"不可区分）；
// 空态=说明+原因+下一步动作（三要素，对齐审计空态的正面样例）。

export function PanelErrorState({ onRetry, hint }: { onRetry: () => void; hint?: string }) {
  const { t } = useI18n();
  return (
    <div className="ndv__card" style={{ padding: 16, display: "flex", alignItems: "center", gap: 12 }}>
      <AlertTriangle size={16} style={{ color: "var(--danger, #e5484d)", flexShrink: 0 }} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="ndv-ovw__line" style={{ color: "var(--fg)" }}>{t("ndv.panel.loadFailed")}{hint ? `：${hint}` : ""}</div>
        <div className="ndv-ovw__line dim" style={{ fontSize: 12 }}>{t("ndv.panel.retryHint")}</div>
      </div>
      <button className="btn btn--small" onClick={onRetry} style={{ flexShrink: 0 }}>
        <RotateCw size={13} /> {t("ndv.panel.retry")}
      </button>
    </div>
  );
}

export function EmptyState({ title, reason, action }: { title: ReactNode; reason?: ReactNode; action?: { label: ReactNode; onClick: () => void } }) {
  return (
    <div style={{ padding: "14px 4px", display: "flex", flexDirection: "column", gap: 6 }}>
      <div className="ndv-ovw__line dim">{title}</div>
      {reason ? <div className="ndv-ovw__line dim" style={{ fontSize: 12 }}>{reason}</div> : null}
      {action ? (
        <div>
          <button className="btn btn--small" role="button" onClick={action.onClick}>{action.label}</button>
        </div>
      ) : null}
    </div>
  );
}
