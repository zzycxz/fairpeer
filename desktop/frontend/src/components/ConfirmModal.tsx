import { useEffect } from "react";
import { AlertTriangle } from "lucide-react";
import { useT } from "../lib/i18n";

// ConfirmModal is a small, app-styled confirmation dialog that replaces the
// jarring native window.confirm(). It reuses the rag-create-overlay/modal look
// so it matches the rest of the cowork UI (same backdrop, card, radius).
//
// Usage: lift a `const [pending, setPending] = useState<null | {title, message, onConfirm}>(null)`
// in the parent, render `{pending && <ConfirmModal {...pending} onClose={() => setPending(null)} />}`,
// and open it via setPending({...}). ESC + backdrop click dismiss without confirming.
export function ConfirmModal({
  title,
  message,
  confirmLabel,
  cancelLabel,
  danger = true,
  onConfirm,
  onClose,
}: {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const confirmText = confirmLabel ?? t("common.delete");
  const cancelText = cancelLabel ?? t("common.cancel");
  // ESC dismisses (cancel) — mirrors ImportModal/AnchoredPopover behavior.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const handleConfirm = () => {
    onClose();
    onConfirm();
  };

  return (
    <div className="rag-create-overlay" onClick={onClose} style={{ zIndex: 9999 }}>
      <div
        className="rag-create-modal"
        onClick={(e) => e.stopPropagation()}
        style={{ width: 380, maxWidth: "90vw", borderRadius: 14, overflow: "hidden" }}
      >
        <div className="rag-create-modal__body" style={{ padding: "20px 20px 16px", gap: 14 }}>
          <div style={{ display: "flex", alignItems: "flex-start", gap: 12 }}>
            <div
              style={{
                flex: "0 0 auto",
                width: 32,
                height: 32,
                borderRadius: 8,
                background: danger ? "var(--danger-soft)" : "var(--accent-soft)",
                color: danger ? "var(--danger)" : "var(--accent)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
              }}
            >
              <AlertTriangle size={17} />
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <h3 style={{ margin: 0, fontSize: 14.5, fontWeight: 600, color: "var(--fg)" }}>{title}</h3>
              <p style={{ margin: "4px 0 0", fontSize: 12.5, lineHeight: 1.5, color: "var(--fg-dim)" }}>{message}</p>
            </div>
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 2 }}>
            <button
              type="button"
              className="btn btn--small"
              onClick={onClose}
              style={{ fontSize: 12.5, padding: "7px 14px", borderRadius: 8 }}
            >
              {cancelText}
            </button>
            <button
              type="button"
              className={danger ? "btn btn--small btn--danger" : "btn btn--small"}
              onClick={handleConfirm}
              style={{ fontSize: 12.5, padding: "7px 14px", borderRadius: 8, fontWeight: 600 }}
            >
              {confirmText}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
