// SideSessionPane — the bottom axis's "副会话" tab (pane-system spec §3.5, P2).
// A parallel-conversation mini view: pick any OTHER open session, read its
// transcript (PreviewSession) and send messages to it (SubmitToTab) while the
// main conversation keeps running in the center. Model/approval follow the
// target tab's own settings — this pane only reads and writes.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Loader2, MessageSquare, SendHorizontal } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { Markdown } from "./Markdown";
import type { HistoryMessage, SessionMeta, TabMeta } from "../lib/types";

const POLL_WHILE_RUNNING_MS = 2500;

function messageText(m: HistoryMessage): string {
  if (typeof m.content === "string") return m.content;
  return "";
}

export function SideSessionPane({
  tabs,
  sessions,
  activeMainTabId,
  selectedId,
  onSelect,
}: {
  tabs: TabMeta[];
  sessions: SessionMeta[];
  activeMainTabId?: string;
  selectedId: string | null;
  onSelect: (tabId: string) => void;
}) {
  const t = useT();
  const [messages, setMessages] = useState<HistoryMessage[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const outRef = useRef<HTMLDivElement | null>(null);

  const candidates = useMemo(
    () => tabs.filter((tab) => tab.tabType !== "file" && tab.id !== activeMainTabId),
    [tabs, activeMainTabId],
  );
  const selected = candidates.find((tab) => tab.id === selectedId) ?? candidates[0] ?? null;

  const sessionPath = useMemo(
    () => (selected ? sessions.find((s) => s.topicId === selected.topicId && !s.deletedAt)?.path : undefined),
    [sessions, selected],
  );

  const refresh = useCallback(async () => {
    if (!sessionPath) {
      setMessages(null);
      return;
    }
    setLoading(true);
    try {
      const list = await app.PreviewSession(sessionPath);
      setMessages(list ?? []);
    } catch {
      setMessages([]);
    } finally {
      setLoading(false);
    }
  }, [sessionPath]);

  useEffect(() => {
    if (!selected) return;
    if (selected.id !== selectedId) onSelect(selected.id);
  }, [selected, selectedId, onSelect]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Poll while the side session is mid-turn so the mini transcript follows.
  useEffect(() => {
    if (!selected?.running) return;
    const timer = window.setInterval(() => void refresh(), POLL_WHILE_RUNNING_MS);
    return () => window.clearInterval(timer);
  }, [selected?.running, refresh]);

  useEffect(() => {
    const el = outRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages]);

  const send = () => {
    const text = draft.trim();
    if (!text || !selected || sending) return;
    setSending(true);
    void app.SubmitToTab(selected.id, text)
      .then(() => {
        setDraft("");
        window.setTimeout(() => void refresh(), 600);
      })
      .catch(() => { /* submit failed — keep the draft */ })
      .finally(() => setSending(false));
  };

  if (!selected) {
    return (
      <div className="side-session__empty">
        <MessageSquare size={20} />
        <div>{t("sideSession.emptyHint")}</div>
      </div>
    );
  }

  return (
    <div className="side-session">
      <div className="side-session__head">
        <MessageSquare size={12} />
        <select
          className="side-session__pick"
          value={selected.id}
          onChange={(e) => onSelect(e.target.value)}
          aria-label={t("sideSession.pick")}
        >
          {candidates.map((tab) => (
            <option key={tab.id} value={tab.id}>
              {tab.label || tab.topicTitle}
              {tab.running ? " ●" : ""}
            </option>
          ))}
        </select>
        {selected.running && <Loader2 size={11} className="composer-phase__spin" aria-hidden="true" />}
        <span className="side-session__spacer" />
        <span className="side-session__path" title={sessionPath}>{sessionPath ?? ""}</span>
      </div>
      <div className="side-session__out" ref={outRef}>
        {loading && messages === null ? (
          <div className="side-session__loading">{t("common.loading")}</div>
        ) : (messages ?? []).length === 0 ? (
          <div className="side-session__loading">{t("sideSession.noMessages")}</div>
        ) : (
          (messages ?? []).map((m, i) =>
            m.role === "user" ? (
              <div key={i} className="msg msg--user">
                <div className="msg__body">
                  <div className="msg__text">{messageText(m)}</div>
                </div>
              </div>
            ) : m.role === "assistant" ? (
              <div key={i} className="msg msg--assistant">
                <div className="msg__body">
                  <Markdown text={messageText(m)} />
                </div>
              </div>
            ) : (
              <div key={i} className="side-session__sysline">{messageText(m)}</div>
            ),
          )
        )}
      </div>
      <div className="side-session__inputrow">
        <input
          className="side-session__input"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              send();
            }
          }}
          placeholder={t("sideSession.inputPlaceholder")}
          disabled={sending}
          spellCheck={false}
          autoComplete="off"
        />
        <button
          type="button"
          className="side-session__send"
          onClick={send}
          disabled={!draft.trim() || sending}
          aria-label={t("composer.send")}
        >
          <SendHorizontal size={13} />
        </button>
      </div>
    </div>
  );
}
