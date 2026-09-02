// TrustDomainPanel — the settings-panel surface of the private-network
// trust domain (docs/TRUSTDOMAIN_SPEC.md §15.3): a read-mostly local board
// (members, tokens, succession clock) plus the emergency brake. Mutating
// actions run the same offline path as the CLI; on multi-admin domains the
// quorum error surfaces and the hint points to the CLI.

import { useCallback, useEffect, useState } from "react";
import { app } from "../lib/bridge";
import type { TrustDomainView } from "../lib/bridge";
import { useToast } from "../lib/toast";
import { useT } from "../lib/i18n";

export function TrustDomainPanel() {
  const t = useT();
  const { showToast } = useToast();
  const [view, setView] = useState<TrustDomainView | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setView(await app.TrustDomainStatus());
    } catch (e) {
      setView({ enabled: true, joined: false, detail: String(e), height: 0, paused: false, successionConfigured: false, successionAfterSec: 0, successionLastActive: 0, successionDue: false });
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const run = async (fn: () => Promise<void>, ok: string) => {
    setBusy(true);
    try {
      await fn();
      showToast(ok, "info");
      await refresh();
    } catch (e) {
      showToast(String(e), "error");
    } finally {
      setBusy(false);
    }
  };

  if (!view) return <p className="settings-hint">{t("common.loading")}</p>;

  // Not enabled / not joined: guidance instead of an empty board.
  if (!view.enabled) {
    return (
      <p className="settings-hint">{t("trustdomain.hint.disabled")}</p>
    );
  }
  if (!view.joined) {
    return (
      <p className="settings-hint">{t("trustdomain.hint.notJoined", { detail: view.detail ?? "" })}</p>
    );
  }

  return (
    <div className="td-panel">
      {view.paused && <div className="banner banner--warn">{t("trustdomain.pausedBanner")}</div>}
      <header className="td-panel__head">
        <div>
          <strong>{t("trustdomain.domain")}</strong>
          <code title={view.domain ?? ""}>{short(view.domain ?? "", 16)}</code>
          <span className="td-panel__meta">
            {t("trustdomain.height", { n: view.height })} · {t("trustdomain.quorum", { n: view.quorum ?? 0 })} ·{" "}
            {view.me === "" ? "" : short(view.me ?? "", 8)}
          </span>
        </div>
        <div className="td-panel__actions">
          {view.paused ? (
            <button disabled={busy} onClick={() => run(() => app.TrustDomainResume(), t("trustdomain.resumed"))}>
              {t("trustdomain.resume")}
            </button>
          ) : (
            <button disabled={busy} className="danger" onClick={() => run(() => app.TrustDomainPause(t("trustdomain.pauseReason")), t("trustdomain.paused"))}>
              {t("trustdomain.pause")}
            </button>
          )}
          <button disabled={busy} onClick={() => run(() => app.TrustDomainAnchor(), t("trustdomain.anchored"))}>
            {t("trustdomain.anchor")}
          </button>
        </div>
      </header>

      <section>
        <h4>{t("trustdomain.members")}</h4>
        <ul className="td-panel__members">
          {(view.members ?? []).map((m) => (
            <li key={m.id} className={m.role === "admin" ? "td-panel__member--admin" : ""}>
              <code>{short(m.id, 12)}</code>
              {m.name ? <span>{m.name}</span> : null}
              <em>{m.role === "admin" ? t("trustdomain.roleAdmin") : t("trustdomain.roleMember")}</em>
              {m.attestation ? <small>{m.attestation}</small> : null}
            </li>
          ))}
          {(view.revoked ?? []).map((id) => (
            <li key={id} className="td-panel__member--revoked">
              <code>{short(id, 12)}</code>
              <em>{t("trustdomain.revoked")}</em>
            </li>
          ))}
        </ul>
      </section>

      <section>
        <h4>{t("trustdomain.tokens")}</h4>
        {(view.tokens ?? []).length === 0 ? (
          <p className="settings-hint">{t("trustdomain.noTokens")}</p>
        ) : (
          <ul className="td-panel__tokens">
            {(view.tokens ?? []).map((tok) => (
              <li key={tok.id}>
                <code>{tok.id}</code>
                <span>{tok.resource} · {tok.ops.join(",")}</span>
                {tok.parent ? <small>{t("trustdomain.delegatedFrom", { id: short(tok.parent, 10) })}</small> : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      {view.successionConfigured && (
        <section>
          <h4>{t("trustdomain.succession")}</h4>
          <p className="td-panel__meta">
            {t("trustdomain.successionDetail", {
              hours: Math.round(view.successionAfterSec / 3600),
              members: (view.successionMembers ?? []).map((m) => short(m, 8)).join(", "),
            })}
            {view.successionDue ? ` — ${t("trustdomain.successionDue")}` : ""}
          </p>
        </section>
      )}

      <p className="settings-hint">{t("trustdomain.cliHint")}</p>
    </div>
  );
}

function short(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) + "…" : s;
}
