@echo off
REM ppt-auto skill - Windows 依赖安装脚本
REM 通过 pip 安装 requirements.txt 中的依赖（python-pptx / Pillow / cairosvg 等）。
REM 本脚本不会安装 Python 本身；请确保系统已装 Python 3.10+ 并加入 PATH。
REM
REM 用法：  setup_python.bat   （或双击运行）

setlocal

set "SKILL_DIR=%~dp0"
set "REQ=%SKILL_DIR%requirements.txt"

REM 选择可用的解释器（优先 py -3 启动器，回退 python，再回退 python3）。
REM 不能只用 where 探测：Windows 商店的 python3 别名占位程序能被 where 找到，
REM 但运行时只会打印商店提示、pip 完全不可用。所以每个候选都要实际执行
REM --version 验证成功后才采用，失败则继续尝试下一个。
py -3 --version >nul 2>&1 && (
    set "PY=py -3"
    goto :install
)
python --version >nul 2>&1 && (
    set "PY=python"
    goto :install
)
python3 --version >nul 2>&1 && (
    set "PY=python3"
    goto :install
)

echo ERROR: 未找到可用的 Python（py -3 / python / python3 均失败）。请先安装 Python 3.10+：
echo   https://www.python.org/downloads/   （安装时勾选 "Add to PATH"）
exit /b 1

:install
echo 使用解释器:
%PY% --version
echo 安装依赖...
%PY% -m pip install -r "%REQ%"
if errorlevel 1 (
    echo.
    echo ERROR: 依赖安装失败，请检查上面的错误信息。
    exit /b 1
)

echo.
echo [OK] 依赖安装完成。现在可以使用 ppt-auto 技能生成 PPT 了。
echo   （如需 PowerPoint 模板分析/PDF 导出，可额外运行：pip install comtypes）
exit /b 0
