// EventEditForm is the create/edit/delete surface for a pure calendar event
// (one without an associated task — agent-created meetings, ICS imports, etc.).
//
// Unlike TaskForm (Trigger → Action → Delivery), a calendar event is a plain
// time-bounded entry, so the form collects: title, start/end time, all-day
// toggle, location, description, color, reminders (minutes), and tags.
//
// On Save the form calls onSubmit with the assembled CalendarEventInput; the
// parent decides create-vs-update by whether an `initial` event was supplied.
// In edit mode a Delete button is offered via onDelete.

import { useState } from "react";
import { X, Zap } from "lucide-react";

import type { CalendarEventInput, CalendarEventView } from "../../lib/types";
import { useT } from "../../lib/i18n";

// Running-partition options for the event's 到点动作 (the compiled task runs
// in this profile's context). Same set as TaskForm's selector.
const PROFILE_OPTIONS = [
  { value: "dev", label: "编码" },
  { value: "cowork", label: "办公" },
  { value: "netdev", label: "运维" },
] as const;

// toLocalInput produces a "YYYY-MM-DDTHH:MM" string from a Date suitable as a
// value for <input type="datetime-local">. datetime-local does not carry a
// timezone offset, so we render the wall-clock fields directly.
function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// defaultStartForNew is "now + 1 hour", snapped to the top of the hour so the
// default looks tidy rather than like an arbitrary minute.
function defaultStartForNew(): string {
  const d = new Date();
  d.setHours(d.getHours() + 1, 0, 0, 0);
  return toLocalInput(d);
}

export function EventEditForm({
  initial,
  initialActionPrompt,
  defaultProfile = "cowork",
  onSubmit,
  onDelete,
  onCancel,
}: {
  initial: CalendarEventView | null;
  // Seeds the 到点动作 textarea in edit mode (the linked task's prompt — the
  // panel looks it up from the loaded task list).
  initialActionPrompt?: string;
  defaultProfile?: string;
  onSubmit: (input: CalendarEventInput) => Promise<void>;
  onDelete?: () => Promise<void>;
  onCancel: () => void;
}) {
  const t = useT();
  const isEdit = !!initial;

  const [title, setTitle] = useState(initial?.title ?? "");
  // start/end are kept in datetime-local format ("YYYY-MM-DDTHH:MM") — the same
  // format CalendarEventView.start uses, so we can pass through unchanged.
  const [start, setStart] = useState(initial?.start ?? defaultStartForNew());
  const [end, setEnd] = useState(initial?.end ?? "");
  const [allDay, setAllDay] = useState(initial?.allDay ?? false);
  const [location, setLocation] = useState(initial?.location ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [color, setColor] = useState(initial?.color ?? "#4488FF");
  // reminders / tags are edited as comma-separated strings for a simple UX and
  // parsed back to number[] / string[] on submit.
  const [reminders, setReminders] = useState((initial?.reminders ?? []).join(","));
  const [tags, setTags] = useState((initial?.tags ?? []).join(","));
  // P4 到点动作: a non-empty prompt compiles the event with a linked one-shot
  // task firing at the event's start. The event's own partition carries over
  // as the task's run identity (this form is human-only by construction).
  const [actionPrompt, setActionPrompt] = useState(initialActionPrompt ?? "");
  const [actionProfile, setActionProfile] = useState(initial?.profile || defaultProfile);
  const hasRecurrence = !!(initial?.recurrence ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>("");

  const parseReminders = (raw: string): number[] => {
    const out: number[] = [];
    for (const part of raw.split(",")) {
      const n = Number(part.trim());
      if (part.trim() !== "" && !Number.isNaN(n)) out.push(n);
    }
    return out;
  };

  const save = async () => {
    setError("");
    if (!title.trim()) {
      setError(t("cowork.automationFormName"));
      return;
    }
    setSaving(true);
    try {
      const input: CalendarEventInput = {
        id: initial?.id ?? "",
        title: title.trim(),
        description: description.trim(),
        location: location.trim(),
        start,
        // allDay events may legitimately have no end time.
        end: allDay && !end ? start : end,
        allDay,
        timezone: initial?.timezone ?? "",
        color,
        recurrence: initial?.recurrence ?? "",
        recurrenceEnd: initial?.recurrenceEnd ?? "",
        reminders: parseReminders(reminders),
        tags: tags.split(",").map((s) => s.trim()).filter(Boolean),
        profile: actionPrompt.trim() ? actionProfile : (initial?.profile ?? ""),
        actionPrompt: actionPrompt.trim(),
        outputMode: initial?.outputMode,
        outputDest: initial?.outputDest,
        outputAccount: initial?.outputAccount,
      };
      await onSubmit(input);
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="cowork-taskform-overlay">
      <div className="cowork-taskform" onClick={(e) => e.stopPropagation()}>
        <header className="cowork-taskform__head">
          <h3>{isEdit ? t("cowork.automationFormTitleEdit") : t("cowork.automationFormTitleNew")}</h3>
          <button className="cowork-task-card__btn" onClick={onCancel} title="close">
            <X size={16} />
          </button>
        </header>

        <div className="cowork-taskform__body">
          {/* Title */}
          <label className="cowork-taskform__label">
            <span className="cowork-taskform__labeltext">{t("cowork.automationFormName")}</span>
            <input
              className="cowork-taskform__input"
              value={title}
              placeholder={t("cowork.automationFormNameHint")}
              onChange={(e) => setTitle(e.target.value)}
            />
          </label>

          {/* Color & Location */}
          <div className="cowork-taskform__section">
            <label className="cowork-taskform__label">
              <span className="cowork-taskform__labeltext">{t("eventEdit.colorAndLocation")}</span>
              <div style={{ display: "flex", gap: "8px" }}>
                <input
                  type="color"
                  className="cowork-taskform__input"
                  style={{ width: "40px", padding: "0" }}
                  value={color}
                  onChange={(e) => setColor(e.target.value)}
                />
                <input
                  className="cowork-taskform__input"
                  style={{ flex: 1 }}
                  value={location}
                  placeholder={t("eventEdit.locationPlaceholder")}
                  onChange={(e) => setLocation(e.target.value)}
                />
              </div>
            </label>
          </div>

          {/* All-day toggle */}
          <div className="cowork-taskform__section">
            <label className="cowork-taskform__label" style={{ flexDirection: "row", alignItems: "center", gap: "8px" }}>
              <input
                type="checkbox"
                checked={allDay}
                onChange={(e) => setAllDay(e.target.checked)}
              />
              <span className="cowork-taskform__labeltext">{t("eventEdit.allDay")}</span>
            </label>
          </div>

          {/* Start / End time */}
          <div className="cowork-taskform__section">
            <label className="cowork-taskform__label">
              <span className="cowork-taskform__labeltext">{t("eventEdit.startTime")}</span>
              <input
                type="datetime-local"
                className="cowork-taskform__input"
                value={start}
                onChange={(e) => setStart(e.target.value)}
              />
            </label>
            {!allDay && (
              <label className="cowork-taskform__label">
                <span className="cowork-taskform__labeltext">{t("eventEdit.endTime")}</span>
                <input
                  type="datetime-local"
                  className="cowork-taskform__input"
                  value={end}
                  onChange={(e) => setEnd(e.target.value)}
                />
              </label>
            )}
          </div>

          {/* Description */}
          <label className="cowork-taskform__label cowork-taskform__label--top">
            <span className="cowork-taskform__labeltext">{t("eventEdit.description")}</span>
            <textarea
              className="cowork-taskform__textarea"
              value={description}
              rows={4}
              placeholder={t("eventEdit.descriptionPlaceholder")}
              onChange={(e) => setDescription(e.target.value)}
            />
          </label>

          {/* Reminders (comma-separated minutes) */}
          <div className="cowork-taskform__section">
            <label className="cowork-taskform__label">
              <span className="cowork-taskform__labeltext">{t("eventEdit.reminders")}</span>
              <input
                className="cowork-taskform__input"
                value={reminders}
                placeholder={t("eventEdit.remindersPlaceholder")}
                onChange={(e) => setReminders(e.target.value)}
              />
            </label>
          </div>

          {/* Tags (comma-separated) */}
          <div className="cowork-taskform__section">
            <label className="cowork-taskform__label">
              <span className="cowork-taskform__labeltext">{t("eventEdit.tags")}</span>
              <input
                className="cowork-taskform__input"
                value={tags}
                placeholder={t("eventEdit.tagsPlaceholder")}
                onChange={(e) => setTags(e.target.value)}
              />
            </label>
          </div>

          {/* P4 到点动作：编译为关联的一次性调度任务（到事件开始时间运行）。
              仅人类 UI 可设；循环事件暂不支持（提示改用定时任务）。 */}
          <div className="cowork-taskform__section">
            <label className="cowork-taskform__label cowork-taskform__label--top">
              <span className="cowork-taskform__labeltext">
                <Zap size={12} style={{ verticalAlign: "-2px", marginRight: 4 }} />
                {t("eventEdit.actionTitle")}
              </span>
              {hasRecurrence ? (
                <span className="cowork-taskform__picker-hint">{t("eventEdit.actionRecurringHint")}</span>
              ) : (
                <>
                  <textarea
                    className="cowork-taskform__textarea"
                    value={actionPrompt}
                    rows={3}
                    placeholder={t("eventEdit.actionPlaceholder")}
                    onChange={(e) => setActionPrompt(e.target.value)}
                  />
                  {actionPrompt.trim() && (
                    <select
                      className="cowork-taskform__input"
                      style={{ marginTop: 8 }}
                      value={actionProfile}
                      onChange={(e) => setActionProfile(e.target.value)}
                      title={t("cal.taskRunsUnder")}
                    >
                      {PROFILE_OPTIONS.map((p) => (
                        <option key={p.value} value={p.value}>{p.label}</option>
                      ))}
                    </select>
                  )}
                  <span className="cowork-taskform__picker-hint">{t("eventEdit.actionHint")}</span>
                </>
              )}
            </label>
          </div>

          {error && <div className="cowork-taskform__error">{error}</div>}
        </div>

        <footer className="cowork-taskform__foot">
          {isEdit && onDelete && (
            <button
              type="button"
              className="btn btn--danger btn--small"
              onClick={() => void onDelete()}
              disabled={saving}
            >
              {t("cowork.automationFormDelete")}
            </button>
          )}
          <div className="cowork-taskform__foot-right">
            <button type="button" className="btn btn--small" onClick={onCancel} disabled={saving}>
              {t("cowork.automationFormCancel")}
            </button>
            <button
              type="button"
              className="btn btn--primary btn--small"
              onClick={() => void save()}
              disabled={saving}
            >
              {saving ? t("cowork.automationRunning") : t("cowork.automationFormSave")}
            </button>
          </div>
        </footer>
      </div>
    </div>
  );
}
