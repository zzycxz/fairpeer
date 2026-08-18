// PreferencePanel is the workspace "偏好" panel for BOTH modes: it manages the
// active mode's selectable preference presets (<mode>-presets.json) — a list of
// named templates ("减少AI味", "最小改动", …) where at most one is "in use" and
// its content is injected into every turn after the portrait files (see
// memory.discoverProfile). cowork shows office presets under "办公偏好", dev
// shows coding presets under "编码偏好"; the mode only differs in the builtin
// seed list, the title and the placeholder copy.
//
// Interaction model (agreed): clicking a row only selects it for editing; the
// "设为使用" action marks the active one; everything — edits, additions,
// duplications, deletions, the active switch — commits atomically via
// SetProfilePresets on 保存, and closing with unsaved changes asks first. The
// backend queues a turn-tail note so a committed switch applies to the current
// session too, then folds it into the cache-stable prefix next session.

import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Copy, Plus, RotateCcw, Save, Trash2, X } from "lucide-react";

import { ConfirmModal } from "../ConfirmModal";
import { app } from "../../lib/bridge";
import { builtinPresetsFor, type PresetMode } from "../../lib/builtinPresets";
import { useT } from "../../lib/i18n";
import { useToast } from "../../lib/toast";
import type { ProfilePreset } from "../../lib/types";

// Soft per-template budget. The backend's hard caps are higher (memory's
// presetMaxContent per item, profileMaxChars over the whole portrait) — this
// just warns before a save would clip.
const presetSoftMaxChars = 500;

function newPresetId(): string {
  return `p-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

// Dirty check compares the user-meaningful fields only (path is display-only).
function payloadKey(active: string, items: ProfilePreset[]): string {
  return JSON.stringify({
    active,
    items: items.map(i => ({ id: i.id, name: i.name, content: i.content, builtin: i.builtin })),
  });
}

export function PreferencePanel({
  mode,
  title,
  onClose
}: {
  mode: PresetMode;
  title?: string;
  onClose?: () => void;
}) {
  const t = useT();
  const { showToast } = useToast();
  const [path, setPath] = useState("");
  const [items, setItems] = useState<ProfilePreset[]>([]);
  const [active, setActive] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [originalKey, setOriginalKey] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<ProfilePreset | null>(null);
  // Set when the next selection change should focus+select the name input —
  // the "add / duplicate" affordance that pushes the user straight into
  // renaming the fresh template instead of leaving “新偏好” cluttering the list.
  const focusNameRef = useRef(false);
  const nameInputRef = useRef<HTMLInputElement | null>(null);
  const [confirmClose, setConfirmClose] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const pv = await app.ProfilePresets();
      const loaded = pv.items ?? [];
      setPath(pv.path ?? "");
      setItems(loaded);
      setActive(pv.active ?? "");
      setSelectedId(loaded.some(i => i.id === pv.active) ? pv.active : (loaded[0]?.id ?? ""));
      setOriginalKey(payloadKey(pv.active ?? "", loaded));
    } catch {
      setItems([]);
      setActive("");
      setSelectedId("");
      setOriginalKey(payloadKey("", []));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  // A freshly added/duplicated item becomes the selection; grab the name input
  // with its text selected so typing replaces the default name immediately.
  useEffect(() => {
    if (!focusNameRef.current) return;
    focusNameRef.current = false;
    nameInputRef.current?.focus();
    nameInputRef.current?.select();
  }, [selectedId]);

  const selected = items.find(i => i.id === selectedId) ?? null;
  const dirty = payloadKey(active, items) !== originalKey;
  const anyOver = items.some(i => i.content.length > presetSoftMaxChars);

  const patchSelected = (patch: Partial<ProfilePreset>) => {
    setItems(prev => prev.map(i => (i.id === selectedId ? { ...i, ...patch } : i)));
  };

  const addPreset = () => {
    const item: ProfilePreset = {
      id: newPresetId(),
      name: t("preference.newName") || "新偏好",
      content: "",
      builtin: false,
    };
    setItems(prev => [...prev, item]);
    setSelectedId(item.id);
    focusNameRef.current = true;
  };

  // copySelected derives a user-owned duplicate of the selected template — the
  // natural authoring path ("a version of 减少AI味 that also …") without editing
  // the builtin in place. Inserted right after the original and ready to rename.
  const copySelected = () => {
    if (!selected) return;
    const copy: ProfilePreset = {
      id: newPresetId(),
      name: `${selected.name} ${t("preference.copySuffix")}`,
      content: selected.content,
      builtin: false,
    };
    setItems(prev => {
      const at = prev.findIndex(i => i.id === selected.id);
      const next = [...prev];
      next.splice(at < 0 ? prev.length : at + 1, 0, copy);
      return next;
    });
    setSelectedId(copy.id);
    focusNameRef.current = true;
  };

  const restoreDefaults = () => {
    const have = new Set(items.map(i => i.id));
    const missing = builtinPresetsFor(mode).filter(d => !have.has(d.id));
    if (!missing.length) return;
    setItems(prev => [...prev, ...missing.map(i => ({ ...i }))]);
    showToast(t("preference.defaultsRestored"));
  };

  const doDelete = () => {
    if (!pendingDelete) return;
    const gone = pendingDelete.id;
    const next = items.filter(i => i.id !== gone);
    setItems(next);
    if (active === gone) setActive(next[0]?.id ?? "");
    if (selectedId === gone) setSelectedId(next[0]?.id ?? "");
    setPendingDelete(null);
  };

  const save = useCallback(async () => {
    if (!dirty || saving) return;
    setSaving(true);
    try {
      await app.SetProfilePresets({ active, items, path });
      await reload(); // re-sync with the normalized state actually on disk
      showToast(t("preference.saved"));
    } catch (err) {
      showToast(String((err as Error)?.message ?? err));
    } finally {
      setSaving(false);
    }
  }, [dirty, saving, active, items, path, reload, showToast, t]);

  // Closing follows the explicit-save model: a dirty state is never dropped
  // silently — ask once (discard vs keep editing) before the backdrop/X closes.
  const requestClose = useCallback(() => {
    if (dirty) {
      setConfirmClose(true);
      return;
    }
    onClose?.();
  }, [dirty, onClose]);

  const defaultTitle = mode === "cowork"
    ? (t("cowork.preference") || "办公偏好")
    : (t("preference.title") || "编码偏好");

  if (loading) {
    return (
      <div className="management-modal-backdrop" onClick={onClose}>
        <div className="management-modal history-modal" onClick={e => e.stopPropagation()}>
          <div className="preference-panel"><div className="empty">{t("common.loading")}</div></div>
        </div>
      </div>
    );
  }

  return (
    <div className="management-modal-backdrop" onClick={requestClose}>
      <div
        className="management-modal history-modal"
        style={{ width: 760, maxWidth: "92vw", height: 580, display: "flex", flexDirection: "column", overflow: "hidden" }}
        onClick={e => e.stopPropagation()}
      >
        <div className="preference-panel" style={{ flex: 1, height: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}>
          <header className="preference-panel__head">
            <div>
              <h2 className="preference-panel__title">{title || defaultTitle}</h2>
              <p className="preference-panel__hint">{t("preference.presetHint")}</p>
            </div>
            <div style={{ display: "flex", gap: "8px", alignItems: "center" }}>
              <button
                className="btn btn--primary btn--small"
                onClick={() => void save()}
                disabled={!dirty || saving || anyOver}
                type="button"
              >
                <Save size={13} />
                {t("common.save")}
              </button>
              {onClose && (
                <button className="btn btn--icon" onClick={requestClose} type="button" aria-label={t("common.close") || "关闭"}>
                  <X size={16} />
                </button>
              )}
            </div>
          </header>

          <div className="preference-panel__body">
            <aside className="preference-preset-list">
              <div className="preference-preset-list__scroll">
                {items.map(it => (
                  <button
                    key={it.id}
                    type="button"
                    className={`preference-preset-list__item ${it.id === selectedId ? "preference-preset-list__item--selected" : ""}`}
                    onClick={() => setSelectedId(it.id)}
                    title={it.name}
                  >
                    <span className="preference-preset-list__name">{it.name || t("preference.namePlaceholder")}</span>
                    {it.id === active && (
                      <span className="preference-preset-list__badge">
                        <Check size={11} />
                        {t("preference.inUse")}
                      </span>
                    )}
                  </button>
                ))}
                {items.length === 0 && (
                  <div className="preference-preset-list__empty">{t("preference.emptyList")}</div>
                )}
              </div>
              <div className="preference-preset-list__foot">
                <button className="btn btn--small" onClick={addPreset} type="button">
                  <Plus size={13} />
                  {t("preference.add")}
                </button>
                <button className="btn btn--small" onClick={restoreDefaults} type="button" title={t("preference.restoreDefaults")}>
                  <RotateCcw size={13} />
                </button>
              </div>
            </aside>

            <div className="preference-preset-editor">
              {selected ? (
                <>
                  <input
                    ref={nameInputRef}
                    className="preference-preset-editor__name"
                    value={selected.name}
                    placeholder={t("preference.namePlaceholder")}
                    onChange={e => patchSelected({ name: e.target.value })}
                    spellCheck={false}
                  />
                  <textarea
                    className="preference-panel__textarea"
                    style={{ flex: 1, minHeight: 0 }}
                    value={selected.content}
                    onChange={e => patchSelected({ content: e.target.value })}
                    placeholder={mode === "cowork" ? t("preference.contentPlaceholderCowork") : t("preference.contentPlaceholderDev")}
                    spellCheck={false}
                  />
                  <div className="preference-preset-editor__actions">
                    {selected.id === active ? (
                      <span className="preference-preset-editor__inuse">
                        <Check size={13} />
                        {t("preference.inUse")}
                      </span>
                    ) : (
                      <button className="btn btn--small" onClick={() => setActive(selected.id)} type="button">
                        <Check size={13} />
                        {t("preference.useThis")}
                      </button>
                    )}
                    <button
                      className="btn btn--small"
                      onClick={copySelected}
                      type="button"
                      title={t("preference.copyHint")}
                    >
                      <Copy size={13} />
                      {t("preference.copy")}
                    </button>
                    <button
                      className="btn btn--small btn--danger"
                      onClick={() => setPendingDelete(selected)}
                      type="button"
                    >
                      <Trash2 size={13} />
                      {t("preference.delete")}
                    </button>
                  </div>
                  <div className="preference-panel__meta">
                    <span className={selected.content.length > presetSoftMaxChars ? "preference-panel__count preference-panel__count--over" : "preference-panel__count"}>
                      {selected.content.length} / {presetSoftMaxChars}
                    </span>
                    {selected.content.length > presetSoftMaxChars && (
                      <span className="preference-panel__warn">{t("preference.tooLong")}</span>
                    )}
                    {path && <span className="preference-panel__path">{path}</span>}
                  </div>
                </>
              ) : (
                <div className="empty" style={{ margin: "auto" }}>{t("preference.emptyList")}</div>
              )}
            </div>
          </div>
        </div>

        {pendingDelete && (
          <ConfirmModal
            title={t("preference.deleteConfirmTitle")}
            message={t("preference.deleteConfirmMsg", { name: pendingDelete.name || t("preference.namePlaceholder") })}
            confirmLabel={t("preference.delete")}
            cancelLabel={t("common.cancel")}
            onConfirm={doDelete}
            onClose={() => setPendingDelete(null)}
          />
        )}
        {confirmClose && (
          <ConfirmModal
            title={t("preference.unsavedTitle")}
            message={t("preference.unsavedMsg")}
            confirmLabel={t("preference.unsavedDiscard")}
            cancelLabel={t("preference.unsavedKeep")}
            onConfirm={() => {
              setConfirmClose(false);
              onClose?.();
            }}
            onClose={() => setConfirmClose(false)}
          />
        )}
      </div>
    </div>
  );
}
