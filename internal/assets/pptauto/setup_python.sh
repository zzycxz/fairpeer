#!/usr/bin/env bash
# ppt-auto skill — macOS/Linux 依赖安装脚本
# 安装 requirements.txt 中的 python-pptx / Pillow / cairosvg 等依赖。
# 本脚本不会安装 Python 本身；请确保系统已有 Python 3.10+。
#
# 安装采用分层策略，规避 PEP 668（Homebrew Python 与 Debian 12+ /
# Ubuntu 23.04+ 的系统环境对 `pip install --user` 直接报
# externally-managed-environment，重试同样的命令永远不会成功）：
#   1. uv 可用          → uv venv 建/复用技能目录内的 .venv + uv pip 安装
#   2. python3 -m venv  → .venv + venv 内 pip 安装（Debian 需 python3-venv）
#   3. 兜底             → pip install --user --break-system-packages
# 只有核心依赖真实可导入才以退出码 0 结束；桌面端只在退出码 0 时写入
# .deps-installed 标记，失败则下次启动重试（增量修复，不会反复全量重装）。
#
# 用法：  bash setup_python.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REQ="$SCRIPT_DIR/requirements.txt"
VENV_DIR="$SCRIPT_DIR/.venv"

# 选择可用的 python3 解释器（优先 python3，回退 python；实际执行 --version
# 验证后才采用，规避商店/占位 stub——能被 command -v 找到但运行即失败）。
BASE_PY=""
if command -v python3 >/dev/null 2>&1 && python3 --version >/dev/null 2>&1; then
    BASE_PY="python3"
elif command -v python >/dev/null 2>&1 && python --version >/dev/null 2>&1; then
    BASE_PY="python"
fi

# verify_imports <python>：核心依赖必须真实可导入才算安装成功。
# 渲染器 cairosvg / resvg_py 二选一即可（cairosvg 依赖原生 cairo 库，
# resvg-py 是零系统依赖的兜底渲染器，故不能把 cairosvg 单独设为硬性要求）。
verify_imports() {
    "$1" - <<'EOF'
import sys

missing = []
for mod in ("pptx", "PIL", "numpy"):
    try:
        __import__(mod)
    except Exception:
        missing.append(mod)
try:
    __import__("cairosvg")
except Exception:
    try:
        __import__("resvg_py")
    except Exception:
        missing.append("cairosvg/resvg_py")
if missing:
    print("缺少依赖: %s" % ", ".join(missing), file=sys.stderr)
    sys.exit(1)
EOF
}

# --- 安装（逐层回退，先成功者胜）----------------------------------------

PYBIN=""

# 1) uv 优先：不动系统环境，依赖隔离在技能目录的 .venv 里
if command -v uv >/dev/null 2>&1; then
    echo "使用 uv 创建/复用虚拟环境: $VENV_DIR"
    if uv venv "$VENV_DIR" \
        && VIRTUAL_ENV="$VENV_DIR" uv pip install -r "$REQ"; then
        PYBIN="$VENV_DIR/bin/python"
    else
        echo "uv 安装失败，回退 python -m venv ..." >&2
    fi
fi

# 2) 标准 venv：无 uv 时走这条；Debian/Ubuntu 缺 python3-venv 会在此失败
if [ -z "$PYBIN" ] && [ -n "$BASE_PY" ]; then
    echo "使用 $BASE_PY 创建/复用虚拟环境: $VENV_DIR"
    if "$BASE_PY" -m venv "$VENV_DIR" \
        && "$VENV_DIR/bin/pip" install -r "$REQ"; then
        PYBIN="$VENV_DIR/bin/python"
    else
        echo "venv 安装失败（Debian/Ubuntu 请先: sudo apt install python3-venv python3-pip），回退 --user ..." >&2
    fi
fi

# 3) 兜底：装进用户目录。--break-system-packages 过 PEP 668；旧版 pip 不认
#    识该选项时会因未知参数报错，再退回纯 --user（无 PEP 668 的老系统有效）
if [ -z "$PYBIN" ] && [ -n "$BASE_PY" ]; then
    echo "兜底：安装到用户目录（--user --break-system-packages）..."
    if ! "$BASE_PY" -m pip install --user --break-system-packages -r "$REQ"; then
        "$BASE_PY" -m pip install --user -r "$REQ"
    fi
    PYBIN="$BASE_PY"
fi

if [ -z "$PYBIN" ]; then
    echo "ERROR: 未找到可用的 python3。请先安装 Python 3.10+：" >&2
    echo "  macOS:   brew install python（或 brew install uv）" >&2
    echo "  Ubuntu:  sudo apt install python3 python3-pip python3-venv" >&2
    exit 1
fi

echo "使用解释器: $($PYBIN --version 2>&1)"

# --- 验证：核心依赖真实可导入才允许成功（退出码非 0 时不落成功标记）------
if ! verify_imports "$PYBIN"; then
    echo "" >&2
    echo "ERROR: 依赖导入验证失败，请检查上方错误信息。" >&2
    echo "  （macOS 若 cairosvg/resvg 均报缺库，请先运行：brew install cairo）" >&2
    exit 1
fi

echo ""
echo "✓ 依赖安装完成并验证通过。现在可以使用 ppt-auto 技能生成 PPT 了。"
echo "  （macOS 若 cairosvg 报错，请先运行：brew install cairo）"
