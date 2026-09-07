// TrustDomainPanel — the settings-panel surface of the private-network
// trust domain (docs/TRUSTDOMAIN_SPEC.md §15.3): a read-mostly local board
// (members, tokens, succession clock) plus the emergency brake. The
// disabled / not-joined states render an onboarding card instead of an
// empty hint: a one-click "create bootstrap domain" button (TrustDomainInit)
// or copyable CLI steps for joining an existing domain. Mutating actions
// run the same offline path as the CLI; on multi-admin domains the quorum
// error surfaces and the hint points to the CLI.

import { useCallback, useEffect, useState } from "react";
import { ShieldCheck } from "lucide-react";
import { app } from "../lib/bridge";
import type { TrustDomainView } from "../lib/bridge";
import { useConfirm } from "../lib/confirm";
import { useToast } from "../lib/toast";
import { useT } from "../lib/i18n";
import { CopyButton } from "./CopyButton";

const TD_IDENTITY_CLI = "fairpeer trustdomain identity";

export function TrustDomainPanel() {
  const t = useT();
  const { showToast } = useToast();
  const confirm = useConfirm();
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

  const run = async (fn: () => Promise<void>, ok: string, action: string) => {
    setBusy(true);
    try {
      await fn();
      showToast(ok, "info");
      await refresh();
    } catch (e) {
      // 动作前缀让 toast 读得出"什么失败了"，而不是裸 Go 错误串。
      showToast(t("trustdomain.actionFailed", { action, detail: String((e as Error)?.message ?? e) }), "error");
    } finally {
      setBusy(false);
    }
  };

  // 紧急刹车是全网性动作：先讲清后果再确认，避免随手点到就刹停。
  const pause = async () => {
    if (!(await confirm({
      title: t("trustdomain.pauseConfirmTitle"),
      message: t("trustdomain.pauseConfirmMsg"),
      danger: true,
      confirmLabel: t("trustdomain.pause"),
    }))) return;
    await run(() => app.TrustDomainPause(t("trustdomain.pauseReason")), t("trustdomain.paused"), t("trustdomain.pause"));
  };

  const createDomain = async () => {
    if (!(await confirm({ title: t("trustdomain.onboarding.confirmTitle"), message: t("trustdomain.onboarding.confirmMsg") }))) return;
    setBusy(true);
    try {
      const domainID = await app.TrustDomainInit();
      showToast(t("trustdomain.onboarding.created", { id: short(domainID, 12) }), "info");
      await refresh();
    } catch (e) {
      showToast(t("trustdomain.actionFailed", { action: t("trustdomain.onboarding.createBtn"), detail: String((e as Error)?.message ?? e) }), "error");
    } finally {
      setBusy(false);
    }
  };

  const enableConfig = () => run(() => app.TrustDomainSetEnabled(true), t("trustdomain.onboarding.enabled"), t("trustdomain.onboarding.enableBtn"));

  // 关闭是软回退：只翻 enabled=false，账本/身份密钥留在磁盘，重新开启即恢复。
  const disableDomain = async () => {
    if (!(await confirm({
      title: t("trustdomain.disableConfirmTitle"),
      message: t("trustdomain.disableConfirmMsg"),
      confirmLabel: t("trustdomain.disable"),
    }))) return;
    await run(() => app.TrustDomainSetEnabled(false), t("trustdomain.disabledToast"), t("trustdomain.disable"));
  };

  // 重置是硬回退（init --force）：丢弃账本重建引导域；多成员域会孤立其它机器，
  // 确认文案按成员数切换为更强警告。
  const resetDomain = async () => {
    const members = (view?.members ?? []).length;
    if (!(await confirm({
      title: t("trustdomain.resetConfirmTitle"),
      message: members > 1 ? t("trustdomain.resetConfirmMsgMulti", { n: members }) : t("trustdomain.resetConfirmMsg"),
      danger: true,
      confirmLabel: t("trustdomain.reset"),
    }))) return;
    setBusy(true);
    try {
      const domainID = await app.TrustDomainReset();
      showToast(t("trustdomain.resetToast", { id: short(domainID, 12) }), "info");
      await refresh();
    } catch (e) {
      showToast(t("trustdomain.actionFailed", { action: t("trustdomain.reset"), detail: String((e as Error)?.message ?? e) }), "error");
    } finally {
      setBusy(false);
    }
  };

  if (!view) return <p className="td-onboarding__loading">{t("common.loading")}</p>;

  // Not enabled: the onboarding card — one-click bootstrap or CLI join guide.
  if (!view.enabled) {
    return (
      <div className="td-onboarding">
        <div className="td-onboarding__intro">
          <ShieldCheck size={18} />
          <div>
            <div className="td-onboarding__title">{t("trustdomain.onboarding.title")}</div>
            <p>{t("trustdomain.onboarding.intro")}</p>
          </div>
        </div>
        <TrustDomainOnboardingSteps busy={busy} onCreate={createDomain} onEnable={enableConfig} />
      </div>
    );
  }
  // Enabled but not joined (no ledger yet): same two paths, plus the reason.
  if (!view.joined) {
    return (
      <div className="td-onboarding">
        <div className="td-onboarding__notice">
          <div className="td-onboarding__title">{t("trustdomain.notJoined.title")}</div>
          <p>{t("trustdomain.notJoined.detail", { detail: view.detail ?? "" })}</p>
        </div>
        <TrustDomainOnboardingSteps busy={busy} onCreate={createDomain} onEnable={enableConfig} />
      </div>
    );
  }

  return (
    <div className="td-panel">
      {view.paused && <div className="banner banner--warn">{t("trustdomain.pausedBanner")}</div>}
      <header className="td-panel__head">
        <div>
          <strong>{t("trustdomain.domain")}</strong>
          <code title={`${t("trustdomain.domainIdTip")}\n${view.domain ?? ""}`}>{short(view.domain ?? "", 16)}</code>
          <span className="td-panel__meta">
            <span title={t("trustdomain.heightTip")}>{t("trustdomain.height", { n: view.height })}</span>
            <span className="td-panel__sep" aria-hidden>·</span>
            <span title={t("trustdomain.quorumTip")}>{t("trustdomain.quorum", { n: view.quorum ?? 0 })}</span>
            {view.me ? (
              <>
                <span className="td-panel__sep" aria-hidden>·</span>
                <span title={t("trustdomain.meTip")}>{t("trustdomain.meLabel")} {short(view.me, 8)}</span>
              </>
            ) : null}
          </span>
        </div>
        <div className="td-panel__actions">
          {view.paused ? (
            <button className="btn btn--primary btn--small" title={t("trustdomain.resumeTip")} disabled={busy} onClick={() => run(() => app.TrustDomainResume(), t("trustdomain.resumed"), t("trustdomain.resume"))}>
              {t("trustdomain.resume")}
            </button>
          ) : (
            <button className="btn btn--secondary btn--small danger" title={t("trustdomain.pauseTip")} disabled={busy} onClick={() => void pause()}>
              {t("trustdomain.pause")}
            </button>
          )}
          <button className="btn btn--secondary btn--small" title={t("trustdomain.anchorTip")} disabled={busy} onClick={() => run(() => app.TrustDomainAnchor(), t("trustdomain.anchored"), t("trustdomain.anchor"))}>
            {t("trustdomain.anchor")}
          </button>
        </div>
      </header>

      <section>
        <h4>{t("trustdomain.members")}</h4>
        <ul className="td-panel__members">
          {(view.members ?? []).map((m) => (
            <li key={m.id} className={m.role === "admin" ? "td-panel__member--admin" : ""}>
              <code title={t("trustdomain.memberTip")}>{short(m.id, 12)}</code>
              {m.id === view.me && <span className="td-panel__self">{t("trustdomain.thisHost")}</span>}
              {m.name ? <span>{m.name}</span> : null}
              <em title={t("trustdomain.quorumTip")}>{m.role === "admin" ? t("trustdomain.roleAdmin") : t("trustdomain.roleMember")}</em>
              {m.attestation ? <small title={t("trustdomain.attestTip")}>{m.attestation}</small> : null}
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
        <h4 title={t("trustdomain.tokensTip")}>{t("trustdomain.tokens")}</h4>
        {(view.tokens ?? []).length === 0 ? (
          <p className="td-panel__meta">{t("trustdomain.noTokens")}</p>
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

      <p className="td-panel__meta">{t("trustdomain.cliHint")}</p>

      <div className="td-panel__maint">
        <span className="td-panel__maint-label">{t("trustdomain.maintLabel")}</span>
        <button className="btn btn--secondary btn--small" disabled={busy} onClick={() => void disableDomain()}>
          {t("trustdomain.disable")}
        </button>
        <button className="btn btn--secondary btn--small danger" disabled={busy} onClick={() => void resetDomain()}>
          {t("trustdomain.reset")}
        </button>
      </div>
    </div>
  );
}

// TrustDomainOnboardingSteps renders the two enable paths shared by the
// disabled and not-joined states: one-click bootstrap (Option A) and the
// copyable CLI admission flow for joining an existing domain (Option B).
function TrustDomainOnboardingSteps({ busy, onCreate, onEnable }: { busy: boolean; onCreate: () => void; onEnable: () => void }) {
  const t = useT();
  return (
    <div className="td-onboarding__steps">
      <div className="td-onboarding__card">
        <div className="td-onboarding__card-title">{t("trustdomain.onboarding.createTitle")}</div>
        <p className="td-onboarding__card-desc">{t("trustdomain.onboarding.createDesc")}</p>
        <button className="btn btn--primary" disabled={busy} onClick={onCreate}>
          {t("trustdomain.onboarding.createBtn")}
        </button>
      </div>
      <div className="td-onboarding__card">
        <div className="td-onboarding__card-title">{t("trustdomain.onboarding.joinTitle")}</div>
        <p className="td-onboarding__card-desc">{t("trustdomain.onboarding.joinDesc")}</p>
        <div className="td-onboarding__step">
          <span>{t("trustdomain.onboarding.stepEnable")}</span>
          <button className="btn btn--secondary btn--small" disabled={busy} onClick={onEnable}>
            {t("trustdomain.onboarding.enableBtn")}
          </button>
        </div>
        <div className="td-onboarding__step">
          <span>{t("trustdomain.onboarding.stepIdentity")}</span>
          <div className="td-onboarding__cmd"><code>{TD_IDENTITY_CLI}</code><CopyButton text={TD_IDENTITY_CLI} /></div>
        </div>
        <div className="td-onboarding__step">
          <span>{t("trustdomain.onboarding.stepAdmit")}</span>
          <div className="td-onboarding__cmd"><code>{t("trustdomain.onboarding.admitCli")}</code><CopyButton text="fairpeer trustdomain admit <key-file> --name <host>" /></div>
        </div>
        <div className="td-onboarding__step">
          <span>{t("trustdomain.onboarding.stepJoin")}</span>
          <div className="td-onboarding__cmd"><code>{t("trustdomain.onboarding.joinCli")}</code><CopyButton text="fairpeer trustdomain join <host:port> <domainID>" /></div>
        </div>
        <p className="td-onboarding__card-desc">{t("trustdomain.onboarding.afterJoin")}</p>
      </div>
    </div>
  );
}

function short(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) + "…" : s;
}
