import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { asArray } from "../lib/array";
import { app, openExternal } from "../lib/bridge";
import { useToast } from "../lib/toast";
import { useT } from "../lib/i18n";
import * as skillDescLib from "../lib/skillDesc";
import type { CapabilitiesView, CatalogEntry, MarketSourceMeta, MCPServerInput, ServerView, SkillRootSkillView, SkillRootView, SkillView } from "../lib/types";
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
  const { showToast } = useToast();
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
      // Match the raw backend description AND the UI-language overlay copy, so
      // a zh keyword like 数字员工 finds browser-auto (SKILL_DESC_DISPLAY_SPEC R3).
      const text = [sk.name, `/${sk.name}`, sk.description, skillDescLib.skillDisplayDescription(sk.name, ""), sk.scope, sk.runAs].join(" ").toLowerCase();
      return text.includes(q);
    });
  }, [view, skillQuery]);
  const skillSummary = useMemo(() => {
    if (!view) return "";
    return skillListSummary(view.skills, filteredSkills, skillQuery.trim().length > 0, t);
  }, [filteredSkills, skillQuery, t, view]);

  const serverGroups = useMemo(() => {
    const servers = sortServersForDisplay(view?.servers ?? []);
    // profileHidden servers (gated out by the active tab's profile — e.g.
    // codegraph in office/ops mode) get their own visibly-labelled group:
    // mixing them into the main list as disabled-looking rows read as "the
    // built-in disappeared".
    const visible = servers.filter((s) => !s.profileHidden);
    return {
      failed: visible.filter((s) => s.status === "failed"),
      active: visible.filter((s) => s.status !== "failed"),
      hidden: servers.filter((s) => s.profileHidden),
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
                            showToast(t("caps.mcpImported", { n: String(n) }), "info");
                            void reload();
                          } catch (e) {
                            showToast(String(e), "error");
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
                  <div className="mem-empty">{emptyServersLabel(view.session, t)}</div>
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
                {serverGroups.hidden.length > 0 && (
                  <div className="cap-server-section">
                    <div className="cap-server-section__title">{t("caps.hiddenServersTitle")}</div>
                    <ServerGroup
                      busy={busy}
                      servers={serverGroups.hidden}
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
                  <div className="mem-empty">{emptySkillsLabel(view.session, t)}</div>
                ) : filteredSkills.length === 0 ? (
                  <div className="mem-empty">{t("caps.noSkillMatches")}</div>
                ) : (
                  <div className="cap-skill-grid">
                    {filteredSkills.map((sk) => (
                      <SkillTile
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
    session: view?.session,
  };
}

function sortServersForDisplay(servers: ServerView[]): ServerView[] {
  return [...servers].sort((a, b) => {
    const priority = serverDisplayPriority(a) - serverDisplayPriority(b);
    if (priority !== 0) return priority;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  });
}

// Session-aware empty-state copy: an empty list means different things
// depending on where Capabilities() got (or didn't get) its data — nothing
// configured vs session not built vs remote-managed. Keeps users from reading
// an unbuilt tab's empty page as "the built-ins were removed".
function emptyServersLabel(session: string | undefined, t: ReturnType<typeof useT>): string {
  if (session === "none") return t("caps.noServersSession");
  if (session === "remote") return t("caps.noServersRemote");
  return t("caps.noServers");
}

function emptySkillsLabel(session: string | undefined, t: ReturnType<typeof useT>): string {
  if (session === "none") return t("caps.noSkillsSession");
  if (session === "remote") return t("caps.noSkillsRemote");
  return t("caps.noSkills");
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

// Domain-bucket classification (2026-09-15 rework): the backend stamps every
// skill with `domain` (frontmatter `domain:` for file skills, the roster for
// builtins) plus `executor`; the name fallback in lib/skillDesc.ts only covers
// derived copies and older released files whose frontmatter predates domain
// tagging. File skills that are not official-name copies fall into the
// self-orchestrated group (自编排技能): browser-flow / browser-ops → 浏览器,
// pentest → 安全渗透, everything else (market installs, misc) → 其他.
const { OFFICIAL_SKILL_DOMAIN, LEGACY_PENTEST_SKILLS } = skillDescLib;

type SkillBucket = "coding" | "office" | "ops" | "general" | "self-browser" | "self-pentest" | "self-other";

function skillBucketOf(sk: SkillView): SkillBucket {
	const domain = (sk.domain || "").toLowerCase();
	if (sk.scope === "builtin" || OFFICIAL_SKILL_DOMAIN[sk.name] !== undefined) {
		switch (domain || OFFICIAL_SKILL_DOMAIN[sk.name] || "") {
			case "code": return "coding";
			case "office": return "office";
			case "netdev": return "ops";
			default: return "general";
		}
	}
	if (LEGACY_PENTEST_SKILLS.has(sk.name)) return "self-pentest";
	const executor = (sk.executor || "").toLowerCase();
	if (executor === "browser-flow" || domain === "browser-ops") return "self-browser";
	if (domain === "pentest" || executor === "pentest-flow") return "self-pentest";
	return "self-other";
}

// SkillDomainSection is one bucket of the builtin tab (coding / office / ops /
// general, or a self-orchestrated subgroup when `subgroup` is set). The bucket
// always lists its skills: mode-deactivated ones stay in place, greyed with an
// inactivity badge — the catalogue reads by domain, not by "what the current
// mode happens to enable". Layout is a compact card grid (expert-team style);
// clicking a tile expands its detail as a full-width row inside the grid.
function SkillDomainSection({
	title,
	subgroup,
	skills,
	busy,
	expandedSkills,
	onToggle,
	onToggleEnabled,
	onDerive,
	onDelete,
}: {
	title: string;
	subgroup?: boolean;
	skills: SkillView[];
	busy: boolean;
	expandedSkills: Set<string>;
	onToggle: (name: string) => void;
	onToggleEnabled: (name: string, enabled: boolean) => void;
	onDerive: (name: string) => void;
	onDelete: (skill: SkillView) => void;
}) {
	const t = useT();
	if (skills.length === 0) return null;
	const activeCount = skills.filter((s) => s.active !== false).length;
	return (
		<div className={subgroup ? "cap-market cap-market--subgroup" : "cap-market"}>
			<div className="cap-skills-head">
				<div className="cap-skills-head__copy">
					<div className={`cap-skills-head__title${subgroup ? " cap-skills-head__title--sub" : ""}`}>{title}</div>
					<div className="cap-skills-head__summary">
						{t("caps.domainSummary", { active: String(activeCount), total: String(skills.length) })}
					</div>
				</div>
			</div>
			<div className="cap-skill-grid">
				{skills.map((sk) => (
					<SkillTile
						key={sk.name}
						skill={sk}
						busy={busy}
						expanded={expandedSkills.has(sk.name)}
						onToggle={() => onToggle(sk.name)}
						onToggleEnabled={(enabled) => onToggleEnabled(sk.name, enabled)}
						onDerive={sk.scope === "builtin" ? () => onDerive(sk.name) : undefined}
						onDelete={(sk.scope === "global" || sk.scope === "project") ? () => onDelete(sk) : undefined}
					/>
				))}
			</div>
		</div>
	);
}

// SkillTile is one compact skill card in the grid. Collapsed: name + enable
// switch, a 2-line description clamp, and status badges. Expanded: full-width
// detail row with the complete description, install provenance, and the
// destructive/derivation actions.
function SkillTile({
  skill,
  busy,
  expanded,
  onToggle,
  onToggleEnabled,
  onDelete,
  onDerive,
}: {
  skill: SkillView;
  busy: boolean;
  expanded: boolean;
  onToggle: () => void;
  onToggleEnabled: (enabled: boolean) => void;
  onDelete?: () => void;
  onDerive?: () => void;
}) {
  const t = useT();
  const description = skillDisplayDescription(skill, t);
  const summary = summarizeSkillDescription(description);
  const inactive = skill.active === false;
  const inactiveBadge = skill.inactiveReason === "draft" ? t("caps.skillDraft")
	: skill.inactiveReason === "domain" ? t("caps.skillNotActiveDomain")
	: t("caps.skillNotActiveMode");
  const inactiveHint = skill.inactiveReason === "domain"
	? t("caps.skillNotActiveDomainHint", { domain: skill.domain || "—" })
	: skill.inactiveReason === "draft" ? t("caps.skillDraftHint") : t("caps.skillNotActiveModeHint");
  const deleteLabel = skill.installedFrom ? t("caps.uninstall") : t("caps.deleteSkill");
  return (
    <div
      className={`cap-skill-tile${expanded ? " cap-skill-tile--expanded" : ""}${!skill.enabled ? " cap-skill-tile--disabled" : ""}${inactive ? " cap-skill-tile--inactive" : ""}`}
    >
      <div className="cap-skill-tile__head">
        <button className="cap-skill-tile__toggle" type="button" onClick={onToggle} aria-expanded={expanded} title={skill.name}>
          <span className="cap-skill-tile__command">/{skill.name}</span>
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
      </div>
      <button className="cap-skill-tile__desc" type="button" onClick={onToggle} aria-expanded={expanded}>
        {summary}
      </button>
      <div className="cap-skill-tile__foot">
        <span className={`cap-skill-badge cap-skill-badge--${skill.scope}`}>{skillScopeLabel(skill.scope, t)}</span>
        {skill.runAs === "subagent" && <span className="cap-skill-badge cap-skill-badge--run">{t("caps.subagent")}</span>}
        {!skill.enabled && <span className="cap-skill-badge cap-skill-badge--off">{t("caps.skillDisabled")}</span>}
        {skill.draft && (
          <Tooltip label={t("caps.skillDraftHint")}>
            <span className="cap-skill-badge cap-skill-badge--off">{t("caps.skillDraft")}</span>
          </Tooltip>
        )}
        {inactive && (
          <Tooltip label={inactiveHint}>
            <span className="cap-skill-badge cap-skill-badge--inactive">{inactiveBadge}</span>
          </Tooltip>
        )}
      </div>
      {expanded && (
        <div className="cap-skill-tile__detail">
          <div className="cap-skill-tile__detail-desc">{description}</div>
          {skill.installedFrom && (
            <div className="cap-skill-tile__detail-src">
              {t("caps.skillInstalledFrom")}: <a href={skill.installedFrom} target="_blank" rel="noreferrer" style={{ color: "inherit", textDecoration: "underline" }}>{skill.installedFrom}</a>
            </div>
          )}
          {(onDerive || onDelete) && (
            <div className="cap-skill-tile__detail-actions">
              {onDerive && (
                <Tooltip label={t("caps.deriveSkillHint")}>
                  <button className="btn btn--small" disabled={busy} onClick={(e) => { e.stopPropagation(); onDerive(); }}>
                    {t("caps.deriveSkill")}
                  </button>
                </Tooltip>
              )}
              {onDelete && (
                <button className="btn btn--small btn--danger" disabled={busy} onClick={(e) => { e.stopPropagation(); onDelete(); }}>
                  {deleteLabel}
                </button>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// skillDisplayDescription shows the skill's description in the UI language:
// `caps.skillDesc.<name>` locale copy for shipped/official skills (the only
// names that have keys — see lib/skillDesc.ts), falling back to the backend/
// on-disk description for everything else (self-orchestrated skills, market
// installs), so user edits to SKILL.md show through immediately.
function skillDisplayDescription(skill: SkillView, t: ReturnType<typeof useT>): string {
	void t; // t only pins reactivity: the tile re-renders on locale switch
	return skillDescLib.skillDisplayDescription(skill.name, skill.description);
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
		// Same three-way split as the drawer: profileHidden servers (gated out
		// by the active tab's profile) render in their own labelled group.
		const visible = servers.filter((s) => !s.profileHidden);
		return {
			failed: visible.filter((s) => s.status === "failed"),
			active: visible.filter((s) => s.status !== "failed"),
			hidden: servers.filter((s) => s.profileHidden),
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
						<div className="mem-empty">{emptyServersLabel(view.session, t)}</div>
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
					{serverGroups.hidden.length > 0 && (
						<div className="cap-server-section">
							<div className="cap-server-section__title">{t("caps.hiddenServersTitle")}</div>
							<ServerGroup
								busy={busy}
								servers={serverGroups.hidden}
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
				const res = await app.SkillMarketSearch(q, "builtin-mcp");
				for (const e of (res.entries || [])) {
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
	const [pendingUninstall, setPendingUninstall] = useState<{ skill: SkillView } | null>(null);
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

	// Delete/uninstall share one flow: both remove the skill files via
	// SkillMarketUninstall (scope decides the root); only the label and the
	// manifest bookkeeping differ.
	const confirmUninstall = useCallback(async () => {
		if (!pendingUninstall) return;
		const { skill } = pendingUninstall;
		setPendingUninstall(null);
		const scope = skill.scope === "project" ? "project" : "global";
		const success = await mutate(() => app.SkillMarketUninstall(skill.name, scope));
		if (success) {
			setSuccessMsg(t("caps.skillDeleted", { name: skill.name }));
			setTimeout(() => setSuccessMsg(null), 3000);
		}
	}, [pendingUninstall, mutate, t]);

	const filteredSkills = useMemo(() => {
		if (!view) return [];
		const q = skillQuery.trim().toLowerCase();
		if (!q) return view.skills;
		return view.skills.filter((sk) => {
			// Raw description + UI-language overlay copy (SKILL_DESC_DISPLAY_SPEC R3).
			const text = [sk.name, "/" + sk.name, sk.description, skillDescLib.skillDisplayDescription(sk.name, ""), sk.scope, sk.runAs].join(" ").toLowerCase();
			return text.includes(q);
		});
	}, [view, skillQuery]);

	// Group skills into domain buckets (coding / office / ops / general) plus
	// the self-orchestrated group (自编排技能: browser / pentest / other).
	// Classification is backend-driven (`domain` + `executor` on SkillView,
	// stamped from frontmatter or the builtin roster) with a name fallback for
	// derived copies and pre-tagging releases — see skillBucketOf. Mode-
	// deactivated skills stay in place inside their bucket, greyed with an
	// inactivity badge, so the catalogue reads by domain and the full roster
	// is always visible.
	const bucketed = useMemo(() => {
		const out: Record<SkillBucket, SkillView[]> = {
			coding: [], office: [], ops: [], general: [],
			"self-browser": [], "self-pentest": [], "self-other": [],
		};
		for (const sk of filteredSkills) out[skillBucketOf(sk)].push(sk);
		return out;
	}, [filteredSkills]);
	const {
		coding: codingSkills,
		office: officeSkills,
		ops: opsSkills,
		general: generalSkills,
		"self-browser": selfBrowserSkills,
		"self-pentest": selfPentestSkills,
		"self-other": selfOtherSkills,
	} = bucketed;
	const userSkills = useMemo(
		() => [...selfBrowserSkills, ...selfPentestSkills, ...selfOtherSkills],
		[selfBrowserSkills, selfPentestSkills, selfOtherSkills],
	);
	// Skills that actually enter the model's index right now — the too-many
	// warning counts these, not the whole catalogue.
	const activeEnabledCount = useMemo(
		() => view?.skills.filter((s) => s.enabled && s.active !== false).length ?? 0,
		[view?.skills],
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
			{/* Headline count for profile-hidden builtins was here — after the
			    domain rework the inactive skills render greyed INSIDE their
			    domain group, so a pointer banner is no longer needed. */}
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

			{/* Too-many-skills advisory: skill BODIES never enter the system
			    prompt (lazy load) and the pinned index is char-capped, but a
			    long index still makes model selection harder — nudge toward
			    disabling the unused tail. */}
			{activeEnabledCount > 25 && (
				<div className="banner" role="status" style={{ marginBottom: "12px" }}>
					{t("caps.skillCountWarn", { n: String(activeEnabledCount) })}
				</div>
			)}

			{/* Domain buckets: coding / office / ops / general. Each always lists
			    its skills — mode-deactivated ones render in place, greyed with a
			    badge — so the catalogue reads by domain, not by "what the
			    current mode happens to enable". */}
			<SkillDomainSection
				title={t("caps.skillCategoryCoding")}
				skills={codingSkills}
				busy={busy}
				expandedSkills={expandedSkills}
				onToggle={toggleSkill}
				onToggleEnabled={(name, enabled) => void mutate(() => app.SetSkillEnabled(name, enabled))}
				onDerive={deriveSkill}
				onDelete={(skill) => setPendingUninstall({ skill })}
			/>

			<SkillDomainSection
				title={t("caps.skillCategoryOffice")}
				skills={officeSkills}
				busy={busy}
				expandedSkills={expandedSkills}
				onToggle={toggleSkill}
				onToggleEnabled={(name, enabled) => void mutate(() => app.SetSkillEnabled(name, enabled))}
				onDerive={deriveSkill}
				onDelete={(skill) => setPendingUninstall({ skill })}
			/>

			<SkillDomainSection
				title={t("caps.skillCategoryOps")}
				skills={opsSkills}
				busy={busy}
				expandedSkills={expandedSkills}
				onToggle={toggleSkill}
				onToggleEnabled={(name, enabled) => void mutate(() => app.SetSkillEnabled(name, enabled))}
				onDerive={deriveSkill}
				onDelete={(skill) => setPendingUninstall({ skill })}
			/>

			{/* Cross-cutting officials (install-capability, schedule-auto, …). */}
			<SkillDomainSection
				title={t("caps.skillCategoryGeneral")}
				skills={generalSkills}
				busy={busy}
				expandedSkills={expandedSkills}
				onToggle={toggleSkill}
				onToggleEnabled={(name, enabled) => void mutate(() => app.SetSkillEnabled(name, enabled))}
				onDerive={deriveSkill}
				onDelete={(skill) => setPendingUninstall({ skill })}
			/>

			{/* Self-orchestrated skills (自编排技能): flow skills produced by the
			    orchestration engines (browser recording today, pentest flows
			    next) plus market installs / misc file skills, subgrouped by
			    domain. Derived builtin copies do NOT land here — they stay in
			    their official domain buckets above. */}
			{userSkills.length > 0 && (
				<>
					<div className="cap-skills-head">
						<div className="cap-skills-head__copy">
							<div className="cap-skills-head__title">{t("caps.mySkills")}</div>
							<div className="cap-skills-head__summary">{skillSummary}</div>
						</div>
					</div>
					<SkillDomainSection
						title={t("caps.selfOrchBrowser")}
						subgroup
						skills={selfBrowserSkills}
						busy={busy}
						expandedSkills={expandedSkills}
						onToggle={toggleSkill}
						onToggleEnabled={(name, enabled) => void mutate(() => app.SetSkillEnabled(name, enabled))}
						onDerive={deriveSkill}
						onDelete={(skill) => setPendingUninstall({ skill })}
					/>
					<SkillDomainSection
						title={t("caps.selfOrchPentest")}
						subgroup
						skills={selfPentestSkills}
						busy={busy}
						expandedSkills={expandedSkills}
						onToggle={toggleSkill}
						onToggleEnabled={(name, enabled) => void mutate(() => app.SetSkillEnabled(name, enabled))}
						onDerive={deriveSkill}
						onDelete={(skill) => setPendingUninstall({ skill })}
					/>
					<SkillDomainSection
						title={t("caps.selfOrchOther")}
						subgroup
						skills={selfOtherSkills}
						busy={busy}
						expandedSkills={expandedSkills}
						onToggle={toggleSkill}
						onToggleEnabled={(name, enabled) => void mutate(() => app.SetSkillEnabled(name, enabled))}
						onDerive={deriveSkill}
						onDelete={(skill) => setPendingUninstall({ skill })}
					/>
				</>
			)}
			</>
			)}
			{pendingUninstall && (
				<ConfirmModal
					title={pendingUninstall.skill.installedFrom
						? t("caps.marketConfirmUninstall", { name: pendingUninstall.skill.name })
						: t("caps.deleteSkillConfirm", { name: pendingUninstall.skill.name })}
					message={pendingUninstall.skill.installedFrom
						? t("caps.marketUninstallWarning")
						: t("caps.deleteSkillWarning")}
					confirmLabel={pendingUninstall.skill.installedFrom ? t("caps.uninstall") : t("caps.deleteSkill")}
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
// the bottom of the skills page. The source picker lists the backend's sources
// (curated + ClawHub defaults plus the user's custom [skills].market_sources
// entries); an EMPTY query browses each source's first page; the URL row
// installs from any ref the engine understands (GitHub repo / raw SKILL.md /
// .mcp.json / local folder / package name) through the same plan→confirm flow
// as search-result installs.
function SkillMarketSection({ installedNames }: { installedNames: Set<string> }) {
	const t = useT();
	const [query, setQuery] = useState("");
	const [searchSource, setSearchSource] = useState("");
	const [searching, setSearching] = useState(false);
	const [results, setResults] = useState<CatalogEntry[] | null>(null);
	const [failedSources, setFailedSources] = useState<Record<string, string>>({});
	const [err, setErr] = useState<string | null>(null);
	const [installing, setInstalling] = useState<string | null>(null);
	const [installMsg, setInstallMsg] = useState<string | null>(null);
	const [pendingInstall, setPendingInstall] = useState<{ entry: CatalogEntry; plan: string } | null>(null);
	const [sources, setSources] = useState<MarketSourceMeta[]>([]);
	const [showAddSource, setShowAddSource] = useState(false);
	const [newSourceName, setNewSourceName] = useState("");
	const [newSourceType, setNewSourceType] = useState<"clawhub-api" | "github-repo">("github-repo");
	const [newSourceURL, setNewSourceURL] = useState("");
	const [urlRef, setUrlRef] = useState("");

	const loadSources = useCallback(async () => {
		try {
			setSources(await app.SkillMarketSources());
		} catch {
			// picker falls back to whatever it already has; searching still works
		}
	}, []);
	useEffect(() => { void loadSources(); }, [loadSources]);

	const sourceLabel = useCallback((sourceId: string) => {
		const s = sources.find((x) => x.id === sourceId);
		return s ? s.name : sourceId;
	}, [sources]);

	// Empty query = browse each source's first page (trending); the backend
	// no longer returns nothing for empty searches.
	const doSearch = useCallback(async (searchQuery: string, source: string) => {
		setSearching(true);
		setErr(null);
		setResults(null);
		setFailedSources({});
		try {
			const res = await app.SkillMarketSearch(searchQuery.trim(), source);
			setResults(res.entries);
			setFailedSources(res.failed ?? {});
		} catch (e) {
			setErr(String((e as Error)?.message ?? e));
		} finally {
			setSearching(false);
		}
	}, []);

	// Initial browse on mount — the market tab shows content immediately.
	useEffect(() => {
		void doSearch("", "");
	}, [doSearch]);

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

	// URL / GitHub install: same plan→confirm flow, name inferred server-side.
	const doInstallURL = useCallback(async () => {
		const ref = urlRef.trim();
		if (!ref) return;
		setInstalling(ref);
		setInstallMsg(null);
		try {
			const plan = await app.SkillMarketInstall(ref, "", "global", false);
			setPendingInstall({ entry: { source: "url", name: ref, slug: "", description: "", installs: 0, contentUrl: ref, installRef: ref }, plan });
			setUrlRef("");
		} catch (e) {
			setInstallMsg(t("caps.marketInstallFailed", { msg: String((e as Error)?.message ?? e) }));
		} finally {
			setInstalling(null);
		}
	}, [t, urlRef]);

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

	const doAddSource = useCallback(async () => {
		setErr(null);
		try {
			await app.SkillMarketSourceAdd(newSourceName.trim(), newSourceType, newSourceURL.trim());
			setInstallMsg(t("caps.marketSourceAdded", { name: newSourceName.trim() }));
			setNewSourceName("");
			setNewSourceURL("");
			setShowAddSource(false);
			await loadSources();
		} catch (e) {
			setErr(String((e as Error)?.message ?? e));
		}
	}, [loadSources, newSourceName, newSourceType, newSourceURL]);

	const doRemoveSource = useCallback(async (id: string) => {
		setErr(null);
		try {
			await app.SkillMarketSourceRemove(id);
			setInstallMsg(t("caps.marketSourceRemoved", { id }));
			if (searchSource === id) setSearchSource("");
			await loadSources();
		} catch (e) {
			setErr(String((e as Error)?.message ?? e));
		}
	}, [loadSources, searchSource]);

	const customSources = sources.filter((s) => s.custom);

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
					style={{ width: "180px", margin: 0 }}
					value={searchSource}
					onChange={(e) => {
						setSearchSource(e.target.value);
						void doSearch(query, e.target.value);
					}}
				>
					<option value="">{t("common.all", { defaultValue: "All Sources" })}</option>
					{sources.map((s) => (
						<option key={s.id} value={s.id}>{s.name}</option>
					))}
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

			{/* Install from a raw URL / GitHub repo — the engine accepts repos
			    (marketplace.json / .mcp.json / SKILL.md scan), raw files, local
			    folders, and package names; a plan is confirmed before writing. */}
			<div className="cap-search" style={{ marginBottom: "12px", display: "flex", gap: "8px" }}>
				<input
					className="mem-input"
					style={{ flex: 1, margin: 0 }}
					type="search"
					placeholder={t("caps.marketURLPlaceholder")}
					title={t("caps.marketURLHint")}
					value={urlRef}
					onChange={(e) => setUrlRef(e.target.value)}
					onKeyDown={(e) => { if (e.key === "Enter") void doInstallURL(); }}
				/>
				<button
					className="btn btn--small"
					style={{ margin: 0 }}
					disabled={installing !== null || urlRef.trim() === ""}
					onClick={() => void doInstallURL()}
				>
					{installing && pendingInstall === null ? t("caps.marketInstalling") : t("caps.marketInstallFromURL")}
				</button>
			</div>

			{/* Custom market source management: defaults (Curated + ClawHub) are
			    fixed; users add their own ClawHub-compatible or GitHub-repo
			    markets here. */}
			<div style={{ marginBottom: "12px" }}>
				<button className="btn btn--small" type="button" onClick={() => setShowAddSource((v) => !v)}>
					{showAddSource ? t("common.collapse", { defaultValue: "Collapse" }) : t("caps.marketAddSource")}
				</button>
				{customSources.length > 0 && (
					<span style={{ marginLeft: "12px", display: "inline-flex", gap: "6px", flexWrap: "wrap" }}>
						{customSources.map((s) => (
							<span key={s.id} className="cap-skill-badge" style={{ display: "inline-flex", alignItems: "center", gap: "4px" }}>
								{s.name}
								<button
									type="button"
									className="btn btn--small btn--danger"
									style={{ margin: 0, padding: "0 6px" }}
									title={t("caps.marketRemoveSource")}
									onClick={() => void doRemoveSource(s.id)}
								>
									×
								</button>
							</span>
						))}
					</span>
				)}
				{showAddSource && (
					<div className="cap-search" style={{ marginTop: "8px", display: "flex", gap: "8px" }}>
						<input
							className="mem-input"
							style={{ flex: "0 0 180px", margin: 0 }}
							type="text"
							placeholder={t("caps.marketSourceNamePlaceholder")}
							value={newSourceName}
							onChange={(e) => setNewSourceName(e.target.value)}
						/>
						<select
							className="mem-input"
							style={{ flex: "0 0 170px", margin: 0 }}
							value={newSourceType}
							onChange={(e) => setNewSourceType(e.target.value as "clawhub-api" | "github-repo")}
						>
							<option value="github-repo">{t("caps.marketSourceTypeGithub")}</option>
							<option value="clawhub-api">{t("caps.marketSourceTypeClawhub")}</option>
						</select>
						<input
							className="mem-input"
							style={{ flex: 1, margin: 0 }}
							type="text"
							placeholder={t("caps.marketSourceURLPlaceholder")}
							value={newSourceURL}
							onChange={(e) => setNewSourceURL(e.target.value)}
						/>
						<button
							className="btn btn--small btn--primary"
							style={{ margin: 0 }}
							disabled={newSourceName.trim() === "" || newSourceURL.trim() === ""}
							onClick={() => void doAddSource()}
						>
							{t("caps.add")}
						</button>
					</div>
				)}
			</div>

			{err && <div className="banner banner--error" style={{ marginBottom: "8px" }}>{err}</div>}
			{installMsg && <div className="banner" style={{ marginBottom: "8px" }}>{installMsg}</div>}
			{Object.keys(failedSources).length > 0 && results !== null && (
				<div className="banner" role="status" style={{ marginBottom: "8px" }}>
					{Object.entries(failedSources).map(([id, reason]) => (
						<div key={id}>
							{sourceLabel(id)} — {t("caps.marketSourceFailed")}: {reason}
						</div>
					))}
				</div>
			)}
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

