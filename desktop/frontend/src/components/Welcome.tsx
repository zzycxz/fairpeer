import logoWordmark from "../assets/welcome-hero.png";
import { useT } from "../lib/i18n";

// Welcome is the empty-state landing: a one-liner, the input affordances
// (/ commands, @ files, Enter), and a few clickable example prompts.
//
// Two profiles differ here:
//   - dev: the 4 welcome.ex1-4 prompts send immediately (one click → one turn),
//     via onPrompt. This is the original behavior.
//   - cowork: 6 office-oriented "starter" bubbles that FILL the composer
//     instead of sending (onInsert), so the user can tweak the prompt first.
//     These bind to cowork capabilities (weekly report, spreadsheet, mind map,
//     expert-team review) plus 2 generic ones (explain code, translate).
//
// profile + onInsert are optional props: Welcome renders the dev layout when
// profile is absent/"dev", preserving the old single-prop contract.

export function Welcome({
  onPrompt,
  profile,
  onInsert,
}: {
  onPrompt: (text: string) => void;
  profile?: "dev" | "cowork" | "netdev";
  onInsert?: (text: string) => void;
}) {
  const t = useT();

  // Dev profile: the classic 4 immediate-send examples.
  const devExamples = [t("welcome.ex1"), t("welcome.ex2"), t("welcome.ex3"), t("welcome.ex4")];

  // Netdev profile: 运维 examples — plain-text strings (no locale key churn);
  // each maps to a real netdev read-only capability (netdev_exec battery,
  // inspection summary, mermaid topology drawing).
  const netdevExamples = [
    "core-sw-1 的 OSPF 邻居一直 down，帮我排查",
    "看看全网设备的 CPU 和内存状态",
    "把当前全网拓扑画成图",
    "汇总今天的巡检结果",
  ];

  // Cowork profile: 6 office starter bubbles (4 office + 2 generic). Each
  // references a real cowork capability so a first-time user discovers what the
  // office mode can do. ex4 (expert-team review) is backed by the
  // expert_team_run tool registered under cowork.
  const coworkExamples = [
    t("welcome.coworkEx1"),
    t("welcome.coworkEx2"),
    t("welcome.coworkEx3"),
    t("welcome.coworkEx4"),
    t("welcome.coworkEx5"),
    t("welcome.coworkEx6"),
  ];

  const isCowork = profile === "cowork";
  const isNetdev = profile === "netdev";
  const examples = isCowork ? coworkExamples : isNetdev ? netdevExamples : devExamples;
  // Cowork bubbles fill the composer (editable before send); dev bubbles send
  // immediately (the original fast-start behavior). Fall back to onPrompt if the
  // cowork caller forgot to wire onInsert, so a click never dead-ends.
  const handlePick = (text: string) => {
    if (isCowork && onInsert) {
      onInsert(text);
    } else {
      onPrompt(text);
    }
  };

  return (
    <div className={`welcome welcome--brand${isCowork ? " welcome--cowork" : ""}${isNetdev ? " welcome--netdev" : ""}`}>
      <span className="welcome__brand">
        <img src={logoWordmark} className="welcome__brand-logo" alt="FairPeer" draggable={false} />
      </span>
      <h2 className="welcome__title">{isNetdev ? "描述故障，我来诊断" : t("welcome.title")}</h2>
      <div className="welcome__tag">
        {isNetdev ? "只读诊断 · 写操作走人工审批的提案 · 全程审计" : t("welcome.tagline")}
      </div>

      <div className="welcome__hints">
        {isNetdev ? (
          <>
            <span>
              <kbd>/</kbd> {t("welcome.hintCommands")}
            </span>
            <span>
              <kbd>⏎</kbd> {t("welcome.hintSend")}
            </span>
          </>
        ) : (
          <>
            <span>
              <kbd>/</kbd> {t("welcome.hintCommands")}
            </span>
            <span>
              <kbd>@</kbd> {t("welcome.hintFiles")}
            </span>
            <span>
              <kbd>⏎</kbd> {t("welcome.hintSend")}
            </span>
          </>
        )}
      </div>

      <div className="welcome__examples">
        {examples.map((ex) => (
          <button key={ex} className="welcome__ex" onClick={() => handlePick(ex)}>
            {ex}
          </button>
        ))}
      </div>
    </div>
  );
}
