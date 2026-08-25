import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { app } from "../lib/bridge";
import { useT, type DictKey } from "../lib/i18n";

const stepKeysOrder: Step[] = ["kind", "config", "hostkey", "connecting", "directory"];
const stepLabelKeys = {
  kind: "remote.step.kind",
  config: "remote.step.config",
  hostkey: "remote.step.hostkey",
  connecting: "remote.step.connecting",
  directory: "remote.step.directory",
} as const;

// RemoteConnectWizard — the 远程连接 flow: pick a connection kind (WSL live in
// P1; Docker/SSH/Server reserved), configure it, connect (binary provisioning
// + handshake happen in the Go transport), then pick the remote directory to
// open as a tab. Mirrors the desktop's local "打开文件夹" flow for remote
// workspaces: the agent, files, git and terminal all run on the remote side.

type WslDistro = { name: string; state: string; version: number; default: boolean };
type DockerContainer = { ID: string; Image: string; Names: string; State: string; Status: string };
type SSHAlias = { alias: string; host: string; user: string; port: number };
type FsEntry = { name: string; dir: boolean };
type ProbeResult = { version: string; goos: string; arch: string; homeDir: string };

type Step = "kind" | "config" | "hostkey" | "connecting" | "directory";

const KINDS = [
  { id: "wsl", available: true },
  { id: "docker", available: true },
  { id: "ssh", available: true },
  { id: "server", available: true },
] as const;

export function RemoteConnectWizard({ onClose }: { onClose: () => void }) {
  const t = useT();
  const [step, setStep] = useState<Step>("kind");
  const [kind, setKind] = useState<string>("wsl");
  const [distros, setDistros] = useState<WslDistro[] | null>(null);
  const [distro, setDistro] = useState("");
  const [containers, setContainers] = useState<DockerContainer[] | null>(null);
  const [container, setContainer] = useState("");
  const [user, setUser] = useState("");
  const [sshHost, setSSHHost] = useState("");
  const [sshPort, setSSHPort] = useState("22");
  const [sshUser, setSSHUser] = useState("");
  const [sshAuth, setSSHAuth] = useState<"password" | "privateKey">("password");
  const [sshPassword, setSSHPassword] = useState("");
  const [sshKeyPath, setSSHKeyPath] = useState("");
  const [sshAliases, setSSHAliases] = useState<SSHAlias[] | null>(null);
  const [serverAddr, setServerAddr] = useState("");
  const [serverToken, setServerToken] = useState("");
  const [serverTLS, setServerTLS] = useState(false);
  const [hostFingerprint, setHostFingerprint] = useState("");
  const [logs, setLogs] = useState<string[]>([]);
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [cwd, setCwd] = useState<string[]>([]);
  const [entries, setEntries] = useState<FsEntry[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const logRef = useRef<HTMLDivElement>(null);

  const log = useCallback((line: string) => {
    setLogs((prev) => [...prev.slice(-200), line]);
  }, []);

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight });
  }, [logs]);

  useEffect(() => {
    if (step !== "config" || kind !== "ssh" || sshAliases !== null) return;
    app.NetDevSSHImportCandidates()
      .then((list) => setSSHAliases(list ?? []))
      .catch(() => setSSHAliases([]));
  }, [step, kind, sshAliases]);

  useEffect(() => {
    if (step !== "config" || kind !== "docker" || containers !== null) return;
    app.ListDockerContainers()
      .then((list) => {
        setContainers(list ?? []);
        if (list?.length) setContainer(list[0].Names.split(",")[0]);
      })
      .catch(() => setContainers([]));
  }, [step, kind, containers]);

  useEffect(() => {
    if (step !== "kind" || distros !== null) return;
    app.ListWSLDistros()
      .then((list) => {
        setDistros(list ?? []);
        const def = list?.find((d) => d.default) ?? list?.[0];
        if (def) setDistro(def.name);
      })
      .catch(() => setDistros([]));
  }, [step, distros]);

  const currentPath = useMemo(() => "/" + cwd.join("/"), [cwd]);

  const sshTargetValue = sshHost.trim();
  const target =
    kind === "docker" ? container : kind === "ssh" ? sshTargetValue : kind === "server" ? serverAddr.trim() : distro;
  const connectUser = kind === "docker" || kind === "ssh" || kind === "server" ? "" : user.trim();

  const startConnect = useCallback(async () => {
    setError("");
    setEntries(null);
    if (kind === "ssh") {
      // First-connect fingerprint confirmation (no silent TOFU): unknown keys
      // are rejected by the transport until the user trusts them here.
      try {
        const info = await app.SSHInspectHost(sshHost.trim(), sshPort.trim(), sshUser.trim());
        setHostFingerprint(info.fingerprint);
        if (!info.trusted) {
          setStep("hostkey");
          return;
        }
      } catch (err) {
        setError(String(err));
        return;
      }
    }
    setStep("connecting");
    setLogs([]);
    await app.RemoteWizardClose().catch(() => {});
    log(t("remote.logDialing", { kind, target }));
    try {
      const res =
        kind === "ssh"
          ? await app.SSHConnect(sshHost.trim(), sshPort.trim(), sshUser.trim(), sshAuth, sshPassword, sshKeyPath.trim(), "")
          : kind === "server"
          ? await app.ServerConnect(serverAddr.trim(), serverToken, serverTLS)
          : await app.RemoteConnectProbe(kind, target, connectUser);
      setProbe(res);
      log(t("remote.logConnected", { version: res.version, goos: res.goos, arch: res.arch }));
      const startDir = res.homeDir && res.homeDir !== "/" ? res.homeDir : "";
      setCwd(startDir ? startDir.replace(/^\/+/, "").split("/").filter(Boolean) : []);
      setStep("directory");
    } catch (err) {
      setError(String(err));
      log(t("remote.logFailed", { error: String(err) }));
    }
  }, [kind, target, connectUser, sshHost, sshPort, sshUser, sshAuth, sshPassword, sshKeyPath, serverAddr, serverToken, serverTLS, log]);

  const trustAndConnect = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      await app.SSHTrustHost(sshHost.trim(), sshPort.trim());
      setStep("connecting");
      setLogs([]);
      log(t("remote.logDialing", { kind, target }));
      const res = await app.SSHConnect(sshHost.trim(), sshPort.trim(), sshUser.trim(), sshAuth, sshPassword, sshKeyPath.trim(), "");
      setProbe(res);
      const startDir = res.homeDir && res.homeDir !== "/" ? res.homeDir : "";
      setCwd(startDir ? startDir.replace(/^\/+/, "").split("/").filter(Boolean) : []);
      setStep("directory");
    } catch (err) {
      setError(String(err));
      log(t("remote.logFailed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  }, [kind, target, sshHost, sshPort, sshUser, sshAuth, sshPassword, sshKeyPath, log]);

  const browse = useCallback(
    async (next: string[]) => {
      setBusy(true);
      setError("");
      const path = "/" + next.join("/");
      try {
        const list = await app.RemoteBrowseList(path);
        setEntries(list ?? []);
        setCwd(next);
      } catch (err) {
        setError(String(err));
      } finally {
        setBusy(false);
      }
    },
    [],
  );

  useEffect(() => {
    if (step === "directory" && entries === null) void browse(cwd);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  const openTab = useCallback(async () => {
    setBusy(true);
    try {
      await app.OpenRemoteTab(kind, target, connectUser, currentPath, "");
      await app.RemoteWizardClose();
      onClose();
    } catch (err) {
      setError(String(err));
      setBusy(false);
    }
  }, [kind, target, connectUser, currentPath, onClose]);

  const close = useCallback(() => {
    if (step === "connecting") return;
    void app.RemoteWizardClose().catch(() => {});
    onClose();
  }, [step, onClose]);

  const canOpen = !busy && (probe ? true : false) && currentPath !== "/";

  return (
    <div className="remote-wizard-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="remote-wizard">
        <header className="remote-wizard-head">
          <div>
            <h2>{t("remote.title")}</h2>
            <p>{t("remote.description")}</p>
          </div>
          <button className="remote-wizard-close" onClick={close} aria-label="close">✕</button>
        </header>
        <ol className="remote-wizard-steps">
          {(stepKeysOrder.filter((k) => k !== "hostkey" || kind === "ssh") as Step[]).map((s, i) => (
            <li key={s} data-state={step === s ? "current" : stepIndex(step) > i ? "done" : "todo"}>
              {t(stepLabelKeys[s])}
            </li>
          ))}
        </ol>

        {error && <div className="remote-wizard-error">{error}</div>}

        {step === "kind" && (
          <div className="remote-wizard-body">
            <div className="remote-kinds">
              {KINDS.map((k) => (
                <button
                  key={k.id}
                  className="remote-kind"
                  data-selected={kind === k.id}
                  disabled={!k.available}
                  onClick={() => setKind(k.id)}
                >
                  <span className="remote-kind-name">{t(`remote.kind.${k.id}` as DictKey)}</span>
                  <span className="remote-kind-desc">
                    {k.available ? t(`remote.kindDesc.${k.id}` as DictKey) : t("remote.comingSoon")}
                  </span>
                </button>
              ))}
            </div>
            <footer className="remote-wizard-foot">
              <button className="remote-btn" onClick={close}>{t("common.cancel")}</button>
              <button className="remote-btn primary" onClick={() => setStep("config")} disabled={!["wsl", "docker", "ssh", "server"].includes(kind)}>
                {t("remote.next")}
              </button>
            </footer>
          </div>
        )}

        {step === "config" && (
          <div className="remote-wizard-body">
            {kind === "wsl" ? (
              <>
                <label className="remote-field">
                  <span>{t("remote.distro")}</span>
                  <select value={distro} onChange={(e) => setDistro(e.target.value)} disabled={distros === null}>
                    <option value="">{distros === null ? t("remote.loading") : t("remote.distroEmpty")}</option>
                    {(distros ?? []).map((d) => (
                      <option key={d.name} value={d.name}>
                        {d.name} · {d.state}
                        {d.default ? " · " + t("remote.distroDefault") : ""}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="remote-field">
                  <span>{t("remote.user")}</span>
                  <input value={user} onChange={(e) => setUser(e.target.value)} placeholder={t("remote.userPlaceholder")} />
                </label>
                <p className="remote-hint">{t("remote.wslHint")}</p>
              </>
            ) : kind === "server" ? (
              <>
                <label className="remote-field">
                  <span>{t("remote.address")}</span>
                  <input value={serverAddr} onChange={(e) => setServerAddr(e.target.value)} placeholder={t("remote.addressPlaceholder")} />
                </label>
                <label className="remote-field">
                  <span>{t("remote.token")}</span>
                  <input type="password" value={serverToken} onChange={(e) => setServerToken(e.target.value)} placeholder={t("remote.tokenPlaceholder")} />
                </label>
                <label className="remote-check">
                  <input type="checkbox" checked={serverTLS} onChange={(e) => setServerTLS(e.target.checked)} />
                  <span>{t("remote.useTLS")}</span>
                </label>
                <p className="remote-hint">{serverTLS ? t("remote.tlsHint") : t("remote.serverHint")}</p>
              </>
            ) : kind === "ssh" ? (
              <>
                <label className="remote-field">
                  <span>{t("remote.alias")}</span>
                  <select
                    value=""
                    onChange={(e) => {
                      const a = (sshAliases ?? []).find((x) => x.alias === e.target.value);
                      if (a) {
                        setSSHHost(a.host || a.alias);
                        setSSHPort(a.port ? String(a.port) : "22");
                        setSSHUser(a.user || "");
                      }
                    }}
                  >
                    <option value="">{sshAliases === null ? t("remote.loading") : t("remote.aliasEmpty")}</option>
                    {(sshAliases ?? []).map((a) => (
                      <option key={a.alias} value={a.alias}>
                        {a.alias} → {a.user ? a.user + "@" : ""}{a.host}{a.port ? ":" + a.port : ""}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="remote-field">
                  <span>{t("remote.host")}</span>
                  <input value={sshHost} onChange={(e) => setSSHHost(e.target.value)} placeholder={t("remote.hostPlaceholder")} />
                </label>
                <div className="remote-field-row">
                  <label className="remote-field">
                    <span>{t("remote.port")}</span>
                    <input value={sshPort} onChange={(e) => setSSHPort(e.target.value)} />
                  </label>
                  <label className="remote-field">
                    <span>{t("remote.username")}</span>
                    <input value={sshUser} onChange={(e) => setSSHUser(e.target.value)} placeholder={t("remote.usernamePlaceholder")} />
                  </label>
                </div>
                <label className="remote-field">
                  <span>{t("remote.authMethod")}</span>
                  <select value={sshAuth} onChange={(e) => setSSHAuth(e.target.value === "privateKey" ? "privateKey" : "password")}>
                    <option value="password">{t("remote.auth.password")}</option>
                    <option value="privateKey">{t("remote.auth.privateKey")}</option>
                  </select>
                </label>
                {sshAuth === "password" ? (
                  <label className="remote-field">
                    <span>{t("remote.password")}</span>
                    <input type="password" value={sshPassword} onChange={(e) => setSSHPassword(e.target.value)} placeholder={t("remote.passwordPlaceholder")} />
                  </label>
                ) : (
                  <label className="remote-field">
                    <span>{t("remote.keyPath")}</span>
                    <input value={sshKeyPath} onChange={(e) => setSSHKeyPath(e.target.value)} placeholder={t("remote.keyPathPlaceholder")} />
                  </label>
                )}
                <p className="remote-hint">{t("remote.sshHint")}</p>
              </>
            ) : (
              <>
                <label className="remote-field">
                  <span>{t("remote.container")}</span>
                  <select value={container} onChange={(e) => setContainer(e.target.value)} disabled={containers === null}>
                    <option value="">{containers === null ? t("remote.loading") : t("remote.containerEmpty")}</option>
                    {(containers ?? []).map((c) => (
                      <option key={c.ID} value={c.Names.split(",")[0]}>
                        {c.Names.split(",")[0]} · {c.Image} · {c.Status}
                      </option>
                    ))}
                  </select>
                </label>
                <p className="remote-hint">{t("remote.dockerHint")}</p>
              </>
            )}
            <footer className="remote-wizard-foot">
              <button className="remote-btn" onClick={() => setStep("kind")}>{t("remote.back")}</button>
              <button className="remote-btn primary" onClick={startConnect} disabled={!target}>
                {t("remote.connect")}
              </button>
            </footer>
          </div>
        )}

        {step === "hostkey" && (
          <div className="remote-wizard-body">
            <p className="remote-hint">{t("remote.hostkeyBody")}</p>
            <div className="remote-fingerprint">
              <code>{hostFingerprint}</code>
            </div>
            <p className="remote-hint">{t("remote.hostkeyHint")}</p>
            <footer className="remote-wizard-foot">
              <button className="remote-btn" onClick={() => setStep("config")}>{t("remote.back")}</button>
              <button className="remote-btn primary" onClick={() => void trustAndConnect()} disabled={busy}>
                {t("remote.trustAndConnect")}
              </button>
            </footer>
          </div>
        )}

        {step === "connecting" && (
          <div className="remote-wizard-body">
            <div className="remote-log" ref={logRef}>
              {logs.map((l, i) => (
                <div key={i}>{l}</div>
              ))}
            </div>
            <footer className="remote-wizard-foot">
              <button className="remote-btn" disabled>{t("remote.connecting")}</button>
            </footer>
          </div>
        )}

        {step === "directory" && (
          <div className="remote-wizard-body remote-picker">
            <div className="remote-path">
              <button className="remote-crumb" onClick={() => void browse([])}>/</button>
              {cwd.map((seg, i) => (
                <button key={i} className="remote-crumb" onClick={() => void browse(cwd.slice(0, i + 1))}>
                  {seg}
                </button>
              ))}
            </div>
            <div className="remote-entries">
              {entries === null && <div className="remote-empty">{t("remote.loading")}</div>}
              {entries?.length === 0 && <div className="remote-empty">{t("remote.dirEmpty")}</div>}
              {(entries ?? [])
                .filter((e) => e.dir)
                .map((e) => (
                  <button
                    key={e.name}
                    className="remote-entry"
                    onDoubleClick={() => void browse([...cwd, e.name])}
                    onClick={() => void browse([...cwd, e.name])}
                  >
                    📁 {e.name}
                  </button>
                ))}
            </div>
            <footer className="remote-wizard-foot">
              <button className="remote-btn" onClick={() => setStep("config")}>{t("remote.back")}</button>
              <button className="remote-btn primary" onClick={openTab} disabled={!canOpen}>
                {t("remote.open", { path: currentPath })}
              </button>
            </footer>
          </div>
        )}
      </div>
    </div>
  );
}

function stepIndex(s: Step): number {
  return stepKeysOrder.indexOf(s);
}
