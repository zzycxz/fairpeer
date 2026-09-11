// useCalendarTasks — the single data-coordination point for the GLOBAL
// calendar/scheduler surface (the panel moved out of cowork to
// components/calendar because all three profiles mount it now).
//
// Why a hook at all: the frontend has no global store. Before this, each
// mount point owned its own loading + event subscriptions, which is how the
// panel accumulated its defects — the grid only ever loaded the
// scheduled-task projection (never ListCalendarEvents, so manual events were
// invisible), and the netdev jobs card loaded once on mount and never
// refreshed. One hook = one merge + one subscription set, everywhere.
//
// Data model (mirrors the Go side's partitioning): events is the MERGED view
// the grid renders — real calendar events plus scheduled-task firings
// projected as events (id "task:<id>", see ListScheduledTasksAsEvents). tasks
// is the raw task list for the sidebar; every task carries the profile
// partition it will run under, so the UI can badge it.

import { useCallback, useEffect, useState } from "react";

import {
  app, onCalendarChanged, onSchedulerChanged,
} from "../lib/bridge";
import type { CalendarEventView, TaskView } from "../lib/types";

// useCalendarEvents loads the merged calendar grid for [since, before):
// scheduled-task firings (projection) + real calendar events, deduped by id
// (task projections use the "task:" prefix so they never collide). Refreshes
// on calendar:changed / scheduler:changed.
export function useCalendarEvents(since: string, before: string) {
  const [events, setEvents] = useState<CalendarEventView[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const [taskEvents, calEvents] = await Promise.all([
        app.ListScheduledTasksAsEvents(since, before),
        app.ListCalendarEvents(since, before),
      ]);
      const merged = [...(calEvents ?? []), ...(taskEvents ?? [])];
      const seen = new Set<string>();
      setEvents(merged.filter((e) => {
        if (seen.has(e.id)) return false;
        seen.add(e.id);
        return true;
      }));
    } catch {
      setEvents([]);
    } finally {
      setLoading(false);
    }
  }, [since, before]);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => onCalendarChanged(() => void refresh()), [refresh]);
  useEffect(() => onSchedulerChanged(() => void refresh()), [refresh]);

  return { events, loading, refresh };
}

// useScheduledTasks loads the task list and keeps it live on
// scheduler:changed — used by the calendar panel sidebar AND the netdev jobs
// card (which previously loaded once on mount and went stale, defect E).
export function useScheduledTasks() {
  const [tasks, setTasks] = useState<TaskView[] | null>(null);

  const refresh = useCallback(async () => {
    try {
      setTasks(await app.ListScheduledTasks());
    } catch {
      setTasks([]);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => onSchedulerChanged(() => void refresh()), [refresh]);

  return { tasks, refresh };
}
