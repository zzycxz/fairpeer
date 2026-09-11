#!/bin/bash
# UOS/Kylin launcher wrapper — /opt/apps/com.fairpeer.desktop/files/bin/fairpeer
#
# Two domestic-desktop fixes the raw binary can't do for itself:
#  1. IME env: DDE ships fcitx; UKUI may ship fcitx or ibus. The WebKitGTK
#     webview only routes composition correctly when the GTK module vars are
#     set, and DE-launched processes often miss them. Explicit user config is
#     never overridden.
#  2. GPU mitigation: domestic desktops commonly pair weak/out-of-tree GPUs
#     (景嘉微 JM7200/JM9, Zhaoxin C-960 iGPU on llvmpipe). UOS's own KB
#     documents browser white-screens on this class, fixed only by disabling
#     hardware compositing — the same failure WEBKIT_DISABLE_COMPOSITING_MODE=1
#     addresses. It ships ON by default (correctness over animation perf on
#     gov-desktop GPUs); comment it out for fleets with proven GL.
set -u
export WEBKIT_DISABLE_COMPOSITING_MODE=1
if [ -z "${GTK_IM_MODULE:-}" ]; then
  if pgrep -x fcitx >/dev/null 2>&1 || pgrep -x fcitx5 >/dev/null 2>&1; then
    export GTK_IM_MODULE=fcitx QT_IM_MODULE=fcitx XMODIFIERS=@im=fcitx
  elif pgrep -x ibus-daemon >/dev/null 2>&1; then
    export GTK_IM_MODULE=ibus QT_IM_MODULE=ibus XMODIFIERS=@im=ibus
  fi
fi
exec /opt/apps/com.fairpeer.desktop/files/bin/fairpeer-desktop "$@"
