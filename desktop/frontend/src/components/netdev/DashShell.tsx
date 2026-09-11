import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { app } from "../../lib/bridge";
import { useI18n } from "../../lib/i18n";
import type { NetDevAuditEntryView, NetDevOverviewSnapshot } from "../../lib/types";
import OverviewPanel, { OverviewJump } from "./OverviewPanel";
import ChainBoard from "./ChainBoard";
import CutoverBoardView from "./CutoverBoardView";
import DiscoveryBoardView from "./DiscoveryBoardView";
import ExposureBoardView from "./ExposureBoardView";

// DashShell — 大屏家族的壳（DASHBOARD spec §4.1/§4.11）。职责：五页签、
// 场景感知默认页、投影模式、底条审计 ticker、刷新纪律（§8.4：写侧事件
// 驱动 + 可见时 60s 兜底；割接屏例外不失焦暂停）。总览快照由壳持有并
// 喂给 OverviewPanel（壳与卡片不双拉）。

export type DashScreen = "overview" | "chain" | "cutover" | "discovery" | "exposure";
const SCREENS: DashScreen[] = ["overview", "chain", "cutover", "discovery", "exposure"];

// 底条分类 → 小色点档位（audit.class → CSS 修饰符）。读取类不着色（默认
// 灰点），写/评估=琥珀，护栏=红，割接/提案=蓝——只做轻量示意，不做告警式
// 排版；失败态由条目红字表达。
const TICK_DOT: Record<string, string> = {
  write: "write", assess: "write", guardrail: "guardrail",
  cutover: "change", proposal: "change", "proposal-write": "change", "proposal-rollback": "change",
};

interface Props {
  initialScreen?: DashScreen;
  initialFinding?: string;
  onClose: () => void;
  onToggleRightRail?: () => void;
  rightRailCollapsed?: boolean;
  onJump?: (j: OverviewJump) => void;
  onFocusDevice?: (device: string) => void;
  /** 手动切屏后置 true：场景感知默认页退位（会话内记住）。 */
  manualSignal?: { screen: DashScreen; finding?: string } | null;
}

export default function DashShell({ initialScreen, initialFinding, onClose, onToggleRightRail, rightRailCollapsed, onJump, onFocusDevice, manualSignal }: Props) {
  const { t } = useI18n();
  const [screen, setScreen] = useState<DashScreen>(initialScreen ?? "overview");
  const [finding, setFinding] = useState(initialFinding ?? "");
  const [snap, setSnap] = useState<NetDevOverviewSnapshot | null>(null);
  const [ticker, setTicker] = useState<NetDevAuditEntryView[]>([]);
  const [projection, setProjection] = useState(false);
  const [rotateSec, setRotateSec] = useState(30);
  const [paused, setPaused] = useState(false); // 兜底轮询手动暂停（⟳/∥）
  const manualRef = useRef(!!initialScreen);
  const hoverRef = useRef(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  // 场景感知默认页（§4.1）：未手动切过时按快照的 scenario 提示落位。
  const loadSnap = useCallback((force: boolean) => {
    app.NetDevOverview(force).then(s => {
      if (!s) return;
      setSnap(s);
      if (!manualRef.current) {
        if (s.scenario_cutover_active) setScreen("cutover");
        else if (s.scenario_discovery_run) setScreen("discovery");
        else setScreen("overview");
      }
    }).catch(() => {}); // best-effort: 失败降级不阻塞
  }, []);
  const loadTicker = useCallback(() => {
    app.NetDevAuditTail(20).then(a => setTicker(a ?? [])).catch(() => {}); // best-effort: 失败降级不阻塞
  }, []);

  useEffect(() => { loadSnap(true); loadTicker(); }, [loadSnap, loadTicker]);
  useEffect(() => { if (manualSignal) { manualRef.current = true; setScreen(manualSignal.screen); setFinding(manualSignal.finding ?? ""); } }, [manualSignal]);

  // 兜底轮询（§8.4）：仅可见时；割接屏例外（不失焦暂停——进行时优先连续性）。
  useEffect(() => {
    if (paused) return;
    let visible = typeof document === "undefined" || document.visibilityState === "visible";
    const onVis = () => { visible = document.visibilityState === "visible"; };
    document.addEventListener("visibilitychange", onVis);
    const tm = setInterval(() => {
      const cutoverException = screen === "cutover";
      if (visible || cutoverException) { loadSnap(false); loadTicker(); }
    }, 60_000);
    return () => { document.removeEventListener("visibilitychange", onVis); clearInterval(tm); };
  }, [paused, screen, loadSnap, loadTicker]);

  // 写侧事件（§3.4）：payload 只有屏枚举，不带数据。
  useEffect(() => {
    const on = () => {
      loadSnap(false);
      loadTicker();
    };
    window.addEventListener("fairpeer:netdev-dash", on);
    return () => window.removeEventListener("fairpeer:netdev-dash", on);
  }, [loadSnap, loadTicker]);

  // 底条轮播素材：同设备同动作聚合（"attempt N"/": CONFIRMED" 后缀归并），
// 时间保留该组最新一条——golden check 每轮记两条、弱口令连试 3 次这类
// 高频重复压成 "×N"，20 条原始尾通常只剩几条有效信息。
const tickItems = useMemo(() => {
  const seen = new Map<string, { time: string; device: string; command: string; cls: string; status: string; n: number }>();
  for (const a of ticker ?? []) { // ticker 已新→旧排序：首见即最新
    const key = `${a.device}|${a.command.replace(/\s*attempt \d+(?:: CONFIRMED)?$/i, "")}`;
    const hit = seen.get(key);
    if (hit) hit.n++;
    else seen.set(key, { time: a.time, device: a.device, command: a.command, cls: a.class, status: a.status, n: 1 });
  }
  return [...seen.values()];
}, [ticker]);
const [tickIdx, setTickIdx] = useState(0);
const [tickHover, setTickHover] = useState(false);
useEffect(() => { setTickIdx(0); }, [tickItems]);
useEffect(() => {
  if (tickHover || tickItems.length < 2) return;
  const tm = setInterval(() => setTickIdx(i => (i + 1) % tickItems.length), 4000);
  return () => clearInterval(tm);
}, [tickHover, tickItems]);

  // 投影模式（§4.11）：轮播 + 悬停暂停 + Esc 先退投影。
  useEffect(() => {
    const tm = setInterval(() => { if (!hoverRef.current && !paused) setScreen(s => SCREENS[(SCREENS.indexOf(s) + 1) % SCREENS.length]); }, rotateSec * 1000);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.stopPropagation(); setProjection(false); }
      // e.code, not e.key: on macOS Option+P types "π", so a key-based test
      // never matches there. KeyP is layout-stable across platforms.
      if (e.altKey && e.code === "KeyP") { e.preventDefault(); setProjection(p => !p); }
      if (projection && e.key === "Escape") { setProjection(false); }
    };
    window.addEventListener("keydown", onKey, true);
    return () => { clearInterval(tm); window.removeEventListener("keydown", onKey, true); };
  }, [projection, rotateSec, paused]);

  const screenBody = useMemo(() => {
    switch (screen) {
      case "chain": return <ChainBoard findingID={finding} onJump={onJump} onFocusDevice={onFocusDevice} />;
      case "cutover": return <CutoverBoardView onJump={onJump} onFocusDevice={onFocusDevice} />;
      case "discovery": return <DiscoveryBoardView onJump={onJump} onFocusDevice={onFocusDevice} />;
      case "exposure": return <ExposureBoardView onJump={onJump} onFocusDevice={onFocusDevice} />;
      default: return <OverviewPanel snapshot={snap} onJump={onJump} onFocusDevice={onFocusDevice} />;
    }
  }, [screen, finding, snap, onJump, onFocusDevice]);

  return (
    <div ref={rootRef} className={`ndv-dash${projection ? " ndv-dash--projection" : ""}`}
      onMouseEnter={() => { hoverRef.current = true; }}
      onMouseLeave={() => { hoverRef.current = false; }}>
      <div className="ndv-dash__bar" role="tablist" aria-label={t("ndv.dash.aria")}>
        <b className="ndv-dash__brand">{t("ndv.dash.title")}</b>
        {SCREENS.map((s, i) => (
          <span key={s} role="tab" aria-selected={screen === s}
            className={`ndv-dash__chip${screen === s ? " ndv-dash__chip--on" : ""}`}
            onClick={() => { manualRef.current = true; setScreen(s); }}>
            {t(`ndv.dash.screen.${s}`)}
            <kbd className="ndv-dash__alt">{i + 1}</kbd>
          </span>
        ))}
        <span className="ndv-dash__spacer" />
        <span className="ndv-dash__btn" role="button" title={t("ndv.dash.railTip")} onClick={() => onToggleRightRail?.()}>
          {rightRailCollapsed ? t("ndv.dash.railOpen") : t("ndv.dash.railClose")}
        </span>
        <span className="ndv-dash__btn" role="button" title={t("ndv.dash.pauseTip")} onClick={() => setPaused(p => !p)}>
          {paused ? t("ndv.dash.paused") : t("ndv.dash.auto")}
        </span>
        {projection && (
          <select className="ndv-dash__rot" value={rotateSec} onChange={e => setRotateSec(Number(e.target.value))} title={t("ndv.dash.rotTip")}>
            {[15, 30, 60].map(n => <option key={n} value={n}>{n}s</option>)}
          </select>
        )}
        <span className="ndv-dash__btn" role="button" title={t("ndv.dash.projTip")} onClick={() => setProjection(p => !p)}>
          {projection ? t("ndv.dash.projExit") : t("ndv.dash.proj")}
        </span>
        <span className="ndv-dash__btn ndv-dash__btn--close" role="button" onClick={onClose}>×</span>
      </div>

      <div className="ndv-dash__body" data-projection={projection}>
        {screenBody}
      </div>

      {/* 底条（§4.11）：审计尾的轮播呈现——单条淡入轮换（4s）+ 悬停暂停 +
          ‹ › 手动翻页；条目按设备+动作聚合（×N），分类只以小色点示意，
          失败条目沿用红字。整条点击进审计页。 */}
      <div className="ndv-dash__ticker" onMouseEnter={() => setTickHover(true)} onMouseLeave={() => setTickHover(false)}>
        <span className="ndv-dash__tickerlabel">{t("ndv.dash.ticker")}</span>
        {tickItems.length === 0 ? (
          <span className="dim">{t("ndv.dash.tickEmpty")}</span>
        ) : (() => {
          const idx = tickIdx % tickItems.length;
          const it = tickItems[idx];
          return (
            <span key={idx} className={`ndv-dash__tickitem${it.status !== "ok" ? " ndv-dash__tickitem--bad" : ""}`} role="button"
              title={it.command} onClick={() => onJump?.({ tab: "audit" })}>
              <span className={`ndv-dash__tickdot ndv-dash__tickdot--${TICK_DOT[it.cls] ?? ""}`} />
              <span className="dim">{(it.time ?? "").slice(0, 16)}</span>
              <span>{it.device || "—"}</span>
              <span className="dim">·</span>
              <span className="ndv-dash__tickcmd">{it.command}</span>
              {it.n > 1 && <span className="ndv-dash__tickcount">×{it.n}</span>}
            </span>
          );
        })()}
        {tickItems.length > 1 && (
          <span className="ndv-dash__ticknav">
            <span role="button" title={t("ndv.dash.tickPrev")} onClick={() => setTickIdx(i => (i - 1 + tickItems.length) % tickItems.length)}>‹</span>
            <span className="dim">{(tickIdx % tickItems.length) + 1}/{tickItems.length}</span>
            <span role="button" title={t("ndv.dash.tickNext")} onClick={() => setTickIdx(i => (i + 1) % tickItems.length)}>›</span>
          </span>
        )}
      </div>
    </div>
  );
}
