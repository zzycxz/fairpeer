// DocPreview shows the original document content with chunk highlights.
// Displayed in the CoworkDock when a user clicks a file in the file list.

import { useEffect, useState } from "react";
import { ArrowLeft } from "lucide-react";

import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import type { DocPreviewView } from "../../lib/types";

export interface DocPreviewProps {
  collection: string;
  docPath: string;
  onBack: () => void;
}

export function DocPreview({ collection, docPath, onBack }: DocPreviewProps) {
  const t = useT();
  const [preview, setPreview] = useState<DocPreviewView | null>(null);
  const [loading, setLoading] = useState(false);
  // Track load failures separately from "not found" so the user sees a clear,
  // retryable error instead of a misleading "not found" card when the backend
  // is temporarily unavailable.
  const [error, setError] = useState<string | null>(null);

  const load = () => {
    if (!collection || !docPath) return;
    setLoading(true);
    setError(null);
    app.GetDocumentPreview(collection, docPath).then((d) => {
      setPreview(d);
      setLoading(false);
    }).catch((e) => {
      setLoading(false);
      setError(String(e || t("cowork.ragLoadFailed")));
    });
  };

  useEffect(() => { load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [collection, docPath]);

  if (loading) {
    return <div className="rag-docpreview__loading">{t("common.loading")}</div>;
  }
  if (error) {
    return (
      <div className="rag-docpreview__empty">
        <div>{t("cowork.ragLoadFailed")}</div>
        <div style={{ fontSize: 12, opacity: 0.7, marginTop: 4 }}>{error}</div>
        <button className="btn btn--sm" style={{ marginTop: 8 }} onClick={load}>{t("common.retry")}</button>
      </div>
    );
  }
  if (!preview) {
    return <div className="rag-docpreview__empty">{t("cowork.ragDocNotFound")}</div>;
  }

  return (
    <div className="rag-docpreview">
      {/* Header */}
      <div className="rag-docpreview__header">
        <button className="rag-docpreview__back" onClick={onBack}>
          <ArrowLeft size={14} />
        </button>
        <div className="rag-docpreview__title">
          {docPath.split(/[/\\]/).pop()}
        </div>
      </div>

      {/* Content */}
      <div className="rag-docpreview__content">
        {preview.chunks && preview.chunks.length > 0 ? (
          preview.chunks.map((chunk, i) => (
            <div key={i} className="rag-docpreview__chunk">
              <div className="rag-docpreview__chunk-label">Chunk #{i + 1}</div>
              <div className="rag-docpreview__chunk-text">{chunk.content}</div>
            </div>
          ))
        ) : (
          <pre className="rag-docpreview__raw">{preview.content}</pre>
        )}
      </div>
    </div>
  );
}
