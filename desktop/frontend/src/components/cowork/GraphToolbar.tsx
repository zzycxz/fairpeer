// GraphToolbar provides controls for the knowledge graph: collection selector,
// search, type filter, selection mode toggle, and export buttons.

import { useEffect, useRef, useState } from "react";
import { Download, Filter, Search, X, Loader2, Sparkles, FolderPlus } from "lucide-react";
import { useT } from "../../lib/i18n";
import type { RagCollectionView } from "../../lib/types";
import { ENTITY_TYPES } from "./entityTypes";

export type SearchMode = "keyword" | "semantic";

export interface GraphToolbarProps {
  collection: string;
  collections: RagCollectionView[];
  onCollectionChange: (name: string) => void;
  searchQuery: string;
  onSearchChange: (q: string) => void;
  searchMode: SearchMode;
  onSearchModeChange: (mode: SearchMode) => void;
  filterTypes: string[];
  onFilterChange: (types: string[]) => void;
  onImport: () => void;
  onExportObsidian: () => void;
  hasData: boolean;
  summary: any;
  summaryLoading: boolean;
  onFetchSummary: () => void;
}

export function GraphToolbar({
  collection,
  collections,
  onCollectionChange,
  searchQuery,
  onSearchChange,
  searchMode,
  onSearchModeChange,
  filterTypes,
  onFilterChange,
  onImport,
  onExportObsidian,
  hasData,
  summary,
  summaryLoading,
  onFetchSummary,
}: GraphToolbarProps) {
  const t = useT();
  const [showFilter, setShowFilter] = useState(false);
  const filterRef = useRef<HTMLDivElement>(null);

  const [showSummaryDropdown, setShowSummaryDropdown] = useState(false);
  const summaryRef = useRef<HTMLDivElement>(null);

  // Close dropdowns when clicking outside
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (filterRef.current && !filterRef.current.contains(e.target as Node)) {
        setShowFilter(false);
      }
      if (summaryRef.current && !summaryRef.current.contains(e.target as Node)) {
        setShowSummaryDropdown(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  // Debounce the search input: keep a responsive local draft while only
  // propagating to the parent (which triggers graph rebuild + backend calls)
  // after the user pauses typing for 300ms. Without this, every keystroke
  // rebuilds all nodes/edges and — in semantic mode — fires a backend request.
  const [draft, setDraft] = useState(searchQuery);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Keep the draft in sync when the parent resets the query (e.g. clear button).
  useEffect(() => { setDraft(searchQuery); }, [searchQuery]);
  const commitSearch = (value: string) => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => onSearchChange(value), 300);
  };

  const toggleType = (key: string) => {
    if (filterTypes.includes(key)) {
      onFilterChange(filterTypes.filter((t) => t !== key));
    } else {
      onFilterChange([...filterTypes, key]);
    }
  };

  return (
    <div className="rag-toolbar">
      {/* Collection selector */}
      <select
        className="rag-toolbar__select"
        value={collection}
        onChange={(e) => onCollectionChange(e.target.value)}
      >
        <option value="">{t("graphToolbar.all")}</option>
        {collections.map((c) => (
          <option key={c.path || c.name} value={c.path || c.name}>
            {c.name} ({c.entities})
          </option>
        ))}
      </select>

      {/* Import button */}
      <button className="rag-toolbar__btn" onClick={onImport} title={t("graphToolbar.importFile")}>
        <FolderPlus size={14} />
        <span>{t("graphToolbar.import")}</span>
      </button>

      {/* Summary button & dropdown */}
      {hasData && (
        <div className="rag-toolbar__filter-wrap" ref={summaryRef} style={{ display: "inline-flex", marginLeft: "4px" }}>
          <button
            className={`rag-toolbar__btn ${showSummaryDropdown ? "rag-toolbar__btn--active" : ""}`}
            onClick={() => {
              if (summary) {
                setShowSummaryDropdown(!showSummaryDropdown);
              } else {
                onFetchSummary();
              }
            }}
            title={summary ? t("graphToolbar.viewSummary") : t("graphToolbar.genSummaryHint")}
            style={summary ? { color: "var(--accent)", borderColor: "var(--accent)", background: showSummaryDropdown ? "rgba(249, 115, 22, 0.08)" : "transparent" } : {}}
            disabled={summaryLoading}
          >
            {summaryLoading ? <Loader2 size={14} className="spin" /> : <Sparkles size={14} />}
            <span>{summaryLoading ? t("graphToolbar.generating") : summary ? t("graphToolbar.summary") : t("graphToolbar.genSummary")}</span>
          </button>

          {showSummaryDropdown && summary && (
            <div className="rag-toolbar__filter-dropdown" style={{ display: "flex", flexDirection: "column", gap: "12px", backdropFilter: "blur(12px)", width: "340px", padding: "16px", left: 0, right: "auto", marginTop: "32px" }}>
              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", paddingBottom: "8px", borderBottom: "1px solid var(--border-soft)" }}>
                <span style={{ fontSize: "13px", fontWeight: 600, color: "var(--fg)" }}>{t("graphToolbar.globalSummary")}</span>
                <button 
                  onClick={() => setShowSummaryDropdown(false)}
                  style={{ background: "transparent", border: "none", cursor: "pointer", color: "var(--fg-faint)", padding: "2px", display: "flex", alignItems: "center", justifyContent: "center" }}
                >
                  <X size={14} />
                </button>
              </div>
              <div style={{ fontSize: "13px", color: "var(--fg-dim)", lineHeight: "1.6", whiteSpace: "pre-wrap" }}>
                {summary.summary}
              </div>
              {summary.themes && (Array.isArray(summary.themes) ? summary.themes : [summary.themes]).length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: "6px", marginTop: "4px" }}>
                  {(Array.isArray(summary.themes) ? summary.themes : [summary.themes]).map((theme: string) => (
                    <span
                      key={theme}
                      onClick={() => {
                        if (typeof onSearchChange === "function") onSearchChange(theme);
                        setShowSummaryDropdown(false);
                      }}
                      style={{ padding: "4px 10px", background: "rgba(249, 115, 22, 0.1)", color: "var(--accent)", borderRadius: "12px", fontSize: "11.5px", cursor: "pointer", border: "1px solid rgba(249, 115, 22, 0.2)", transition: "all 0.2s ease" }}
                      title={t("graphToolbar.clickToSearch")}
                    >
                      {theme}
                    </span>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Search */}
      <div className="rag-toolbar__search">
        <Search size={14} />
        <input
          type="text"
          placeholder={searchMode === "semantic" ? t("graphToolbar.semanticSearch") : t("graphToolbar.entitySearch")}
          value={draft}
          onChange={(e) => { setDraft(e.target.value); commitSearch(e.target.value); }}
        />
        <button
          className={`rag-toolbar__search-mode ${searchMode === "semantic" ? "rag-toolbar__search-mode--active" : ""}`}
          onClick={() => onSearchModeChange(searchMode === "keyword" ? "semantic" : "keyword")}
          title={searchMode === "keyword" ? t("graphToolbar.switchToSemantic") : t("graphToolbar.switchToKeyword")}
        >
          {searchMode === "keyword" ? t("graphToolbar.keywordShort") : t("graphToolbar.semanticShort")}
        </button>
        {searchQuery && (
          <button className="rag-toolbar__clear" onClick={() => onSearchChange("")}>
            <X size={12} />
          </button>
        )}
      </div>

      {/* Filter Toggle Button & Dropdown Popover */}
      <div className="rag-toolbar__filter-wrap" ref={filterRef} style={{ display: "inline-flex", marginLeft: "8px" }}>
        <button
          type="button"
          className={`rag-toolbar__btn ${showFilter || filterTypes.length > 0 ? "rag-toolbar__btn--active" : ""}`}
          onClick={() => setShowFilter(!showFilter)}
          title={t("graphToolbar.filterEntityType")}
          style={filterTypes.length > 0 ? { color: "var(--accent)", borderColor: "var(--accent)", background: "rgba(249, 115, 22, 0.08)" } : {}}
        >
          <Filter size={14} />
          <span>{t("graphToolbar.filter")}</span>
          {filterTypes.length > 0 && (
            <span style={{
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              marginLeft: "4px",
              padding: "0 6px",
              borderRadius: "10px",
              background: "var(--accent)",
              color: "#fff",
              fontSize: "11px",
              fontWeight: 600,
              lineHeight: "16px",
            }}>
              {filterTypes.length}
            </span>
          )}
        </button>

        {showFilter && (
          <div className="rag-toolbar__filter-dropdown" style={{ display: "flex", flexDirection: "column", gap: "8px", backdropFilter: "blur(12px)" }}>
            <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", paddingBottom: "6px", borderBottom: "1px solid var(--border-soft)" }}>
              <span style={{ fontSize: "12px", fontWeight: 600, color: "var(--fg)" }}>{t("graphToolbar.entityTypeFilter")}</span>
              <span style={{ fontSize: "11px", color: "var(--fg-faint)" }}>
                {filterTypes.length > 0 ? t("graphToolbar.nSelected", { count: String(filterTypes.length) }) : t("graphToolbar.showAll")}
              </span>
            </div>
            
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "4px", maxHeight: "280px", overflowY: "auto", paddingRight: "4px" }}>
              {ENTITY_TYPES.map((et) => {
                const active = filterTypes.length === 0 || filterTypes.includes(et.key);
                return (
                  <button
                    key={et.key}
                    type="button"
                    onClick={() => toggleType(et.key)}
                    className="rag-toolbar__filter-item"
                    style={{
                      border: `1px solid ${active ? et.color : "transparent"}`,
                      background: active ? `color-mix(in srgb, ${et.color} 15%, transparent)` : "transparent",
                      opacity: active ? 1 : 0.6,
                    }}
                  >
                    <span style={{ width: "8px", height: "8px", borderRadius: "50%", background: et.color, flexShrink: 0 }} />
                    <span style={{ flex: 1, textAlign: "left", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {et.label}
                    </span>
                  </button>
                );
              })}
            </div>

            {filterTypes.length > 0 && (
              <button
                type="button"
                className="rag-toolbar__filter-clear"
                onClick={() => onFilterChange([])}
              >
                {t("graphToolbar.resetClear")}
              </button>
            )}
          </div>
        )}
      </div>

      {/* Export */}
      <button className="rag-toolbar__btn" onClick={onExportObsidian} title={t("graphToolbar.exportObsidian")}>
        <Download size={14} />
      </button>
    </div>
  );
}
