import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { asArray } from "../lib/array";
import { app, openExternal } from "../lib/bridge";
import { useToast } from "../lib/toast";
import { useT } from "../lib/i18n";
import type { CapabilitiesView, CatalogEntry, MCPServerInput, ServerView, SkillRootSkillView, SkillRootView, SkillView } from "../lib/types";
import { InlineConfirmButton } from "./InlineConfirmButton";
import { ResizableDrawer } from "./ResizableDrawer";
import { Tooltip } from "./Tooltip";
import { ModalCloseButton } from "./ModalCloseButton";
import { ConfirmModal } from "./ConfirmModal";

// CapabilitiesPanel is the desktop MCP & Skills drawer — the GUI counterpart to
// the CLI's /mcp + /skill, aligning with Claude Code's Customize → Connectors:
// each server shows a connected/failed dot, transport, and tool/prompt/resource
// counts, with add / remove / retry; skills list their scope and run mode.
type CapTab = "servers" | "skills";

export function CapabilitiesPanel({
  onClose,
  initialTab = "servers",
}: {
  onClose: () => void;
  initialTab?: CapTab;
}) {
  const t = useT();
  const [view, setView] = useState<CapabilitiesView | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [mcpSubtab, setMcpSubtab] = useState<"builtin" | "market">("builtin");
  const [editing, setEditing] = useState<string | null>(null);
  const [tab, setTab] = useState<CapTab>(initialTab);
  const [skillQuery, setSkillQuery] = useState("");
  const [expandedSkills, setExpandedSkills] = useState<Set<string>>(() => new Set());
  const [expandedErrors, setExpandedErrors] = useState<Set<string>>(() => new Set());
  const [expandedServers, setExpandedServers] = useState<Set<string>>(() => new Set());
  const [expandedServerTools, setExpandedServerTools] = useState<Set<string>>(() => new Set());

  const reload = useCallback(async () => {
    setView(normalizeCapabilitiesView(await app.Capabilities().catch(() => ({ servers: [], skills: [], skillRoots: [] }))));
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  useEffect(() => {
    if (tab !== "servers" || !view?.servers.some((s) => s.status === "initializing" || s.status === "deferred")) return;
    const id = window.setInterval(() => void reload(), 2500);
    return () => window.clearInterval(id);
  }, [reload, tab, view?.servers]);

  // mutate runs an MCP edit, re-reads the snapshot, and surfaces any failure as an
  // inline banner (a connect error, a missing binary, a bad URL).
  const mutate = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      await reload();
      return true;
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      await reload();
      return false;
    } finally {
      setBusy(false);
    }
  };

  const summary = useMemo(() => {
    if (!view) return "";
    return t("caps.summary", {
      connected: view.servers.filter((s) => s.status === "connected").length,
      failed: view.servers.filter((s) => s.status === "failed").length,
      skills: view.skills.length,
    });
  }, [view, t]);

  const filteredSkills = useMemo(() => {
    if (!view) return [];
    const q = skillQuery.trim().toLowerCase();
    if (!q) return view.skills;
    return view.skills.filter((sk) => {
      const text = [sk.name, `/${sk.name}`, sk.description, sk.scope, sk.runAs].join(" ").toLowerCase();
      return text.includes(q);
    });
  }, [view, skillQuery]);
  const skillSummary = useMemo(() => {
    if (!view) return "";
    return skillListSummary(view.skills, filteredSkills, skillQuery.trim().length > 0, t);
  }, [filteredSkills, skillQuery, t, view]);

  const serverGroups = useMemo(() => {
    const servers = sortServersForDisplay(view?.servers ?? []);
    return {
      failed: servers.filter((s) => s.status === "failed"),
      active: servers.filter((s) => s.status !== "failed"),
    };
  }, [view]);

  const toggleSkill = useCallback((name: string) => {
    setExpandedSkills((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  const toggleError = useCallback((name: string) => {
    setExpandedErrors((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  const toggleServer = useCallback((name: string) => {
    setExpandedServers((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  const toggleServerTools = useCallback((name: string) => {
    setExpandedServerTools((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  return (
    <ResizableDrawer onClose={onClose} subtle>
        <header className="drawer__head">
          <div>
            <div className="drawer__title">{t("caps.title")}</div>
            {view && <div className="drawer__summary">{summary}</div>}
          </div>
          <div className="drawer__actions">
            <Tooltip label={t("caps.refresh")}>
              <button className="chip" disabled={busy} onClick={() => void reload()}>
                ↻
              </button>
            </Tooltip>
            <ModalCloseButton label={t("common.close")} onClick={onClose} />
          </div>
        </header>

        {!view ? (
          <div className="empty">{t("caps.loading")}</div>
        ) : (
          <div className="drawer__body">
            {err && <div className="banner banner--error">{err}</div>}

            <div className="cap-tabs" role="tablist" aria-label={t("caps.title")}>
              <button
                className={`cap-tab${tab === "servers" ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={tab === "servers"}
                onClick={() => setTab("servers")}
              >
                {t("caps.connectorsTab")}
              </button>
              <button
                className={`cap-tab${tab === "skills" ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={tab === "skills"}
                onClick={() => setTab("skills")}
              >
                {t("caps.skillsTab")}
              </button>
            </div>

            {tab === "servers" ? (
              <section className="mem-section">
                <div className="settings-subtabs">
                  <button type="button" className={`settings-subtab${mcpSubtab === "builtin" ? " settings-subtab--active" : ""}`} aria-selected={mcpSubtab === "builtin"} onClick={() => setMcpSubtab("builtin")}>{t("caps.mcpTabBuiltin")}</button>
                  <button type="button" className={`settings-subtab${mcpSubtab === "market" ? " settings-subtab--active" : ""}`} aria-selected={mcpSubtab === "market"} onClick={() => setMcpSubtab("market")}>{t("caps.mcpTabMarket")}</button>
                </div>
                {mcpSubtab === "market" ? (
                  <McpMarketSection busy={busy} installedNames={new Set(view.servers.map((s) => s.name))} onInstalled={() => void reload()} />
                ) : (
                  <>
                <div className="cap-mcp-toolbar cap-mcp-toolbar--drawer">
                  {!adding && (
                    <>
                      <button className="btn btn--small" disabled={busy} onClick={() => setAdding(true)}>
                        {t("caps.addServer")}
                      </button>
                      <button
                        className="btn btn--small"
                        disabled={busy}
                        onClick={async () => {
                          const text = prompt(t("caps.pasteMCPJSON"));
                          if (!text?.trim()) return;
                          try {
                            const n = await app.ImportMCPServersJSON(text.trim());
                            useToast().showToast(t("caps.mcpImported", { n: String(n) }), "info");
                            void reload();
                          } catch (e) {
                            useToast().showToast(String(e), "error");
                          }
                        }}
                      >
                        {t("caps.pasteImport")}
                      </button>
                    </>
                  )}
                </div>
                {serverGroups.failed.length > 0 && (
                  <FailedServersNotice
                    servers={serverGroups.failed}
                    expanded={expandedErrors}
                    onToggle={toggleError}
                    onRetry={(name) => void mutate(() => app.ReconnectMCPServer(name))}
                    onRetryMany={(names) => void mutate(() => Promise.allSettled(names.map((name) => app.ReconnectMCPServer(name))))}
                    onConfirmClearAuth={(name) => void mutate(() => app.ClearMCPServerAuthentication(name))}
                    onConfirm={(name) => void mutate(() => app.RemoveMCPServer(name))}
                    onConfirmMany={(names) => void mutate(() => Promise.allSettled(names.map((name) => app.RemoveMCPServer(name))))}
                    busy={busy}
                  />
                )}
                {view.servers.length === 0 && !adding && (
                  <div className="mem-empty">{t("caps.noServers")}</div>
                )}
                {serverGroups.active.length > 0 && (
                  <div className="cap-server-section">
                    <div className="cap-server-section__title">{t("caps.availableServers")}</div>
                    <ServerGroup
                      busy={busy}
                      servers={serverGroups.active}
                      expanded={expandedServers}
                      expandedTools={expandedServerTools}
                      editing={editing}
                      onConfirm={(name) => void mutate(() => app.RemoveMCPServer(name))}
                      onEdit={(name) => {
                        setEditing(name);
                      }}
                      onCancelEdit={() => setEditing(null)}
                      onRetry={(name) => void mutate(() => app.ReconnectMCPServer(name))}
                      onReconnect={(name) => void mutate(() => app.ReconnectMCPServer(name))}
                      onConfirmClearAuth={(name) => void mutate(() => app.ClearMCPServerAuthentication(name))}
                      onToggle={(name, on) => void mutate(() => app.SetMCPServerEnabled(name, on))}
                      onUpdate={(name, input) =>
                        void mutate(() => app.UpdateMCPServer(name, input)).then((ok) => {
                          if (ok) setEditing(null);
                        })
                      }
                      onToggleDetails={toggleServer}
                      onToggleTools={toggleServerTools}
                    />
                  </div>
                )}
                {adding ? (
                  <div style={{ display: "flex", gap: "16px", flexWrap: "wrap", alignItems: "flex-start", marginTop: "16px" }}>
                    <div style={{ flex: "1 1 300px" }}>
                      <h3 className="cap-list__heading" style={{ marginBottom: "8px" }}>{t("caps.mcpAdvancedTitle")}</h3>
                      <AddServerForm
                        busy={busy}
                        onCancel={() => setAdding(false)}
                        onAdd={(input) => void mutate(() => app.AddMCPServer(input).then(() => setAdding(false)))}
                      />
                    </div>
                  </div>
                ) : null}
                  </>
                )}
              </section>
            ) : (
              <section className="mem-section">
                <div className="cap-search">
                  <input
                    className="mem-input"
                    type="search"
                    placeholder={t("caps.searchSkills")}
                    value={skillQuery}
                    onChange={(e) => setSkillQuery(e.target.value)}
                  />
                </div>
                <SkillSources
                  roots={view.skillRoots ?? []}
                  busy={busy}
                  onAdd={() => mutate(async () => {
                    const path = await app.PickSkillFolder();
                    if (path) await app.AddSkillPath(path);
                  })}
                  onRefresh={() => mutate(() => app.RefreshSkills())}
                  onRemove={(path) => mutate(() => app.RemoveSkillPath(path))}
                />
                <div className="cap-skills-head">
                  <div className="cap-skills-head__copy">
                    <div className="cap-skills-head__title">{t("caps.skills")}</div>
                    <div className="cap-skills-head__summary">{skillSummary}</div>
                  </div>
                </div>
                {view.skills.length === 0 ? (
                  <div className="mem-empty">{t("caps.noSkills")}</div>
                ) : filteredSkills.length === 0 ? (
                  <div className="mem-empty">{t("caps.noSkillMatches")}</div>
                ) : (
                  <div className="cap-skills">
                    {filteredSkills.map((sk) => (
                      <SkillRow
                        key={sk.name}
                        skill={sk}
                        busy={busy}
                        expanded={expandedSkills.has(sk.name)}
                        onToggle={() => toggleSkill(sk.name)}
                        onToggleEnabled={(enabled) => void mutate(() => app.SetSkillEnabled(sk.name, enabled))}
                      />
                    ))}
                  </div>
                )}
              </section>
            )}
          </div>
        )}
    </ResizableDrawer>
  );
}

function normalizeCapabilitiesView(view: CapabilitiesView | null | undefined): CapabilitiesView {
  return {
    servers: sortServersForDisplay(
      asArray(view?.servers).map((server) => ({
        ...server,
        args: asArray(server.args),
        envKeys: asArray(server.envKeys),
        toolList: asArray(server.toolList),
      })),
    ),
    skills: asArray(view?.skills),
    skillRoots: asArray(view?.skillRoots).map((root) => ({
      ...root,
      removable: Boolean(root.removable),
      skillItems: asArray(root.skillItems),
    })),
  };
}

function sortServersForDisplay(servers: ServerView[]): ServerView[] {
  return [...servers].sort((a, b) => {
    const priority = serverDisplayPriority(a) - serverDisplayPriority(b);
    if (priority !== 0) return priority;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  });
}

function serverDisplayPriority(server: ServerView): number {
  if (server.status === "failed" || server.authStatus === "required") return 0;
  if (server.builtIn) return 1;
  if (server.status !== "disabled") return 2;
  return 3;
}

function skillListSummary(skills: SkillView[], filtered: SkillView[], searching: boolean, t: ReturnType<typeof useT>): string {
  if (searching) {
    return t("caps.skillsSummaryMatches", { matched: filtered.length, total: skills.length });
  }
  const parts = [t("caps.skillsSummaryAvailable", { skills: skills.length })];
  const scopes = ["project", "custom", "global", "builtin"];
  for (const scope of scopes) {
    const count = skills.filter((skill) => skill.scope === scope).length;
    if (count > 0) parts.push(skillScopeSummary(scope, count, t));
  }
  return parts.join(" · ");
}

function mcpServerSummary(servers: ServerView[], t: ReturnType<typeof useT>): string {
  return t("caps.mcpSummary", {
    connected: servers.filter((s) => s.status === "connected").length,
    failed: servers.filter((s) => s.status === "failed").length,
    tools: servers.reduce((total, server) => total + (server.tools || 0), 0),
  });
}

function skillScopeSummary(scope: string, count: number, t: ReturnType<typeof useT>): string {
  switch (scope) {
    case "builtin":
      return t("caps.skillsSummaryBuiltin", { count });
    case "project":
      return t("caps.skillsSummaryProject", { count });
    case "custom":
      return t("caps.skillsSummaryCustom", { count });
    case "global":
      return t("caps.skillsSummaryGlobal", { count });
    default:
      return `${count} ${scope}`;
  }
}

function skillSourceSummary(active: number, missing: number, empty: number, t: ReturnType<typeof useT>): string {
  const parts: string[] = [];
  if (active > 0) parts.push(t("caps.sourcesSummaryActive", { active }));
  if (missing > 0) parts.push(t("caps.sourcesSummaryMissing", { missing }));
  if (empty > 0) parts.push(t("caps.sourcesSummaryEmpty", { empty }));
  return parts.length > 0 ? parts.join(" · ") : t("caps.sourcesSummaryNone");
}

function SkillSources({
  roots,
  busy,
  onAdd,
  onRefresh,
  onRemove,
}: {
  roots: SkillRootView[];
  busy: boolean;
  onAdd: () => void;
  onRefresh: () => void;
  onRemove: (path: string) => void;
}) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const [showDiagnostics, setShowDiagnostics] = useState(false);
  const [expandedRootSkills, setExpandedRootSkills] = useState<Set<string>>(() => new Set());
  const [fullRootSkills, setFullRootSkills] = useState<Set<string>>(() => new Set());
  const primaryRoots = roots.filter(isPrimarySkillRoot);
  const diagnosticRoots = roots.filter((root) => !isPrimarySkillRoot(root));
  const diagnosticsVisible = expanded && showDiagnostics;
  const shownRoots = diagnosticsVisible ? [...primaryRoots, ...diagnosticRoots] : primaryRoots;
  const summaryRoots = diagnosticsVisible ? roots : primaryRoots;
  const active = summaryRoots.filter((root) => root.skills > 0).length;
  const missing = summaryRoots.filter((root) => root.status === "missing").length;
  const empty = summaryRoots.filter((root) => root.status === "ok" && root.skills === 0).length;
  const toggleRootSkills = (key: string) => {
    setExpandedRootSkills((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };
  const toggleRootSkillFull = (key: string) => {
    setFullRootSkills((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };
  return (
    <div className={`cap-sources${expanded ? " cap-sources--expanded" : ""}`}>
      <div className="cap-sources__head">
        <div className="cap-sources__copy">
          <div className="cap-sources__title">{t("caps.sources")}</div>
          <div className="cap-sources__summary">{skillSourceSummary(active, missing, empty, t)}</div>
        </div>
        {!expanded && (
          <div className="cap-sources__actions">
            <button className="btn btn--small" type="button" onClick={() => setExpanded(true)} aria-expanded={expanded}>
              {t("caps.manageSkillSources")}
            </button>
          </div>
        )}
      </div>
      {expanded && (
        <>
          <div className="cap-sources__manage">
            <div className="cap-sources__manage-actions">
              <button className="btn btn--small" disabled={busy} onClick={onRefresh}>
                {t("caps.refreshSkills")}
              </button>
              <button className="btn btn--small" disabled={busy} onClick={onAdd}>
                {t("caps.addSkillFolder")}
              </button>
            </div>
            <button
              className="btn btn--small"
              type="button"
              onClick={() => {
                setShowDiagnostics(false);
                setExpanded(false);
              }}
              aria-expanded={expanded}
            >
              {t("common.collapse")}
            </button>
          </div>
          {shownRoots.length === 0 ? (
            <div className="mem-empty">{t("caps.noSkillRoots")}</div>
          ) : (
            <div className="cap-source-list">
              {shownRoots.map((root) => {
                const key = skillRootKey(root);
                const rootSkills = root.skillItems ?? [];
                const rootSkillsExpanded = expandedRootSkills.has(key);
                const rootSkillsFull = fullRootSkills.has(key);
                const canShowRootSkills = rootSkills.length > 0;
                const canRemoveRoot = root.removable;
                return (
                  <div className={`cap-source cap-source--${skillRootTone(root)}`} key={key}>
                    <span className={`cap-dot cap-dot--${skillRootDot(root)}`} />
                    <div className="cap-source__text">
                      <div className="cap-source__head">
                        <div className="cap-source__label" title={root.dir}>
                          {skillRootLabel(root)}
                        </div>
                      </div>
                      <div className="cap-source__meta">
                        <span>{skillRootStatus(root, t)}</span>
                        <span>{t("caps.skillRootCount", { skills: root.skills })}</span>
                        {root.configured && <span>{t("caps.skillRootConfigured")}</span>}
                      </div>
                      {(canShowRootSkills || canRemoveRoot) && (
                        <div className="cap-source-actions">
                          <>
                            {canShowRootSkills && (
                              <button
                                className="btn btn--small"
                                disabled={busy}
                                type="button"
                                aria-expanded={rootSkillsExpanded}
                                onClick={() => toggleRootSkills(key)}
                              >
                                {rootSkillsExpanded ? t("caps.hideSkills") : t("caps.showSkills")}
                              </button>
                              )}
                              {canRemoveRoot && (
                                <InlineConfirmButton
                                  label={t("caps.skillRootRemove")}
                                  confirmLabel={t("caps.skillRootConfirmRemove")}
                                  cancelLabel={t("common.cancel")}
                                  disabled={busy}
                                  danger
                                  onConfirm={() => onRemove(root.dir)}
                                />
                              )}
                            </>
                        </div>
                      )}
                      {rootSkillsExpanded && rootSkills.length > 0 && (
                        <SkillRootSkillsList
                          skills={rootSkills}
                          showAll={rootSkillsFull}
                          onToggleAll={() => toggleRootSkillFull(key)}
                        />
                      )}
                      {root.warning && <div className="cap-source__warning">{root.warning}</div>}
                    </div>
                    <div className="cap-source__badges">
                      {skillRootBadges(root, t).map((badge) => (
                        <span className={`cap-source-badge cap-source-badge--${badge.tone}`} key={badge.label}>
                          {badge.label}
                        </span>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
          {diagnosticRoots.length > 0 && (
            <button className="cap-diagnostics" type="button" onClick={() => setShowDiagnostics((v) => !v)}>
              {diagnosticsVisible ? t("caps.hideDiagnostics") : t("caps.showDiagnostics", { count: diagnosticRoots.length })}
            </button>
          )}
        </>
      )}
    </div>
  );
}

const skillRootPreviewLimit = 5;

function SkillRootSkillsList({
  skills,
  showAll,
  onToggleAll,
}: {
  skills: SkillRootSkillView[];
  showAll: boolean;
  onToggleAll: () => void;
}) {
  const t = useT();
  const visible = showAll ? skills : skills.slice(0, skillRootPreviewLimit);
  return (
    <div className="cap-source-skills">
      {visible.map((skill) => (
        <div className="cap-source-skill" key={`${skill.scope}:${skill.name}`}>
          <div className="cap-source-skill__head">
            <span className="cap-source-skill__name">/{skill.name}</span>
            <span className="cap-source-skill__badges">
              <span className={`cap-skill-badge cap-skill-badge--${skill.scope}`}>{skillScopeLabel(skill.scope, t)}</span>
              {skill.runAs === "subagent" && <span className="cap-skill-badge cap-skill-badge--run">{t("caps.subagent")}</span>}
            </span>
          </div>
          {skill.description && <div className="cap-source-skill__desc">{skill.description}</div>}
        </div>
      ))}
      {skills.length > skillRootPreviewLimit && (
        <button className="cap-source-skills__more" type="button" onClick={onToggleAll}>
          {showAll ? t("common.collapse") : t("caps.skillRootShowAllSkills", { count: skills.length })}
        </button>
      )}
    </div>
  );
}

function skillRootKey(root: SkillRootView): string {
  return `${root.scope}:${root.priority}:${root.dir}`;
}

function isPrimarySkillRoot(root: SkillRootView): boolean {
  return root.skills > 0 || root.configured || Boolean(root.warning);
}

function skillRootTone(root: SkillRootView): "active" | "empty" | "problem" {
  if (root.warning || root.status === "inactive" || root.status === "unreadable") return "problem";
  if (root.skills > 0) return "active";
  return "empty";
}

function skillRootDot(root: SkillRootView): "connected" | "disabled" | "failed" {
  const tone = skillRootTone(root);
  if (tone === "active") return "connected";
  if (tone === "empty") return "disabled";
  return "failed";
}

function skillRootStatus(root: SkillRootView, t: ReturnType<typeof useT>): string {
  if (root.status === "ok" && root.skills > 0) return t("caps.skillRootActive");
  if (root.status === "ok") return t("caps.skillRootEmpty");
  return root.status;
}

function skillRootLabel(root: SkillRootView): string {
  return root.dir;
}

function skillRootBadges(root: SkillRootView, t: ReturnType<typeof useT>): Array<{ label: string; tone: "scope" | "builtin" | "configured" | "missing" }> {
  const badges: Array<{ label: string; tone: "scope" | "builtin" | "configured" | "missing" }> = [
    { label: skillScopeLabel(root.scope, t), tone: "scope" },
    root.scope === "custom"
      ? { label: root.configured ? t("caps.skillRootUserConfigured") : t("caps.skillRootConfiguredPath"), tone: "configured" }
      : { label: t("caps.skillRootBuiltinPath"), tone: "builtin" },
  ];
  if (root.status === "missing") {
    badges.push({ label: t("caps.skillRootMissing"), tone: "missing" });
  }
  return badges;
}

function ServerGroup({
  servers,
  expanded,
  expandedTools,
  busy,
  editing,
  onConfirm,
  onEdit,
  onCancelEdit,
  onRetry,
  onReconnect,
  onConfirmClearAuth,
  onToggle,
  onUpdate,
  onToggleDetails,
  onToggleTools,
}: {
  servers: ServerView[];
  expanded: Set<string>;
  expandedTools: Set<string>;
  busy: boolean;
  editing: string | null;
  onConfirm: (name: string) => void;
  onEdit: (name: string) => void;
  onCancelEdit: () => void;
  onRetry: (name: string) => void;
  onReconnect: (name: string) => void;
  onConfirmClearAuth: (name: string) => void;
  onToggle: (name: string, on: boolean) => void;
  onUpdate: (name: string, input: MCPServerInput) => void;
  onToggleDetails: (name: string) => void;
  onToggleTools: (name: string) => void;
}) {
  if (servers.length === 0) return null;
  return (
    <div className="cap-server-group">
      {servers.map((s) => (
        <ServerRow
          key={s.name}
          s={s}
          expanded={expanded.has(s.name)}
          toolsExpanded={expandedTools.has(s.name)}
          busy={busy}
          editing={editing === s.name}
          onConfirm={() => onConfirm(s.name)}
          onEdit={() => onEdit(s.name)}
          onCancelEdit={onCancelEdit}
          onRetry={() => onRetry(s.name)}
          onReconnect={() => onReconnect(s.name)}
          onConfirmClearAuth={() => onConfirmClearAuth(s.name)}
          onToggle={(on) => onToggle(s.name, on)}
          onUpdate={(input) => onUpdate(s.name, input)}
          onToggleDetails={() => onToggleDetails(s.name)}
          onToggleTools={() => onToggleTools(s.name)}
        />
      ))}
    </div>
  );
}

function FailedServersNotice({
  servers,
  expanded,
  busy,
  onToggle,
  onRetry,
  onRetryMany,
  onConfirmClearAuth,
  onConfirm,
  onConfirmMany,
}: {
  servers: ServerView[];
  expanded: Set<string>;
  busy: boolean;
  onToggle: (name: string) => void;
  onRetry: (name: string) => void;
  onRetryMany: (names: string[]) => void;
  onConfirmClearAuth: (name: string) => void;
  onConfirm: (name: string) => void;
  onConfirmMany: (names: string[]) => void;
}) {
  const t = useT();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [bulkOpen, setBulkOpen] = useState(false);
  const groups = useMemo(() => failureGroups(servers, t), [servers, t]);
  const removableFailures = useMemo(() => servers.filter(canBulkRemoveFailure), [servers]);
  const retryNames = useMemo(() => servers.map((s) => s.name), [servers]);
  return (
    <div className="cap-failures" role="region" aria-label={t("caps.failureTitle", { failed: servers.length })}>
      <div className="cap-failures__head">
        <div>
          <div className="cap-failures__title">{t("caps.failureTitle", { failed: servers.length })}</div>
          <div className="cap-failures__hint">{t("caps.failureHint")}</div>
        </div>
        <div className="cap-failures__actions">
          <button className="btn btn--small" disabled={busy} type="button" onClick={() => setDetailsOpen((v) => !v)} aria-expanded={detailsOpen}>
            {detailsOpen ? t("caps.hideFailureDetails") : t("caps.showFailureDetails")}
          </button>
          <button className="btn btn--small" disabled={busy || retryNames.length === 0} type="button" onClick={() => onRetryMany(retryNames)}>
            {t("caps.retryAll")}
          </button>
          {removableFailures.length > 0 && (
            <button className="btn btn--small" disabled={busy} type="button" onClick={() => setBulkOpen((v) => !v)} aria-expanded={bulkOpen}>
              {t("caps.bulkActions")}
            </button>
          )}
        </div>
      </div>
      <div className="cap-failures__meta">
        <div className="cap-failures__chips" aria-label={t("caps.failureGroups")}>
          {groups.map((group) => (
            <span className="cap-failure-chip" key={group.kind}>{group.label}</span>
          ))}
        </div>
      </div>
      {bulkOpen && removableFailures.length > 0 && (
        <div className="cap-failures__bulk">
          <InlineConfirmButton
            label={t("caps.removeInvalid", { count: removableFailures.length })}
            confirmLabel={t("caps.confirmRemoveInvalid", { count: removableFailures.length })}
            cancelLabel={t("common.cancel")}
            disabled={busy}
            danger
            onConfirm={() => onConfirmMany(removableFailures.map((s) => s.name))}
          />
        </div>
      )}
      {detailsOpen && <div className="cap-failures__list">
        {servers.map((s) => {
          const open = expanded.has(s.name);
          const error = s.error || t("caps.failed");
          const actionLabel = serverActionLabel(s, t);
          const handlePrimaryAction = () => {
            if (shouldOpenAuth(s)) {
              openExternal((s.authUrl || "").trim());
              return;
            }
            onRetry(s.name);
          };
          return (
            <div className="cap-failure" key={s.name}>
              <div className="cap-failure__main">
                <span className="cap-dot cap-dot--failed" />
                <div className="cap-failure__text">
                  <div className="cap-failure__name">{s.name}</div>
                  <div className="cap-failure__summary">{s.authStatus === "required" ? t("caps.authRequiredSummary") : summarizeServerError(error, t)}</div>
                </div>
              </div>
              <div className="cap-failure__actions">
                <button className="btn btn--small" disabled={busy} onClick={handlePrimaryAction}>
                  {actionLabel}
                </button>
                {canClearAuth(s) && (
                  <InlineConfirmButton
                    label={t("caps.clearAuth")}
                    confirmLabel={t("caps.confirmClearAuth")}
                    cancelLabel={t("common.cancel")}
                    disabled={busy}
                    onConfirm={() => onConfirmClearAuth(s.name)}
                  />
                )}
                <button className="btn btn--small" onClick={() => onToggle(s.name)} aria-expanded={open}>
                  {open ? t("common.collapse") : t("caps.showLog")}
                </button>
                {!s.builtIn && (
                  <InlineConfirmButton
                    label={t("caps.remove")}
                    confirmLabel={t("caps.confirmRemove")}
                    cancelLabel={t("common.cancel")}
                    disabled={busy}
                    danger
                    onConfirm={() => onConfirm(s.name)}
                  />
                )}
              </div>
              {open && (
                <div className="cap-failure__logbox">
                  <div className="cap-failure__logbar">
                    <span>{t("caps.rawLog")}</span>
                    <button className="btn btn--small" onClick={() => void navigator.clipboard?.writeText(error)}>
                      {t("caps.copyLog")}
                    </button>
                  </div>
                  <pre className="cap-failure__log">{error}</pre>
                </div>
              )}
            </div>
          );
        })}
      </div>}
    </div>
  );
}

function ServerRow({
  s,
  expanded,
  toolsExpanded,
  busy,
  editing,
  onConfirm,
  onEdit,
  onCancelEdit,
  onRetry,
  onReconnect,
  onConfirmClearAuth,
  onToggle,
  onUpdate,
  onToggleDetails,
  onToggleTools,
}: {
  s: ServerView;
  expanded: boolean;
  toolsExpanded: boolean;
  busy: boolean;
  editing: boolean;
  onConfirm: () => void;
  onEdit: () => void;
  onCancelEdit: () => void;
  onRetry: () => void;
  onReconnect: () => void;
  onConfirmClearAuth: () => void;
  onToggle: (on: boolean) => void;
  onUpdate: (input: MCPServerInput) => void;
  onToggleDetails: () => void;
  onToggleTools: () => void;
}) {
  const t = useT();
  const actionLabel = serverActionLabel(s, t);
  const tools = s.toolList ?? [];
  let sub =
    s.profileHidden
      ? t("caps.profileHidden")
      : s.status === "failed"
      ? s.error || t("caps.failed")
      : s.status === "initializing"
        ? t("caps.initializing")
      : s.status === "deferred"
        ? t("caps.deferred")
      : s.status === "disabled"
        ? s.configured && !s.autoStart
          ? t("caps.disabledAutoStart")
          : t("caps.disabled")
        : t("caps.counts", { tools: s.tools, prompts: s.prompts, resources: s.resources });
  if (s.authStatus === "possible" && s.status !== "failed") {
    sub = `${sub} · ${t("caps.authPossibleShort")}`;
  }
  const enabled = s.status === "connected" || s.status === "deferred" || s.status === "initializing";
  const handlePrimaryAction = () => {
    if (shouldOpenAuth(s)) {
      openExternal((s.authUrl || "").trim());
      return;
    }
    onRetry();
  };
  return (
    <div className={`cap-server-entry${s.status === "disabled" ? " cap-server-entry--disabled" : ""}`}>
      <Tooltip label={s.error} disabled={!s.error} fill block>
        <div className={`cap-row${s.status === "disabled" ? " cap-row--disabled" : ""}`}>
          <Tooltip label={expanded ? t("caps.collapseDetails") : t("caps.expandDetails")}>
            <button
              className="cap-disclosure"
              aria-expanded={expanded}
              onClick={onToggleDetails}
            >
              {expanded ? "⌄" : "›"}
            </button>
          </Tooltip>
          <span className={`cap-dot cap-dot--${s.status}`} />
          <div className="cap-row__text">
            <div className="cap-row__head">
              <span className="cap-row__name">{s.name}</span>
              <span className="cap-row__transport">{s.transport}</span>
              {s.builtIn && <span className="cap-row__builtin">{t("caps.builtIn")}</span>}
            </div>
            <div className="cap-row__sub">{sub}</div>
          </div>
          <div className="cap-row__actions">
            {s.status === "failed" ? (
              <button className="btn btn--small" disabled={busy} onClick={handlePrimaryAction}>
                {actionLabel}
              </button>
            ) : s.status === "initializing" ? (
              <span className="cap-row__pending">{t("caps.initializingShort")}</span>
            ) : (
              <Tooltip label={enabled ? t("caps.disable") : t("caps.enable")}>
                <label className="cap-switch">
                  <input
                    type="checkbox"
                    checked={enabled}
                    disabled={busy}
                    onChange={(e) => onToggle(e.target.checked)}
                  />
                  <span className="cap-switch__track" />
                </label>
              </Tooltip>
            )}
          </div>
        </div>
      </Tooltip>
      {expanded && (
        <ServerDetails
          s={s}
          tools={tools}
          busy={busy}
          onConfirm={onConfirm}
          onConnectNow={onRetry}
          onReconnect={onReconnect}
          onConfirmClearAuth={onConfirmClearAuth}
          toolsExpanded={toolsExpanded}
          editing={editing}
          onEdit={onEdit}
          onCancelEdit={onCancelEdit}
          onUpdate={onUpdate}
          onToggleTools={onToggleTools}
        />
      )}
    </div>
  );
}

function ServerDetails({
  s,
  tools,
  busy,
  onConfirm,
  onConnectNow,
  onReconnect,
  onConfirmClearAuth,
  toolsExpanded,
  editing,
  onEdit,
  onCancelEdit,
  onUpdate,
  onToggleTools,
}: {
  s: ServerView;
  tools: ServerView["toolList"];
  busy: boolean;
  onConfirm: () => void;
  onConnectNow: () => void;
  onReconnect: () => void;
  onConfirmClearAuth: () => void;
  toolsExpanded: boolean;
  editing: boolean;
  onEdit: () => void;
  onCancelEdit: () => void;
  onUpdate: (input: MCPServerInput) => void;
  onToggleTools: () => void;
}) {
  const t = useT();
  const command = serverCommand(s);
  const canEditConfig = s.configured && !s.builtIn;
  const canConnectNow = s.status === "deferred" || s.status === "disabled";
  const canReconnect = s.status === "connected";
  const canShowTools = (s.tools ?? 0) > 0 || (tools?.length ?? 0) > 0;
  const showClearAuth = canClearAuth(s);
  const authLabel = serverAuthLabel(s, t);
  if (editing && canEditConfig) {
    return (
      <div className="cap-server-details">
        <EditServerForm s={s} busy={busy} onCancel={onCancelEdit} onSave={onUpdate} />
      </div>
    );
  }
  return (
    <div className="cap-server-details">
      <div className="cap-detail-grid">
        <div className="cap-detail">
          <span className="cap-detail__label">{t("caps.status")}</span>
          <span className="cap-detail__value">{serverStatusLabel(s, t)}</span>
        </div>
        <div className="cap-detail">
          <span className="cap-detail__label">{t("caps.transport")}</span>
          <span className="cap-detail__value">{s.transport}</span>
        </div>
        {authLabel && (
          <div className="cap-detail">
            <span className="cap-detail__label">{t("caps.auth")}</span>
            <span className="cap-detail__value">{authLabel}</span>
          </div>
        )}
        {command && (
          <div className="cap-detail cap-detail--wide">
            <span className="cap-detail__label">{s.transport === "stdio" ? t("caps.command") : t("caps.url")}</span>
            <span className="cap-detail__code">{command}</span>
          </div>
        )}
        {s.envKeys && s.envKeys.length > 0 && (
          <div className="cap-detail cap-detail--wide">
            <span className="cap-detail__label">{t("caps.envKeys")}</span>
            <span className="cap-detail__value">{s.envKeys.join(", ")}</span>
          </div>
        )}
      </div>
      <div className="cap-detail-actions">
        {canConnectNow && (
          <button className="btn btn--small" disabled={busy} onClick={onConnectNow}>
            {t("caps.connectNow")}
          </button>
        )}
        {canReconnect && (
          <button className="btn btn--small" disabled={busy} onClick={onReconnect}>
            {t("caps.reconnect")}
          </button>
        )}
        {canShowTools && (
          <button className="btn btn--small" disabled={busy} onClick={onToggleTools} aria-expanded={toolsExpanded}>
            {toolsExpanded ? t("caps.hideTools") : t("caps.showTools")}
          </button>
        )}
        {showClearAuth && (
          <InlineConfirmButton
            label={t("caps.clearAuth")}
            confirmLabel={t("caps.confirmClearAuth")}
            cancelLabel={t("common.cancel")}
            disabled={busy}
            onConfirm={onConfirmClearAuth}
          />
        )}
        {canEditConfig && (
          <>
            <button className="btn btn--small" disabled={busy} onClick={onEdit}>
              {t("caps.editConfig")}
            </button>
            <InlineConfirmButton
              label={t("caps.remove")}
              confirmLabel={t("caps.confirmRemove")}
              cancelLabel={t("common.cancel")}
              disabled={busy}
              danger
              onConfirm={onConfirm}
            />
          </>
        )}
      </div>
      {toolsExpanded && (
        tools && tools.length > 0 ? (
          <div className="cap-tool-list">
            <div className="cap-tool-list__title">{t("caps.tools")}</div>
            {tools.map((tool) => (
              <div className="cap-tool" key={tool.name}>
                <div className="cap-tool__name">{tool.name}</div>
                {tool.description && <div className="cap-tool__desc">{tool.description}</div>}
              </div>
            ))}
          </div>
        ) : (
          <div className="cap-tool-empty">{t("caps.noToolDetails")}</div>
        )
      )}
    </div>
  );
}

function EditServerForm({
  s,
  busy,
  onCancel,
  onSave,
}: {
  s: ServerView;
  busy: boolean;
  onCancel: () => void;
  onSave: (input: MCPServerInput) => void;
}) {
  const t = useT();
  const initialTransport = normalizeTransportValue(s.transport);
  const [transport, setTransport] = useState(initialTransport);
  const [command, setCommand] = useState(initialTransport === "stdio" ? serverCommand(s) : "");
  const [url, setUrl] = useState(initialTransport === "stdio" ? "" : s.url || serverCommand(s));
  const [env, setEnv] = useState("");
  const isStdio = transport === "stdio";
  const ready = isStdio ? command.trim() !== "" : url.trim() !== "";

  const submit = () => {
    const parts = command.trim().split(/\s+/).filter(Boolean);
    const envText = env.trim();
    onSave({
      name: s.name,
      transport,
      command: isStdio ? (parts[0] ?? "") : "",
      args: isStdio ? parts.slice(1) : [],
      url: isStdio ? "" : url.trim(),
      env: envText === "" ? null : parseEnvText(envText),
    });
  };

  return (
    <div className="cap-config-edit">
      <div className="cap-detail-grid">
        <div className="cap-detail">
          <span className="cap-detail__label">{t("caps.name")}</span>
          <span className="cap-detail__value">{s.name}</span>
        </div>
        <label className="cap-detail cap-detail--select">
          <span className="cap-detail__label">{t("caps.transport")}</span>
          <select className="mem-select" value={transport} disabled={busy} onChange={(e) => setTransport(e.target.value)}>
            <option value="stdio">stdio</option>
            <option value="http">http</option>
            <option value="sse">sse</option>
          </select>
        </label>
        {isStdio ? (
          <label className="cap-detail cap-detail--wide">
            <span className="cap-detail__label">{t("caps.command")}</span>
            <input className="mem-input" value={command} disabled={busy} onChange={(e) => setCommand(e.target.value)} placeholder={t("caps.commandPlaceholder")} />
          </label>
        ) : (
          <label className="cap-detail cap-detail--wide">
            <span className="cap-detail__label">{t("caps.url")}</span>
            <input className="mem-input" value={url} disabled={busy} onChange={(e) => setUrl(e.target.value)} placeholder={t("caps.urlPlaceholder")} />
          </label>
        )}
        <label className="cap-detail cap-detail--wide">
          <span className="cap-detail__label">{t("caps.envLabel")}</span>
          <textarea className="mem-textarea cap-config-edit__env" value={env} disabled={busy} onChange={(e) => setEnv(e.target.value)} placeholder={t("caps.envPlaceholder")} spellCheck={false} />
        </label>
        {s.envKeys && s.envKeys.length > 0 && (
          <div className="cap-detail cap-detail--wide">
            <span className="cap-detail__label">{t("caps.envKeys")}</span>
            <span className="cap-detail__value">{s.envKeys.join(", ")}</span>
            <span className="cap-edit-hint">{t("caps.envPreserveHint")}</span>
          </div>
        )}
      </div>
      <div className="cap-detail-actions">
        <button className="btn btn--small" disabled={busy} onClick={onCancel}>
          {t("common.cancel")}
        </button>
        <button className="btn btn--primary btn--small" disabled={busy || !ready} onClick={submit}>
          {t("caps.saveConfig")}
        </button>
      </div>
    </div>
  );
}

function serverCommand(s: ServerView): string {
  if (s.transport === "stdio") return [s.command, ...(s.args ?? [])].filter(Boolean).join(" ").trim();
  return (s.url || "").trim();
}

function normalizeTransportValue(transport: string): string {
  return transport === "http" || transport === "sse" ? transport : "stdio";
}

function parseEnvText(env: string): Record<string, string> {
  const envMap: Record<string, string> = {};
  for (const line of env.split("\n")) {
    const eq = line.indexOf("=");
    if (eq > 0) envMap[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return envMap;
}

function serverStatusLabel(s: ServerView, t: ReturnType<typeof useT>): string {
  switch (s.status) {
    case "connected":
      return t("caps.connected");
    case "deferred":
      return t("caps.deferred");
    case "initializing":
      return t("caps.initializing");
    case "disabled":
      return s.configured && !s.autoStart ? t("caps.disabledAutoStart") : t("caps.disabled");
    case "failed":
      if (s.authStatus === "required") return t("caps.authRequired");
      return t("caps.failed");
    default:
      return s.status;
  }
}

function summarizeServerError(error: string, t: ReturnType<typeof useT>): string {
  const normalized = error.replace(/\s+/g, " ").trim();
  const plugin = normalized.match(/plugin "([^"]+)"/i)?.[1];
  if (plugin === "codegraph" && normalized.includes("context deadline exceeded")) {
    return t("caps.codegraphWarming");
  }
  const npmCode = normalized.match(/\bnpm error code ([A-Z0-9_]+)/i)?.[1];
  const errno = normalized.match(/\berrno (-?\d+)/i)?.[1];
  const reason = npmCode
    ? `npm ${npmCode}${errno ? ` (${errno})` : ""}`
    : normalized.split(/(?:\.\s+|\n)/)[0];
  const summary = plugin ? `${plugin}: ${reason}` : reason;
  return summary.length > 180 ? `${summary.slice(0, 176).trim()}…` : summary;
}

type FailureKind = "auth" | "missing-command" | "command-unavailable" | "network" | "other";

function failureKind(server: ServerView): FailureKind {
  if (server.authStatus === "required") return "auth";
  const err = (server.error || "").toLowerCase();
  if (err.includes("command is required")) return "missing-command";
  if (
    err.includes("command not found") ||
    err.includes("executable file not found") ||
    err.includes("no such file") ||
    err.includes("enoent")
  ) {
    return "command-unavailable";
  }
  if (
    err.includes("401") ||
    err.includes("403") ||
    err.includes("unauthorized") ||
    err.includes("forbidden") ||
    err.includes("timeout") ||
    err.includes("network")
  ) {
    return "network";
  }
  return "other";
}

function failureGroups(servers: ServerView[], t: ReturnType<typeof useT>): Array<{ kind: FailureKind; label: string }> {
  const counts = new Map<FailureKind, number>();
  for (const server of servers) {
    const kind = failureKind(server);
    counts.set(kind, (counts.get(kind) ?? 0) + 1);
  }
  const order: FailureKind[] = ["missing-command", "command-unavailable", "auth", "network", "other"];
  return order.flatMap((kind) => {
    const count = counts.get(kind) ?? 0;
    if (count === 0) return [];
    return [{ kind, label: failureGroupLabel(kind, count, t) }];
  });
}

function failureGroupLabel(kind: FailureKind, count: number, t: ReturnType<typeof useT>): string {
  switch (kind) {
    case "auth":
      return t("caps.failureGroupAuth", { count });
    case "missing-command":
      return t("caps.failureGroupMissingCommand", { count });
    case "command-unavailable":
      return t("caps.failureGroupCommandUnavailable", { count });
    case "network":
      return t("caps.failureGroupNetwork", { count });
    default:
      return t("caps.failureGroupOther", { count });
  }
}

function canBulkRemoveFailure(server: ServerView): boolean {
  if (server.builtIn || !server.configured) return false;
  const kind = failureKind(server);
  return kind === "missing-command" || kind === "command-unavailable";
}

function serverActionLabel(s: ServerView, t: ReturnType<typeof useT>): string {
  const err = (s.error || "").toLowerCase();
  if (shouldOpenAuth(s)) return t("caps.reauthorize");
  if (
    err.includes("command not found") ||
    err.includes("executable file not found") ||
    err.includes("no such file") ||
    err.includes("enoent")
  ) {
    return t("caps.checkCommand");
  }
  return t("caps.retry");
}

function serverAuthLabel(s: ServerView, t: ReturnType<typeof useT>): string {
  if (s.authStatus === "required") return t("caps.authRequired");
  if (s.authStatus === "possible") return t("caps.authPossible");
  return "";
}

function shouldOpenAuth(s: ServerView): boolean {
  const url = (s.authUrl || "").trim();
  return s.authStatus === "required" && /^https?:\/\//i.test(url);
}

function canClearAuth(s: ServerView): boolean {
  if (!s.configured || s.builtIn) return false;
  return Boolean(s.authConfigured || s.authStatus === "required" || s.authStatus === "possible" || isRemoteTransport(s.transport));
}

function isRemoteTransport(transport?: string): boolean {
  const value = (transport || "").trim().toLowerCase();
  return value === "http" || value === "streamable-http" || value === "sse";
}

function SkillRow({
  skill,
  busy,
  expanded,
  onToggle,
  onToggleEnabled,
  onUninstall,
  onDerive,
}: {
  skill: SkillView;
  busy: boolean;
  expanded: boolean;
  onToggle: () => void;
  onToggleEnabled: (enabled: boolean) => void;
  onUninstall?: () => void;
  onDerive?: () => void;
}) {
  const t = useT();
  const summary = summarizeSkillDescription(skill.description);
  const canExpand = summary !== skill.description;
  return (
    <div
      className={`cap-skill-card${expanded ? " cap-skill-card--expanded" : ""}${canExpand ? " cap-skill-card--expandable" : ""}${!skill.enabled ? " cap-skill-card--disabled" : ""}`}
    >
      <div className="cap-skill-card__top">
        <button className="cap-skill-card__toggle" type="button" onClick={onToggle} aria-expanded={expanded}>
          <span className="cap-skill-card__head">
            <span className="cap-skill-card__icon">/</span>
            <span className="cap-skill-card__main">
              <span className="cap-skill-card__command">{skill.name}</span>
              <span className="cap-skill-card__badges">
                <span className={`cap-skill-badge cap-skill-badge--${skill.scope}`}>{skillScopeLabel(skill.scope, t)}</span>
                {skill.runAs === "subagent" && <span className="cap-skill-badge cap-skill-badge--run">{t("caps.subagent")}</span>}
                {!skill.enabled && <span className="cap-skill-badge cap-skill-badge--off">{t("caps.skillDisabled")}</span>}
              </span>
            </span>
          </span>
        </button>
        <Tooltip label={skill.enabled ? t("caps.disableSkill") : t("caps.enableSkill")}>
          <label className="cap-switch">
            <input
              type="checkbox"
              checked={skill.enabled}
              disabled={busy}
              onChange={(e) => onToggleEnabled(e.target.checked)}
            />
            <span className="cap-switch__track" />
          </label>
        </Tooltip>
        {onUninstall && (
          <button className="btn btn--small btn--danger" style={{ marginLeft: "8px" }} disabled={busy} onClick={(e) => { e.stopPropagation(); onUninstall(); }}>
            {t("caps.uninstall")}
          </button>
        )}
        {onDerive && (
          <Tooltip label={t("caps.deriveSkillHint")}>
            <button className="btn btn--small" style={{ marginLeft: "8px" }} disabled={busy} onClick={(e) => { e.stopPropagation(); onDerive(); }}>
              {t("caps.deriveSkill")}
            </button>
          </Tooltip>
        )}
      </div>
      <div className="cap-skill-card__desc">
        {expanded ? skill.description : summary}
        {expanded && skill.installedFrom && (
          <div style={{ marginTop: 8, fontSize: "13px", color: "var(--text-muted)", wordBreak: "break-all" }}>
            {t("caps.skillInstalledFrom")}: <a href={skill.installedFrom} target="_blank" rel="noreferrer" style={{ color: "inherit", textDecoration: "underline" }}>{skill.installedFrom}</a>
          </div>
        )}
      </div>
      {canExpand && (
        <button className="cap-skill-card__more" type="button" onClick={onToggle} aria-expanded={expanded}>
          {expanded ? t("common.collapse") : t("common.expand")}
        </button>
      )}
    </div>
  );
}

function skillScopeLabel(scope: string, t: ReturnType<typeof useT>): string {
  switch (scope) {
    case "builtin":
      return t("caps.skillScopeBuiltin");
    case "project":
      return t("caps.skillScopeProject");
    case "custom":
      return t("caps.skillScopeCustom");
    case "global":
      return t("caps.skillScopeGlobal");
    default:
      return scope;
  }
}

function summarizeSkillDescription(description: string): string {
  const normalized = description.replace(/\s+/g, " ").trim();
  if (normalized.length <= 132) return normalized;
  const sentence = normalized.match(/^.{48,132}?[。.!?；;，,]/u)?.[0]?.trim();
  if (sentence && sentence.length >= 48) return sentence.replace(/[。.!?；;，,]$/u, "");
  return `${normalized.slice(0, 128).trim()}…`;
}

function AddServerForm({
  busy,
  onCancel,
  onAdd,
}: {
  busy: boolean;
  onCancel: () => void;
  onAdd: (input: MCPServerInput) => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [transport, setTransport] = useState("stdio");
  const [command, setCommand] = useState("");
  const [url, setUrl] = useState("");
  const [env, setEnv] = useState("");

  const isStdio = transport === "stdio";
  const ready = name.trim() !== "" && (isStdio ? command.trim() !== "" : url.trim() !== "");

  const submit = () => {
    const parts = command.trim().split(/\s+/).filter(Boolean);
    const envMap: Record<string, string> = {};
    for (const line of env.split("\n")) {
      const eq = line.indexOf("=");
      if (eq > 0) envMap[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }
    onAdd({
      name: name.trim(),
      transport,
      command: isStdio ? (parts[0] ?? "") : "",
      args: isStdio ? parts.slice(1) : [],
      url: isStdio ? "" : url.trim(),
      env: envMap,
    });
  };

  return (
    <div className="prov-card prov-card--edit">
      <div style={{ padding: "12px", backgroundColor: "var(--bg-2)", borderRadius: "var(--radius-md)", marginBottom: "16px", fontSize: "13px", lineHeight: "1.5", color: "var(--fg-2)" }}>
        {t("caps.mcpMarketTipBefore")} <a href="https://smithery.ai" target="_blank" rel="noreferrer" style={{ color: "var(--accent)", textDecoration: "none" }}>Smithery.ai</a> / <a href="https://mcpmarket.cn" target="_blank" rel="noreferrer" style={{ color: "var(--accent)", textDecoration: "none" }}>mcpmarket.cn</a> {t("caps.mcpMarketTipAfter")}
      </div>
      <input className="mem-input" placeholder={t("caps.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
      <label className="set-label">{t("caps.transport")}</label>
      <select className="mem-select" value={transport} onChange={(e) => setTransport(e.target.value)}>
        <option value="stdio">stdio</option>
        <option value="http">http</option>
        <option value="sse">sse</option>
      </select>
      {isStdio ? (
        <input className="mem-input" placeholder={t("caps.commandPlaceholder")} value={command} onChange={(e) => setCommand(e.target.value)} />
      ) : (
        <input className="mem-input" placeholder={t("caps.urlPlaceholder")} value={url} onChange={(e) => setUrl(e.target.value)} />
      )}
      <label className="set-label">{t("caps.envLabel")}</label>
      <textarea className="mem-textarea" value={env} onChange={(e) => setEnv(e.target.value)} placeholder={t("caps.envPlaceholder")} spellCheck={false} />
      <div className="prov-card__actions">
        <button className="btn btn--small" onClick={onCancel} disabled={busy}>
          {t("common.cancel")}
        </button>
        <button className="btn btn--primary btn--small" onClick={submit} disabled={busy || !ready}>
          {t("caps.add")}
        </button>
      </div>
    </div>
  );
}

// MCPServersSettingsPage is a self-contained MCP servers management page
// embedded inside the settings centre.
export function MCPServersSettingsPage({ initialHighlight }: { initialHighlight?: string }) {
	const t = useT();
	const [view, setView] = useState<CapabilitiesView | null>(null);
	const [busy, setBusy] = useState(false);
	const [err, setErr] = useState<string | null>(null);
	const [adding, setAdding] = useState(false);
	const [editing, setEditing] = useState<string | null>(null);
	const [expandedErrors, setExpandedErrors] = useState<Set<string>>(() => new Set());
	const [expandedServers, setExpandedServers] = useState<Set<string>>(() => new Set(initialHighlight ? [initialHighlight] : []));
	const [expandedServerTools, setExpandedServerTools] = useState<Set<string>>(() => new Set());
	const [mcpSubtab, setMcpSubtab] = useState<"builtin" | "market">("builtin");

	const reload = useCallback(async () => {
		setView(normalizeCapabilitiesView(await app.Capabilities().catch(() => ({ servers: [], skills: [], skillRoots: [] }))));
	}, []);
	useEffect(() => { void reload(); }, [reload]);
	useEffect(() => {
		if (!view || !view.servers.some((s) => s.status === "initializing" || s.status === "deferred")) return;
		const id = window.setInterval(() => void reload(), 2500);
		return () => window.clearInterval(id);
	}, [reload, view]);

	const mutate = async (fn: () => Promise<unknown>) => {
		setBusy(true);
		setErr(null);
		try {
			await fn();
			await reload();
			return true;
		} catch (e) {
			setErr(String((e as Error)?.message ?? e));
			await reload();
			return false;
		} finally {
			setBusy(false);
		}
	};

	const serverGroups = useMemo(() => {
		const servers = sortServersForDisplay(view?.servers ?? []);
		return {
			failed: servers.filter((s) => s.status === "failed"),
			active: servers.filter((s) => s.status !== "failed"),
		};
	}, [view]);

	const toggleError = useCallback((name: string) => {
		setExpandedErrors((prev) => { const next = new Set(prev); if (next.has(name)) next.delete(name); else next.add(name); return next; });
	}, []);
	const toggleServer = useCallback((name: string) => {
		setExpandedServers((prev) => { const next = new Set(prev); if (next.has(name)) next.delete(name); else next.add(name); return next; });
	}, []);
	const toggleServerTools = useCallback((name: string) => {
		setExpandedServerTools((prev) => { const next = new Set(prev); if (next.has(name)) next.delete(name); else next.add(name); return next; });
	}, []);

	const summary = useMemo(() => {
		if (!view) return "";
		return mcpServerSummary(view.servers, t);
	}, [view, t]);

	if (!view) return <div className="empty">{t("caps.loading")}</div>;

	return (
		<section className="mem-section">
			{err && serverGroups.failed.length === 0 && <div className="banner banner--error">{err}</div>}
			<div className="settings-subtabs">
				<button type="button" className={`settings-subtab${mcpSubtab === "builtin" ? " settings-subtab--active" : ""}`} aria-selected={mcpSubtab === "builtin"} onClick={() => setMcpSubtab("builtin")}>{t("caps.mcpTabBuiltin")}</button>
				<button type="button" className={`settings-subtab${mcpSubtab === "market" ? " settings-subtab--active" : ""}`} aria-selected={mcpSubtab === "market"} onClick={() => setMcpSubtab("market")}>{t("caps.mcpTabMarket")}</button>
			</div>
			{mcpSubtab === "market" ? (
				<McpMarketSection busy={busy} installedNames={new Set(view.servers.map((s) => s.name))} onInstalled={() => void reload()} />
			) : (
				<>
					<div className="cap-mcp-toolbar">
				{view.servers.length > 0 ? <div className="drawer__summary">{summary}</div> : <span />}
				<div className="cap-mcp-toolbar__actions">
					{!adding && (
						<button className="btn btn--small" disabled={busy} onClick={() => setAdding(true)}>
							{t("caps.addServer")}
						</button>
					)}
				</div>
			</div>
			{serverGroups.failed.length > 0 && (
						<FailedServersNotice
							servers={serverGroups.failed}
							expanded={expandedErrors}
							busy={busy}
							onToggle={toggleError}
							onRetry={(name) => void mutate(() => app.ReconnectMCPServer(name))}
							onRetryMany={(names) => void mutate(() => Promise.allSettled(names.map((name) => app.ReconnectMCPServer(name))))}
							onConfirmClearAuth={(name) => void mutate(() => app.ClearMCPServerAuthentication(name))}
							onConfirm={(name) => void mutate(() => app.RemoveMCPServer(name))}
							onConfirmMany={(names) => void mutate(() => Promise.allSettled(names.map((name) => app.RemoveMCPServer(name))))}
						/>
					)}
					{view.servers.length === 0 && !adding && (
						<div className="mem-empty">{t("caps.noServers")}</div>
					)}
					{serverGroups.active.length > 0 && (
						<div className="cap-server-section">
							<div className="cap-server-section__title">{t("caps.availableServers")}</div>
							<ServerGroup
								busy={busy}
								servers={serverGroups.active}
								expanded={expandedServers}
								expandedTools={expandedServerTools}
								editing={editing}
								onConfirm={(name) => void mutate(() => app.RemoveMCPServer(name))}
								onEdit={(name) => { setEditing(name); }}
								onCancelEdit={() => setEditing(null)}
								onRetry={(name) => void mutate(() => app.ReconnectMCPServer(name))}
								onReconnect={(name) => void mutate(() => app.ReconnectMCPServer(name))}
								onConfirmClearAuth={(name) => void mutate(() => app.ClearMCPServerAuthentication(name))}
								onToggle={(name, on) => void mutate(() => app.SetMCPServerEnabled(name, on))}
								onUpdate={(name, input) =>
									void mutate(() => app.UpdateMCPServer(name, input)).then((ok) => {
										if (ok) setEditing(null);
									})
								}
								onToggleDetails={toggleServer}
								onToggleTools={toggleServerTools}
							/>
						</div>
					)}
					{adding && (
						<div style={{ display: "flex", gap: "16px", flexWrap: "wrap", alignItems: "flex-start", marginTop: "16px" }}>
							<div style={{ flex: "1 1 300px" }}>
								<h3 className="cap-list__heading" style={{ marginBottom: "8px" }}>{t("caps.mcpAdvancedTitle")}</h3>
								<AddServerForm
									busy={busy}
									onCancel={() => setAdding(false)}
									onAdd={(input) => void mutate(() => app.AddMCPServer(input).then(() => setAdding(false)))}
								/>
							</div>
				</div>
			)}
				</>
			)}
		</section>
	);
}

// MarketCard is the unified shape McpMarketSection renders: builtin-curated
// entries (installRef = npx command) and official-Registry entries (command/args
// or url) collapse into one card carrying a closure that builds the MCPServerInput.
type MarketCard = {
	key: string;
	name: string;
	installName: string;
	desc: string;
	sourceLabel: string;
	transport?: string;
	installable: boolean;
	unavailableReason?: string;
	registryName?: string;
	buildInput: () => MCPServerInput;
};

// McpMarketSection is the MCP "remote marketplace" tab: browse the official MCP
// Registry and the builtin curated list, search, and install any server straight
// into config with one click — the same AddMCPServer path as manual add.
function McpMarketSection({
	busy,
	installedNames,
	onInstalled,
}: {
	busy: boolean;
	installedNames: Set<string>;
	onInstalled: () => void;
}) {
	const t = useT();
	const [query, setQuery] = useState("");
	const [source, setSource] = useState<"" | "builtin" | "registry">("");
	const [searching, setSearching] = useState(false);
	const [cards, setCards] = useState<MarketCard[] | null>(null);
	const [err, setErr] = useState<string | null>(null);
	const [installing, setInstalling] = useState<string | null>(null);
	const [installMsg, setInstallMsg] = useState<string | null>(null);
	const [registryNote, setRegistryNote] = useState<string | null>(null);
	const genRef = useRef(0);

	const sourceLabel = useCallback((id: string) => {
		switch (id) {
			case "builtin": return t("caps.mcpMarketSourceBuiltin");
			case "registry": return t("caps.mcpMarketSourceRegistry");
			default: return id;
		}
	}, [t]);

	const buildCards = useCallback(async (q: string, src: string): Promise<MarketCard[]> => {
		const out: MarketCard[] = [];
		if (src === "" || src === "builtin") {
			try {
				const entries = await app.SkillMarketSearch(q, "builtin-mcp");
				for (const e of (entries || [])) {
					const parts = (e.installRef || "").trim().split(/\s+/).filter(Boolean);
					const name = e.name;
					out.push({
						key: `builtin:${name}`,
						name,
						installName: name,
						desc: e.description,
						sourceLabel: sourceLabel("builtin"),
						installable: parts.length > 0,
						buildInput: () => ({ name, transport: "stdio", command: parts[0] || "npx", args: parts.slice(1), url: "" }),
					});
				}
			} catch { /* builtin is offline-curated; never blocks */ }
		}
		if (src === "" || src === "registry") {
			try {
				setRegistryNote(null);
				const view = await app.MCPRegistrySearch(q);
				if (view.warning) {
					setRegistryNote(view.cached ? t("caps.mcpMarketRegistryCached", { msg: view.warning }) : view.warning);
				}
				for (const e of (view.servers || [])) {
					const suggested = e.suggestedName || e.name;
					out.push({
						key: `registry:${e.name}`,
						name: e.title || suggested,
						installName: suggested,
						registryName: e.name,
						desc: e.description || "",
						sourceLabel: sourceLabel("registry"),
						transport: e.transport,
						installable: e.installable,
						unavailableReason: e.unavailableReason,
						buildInput: () => ({
							name: suggested,
							transport: e.transport === "http" || e.transport === "sse" ? e.transport : "stdio",
							command: e.command || "",
							args: e.args || [],
							url: e.url || "",
						}),
					});
				}
			} catch (e) {
				setErr(String((e as Error)?.message ?? e));
			}
		}
		return out;
	}, [sourceLabel, t]);

	const doSearch = useCallback(async (q: string, src: string) => {
		const gen = ++genRef.current;
		setSearching(true);
		setErr(null);
		setInstallMsg(null);
		setRegistryNote(null);
		try {
			const next = await buildCards(q, src);
			if (gen !== genRef.current) return; // a newer search superseded this one
			setCards(next);
		} finally {
			if (gen === genRef.current) setSearching(false);
		}
	}, [buildCards]);

	// Load on mount and whenever the selected source changes. Query is applied on
	// Enter / button click so the Registry isn't hit on every keystroke.
	useEffect(() => {
		void doSearch(query, source);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [doSearch, source]);

	const doInstall = useCallback(async (card: MarketCard) => {
		setInstalling(card.key);
		setErr(null);
		setInstallMsg(null);
		try {
			let input = card.buildInput();
			// Registry installs must use freshly fetched metadata, never the disk
			// cache — a cached package may have been removed or changed since store.
			if (card.registryName) {
				const fresh = await app.MCPRegistryResolve(card.registryName);
				input = {
					name: fresh.suggestedName || fresh.name,
					transport: fresh.transport === "http" || fresh.transport === "sse" ? fresh.transport : "stdio",
					command: fresh.command || "",
					args: fresh.args || [],
					url: fresh.url || "",
				};
			}
			await app.AddMCPServer(input);
			setInstallMsg(t("caps.mcpMarketInstalled", { name: card.name }));
			onInstalled();
		} catch (e) {
			setErr(t("caps.mcpMarketInstallFailed", { msg: String((e as Error)?.message ?? e) }));
		} finally {
			setInstalling(null);
		}
	}, [onInstalled, t]);

	const doUninstall = useCallback(async (card: MarketCard) => {
		setInstalling(card.key);
		setErr(null);
		setInstallMsg(null);
		try {
			await app.RemoveMCPServer(card.installName);
			setInstallMsg(t("caps.mcpMarketUninstalled", { name: card.name }));
			onInstalled(); // shared reload callback — refreshes installed badges
		} catch (e) {
			setErr(t("caps.mcpMarketUninstallFailed", { msg: String((e as Error)?.message ?? e) }));
		} finally {
			setInstalling(null);
		}
	}, [onInstalled, t]);

	return (
		<div className="cap-market" style={{ marginTop: "16px" }}>
			<div className="cap-search" style={{ marginBottom: "12px", display: "flex", gap: "8px" }}>
				<select className="mem-input" style={{ flex: "0 0 150px", width: "150px", margin: 0 }} value={source} onChange={(e) => setSource(e.target.value as "" | "builtin" | "registry")}>
					<option value="">{t("caps.mcpMarketSourceAll")}</option>
					<option value="builtin">{t("caps.mcpMarketSourceBuiltin")}</option>
					<option value="registry">{t("caps.mcpMarketSourceRegistry")}</option>
				</select>
				<input
					className="mem-input"
					style={{ flex: 1, margin: 0 }}
					type="search"
					placeholder={t("caps.mcpSearchPlaceholder")}
					value={query}
					onChange={(e) => setQuery(e.target.value)}
					onKeyDown={(e) => { if (e.key === "Enter") void doSearch(query, source); }}
				/>
				<button className="btn btn--small" style={{ margin: 0 }} disabled={searching} onClick={() => void doSearch(query, source)}>
					{searching ? t("caps.mcpSearching") : t("caps.mcpSearch")}
				</button>
			</div>
			{registryNote && <div className="banner" role="status" style={{ marginBottom: "8px" }}>{registryNote}</div>}
			{err && <div className="banner banner--error" role="alert" style={{ marginBottom: "8px" }}>{err}</div>}
			{installMsg && <div className="banner banner--success" style={{ marginBottom: "8px" }}>{installMsg}</div>}
			{searching && cards === null && <div className="mem-empty">{t("caps.loading")}</div>}
			{!searching && cards && cards.length === 0 && <div className="mem-empty">{t("caps.mcpNoResults")}</div>}
			{cards && cards.length > 0 && (
				<div className="cap-skills">
					{cards.map((card) => {
						const installed = installedNames.has(card.installName);
						return (
							<div key={card.key} className="cap-skill-card">
								<div className="cap-skill-card__head">
									<span className="cap-skill-card__name">{card.name}</span>
									{card.transport && <span className="cap-skill-badge">{card.transport}</span>}
									<div style={{ flex: 1 }} />
									{installed ? (
										<>
											<span className="cap-skill-badge cap-skill-badge--off">{t("caps.mcpMarketInstalledBadge")}</span>
											<InlineConfirmButton
												label={t("caps.uninstall")}
												confirmLabel={t("caps.confirmRemove")}
												cancelLabel={t("common.cancel")}
												disabled={busy || installing !== null}
												danger
												onConfirm={() => void doUninstall(card)}
											/>
										</>
									) : card.installable ? (
										<button
											className="btn btn--small btn--primary"
											disabled={busy || installing !== null}
											onClick={() => void doInstall(card)}
										>
											{installing === card.key ? t("caps.marketInstalling") : t("caps.marketInstall")}
										</button>
									) : (
										<span className="cap-skill-badge cap-skill-badge--off">{t("caps.mcpMarketManualSetup")}</span>
									)}
								</div>
								<div className="cap-skill-card__desc">
									{card.desc}
									{!card.installable && card.unavailableReason ? ` — ${card.unavailableReason}` : ""}
								</div>
								<div className="cap-skill-card__desc" style={{ opacity: 0.6 }}>{card.sourceLabel}</div>
							</div>
						);
					})}
				</div>
			)}
		</div>
	);
}

// SkillsSettingsPage is a self-contained skills management page embedded inside
// the settings centre.
export function SkillsSettingsPage({ initialHighlight }: { initialHighlight?: string }) {
	const t = useT();
	const [view, setView] = useState<CapabilitiesView | null>(null);
	const [busy, setBusy] = useState(false);
	const [err, setErr] = useState<string | null>(null);
	const [skillQuery, setSkillQuery] = useState(initialHighlight || "");
	const [expandedSkills, setExpandedSkills] = useState<Set<string>>(() => new Set(initialHighlight ? [initialHighlight] : []));
	const [skillSubtab, setSkillSubtab] = useState<"builtin" | "market">("builtin");
	const [pendingUninstall, setPendingUninstall] = useState<string | null>(null);
	const [successMsg, setSuccessMsg] = useState<string | null>(null);

	const reload = useCallback(async () => {
		setView(normalizeCapabilitiesView(await app.Capabilities().catch(() => ({ servers: [], skills: [], skillRoots: [] }))));
	}, []);
	useEffect(() => { void reload(); }, [reload]);

	const mutate = async (fn: () => Promise<unknown>) => {
		setBusy(true);
		setErr(null);
		try {
			await fn();
			await reload();
			return true;
		} catch (e) {
			setErr(String((e as Error)?.message ?? e));
			await reload();
			return false;
		} finally {
			setBusy(false);
		}
	};

	const confirmUninstall = useCallback(async () => {
		if (!pendingUninstall) return;
		const name = pendingUninstall;
		setPendingUninstall(null);
		const success = await mutate(() => app.SkillMarketUninstall(name, "global"));
		if (success) {
			setSuccessMsg(t("caps.marketUninstalled"));
			setTimeout(() => setSuccessMsg(null), 3000);
		}
	}, [pendingUninstall, mutate, t]);

	const filteredSkills = useMemo(() => {
		if (!view) return [];
		const q = skillQuery.trim().toLowerCase();
		if (!q) return view.skills;
		return view.skills.filter((sk) => {
			const text = [sk.name, "/" + sk.name, sk.description, sk.scope, sk.runAs].join(" ").toLowerCase();
			return text.includes(q);
		});
	}, [view, skillQuery]);

	// Group skills by whether they are IN EFFECT under the current product
	// profile, not by hardcoded names. The backend tags each skill with
	// `active` (true when the profile's whitelist surfaces it). This replaces
	// the old name-based "coding vs office" split which misclassified skills
	// (email/rag/schedule were dumped into "coding") and broke when a skill was
	// shadowed by a global override (ppt-auto showed as both builtin-office and
	// global). Grouping by `active` reflects the real prompt the model sees.
	const OFFICIAL_SKILLS = useMemo(() => new Set(["init", "explore", "research", "install-capability", "review", "security-review", "test", "document-auto", "email-auto", "schedule-auto", "knowledge-auto", "expert-auto", "browser-auto", "desktop-auto", "ppt-auto"]), []);
	const OFFICE_SKILLS = useMemo(() => new Set(["document-auto", "email-auto", "schedule-auto", "knowledge-auto", "expert-auto", "browser-auto", "desktop-auto", "ppt-auto"]), []);
	const OPS_SKILLS = useMemo(() => new Set(["netdev-help"]), []);

	const officialSkills = useMemo(
		() => filteredSkills.filter((sk) => sk.scope === "builtin" || OFFICIAL_SKILLS.has(sk.name)),
		[filteredSkills, OFFICIAL_SKILLS],
	);
	const activeOfficialSkills = useMemo(
		() => officialSkills.filter((sk) => sk.active !== false),
		[officialSkills],
	);
	const inactiveOfficialSkills = useMemo(
		() => officialSkills.filter((sk) => sk.active === false),
		[officialSkills],
	);

	const officeSkills = useMemo(
		() => activeOfficialSkills.filter((sk) => OFFICE_SKILLS.has(sk.name)),
		[activeOfficialSkills, OFFICE_SKILLS],
	);
	const opsSkills = useMemo(
		() => activeOfficialSkills.filter((sk) => OPS_SKILLS.has(sk.name)),
		[activeOfficialSkills, OPS_SKILLS],
	);
	const generalSkills = useMemo(
		() => activeOfficialSkills.filter((sk) => !OFFICE_SKILLS.has(sk.name) && !OPS_SKILLS.has(sk.name)),
		[activeOfficialSkills, OFFICE_SKILLS, OPS_SKILLS],
	);
	const userSkills = useMemo(
		() => filteredSkills.filter((sk) => sk.scope !== "builtin" && !OFFICIAL_SKILLS.has(sk.name)),
		[filteredSkills, OFFICIAL_SKILLS],
	);

	const skillSummary = useMemo(() => {
		if (!view) return "";
		return skillListSummary(userSkills, filteredSkills.filter((sk) => sk.scope !== "builtin"), skillQuery.trim().length > 0, t);
	}, [filteredSkills, skillQuery, t, view, userSkills]);

	const { showToast } = useToast();
	const deriveSkill = useCallback((name: string) => {
		void mutate(async () => {
			const path = await app.DeriveEditableSkill(name);
			if (path) showToast(t("caps.deriveSkillDone", { path }), "info");
		});
	}, [mutate, showToast, t]);

	const toggleSkill = useCallback((name: string) => {
		setExpandedSkills((prev) => { const next = new Set(prev); if (next.has(name)) next.delete(name); else next.add(name); return next; });
	}, []);

	if (!view) return <div className="empty">{t("caps.loading")}</div>;

	return (
		<section className="mem-section">
			{err && <div className="banner banner--error">{err}</div>}
			{successMsg && <div className="banner banner--success">{successMsg}</div>}
			<div className="settings-subtabs">
				<button
					type="button"
					className={`settings-subtab${skillSubtab === "builtin" ? " settings-subtab--active" : ""}`}
					aria-selected={skillSubtab === "builtin"}
					onClick={() => setSkillSubtab("builtin")}
				>
					{t("caps.skillTabBuiltin")}
				</button>
				<button
					type="button"
					className={`settings-subtab${skillSubtab === "market" ? " settings-subtab--active" : ""}`}
					aria-selected={skillSubtab === "market"}
					onClick={() => setSkillSubtab("market")}
				>
					{t("caps.skillTabMarket")}
				</button>
			</div>

			{skillSubtab === "market" ? (
				<SkillMarketSection installedNames={new Set(view?.skills.map((s) => s.name) ?? [])} />
			) : (
				<>
			<div className="cap-search" style={{ marginTop: "12px", marginBottom: "12px" }}>
				<input
					className="mem-input"
					type="search"
					placeholder={t("caps.searchSkills")}
					value={skillQuery}
					onChange={(e) => setSkillQuery(e.target.value)}
				/>
			</div>
			<SkillSources
				roots={view.skillRoots ?? []}
				busy={busy}
				onAdd={() => mutate(async () => {
					const path = await app.PickSkillFolder();
					if (path) await app.AddSkillPath(path);
				})}
				onRefresh={() => mutate(() => app.RefreshSkills())}
				onRemove={(path) => mutate(() => app.RemoveSkillPath(path))}
			/>

			{/* General development skills (active in the current mode). */}
			{generalSkills.length > 0 && (
				<div className="cap-market">
					<div className="cap-skills-head">
						<div className="cap-skills-head__copy">
							<div className="cap-skills-head__title">{t("caps.skillCategoryGeneral")}</div>
							<div className="cap-skills-head__summary">
								{t("caps.marketSummary", { on: generalSkills.filter((s) => s.enabled).length, total: generalSkills.length })}
							</div>
						</div>
					</div>
					<div className="cap-skills">
						{generalSkills.map((sk) => (
							<SkillRow
								key={sk.name}
								skill={sk}
								busy={busy}
								expanded={expandedSkills.has(sk.name)}
								onToggle={() => toggleSkill(sk.name)}
								onToggleEnabled={(enabled) => void mutate(() => app.SetSkillEnabled(sk.name, enabled))}
								onDerive={sk.scope === "builtin" ? () => deriveSkill(sk.name) : undefined}
							/>
						))}
					</div>
				</div>
			)}

			{/* Office automation skills (document/email/schedule/rag/browser/etc). */}
			{officeSkills.length > 0 && (
				<div className="cap-market">
					<div className="cap-skills-head">
						<div className="cap-skills-head__copy">
							<div className="cap-skills-head__title">{t("caps.skillCategoryOffice")}</div>
							<div className="cap-skills-head__summary">
								{t("caps.marketSummary", { on: officeSkills.filter((s) => s.enabled).length, total: officeSkills.length })}
							</div>
						</div>
					</div>
					<div className="cap-skills">
						{officeSkills.map((sk) => (
							<SkillRow
								key={sk.name}
								skill={sk}
								busy={busy}
								expanded={expandedSkills.has(sk.name)}
								onToggle={() => toggleSkill(sk.name)}
								onToggleEnabled={(enabled) => void mutate(() => app.SetSkillEnabled(sk.name, enabled))}
								onDerive={sk.scope === "builtin" ? () => deriveSkill(sk.name) : undefined}
							/>
						))}
					</div>
				</div>
			)}

			{/* Ops skills (netdev quick-reference card — active in netdev mode). */}
			{opsSkills.length > 0 && (
				<div className="cap-market">
					<div className="cap-skills-head">
						<div className="cap-skills-head__copy">
							<div className="cap-skills-head__title">{t("caps.skillCategoryOps")}</div>
							<div className="cap-skills-head__summary">
								{t("caps.marketSummary", { on: opsSkills.filter((s) => s.enabled).length, total: opsSkills.length })}
							</div>
						</div>
					</div>
					<div className="cap-skills">
						{opsSkills.map((sk) => (
							<SkillRow
								key={sk.name}
								skill={sk}
								busy={busy}
								expanded={expandedSkills.has(sk.name)}
								onToggle={() => toggleSkill(sk.name)}
								onToggleEnabled={(enabled) => void mutate(() => app.SetSkillEnabled(sk.name, enabled))}
								onDerive={sk.scope === "builtin" ? () => deriveSkill(sk.name) : undefined}
							/>
						))}
					</div>
				</div>
			)}

			{/* Built-in skills the current mode HIDES (profile whitelist). They're
			    not in the model's prompt; switching mode brings them back. Shown
			    greyed so the user understands the distinction. */}
			{inactiveOfficialSkills.length > 0 && (
				<div className="cap-market cap-market--inactive">
					<div className="cap-skills-head">
						<div className="cap-skills-head__copy">
							<div className="cap-skills-head__title">{t("caps.marketTitleInactive")}</div>
							<div className="cap-skills-head__summary">
								{t("caps.marketInactiveSummary", { count: inactiveOfficialSkills.length })}
							</div>
						</div>
					</div>
					<div className="cap-skills">
						{inactiveOfficialSkills.map((sk) => (
							<SkillRow
								key={sk.name}
								skill={sk}
								busy={busy}
								expanded={expandedSkills.has(sk.name)}
								onToggle={() => toggleSkill(sk.name)}
								onToggleEnabled={(enabled) => void mutate(() => app.SetSkillEnabled(sk.name, enabled))}
								onDerive={sk.scope === "builtin" ? () => deriveSkill(sk.name) : undefined}
							/>
						))}
					</div>
				</div>
			)}

			{/* User's own skills (project / global / custom): managed list. */}
			{userSkills.length > 0 && (
				<>
					<div className="cap-skills-head">
						<div className="cap-skills-head__copy">
							<div className="cap-skills-head__title">{t("caps.mySkills")}</div>
							<div className="cap-skills-head__summary">{skillSummary}</div>
						</div>
					</div>
					<div className="cap-skills">
						{userSkills.map((sk) => (
							<SkillRow
								key={sk.name}
								skill={sk}
								busy={busy}
								expanded={expandedSkills.has(sk.name)}
								onToggle={() => toggleSkill(sk.name)}
								onToggleEnabled={(enabled) => void mutate(() => app.SetSkillEnabled(sk.name, enabled))}
								onUninstall={sk.installedFrom ? () => setPendingUninstall(sk.name) : undefined}
							/>
						))}
					</div>
				</>
			)}
			</>
			)}
			{pendingUninstall && (
				<ConfirmModal
					title={t("caps.marketConfirmUninstall", { name: pendingUninstall })}
					message={t("caps.marketUninstallWarning", { name: pendingUninstall })}
					confirmLabel={t("caps.uninstall")}
					cancelLabel={t("common.cancel")}
					danger={true}
					onConfirm={() => void confirmUninstall()}
					onClose={() => setPendingUninstall(null)}
				/>
			)}
		</section>
	);
}

// SkillMarketSection is the marketplace browse/search/install UI embedded at
// the bottom of the skills page. Users search across all default sources
// (Anthropic, OpenAI, ClawHub, curated) and install any skill directly from
// the GUI — no need to go through the agent's natural-language flow.
function SkillMarketSection({ installedNames }: { installedNames: Set<string> }) {
	const t = useT();
	const [query, setQuery] = useState("");
	const [searchSource, setSearchSource] = useState("");
	const [searching, setSearching] = useState(false);
	const [results, setResults] = useState<CatalogEntry[] | null>(null);
	const [err, setErr] = useState<string | null>(null);
	const [installing, setInstalling] = useState<string | null>(null);
	const [installMsg, setInstallMsg] = useState<string | null>(null);
	const [pendingInstall, setPendingInstall] = useState<{ entry: CatalogEntry; plan: string } | null>(null);

	const sourceLabel = useCallback((sourceId: string) => {
		switch (sourceId) {
			case "builtin": return t("caps.sourceBuiltin", { defaultValue: "Curated" });
			case "clawhub": return "ClawHub";
			case "anthropic": return "Anthropic";
			case "openai": return "OpenAI";
			default: return sourceId;
		}
	}, [t]);

	const doSearch = useCallback(async (searchQuery: string, source: string) => {
		const q = searchQuery.trim();
		setSearching(true);
		setErr(null);
		setResults(null);
		try {
			let entries: CatalogEntry[] = [];
			if (q || source) {
				entries = await app.SkillMarketSearch(q, source);
			}
			setResults(entries);
		} catch (e) {
			setErr(String((e as Error)?.message ?? e));
		} finally {
			setSearching(false);
		}
	}, []);

	// Load builtins on mount
	useEffect(() => {
		void doSearch("", searchSource);
	}, [doSearch, searchSource]);

	const doInstall = useCallback(async (entry: CatalogEntry) => {
		if (!entry.installRef) return;
		setInstalling(entry.installRef);
		setInstallMsg(null);
		try {
			const plan = await app.SkillMarketInstall(entry.installRef, entry.name, "global", false);
			setPendingInstall({ entry, plan });
		} catch (e) {
			setInstallMsg(t("caps.marketInstallFailed", { msg: String((e as Error)?.message ?? e) }));
		} finally {
			setInstalling(null);
		}
	}, [t]);

	const confirmInstall = useCallback(async () => {
		if (!pendingInstall) return;
		const { entry } = pendingInstall;
		setPendingInstall(null);
		setInstalling(entry.installRef);
		try {
			const result = await app.SkillMarketInstall(entry.installRef, entry.name, "global", true);
			setInstallMsg(t("caps.marketInstalledWithResult", { result }));
		} catch (e) {
			setInstallMsg(t("caps.marketInstallFailed", { msg: String((e as Error)?.message ?? e) }));
		} finally {
			setInstalling(null);
		}
	}, [pendingInstall, t]);

	return (
		<div className="cap-market" style={{ marginTop: "24px" }}>
			<div className="cap-skills-head">
				<div className="cap-skills-head__copy">
					<div className="cap-skills-head__title">{t("caps.marketBrowse")}</div>
				</div>
			</div>
			<div className="cap-search" style={{ marginBottom: "12px", display: "flex", gap: "8px" }}>
				<select
					className="mem-input"
					style={{ width: "160px", margin: 0 }}
					value={searchSource}
					onChange={(e) => setSearchSource(e.target.value)}
				>
					<option value="">{t("common.all", { defaultValue: "All Sources" })}</option>
					<option value="clawhub">ClawHub</option>
					<option value="anthropic">Anthropic</option>
					<option value="openai">OpenAI</option>
				</select>
				<input
					className="mem-input"
					style={{ flex: 1, margin: 0 }}
					type="search"
					placeholder={t("caps.marketSearchPlaceholder")}
					value={query}
					onChange={(e) => setQuery(e.target.value)}
					onKeyDown={(e) => { if (e.key === "Enter") void doSearch(query, searchSource); }}
				/>
				<button
					className="btn btn--small"
					style={{ margin: 0 }}
					disabled={searching}
					onClick={() => void doSearch(query, searchSource)}
				>
					{searching ? t("caps.marketSearching") : t("caps.marketSearch")}
				</button>
			</div>
			{err && <div className="banner banner--error" style={{ marginBottom: "8px" }}>{err}</div>}
			{installMsg && <div className="banner" style={{ marginBottom: "8px" }}>{installMsg}</div>}
			{results !== null && (
				<>
					{results.length === 0 ? (
						<div className="mem-empty">{t("caps.marketNoResults")}</div>
					) : (
						<div className="cap-skills">
							{results.length > 0 && (
								<>
									<h3 className="cap-list__heading" style={{ marginTop: 16 }}>{t("caps.skillCommunity")}</h3>
									{results.map((e, i) => (
										<div key={`sr-${e.name}-${i}`} className="cap-skill-card">
											<div className="cap-skill-card__head">
												<span className="cap-skill-card__name">{e.name}</span>
												<span className="cap-skill-badge">{sourceLabel(e.source)}</span>
												{e.author && <span className="cap-skill-badge cap-skill-badge--off">{e.author}</span>}
												{e.installs > 0 && (
													<span className="cap-skill-badge cap-skill-badge--off">
														{t("caps.marketInstalls", { n: e.installs })}
													</span>
												)}
												<div style={{ flex: 1 }} />
												{installedNames.has(e.name) ? (
													<button className="btn btn--small" disabled>
														✓ {t("caps.marketAlreadyInstalled")}
													</button>
												) : (
													<button
														className="btn btn--small btn--primary"
														disabled={installing === e.installRef || !e.installRef}
														onClick={() => void doInstall(e)}
													>
														{installing === e.installRef ? t("caps.marketInstalling") : t("caps.marketInstall")}
													</button>
												)}
											</div>
											<div className="cap-skill-card__desc">{e.description}</div>
										</div>
									))}
								</>
							)}
						</div>
					)}
				</>
			)}
			{pendingInstall && (
				<ConfirmModal
					title={t("caps.marketConfirmInstall", { name: pendingInstall.entry.name })}
					message={pendingInstall.plan}
					confirmLabel={t("caps.marketInstall")}
					cancelLabel={t("common.cancel")}
					danger={false}
					onConfirm={() => void confirmInstall()}
					onClose={() => setPendingInstall(null)}
				/>
			)}
		</div>
	);
}

