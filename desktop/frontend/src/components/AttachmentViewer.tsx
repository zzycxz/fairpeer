import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { FileText, Folder, FolderOpen, FolderSearch, Image as ImageIcon, Loader2, X, ZoomIn, ZoomOut } from "lucide-react";
import { Markdown } from "./Markdown";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useToast } from "../lib/toast";
import type { FilePreview } from "../lib/types";

// AttachmentViewer is the lightbox behind every chat attachment. Message chips,
// tool-card images, markdown images and composer chips all call
// openAttachmentViewer(); the layer mounted once in App renders the modal, so
// callers don't each need their own overlay plumbing.
//
// Images resolve through AttachmentDataURL (data URL) or ReadFile (media token
// URL for workspace files); other files preview as text or PDF when the kernel
// can serve one, and everything always exposes "open with system app" /
// "show in file manager" via the existing Go bindings.

export interface ViewerTarget {
  path: string;
  name: string;
  kind: "image" | "file" | "folder";
  source: "attachment" | "workspace";
  /** Already-known data URL (composer chips keep their preview in memory). */
  previewUrl?: string;
}

let current: ViewerTarget | null = null;
const listeners = new Set<() => void>();

export function openAttachmentViewer(target: ViewerTarget): void {
  current = target;
  listeners.forEach((l) => l());
}

export function closeAttachmentViewer(): void {
  current = null;
  listeners.forEach((l) => l());
}

function useViewerTarget(): ViewerTarget | null {
  const [target, setTarget] = useState(current);
  useEffect(() => {
    const listener = () => setTarget(current);
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }, []);
  return target;
}

type ImageState =
  | { status: "loading" }
  | { status: "ready"; url: string }
  | { status: "error" };

// ImageBody resolves the full-size image. Attachment-source paths go through
// AttachmentDataURL (base64, ≤10 MB); anything else — or an oversized
// attachment — falls back to ReadFile's token URL, which streams from disk.
function ImageBody({ target, onMeta }: { target: ViewerTarget; onMeta?: (w: number, h: number) => void }) {
  const [state, setState] = useState<ImageState>({ status: "loading" });
  const [zoom, setZoom] = useState(1);
  const key = `${target.source}\n${target.path}\n${target.previewUrl ?? ""}`;

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    setZoom(1);
    (async () => {
      let url = target.previewUrl ?? "";
      if (!url && target.source === "attachment") {
        try {
          url = await app.AttachmentDataURL(target.path);
        } catch {
          url = "";
        }
      }
      if (!url) {
        try {
          const preview = await app.ReadFile(target.path);
          if (preview.kind === "image" && preview.url) url = preview.url;
        } catch {
          url = "";
        }
      }
      if (!cancelled) setState(url ? { status: "ready", url } : { status: "error" });
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  if (state.status === "loading") {
    return <ViewerLoading />;
  }
  if (state.status === "error") {
    return <ViewerHint icon={<ImageIcon size={22} />} textKey="viewer.loadFailed" actions={<ViewerActions target={target} />} />;
  }
  return (
    <>
      <div className="attachment-viewer__zoombar" onClick={(e) => e.stopPropagation()}>
        <button type="button" className="attachment-viewer__zoombtn" onClick={() => setZoom((z) => Math.max(1, z - 0.5))} aria-label="zoom out">
          <ZoomOut size={14} />
        </button>
        <span className="attachment-viewer__zoomval">{Math.round(zoom * 100)}%</span>
        <button type="button" className="attachment-viewer__zoombtn" onClick={() => setZoom((z) => Math.min(4, z + 0.5))} aria-label="zoom in">
          <ZoomIn size={14} />
        </button>
        {zoom !== 1 && (
          <button type="button" className="attachment-viewer__zoombtn attachment-viewer__zoombtn--reset" onClick={() => setZoom(1)} aria-label="reset zoom">
            1:1
          </button>
        )}
      </div>
      <div className="attachment-viewer__stage" onClick={(e) => e.stopPropagation()}>
        <img
          className="attachment-viewer__img"
          src={state.url}
          alt={target.name}
          draggable={false}
          style={{ maxWidth: `${zoom * 100}%`, maxHeight: `${zoom * 100}%` }}
          onLoad={(e) => onMeta?.(e.currentTarget.naturalWidth, e.currentTarget.naturalHeight)}
        />
      </div>
    </>
  );
}

type FileState =
  | { status: "loading" }
  | { status: "ready"; preview: FilePreview }
  | { status: "error" };

// FileBody previews non-image attachments: PDFs stream through ReadFile's media
// token URL in an iframe, text-ish files render their (possibly truncated)
// body, and anything binary falls back to a hint card — the header's open /
// reveal actions cover those.
function FileBody({ target }: { target: ViewerTarget }) {
  const t = useT();
  const [state, setState] = useState<FileState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    app.ReadFile(target.path)
      .then((preview) => {
        if (!cancelled) setState(preview.err ? { status: "error" } : { status: "ready", preview });
      })
      .catch(() => {
        if (!cancelled) setState({ status: "error" });
      });
    return () => {
      cancelled = true;
    };
  }, [target.path]);

  if (state.status === "loading") {
    return <ViewerLoading text={t("viewer.parsingDoc")} />;
  }
  if (state.status === "error") {
    return <ViewerHint icon={<FileText size={22} />} textKey="viewer.loadFailed" actions={<ViewerActions target={target} />} />;
  }
  const preview = state.preview;
  if (preview.kind === "pdf" && preview.url) {
    return (
      <div className="attachment-viewer__stage" onClick={(e) => e.stopPropagation()}>
        <iframe className="attachment-viewer__pdf" src={preview.url} title={target.name} />
      </div>
    );
  }
  if (preview.kind === "audio" && preview.url) {
    return (
      <div className="attachment-viewer__stage" onClick={(e) => e.stopPropagation()}>
        <audio className="attachment-viewer__audio" controls src={preview.url} preload="metadata" />
      </div>
    );
  }
  if (preview.kind === "video" && preview.url) {
    return (
      <div className="attachment-viewer__stage" onClick={(e) => e.stopPropagation()}>
        <video className="attachment-viewer__video" controls src={preview.url} preload="metadata" />
      </div>
    );
  }
  if (preview.kind === "html" && preview.url) {
    // Sandbox with no permissions: scripts, forms and same-origin access are
    // all blocked, so a workspace HTML file can't touch the app.
    return (
      <div className="attachment-viewer__stage" onClick={(e) => e.stopPropagation()}>
        <iframe className="attachment-viewer__html" src={preview.url} title={target.name} sandbox="" />
      </div>
    );
  }
  if (!preview.binary && preview.body) {
    const note = preview.truncated ? <div className="attachment-viewer__truncated">{t("viewer.truncated")}</div> : null;
    if (/\.(md|markdown)$/i.test(target.path)) {
      return (
        <div className="attachment-viewer__stage attachment-viewer__stage--text" onClick={(e) => e.stopPropagation()}>
          {note}
          <div className="attachment-viewer__md">
            <Markdown text={preview.body} />
          </div>
        </div>
      );
    }
    return (
      <div className="attachment-viewer__stage attachment-viewer__stage--text" onClick={(e) => e.stopPropagation()}>
        {note}
        <pre className="attachment-viewer__pre">{preview.body}</pre>
      </div>
    );
  }
  return <ViewerHint icon={<FileText size={22} />} textKey="viewer.binaryFile" actions={<ViewerActions target={target} />} />;
}

function ViewerLoading({ text }: { text?: string }) {
  return (
    <div className="attachment-viewer__state">
      <Loader2 size={22} className="attachment-viewer__spin" />
      {text ? <p className="attachment-viewer__state-note">{text}</p> : null}
    </div>
  );
}

// ViewerActions renders the prominent open/reveal buttons used inside hint
// cards (the header keeps its compact icon buttons). Both bind to the same Go
// handlers; failures surface as a toast.
function ViewerActions({ target }: { target: ViewerTarget }) {
  const t = useT();
  const { showToast } = useToast();
  const run = (fn: () => Promise<void>) => {
    fn().catch(() => showToast(t("viewer.openFailed"), "error"));
  };
  return (
    <div className="attachment-viewer__actions">
      <button
        type="button"
        className="attachment-viewer__action btn btn--secondary"
        onClick={() => run(() => app.OpenWorkspacePath(target.path))}
      >
        <FolderOpen size={14} />
        {target.kind === "folder" ? t("viewer.openFolder") : t("viewer.open")}
      </button>
      <button
        type="button"
        className="attachment-viewer__action btn btn--secondary"
        onClick={() => run(() => app.RevealWorkspacePath(target.path))}
      >
        <FolderSearch size={14} />
        {t("workspace.revealInFileManager")}
      </button>
    </div>
  );
}

function ViewerHint({ icon, textKey, actions }: { icon: ReactNode; textKey: "viewer.loadFailed" | "viewer.binaryFile" | "viewer.folderHint"; actions?: ReactNode }) {
  const t = useT();
  return (
    <div className="attachment-viewer__hint">
      <span className="attachment-viewer__hint-icon">{icon}</span>
      <p>{t(textKey)}</p>
      {actions}
    </div>
  );
}

export function AttachmentViewer() {
  const target = useViewerTarget();
  const t = useT();
  const { showToast } = useToast();
  const [meta, setMeta] = useState("");

  useEffect(() => {
    if (!target) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeAttachmentViewer();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [target]);

  useEffect(() => {
    setMeta("");
  }, [target?.path]);

  if (!target) return null;

  const openWith = async () => {
    try {
      await app.OpenWorkspacePath(target.path);
    } catch {
      showToast(t("viewer.openFailed"), "error");
    }
  };
  const reveal = async () => {
    try {
      await app.RevealWorkspacePath(target.path);
    } catch {
      showToast(t("viewer.openFailed"), "error");
    }
  };

  return (
    <div className="attachment-viewer" role="dialog" aria-modal="true" aria-label={target.name} onClick={closeAttachmentViewer}>
      <div className="attachment-viewer__header" onClick={(e) => e.stopPropagation()}>
        <span className={`attachment-viewer__kind attachment-viewer__kind--${target.kind}`}>
          {target.kind === "image" ? <ImageIcon size={14} /> : target.kind === "folder" ? <Folder size={14} /> : <FileText size={14} />}
        </span>
        <span className="attachment-viewer__title">
          <span className="attachment-viewer__name">{target.name}</span>
          <span className="attachment-viewer__path">
            {target.path}
            {meta ? ` · ${meta}` : ""}
          </span>
        </span>
        <button type="button" className="attachment-viewer__btn" onClick={openWith} title={t("viewer.open")} aria-label={t("viewer.open")}>
          <FolderOpen size={15} />
        </button>
        <button type="button" className="attachment-viewer__btn" onClick={reveal} title={t("workspace.revealInFileManager")} aria-label={t("workspace.revealInFileManager")}>
          <FolderSearch size={15} />
        </button>
        <button type="button" className="attachment-viewer__btn attachment-viewer__btn--close" onClick={closeAttachmentViewer} title={t("viewer.close")} aria-label={t("viewer.close")}>
          <X size={16} />
        </button>
      </div>
      {target.kind === "image" && <ImageBody target={target} onMeta={(w, h) => setMeta(`${w}×${h}`)} />}
      {target.kind === "file" && <FileBody target={target} />}
      {target.kind === "folder" && <ViewerHint icon={<Folder size={22} />} textKey="viewer.folderHint" actions={<ViewerActions target={target} />} />}
    </div>
  );
}
