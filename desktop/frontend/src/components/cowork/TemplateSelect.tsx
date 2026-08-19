// TemplateSelect provides the deep extraction UI: template selection, silent/
// immediate extraction modes, progress tracking, and result display. Shown in
// CoworkDock when the user clicks "深度提取".

import { useEffect, useRef, useState } from "react";
import { Zap, Clock, RefreshCw, ArrowRight, Eye, Trash2, Folder, Sparkles, CornerDownRight } from "lucide-react";
import { CustomSelect, type CustomSelectOption } from "./CustomSelect";

import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { asArray } from "../../lib/array";
import { useToast } from "../../lib/toast";
import { useConfirm } from "../../lib/confirm";
import type { RagExtractResultView, RagEntityBrief, RagNodeView } from "../../lib/types";
import { ENTITY_TYPE_LABELS, ENTITY_TYPE_COLORS } from "./entityTypes";

interface TemplateField {
  name: string;
  description: string;
}

interface Template {
  name: string;
  displayName: string;
  description: string;
  category: string;
  available: boolean;
  templateType: string;
  entityFields: TemplateField[];
  relationFields: TemplateField[];
}

interface ExtractJob {
  id: string;
  collection: string;
  path: string;
  template: string;
  status: string;
  progress: number;
  error: string;
  entities: number;
  relations: number;
}

export interface TemplateSelectProps {
  collection: string;
  collections?: Array<{ name: string; path: string; parent: string; documents?: number }>;
  onCollectionChange?: (name: string) => void;
  onBack: () => void;
  onViewGraph?: () => void;
}

const MAX_POLL_TICKS = 150; // 150 × 2s = 5min max

export function TemplateSelect({ collection, collections, onCollectionChange, onBack, onViewGraph }: TemplateSelectProps) {
  const t = useT();
  const { showToast } = useToast();
  const confirm = useConfirm();
  const [templates, setTemplates] = useState<Template[]>([]);
  const [heReady, setHeReady] = useState<boolean | null>(null);
  const [selectedTemplate, setSelectedTemplate] = useState("general/graph");
  const [jobs, setJobs] = useState<ExtractJob[]>([]);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<RagExtractResultView | null>(null);
  const [showResult, setShowResult] = useState(false);
  const [, setDocCount] = useState<number>(-1); // -1 = loading; setter used in effect
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Load templates, HE health, and document count on mount.
  useEffect(() => {
    app.HEHealth().then((h) => setHeReady(h.ready)).catch(() => setHeReady(false));
    app.RagListHETemplates().then((ts) => {
      if (ts.length > 0) setTemplates(ts);
    }).catch(() => {});
    app.RagExtractResult(collection).then((r) => {
      if (r.hasData) setResult(r);
    }).catch(() => {});
    // Check if the collection has any documents (flatten tree to find all file nodes).
    app.ListRagTree(collection).then((tree) => {
      const flatAll = (nodes: RagNodeView[]): RagNodeView[] => {
        const out: RagNodeView[] = [];
        for (const n of nodes) { out.push(n); if (n.children) out.push(...flatAll(n.children)); }
        return out;
      };
      const count = flatAll(tree).filter((n) => n.kind === "file").length;
      setDocCount(count);
    }).catch(() => setDocCount(0));
  }, [collection]);

  // Clean up polling on unmount.
  useEffect(() => {
    return () => {
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, []);

  const stopPolling = () => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  };

  // Cancel the current extraction: stop polling and re-enable the UI.
  // Note: this only resets the front-end state — the backend worker continues
  // processing remaining chunks in the background (by design, no orphaned jobs).
  const handleCancel = () => {
    stopPolling();
    setLoading(false);
    showToast(t("templateSelect.stoppedListening"), "info");
  };

  const handleSilentExtract = async () => {
    setLoading(true);
    try {
      await app.RagStartExtract(collection, selectedTemplate, "silent");
      onBack();
    } catch (e) {
      const msg = String(e);
      if (msg.includes("no documents")) {
        showToast(t("templateSelect.noDocs"), "error");
      } else {
        showToast(`${t("templateSelect.startFailed")}: ${msg}`, "error");
      }
    } finally {
      setLoading(false);
    }
  };

  const handleFullExtract = async () => {
    if (!(await confirm({ title: t("templateSelect.reExtract"), message: t("templateSelect.reExtractMsg") }))) return;
    setLoading(true);
    setJobs([]);
    setShowResult(false);
    setResult(null);
    try {
      await app.RagStartExtract(collection, selectedTemplate, "full");
      stopPolling();
      let ticks = 0;
      pollRef.current = setInterval(() => {
        ticks++;
        Promise.all([
          app.ListRagTree(collection),
          app.RagExtractResult(collection),
        ]).then(([tree, extractResult]) => {
          const flat = flatNodes(tree);
          const mapped: ExtractJob[] = flat
            .filter((n) => n.status === "extracting" || n.status === "queued" || n.status === "error" || n.status === "enriched")
            .map((n) => ({
              id: n.jobId || n.key,
              collection,
              path: n.path || n.label,
              template: selectedTemplate,
              status: n.status === "error" ? "failed" : n.status === "enriched" ? "done" : n.status,
              progress: n.totalChunks > 0 ? Math.min(100, Math.round((n.doneChunks / n.totalChunks) * 100)) : 0,
              error: n.errorMsg ?? "",
              entities: n.entityCount ?? 0,
              relations: 0,
            }));
          setJobs(mapped);
          setResult(extractResult);
          const fileNodes = flat.filter((n) => n.kind === "file");
          const allDone = fileNodes.length === 0 || fileNodes.every(
            (n) => n.status !== "extracting" && n.status !== "queued" && n.status !== "pending"
          );
          if (allDone || ticks >= MAX_POLL_TICKS) {
            stopPolling();
            setLoading(false);
            const errorCount = mapped.filter((j) => j.status === "failed").length;
            if (errorCount > 0) {
              showToast(t("templateSelect.extractPartialDone", { count: String(errorCount) }), "warn");
            }
            if (extractResult.hasData) {
              setShowResult(true);
            }
          }
        }).catch(() => {
          if (ticks >= MAX_POLL_TICKS) {
            stopPolling();
            setLoading(false);
          }
        });
      }, 2000);
    } catch (e) {
      setLoading(false);
      showToast(`${t("templateSelect.startFailed")}: ${e}`, "error");
    }
  };

  // Helper: recursively flatten the nested tree into a flat list of all nodes.
  const flatNodes = (nodes: RagNodeView[]): RagNodeView[] => {
    const result: RagNodeView[] = [];
    for (const n of nodes) {
      result.push(n);
      if (n.children && n.children.length > 0) {
        result.push(...flatNodes(n.children));
      }
    }
    return result;
  };

  const handleImmediateExtract = async () => {
    setLoading(true);
    setJobs([]);
    setShowResult(false);
    setResult(null);

    try {
      await app.RagStartExtract(collection, selectedTemplate, "incremental");
      // Poll for progress with timeout guard.
      stopPolling();
      let ticks = 0;
      pollRef.current = setInterval(() => {
        ticks++;
        // Fetch both tree progress and extraction result.
        Promise.all([
          app.ListRagTree(collection),
          app.RagExtractResult(collection),
        ]).then(([tree, extractResult]) => {
          // Flatten nested tree so we can find file-level status regardless of folder depth.
          const flat = flatNodes(tree);
          // Map tree nodes to job-like progress objects.
          const mapped: ExtractJob[] = flat
            .filter((n) => n.status === "extracting" || n.status === "queued" || n.status === "error" || n.status === "enriched")
            .map((n) => ({
              id: n.jobId || n.key,
              collection,
              path: n.path || n.label,
              template: selectedTemplate,
              status: n.status === "error" ? "failed" : n.status === "enriched" ? "done" : n.status,
              progress: n.totalChunks > 0 ? Math.min(100, Math.round((n.doneChunks / n.totalChunks) * 100)) : 0,
              error: n.errorMsg ?? "",
              entities: n.entityCount ?? 0,
              relations: 0,
            }));
          setJobs(mapped);
          setResult(extractResult);

          const fileNodes = flat.filter((n) => n.kind === "file");
          // A file is "settled" when it's done, enriched, or errored (not still
          // extracting or queued). allDone = every file has settled.
          const allDone = fileNodes.length === 0 || fileNodes.every(
            (n) => n.status !== "extracting" && n.status !== "queued" && n.status !== "pending"
          );
          if (allDone || ticks >= MAX_POLL_TICKS) {
            stopPolling();
            setLoading(false);
            const errorCount = mapped.filter((j) => j.status === "failed").length;
            if (errorCount > 0) {
              showToast(t("templateSelect.extractPartialDone", { count: String(errorCount) }), "warn");
            } else {
              showToast(t("templateSelect.extractDone", { entities: String(extractResult.entityCount), relations: String(extractResult.relationCount) }), "info");
            }
            if (extractResult.hasData) {
              // Auto-switch to graph view — the user just watched extraction,
              // they want to see the results, not stay on a progress screen.
              onBack();
              window.dispatchEvent(new CustomEvent("rag:fit-view"));
            }
          }
        }).catch(() => {
          if (ticks >= MAX_POLL_TICKS) {
            stopPolling();
            setLoading(false);
          }
        });
      }, 2000);
    } catch (e) {
      setLoading(false);
      const msg = String(e);
      if (msg.includes("no documents")) {
        showToast(t("templateSelect.noDocs"), "error");
      } else {
        showToast(`${t("templateSelect.startFailed")}: ${msg}`, "error");
      }
    }
  };


  // Result panel: show after extraction completes.
  if (showResult && result) {
    return <ExtractionResult result={result} onBack={onBack} onViewGraph={onViewGraph} onReExtract={() => { setShowResult(false); setResult(null); }} />;
  }

  return (
    <div className="rag-template">
      {/* Header */}
      <div className="rag-template__header">
        <button className="rag-template__back" onClick={onBack}>←</button>
        <span className="rag-template__title">{t("templateSelect.deepExtract")}</span>
      </div>

      {/* Collection — clean dropdown selector */}
      <div className="rag-template__section">
        <span className="rag-template__label">{t("templateSelect.collectionLabel")}</span>
        {collections && onCollectionChange ? (
          <CustomSelect
            value={collection}
            onChange={onCollectionChange}
            icon={<Folder size={14} style={{ color: "var(--accent)" }} />}
            options={(() => {
              const opts: CustomSelectOption[] = [{
                value: "",
                label: t("cowork.allDocs", { count: String(collections.reduce((s, c) => s + (c.documents ?? 0), 0)) }),
                icon: <Folder size={13} style={{ color: "var(--accent)" }} />,
              }];
              
              const sorted = [...collections].sort((a, b) => a.name.localeCompare(b.name));
              sorted.forEach(c => {
                // Use c.path (full "工作/管理办法") for depth + value — c.name is
                // already the leaf, so splitting it never finds nesting, and using
                // it as the value would create a duplicate top-level collection.
                const fullPath = c.path || c.name;
                const parts = fullPath.split("/");
                const depth = parts.length - 1;
                const displayName = c.name;
                
                opts.push({
                  value: fullPath,
                  label: (
                    <span style={{ 
                      display: "flex", 
                      justifyContent: "space-between", 
                      width: "100%", 
                      alignItems: "center",
                      paddingLeft: depth > 0 ? `${depth * 14}px` : "0px",
                    }}>
                      <span style={{ display: "flex", alignItems: "center", gap: "6px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>
                        {depth > 0 && <CornerDownRight size={12} style={{ color: "var(--fg-faint)", opacity: 0.7, marginTop: "-2px" }} />}
                        <Folder size={13} style={{ color: "var(--fg-faint)", opacity: 0.7 }} />
                        <span title={c.name} style={{ fontWeight: depth === 0 ? 500 : 400 }}>{displayName}</span>
                      </span>
                      <span style={{ fontSize: "10px", color: "var(--fg-faint)", marginLeft: "6px", flexShrink: 0 }}>
                        {t("cowork.nArticles", { count: String(c.documents) })}
                      </span>
                    </span>
                  )
                });
              });
              return opts;
            })()}
          />
        ) : (
          <span className="rag-template__value">{collection || t("cowork.all")}</span>
        )}
      </div>

      {/* Engine Status */}
      <div className="rag-template__section">
        <span className="rag-template__label">{t("templateSelect.extractEngine")}</span>
        <span className="rag-template__status rag-template__status--ok">
          {heReady
            ? t("templateSelect.engineReady") + " (HE enhanced)"
            : t("templateSelect.engineReady") + " (adaptive two-stage)"}
        </span>
      </div>

      {/* Template selection — clean dropdown + single active preview card */}
      <div className="rag-template__section">
        <span className="rag-template__label">{t("templateSelect.extractTemplate")}</span>
        {templates.length > 0 ? (
          <>
            <CustomSelect
              value={selectedTemplate}
              onChange={setSelectedTemplate}
              icon={<Zap size={14} style={{ color: "var(--accent)" }} />}
              options={templates.map((t) => ({
                value: t.name,
                label: t.displayName || t.name,
                subtitle: t.templateType || undefined,
                icon: <Zap size={13} />,
              }))}
            />
            {/* Single Selected Template Description Card */}
            {(() => {
              const sel = templates.find((t) => t.name === selectedTemplate);
              if (!sel) return null;
              const entityFields = asArray(sel.entityFields);
              const relationFields = asArray(sel.relationFields);
              return (
                <div className="rag-template__card rag-template__card--active" style={{ marginTop: 10, cursor: "default" }}>
                  <div className="rag-template__card-title">
                    <span>{sel.displayName || sel.name}</span>
                    {sel.templateType && <span className="rag-template__card-type">{sel.templateType}</span>}
                  </div>
                  {sel.description && (
                    <div className="rag-template__desc-text" style={{ marginTop: 4 }}>{sel.description}</div>
                  )}
                  {(entityFields.length > 0 || relationFields.length > 0) && (
                    <div className="rag-template__fields" style={{ marginTop: 8 }}>
                      {entityFields.length > 0 && (
                        <span className="rag-template__field-group">
                          <span className="rag-template__field-label">{t("templateSelect.entities")}</span>
                          {entityFields.map((f) => (
                            <span key={f.name} className="rag-template__field-chip" title={f.description}>{f.name}</span>
                          ))}
                        </span>
                      )}
                      {relationFields.length > 0 && (
                        <span className="rag-template__field-group">
                          <span className="rag-template__field-label">{t("templateSelect.relations")}</span>
                          {relationFields.map((f) => (
                            <span key={f.name} className="rag-template__field-chip" title={f.description}>{f.name}</span>
                          ))}
                        </span>
                      )}
                    </div>
                  )}
                </div>
              );
            })()}
          </>
        ) : (
          <div
            className="cowork-dock__empty-state"
            style={{
              margin: "24px 12px",
              padding: "36px 20px",
              border: "1px dashed var(--border-soft)",
              borderRadius: 12,
              background: "var(--bg-elev)",
              textAlign: "center",
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              gap: 6,
            }}
          >
            <div
              style={{
                width: 44,
                height: 44,
                borderRadius: "50%",
                background: "var(--bg-soft)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                marginBottom: 4,
                border: "1px solid var(--border-soft)",
                color: "var(--accent)",
              }}
            >
              <Sparkles size={22} style={{ opacity: 0.9 }} />
            </div>
            <p style={{ fontWeight: 600, color: "var(--fg)", fontSize: 13, margin: 0 }}>{t("templateSelect.noCustomTemplates")}</p>
            <p style={{ fontSize: 11.5, color: "var(--fg-faint)", margin: 0, lineHeight: 1.5, maxWidth: 260 }}>
              {t("templateSelect.builtinHint")}
            </p>
          </div>
        )}
      </div>

      {/* Action buttons — Primary High-Impact Button with Secondary Sub-row */}
      <div className="rag-template__actions">
        {loading ? (
          <button
            className="btn rag-template__btn"
            onClick={handleCancel}
            style={{ color: "var(--fg-dim)", borderColor: "var(--border)", width: "100%" }}
          >
            <RefreshCw size={14} />
            <span>{t("templateSelect.stopListening")}</span>
          </button>
        ) : (
          <>
            <button
              className="rag-template__btn rag-template__btn--primary"
              onClick={() => void handleImmediateExtract()}
              disabled={loading}
              style={{ width: "100%" }}
            >
              <Zap size={15} />
              <span>{t("templateSelect.smartIncremental")}</span>
            </button>
            <div className="rag-template__actions-sub">
              <button
                className="rag-template__btn"
                onClick={() => void handleFullExtract()}
                disabled={loading}
                style={{ flex: 1 }}
                title={t("templateSelect.fullRebuildHint")}
              >
                <RefreshCw size={13} />
                <span>{t("templateSelect.fullRebuild")}</span>
              </button>
              <button
                className="rag-template__btn"
                onClick={() => void handleSilentExtract()}
                disabled={loading}
                style={{ flex: 1 }}
                title={t("templateSelect.silentHint")}
              >
                <Clock size={13} />
                <span>{t("templateSelect.silentUnderstanding")}</span>
              </button>
            </div>
          </>
        )}
      </div>

      {/* Live stats during extraction */}
      {loading && result && (
        <div className="rag-template__live-stats">
          <span className="rag-template__live-stat">
            {t("templateSelect.entitiesCount")} <strong>{result.entityCount}</strong>
          </span>
          <span className="rag-template__live-stat">
            {t("templateSelect.relationsCount")} <strong>{result.relationCount}</strong>
          </span>
        </div>
      )}

      {/* Progress */}
      {jobs.length > 0 && (
        <div className="rag-template__progress">
          {/* Overall progress summary */}
          {(() => {
            const total = jobs.length;
            const doneCount = jobs.filter(j => j.status === "done").length;
            const failedCount = jobs.filter(j => j.status === "failed").length;
            const pendingCount = jobs.filter(j => j.status === "queued" || j.status === "pending").length;
            const currentFile = jobs.find(j => j.status === "extracting" || j.status === "running");
            return (
              <div className="rag-template__progress-summary">
                <div className="rag-template__progress-bar-wrap">
                  <div className="rag-template__progress-bar"
                    style={{ width: `${total > 0 ? Math.round((doneCount + failedCount) / total * 100) : 0}%` }} />
                </div>
                <span className="rag-template__progress-text">
                  {loading ? (
                    currentFile ? t("templateSelect.understanding", { file: currentFile.path.split(/[/\\]/).pop() || "", progress: String(currentFile.progress) }) :
                    pendingCount > 0 ? t("templateSelect.waiting", { done: String(doneCount), total: String(total) }) :
                    t("templateSelect.progressDone", { done: String(doneCount), total: String(total) })
                  ) : (
                    t("templateSelect.progressDone", { done: String(doneCount), total: String(total) }) + (failedCount > 0 ? `, ${failedCount} failed` : "")
                  )}
                </span>
              </div>
            );
          })()}
          {/* Per-file detail */}
          {jobs.map((j) => (
            <div key={j.id} className={`rag-template__job rag-template__job--${j.status}`}>
              <span className="rag-template__job-path">{j.path.split(/[/\\]/).pop()}</span>
              <span className="rag-template__job-status">
                {j.status === "done" ? `✓ ${j.entities} ${t("templateSelect.entitiesCount").toLowerCase()}` :
                 j.status === "running" || j.status === "extracting" ? `${j.progress}%` :
                 j.status === "failed" ? `✗ failed` :
                 j.status === "queued" || j.status === "pending" ? "waiting" :
                 j.status}
              </span>
              {j.status === "failed" && (
                <button className="rag-template__retry" title={j.error} onClick={() => void handleImmediateExtract()}>
                  <RefreshCw size={12} />
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Existing result hint - Layered Asset Card Design */}
      {!loading && result && result.hasData && !showResult && (
        <div
          className="cowork-dock__existing-card"
          style={{
            margin: "4px 12px 14px",
            padding: "12px 14px",
            background: "var(--bg-elev)",
            border: "1px solid var(--border-soft)",
            borderRadius: 10,
            display: "flex",
            flexDirection: "column",
            gap: 10,
            boxShadow: "0 2px 6px rgba(0,0,0,0.03)",
          }}
        >
          {/* 上层：图谱资产标题与双色数字胶囊 */}
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12.5, fontWeight: 600, color: "var(--fg)" }}>
              <span style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--accent)" }} />
              <span>{t("templateSelect.extractedKnowledge")}</span>
            </div>
            <div style={{ display: "flex", alignItems: "center", gap: 5, fontSize: 11 }}>
              <span style={{ padding: "2px 7px", borderRadius: 10, background: "var(--accent-soft)", color: "var(--accent)", fontWeight: 500 }}>
                {result.entityCount} {t("templateSelect.entitiesCount").toLowerCase()}
              </span>
              <span style={{ padding: "2px 7px", borderRadius: 10, background: "var(--accent-alt-soft)", color: "var(--accent-alt)", fontWeight: 500 }}>
                {result.relationCount} {t("templateSelect.relationsCount").toLowerCase()}
              </span>
            </div>
          </div>

          {/* 下层：主次清晰的操作按钮区分 */}
          <div style={{ display: "flex", alignItems: "center", gap: 8, paddingTop: 6, borderTop: "1px dashed var(--border-soft)" }}>
            <button
              type="button"
              onClick={() => setShowResult(true)}
              style={{
                flex: 1,
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                gap: 5,
                height: 28,
                padding: "0 10px",
                borderRadius: 6,
                background: "var(--accent)",
                color: "#fff",
                border: "none",
                fontSize: 11.5,
                fontWeight: 500,
                cursor: "pointer",
                boxShadow: "0 1px 2px rgba(0,0,0,0.05)",
              }}
            >
              <Eye size={13} />
              <span>{t("templateSelect.viewDetails")}</span>
            </button>
            <button
              type="button"
              onClick={async () => {
                if (await confirm({ title: t("templateSelect.cleanKnowledge"), message: t("templateSelect.cleanKnowledgeMsg") })) {
                  await app.RagCleanCollection(collection);
                  setResult(null);
                }
              }}
              style={{
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                gap: 4,
                height: 28,
                padding: "0 10px",
                borderRadius: 6,
                background: "transparent",
                color: "var(--fg-dim)",
                border: "1px solid var(--border-soft)",
                fontSize: 11.5,
                cursor: "pointer",
              }}
              title={t("templateSelect.cleanKnowledge")}
            >
              <Trash2 size={13} />
              <span>{t("templateSelect.clean")}</span>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
//  ExtractionResult — post-extraction summary panel
// ---------------------------------------------------------------------------

function ExtractionResult({ result, onBack, onViewGraph, onReExtract }: {
  result: RagExtractResultView;
  onBack: () => void;
  onViewGraph?: () => void;
  onReExtract: () => void;
}) {
  const t = useT();
  // Group entities by type for distribution bar.
  const typeGroups = new Map<string, number>();
  for (const e of result.topEntities) {
    typeGroups.set(e.type, (typeGroups.get(e.type) ?? 0) + 1);
  }
  const totalEntities = result.entityCount || 1; // avoid /0

  return (
    <div className="rag-result">
      {/* Header */}
      <div className="rag-result__header">
        <button className="rag-template__back" onClick={onBack}>←</button>
        <span className="rag-result__title">{t("templateSelect.extractResult")}</span>
      </div>

      {/* Summary */}
      <div className="rag-result__summary">
        <div className="rag-result__stat">
          <span className="rag-result__stat-num">{result.entityCount}</span>
          <span className="rag-result__stat-label">{t("templateSelect.entities")}</span>
        </div>
        <div className="rag-result__stat">
          <span className="rag-result__stat-num">{result.relationCount}</span>
          <span className="rag-result__stat-label">{t("templateSelect.relations")}</span>
        </div>
        <div className="rag-result__stat">
          <span className="rag-result__stat-num">{result.doneCount}/{result.jobCount}</span>
          <span className="rag-result__stat-label">{t("cowork.files")}</span>
        </div>
      </div>

      {/* Entity type distribution */}
      {typeGroups.size > 0 && (
        <div className="rag-result__section">
          <div className="rag-result__section-title">{t("templateSelect.entityTypeDistribution")}</div>
          <div className="rag-result__distribution">
            {Array.from(typeGroups.entries()).map(([type, count]) => (
              <div key={type} className="rag-result__dist-row">
                <span className="rag-result__dist-label">{ENTITY_TYPE_LABELS[type] ?? type}</span>
                <div className="rag-result__dist-bar">
                  <div
                    className="rag-result__dist-fill"
                    style={{
                      width: `${(count / totalEntities) * 100}%`,
                      background: ENTITY_TYPE_COLORS[type] ?? "#95A5A6",
                    }}
                  />
                </div>
                <span className="rag-result__dist-count">{count}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Top entities */}
      {result.topEntities.length > 0 && (
        <div className="rag-result__section">
          <div className="rag-result__section-title">{t("templateSelect.topEntities")}</div>
          <div className="rag-result__entity-list">
            {result.topEntities.slice(0, 10).map((e) => (
              <EntityCard key={e.name} entity={e} />
            ))}
          </div>
        </div>
      )}

      {/* Top relations */}
      {result.topRelations.length > 0 && (
        <div className="rag-result__section">
          <div className="rag-result__section-title">{t("templateSelect.relationExamples")}</div>
          <div className="rag-result__relation-list">
            {result.topRelations.slice(0, 5).map((r, i) => (
              <div key={i} className="rag-result__relation">
                <span className="rag-result__rel-node">{r.source}</span>
                <span className="rag-result__rel-type">{r.type}</span>
                <ArrowRight size={12} />
                <span className="rag-result__rel-node">{r.target}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Actions */}
      <div className="rag-result__actions">
        {onViewGraph && (
          <button className="btn btn--primary" onClick={onViewGraph}>
            <Eye size={14} />
            <span>{t("templateSelect.viewGraph")}</span>
          </button>
        )}
        <button className="btn" onClick={onReExtract}>
          <RefreshCw size={14} />
          <span>{t("templateSelect.changeTemplateReExtract")}</span>
        </button>
      </div>
    </div>
  );
}

function EntityCard({ entity }: { entity: RagEntityBrief }) {
  const t = useT();
  const label = ENTITY_TYPE_LABELS[entity.type] ?? entity.type;
  const color = ENTITY_TYPE_COLORS[entity.type] ?? "#95A5A6";
  return (
    <div className="rag-result__entity">
      <span className="rag-result__entity-type" style={{ background: color }}>{label}</span>
      <div className="rag-result__entity-info">
        <span className="rag-result__entity-name">{entity.nameRaw || entity.name}</span>
        {entity.description && (
          <span className="rag-result__entity-desc">{entity.description.slice(0, 60)}{entity.description.length > 60 ? "…" : ""}</span>
        )}
      </div>
      {entity.relationCount > 0 && (
        <span className="rag-result__entity-rels">{entity.relationCount} {t("templateSelect.relationsCount").toLowerCase()}</span>
      )}
    </div>
  );
}
