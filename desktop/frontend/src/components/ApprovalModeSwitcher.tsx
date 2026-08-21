import { useCallback, useEffect, useRef, useState } from "react";
import { Check, ChevronsUpDown, Shield, ShieldAlert, ShieldCheck } from "lucide-react";
import { useT } from "../lib/i18n";
import type { DictKey } from "../lib/i18n";
import type { ToolApprovalMode } from "../lib/types";
import { ANCHORED_POPOVER_CLOSE_MS, AnchoredPopover } from "./AnchoredPopover";
import { Tooltip } from "./Tooltip";

// ApprovalModeSwitcher mirrors EffortSwitcher/ModelSwitcher: an AnchoredPopover
// dropdown replacing the old three-button modebar. Three permission postures,
// each named for what it does to the user's files — 变更询问 / 自动编辑 /
// 完全访问 (uniform four-character labels). Menu items stay compact one-liners;
// the per-mode description rides on hover so the open menu reads at a glance
// while the full explanation is still one hover away. The trigger is tinted per
// mode (full access reads as a warning) because the active posture is
// safety-relevant state the user should see without opening anything.
const MODES: Array<{
  value: ToolApprovalMode;
  icon: typeof Shield;
  labelKey: DictKey;
  titleKey: DictKey;
  descKey: DictKey;
}> = [
  {
    value: "ask",
    icon: Shield,
    labelKey: "composer.accessAsk",
    titleKey: "composer.accessAskTitle",
    descKey: "composer.accessAskDesc",
  },
  {
    value: "auto",
    icon: ShieldCheck,
    labelKey: "composer.accessAuto",
    titleKey: "composer.accessAutoTitle",
    descKey: "composer.accessAutoDesc",
  },
  {
    value: "yolo",
    icon: ShieldAlert,
    labelKey: "composer.accessYolo",
    titleKey: "composer.accessYoloTitle",
    descKey: "composer.accessYoloDesc",
  },
];

export function ApprovalModeSwitcher({
  mode,
  disabled = false,
  onPick,
}: {
  mode: ToolApprovalMode;
  disabled?: boolean;
  onPick: (mode: ToolApprovalMode) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [closing, setClosing] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const closeTimerRef = useRef<number | null>(null);

  const clearCloseTimer = useCallback(() => {
    if (closeTimerRef.current === null) return;
    window.clearTimeout(closeTimerRef.current);
    closeTimerRef.current = null;
  }, []);

  const openMenu = useCallback(() => {
    clearCloseTimer();
    setClosing(false);
    setOpen(true);
  }, [clearCloseTimer]);

  const closeMenu = useCallback(
    (afterClose?: () => void) => {
      clearCloseTimer();
      setClosing(true);
      window.requestAnimationFrame(() => setOpen(false));
      const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      closeTimerRef.current = window.setTimeout(() => {
        closeTimerRef.current = null;
        setClosing(false);
        afterClose?.();
      }, reduceMotion ? 0 : ANCHORED_POPOVER_CLOSE_MS);
    },
    [clearCloseTimer],
  );

  useEffect(() => () => clearCloseTimer(), [clearCloseTimer]);

  const current = MODES.find((m) => m.value === mode) ?? MODES[0];
  const CurrentIcon = current.icon;
  const pick = (next: ToolApprovalMode) => {
    closeMenu(() => {
      if (next !== mode) onPick(next);
    });
  };

  return (
    <div className={`modelsw approvalsw approvalsw--${current.value}`}>
      <Tooltip label={t(current.titleKey)} disabled={open && !closing}>
        <button
          ref={triggerRef}
          type="button"
          className="modelsw__trigger approvalsw__trigger"
          disabled={disabled}
          aria-haspopup="menu"
          aria-expanded={open && !closing}
          aria-label={t("composer.accessMenuTitle")}
          onClick={() => (open || closing ? closeMenu() : openMenu())}
        >
          <CurrentIcon size={13} className="modelsw__kind approvalsw__kind" />
          <span className="modelsw__label">{t(current.labelKey)}</span>
          <ChevronsUpDown size={11} />
        </button>
      </Tooltip>
      <AnchoredPopover
        open={open && !disabled}
        closing={closing}
        anchorRef={triggerRef}
        onClose={() => closeMenu()}
        className="composer-access-menu approvalsw__menu"
        align="start"
      >
        <div className="composer-access-menu__section">
          <div className="composer-access-menu__label">{t("composer.accessMenuTitle")}</div>
          {MODES.map(({ value, icon: Icon, labelKey, descKey }) => (
            <Tooltip key={value} label={t(descKey)} side="top">
              <button
                type="button"
                role="menuitemradio"
                aria-checked={value === mode}
                className={`composer-access-menu__item approvalsw__item${value === mode ? " composer-access-menu__item--active" : ""}`}
                onClick={() => pick(value)}
              >
                <Icon size={15} />
                <span className="composer-access-menu__title approvalsw__label">{t(labelKey)}</span>
                {value === mode && <Check size={13} className="approvalsw__check" />}
              </button>
            </Tooltip>
          ))}
        </div>
      </AnchoredPopover>
    </div>
  );
}
