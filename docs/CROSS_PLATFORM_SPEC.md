# FairPeer 跨平台规格说明书（现状 + 差距）

> **版本**: v2.0 | **日期**: 2026-09-10 | **状态**: 已按源码逐条验证
>
> **v2.0 说明**: 本版取代 v1.0（2026-08-05，"待评审"的 `internal/platform` 抽象层设计——该方案从未实施）。v2.0 以代码库**实际采用**的"按文件构建标签"模式为准，沉淀 2026-09 两轮跨平台排查（每轮 3 遍）修复的全部问题台账、当前能力契约与遗留差距。每条契约给出代码锚点（函数/文件名），供后续验证与回归。
>
> **平台目标**: Windows 10+（第一公民）、macOS 12+、Linux（Ubuntu 22.04+/Debian 12+/主流发行版；X11 一等公民，Wayland 显式降级）。BSD/exotic unix 平台仅保证可编译（stubs），不承诺功能。

---

## 一、适配架构约定（实际模式）

1. **按文件构建标签**，而非抽象层：`*_windows.go` / `*_darwin.go` / `*_linux.go` / `*_bsd.go` / `*_other.go`（或 `*_unix.go`）成对/成组出现；无标签文件只调用跨组都存在的符号。典型：`internal/proc/{hide,kill,priority,powershell}_{windows,other}.go`、`internal/secret/kek_*.go`、`internal/netdev/console_{windows,unix,other}.go + console_termios_{linux,darwin,bsd}.go`。
2. **GOOS 分支**仅当逻辑同文件时使用 `runtime.GOOS`；分支必须三平台显式（禁止 `!= "windows"` 当 unix 用而不考虑 darwin 差异）。
3. **外部依赖探测**优先 `exec.LookPath` + 可配置路径覆盖（`nmap_path`/`netprobe_path`/`CHROME_PATH`），错误信息必须列出所试工具与安装提示。
4. **设备侧 vs 宿主侧**：netdev 的按厂商命令表（driver/hosts.go）面向被管设备；宿主侧能力（本机抓屏/ARP/代理）按桌面 GOOS 分派——两条轴不得混淆。
5. **发布矩阵**：win/amd64+arm64、linux/amd64+arm64（webkit2_41 tag、CGO via webkitgtk）、darwin/amd64+arm64（CGO，必须真实 Mac 出包）；headless CLI（cmd/fairpeer）构造性无 CGO（cmd/ 下无 `import "C"`），Makefile `cross` 已显式钉 `CGO_ENABLED=0`（npm/build.mjs 同）。
6. **darwin 编译验证**：无 Mac 环境时用 overlay 把 `tray.go`/`tray_loop_external.go` 替换为桩（systray 需 CGO），见 `scratch/darwin-overlay/overlay.json`；正式 darwin 出包仍需 Mac。

---

## 二、能力契约矩阵（当前状态）

图例：✅ 完整 | 🟡 可用但有界（注明边界）| ❌ 缺失/不可用 | ➖ 平台概念不适用

### 2.1 Agent 桌面自动化（internal/tool/builtin）

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| screen_click/type/scroll/key | ✅ SendInput | ✅ cliclick（Retina 坐标自动换算；fwd-delete/esc/arrow 等已修正）| 🟡 xdotool（X11；Wayland ❌）| `input_other.go: macScreenMetrics/macToLogical/macKeyName` |
| screenshot（全屏+区域）| ✅ 含 region | ✅ `screencapture -R`（物理→逻辑换算）| ✅ scrot -a / grim -g；无工具时回退全屏 | `screen_windows.go` vs `screen_other.go`、`capture_{darwin,linux}.go: captureRegion` |
| 抓屏工具链回退 | ✅ 系统API | ✅ screencapture（系统自带）| ✅ scrot→gnome-screenshot→spectacle→maim→grim | `capture_linux.go` |
| screen_perceive | ✅ UIA+SoM 标注 | 🟡 VLM 自由文本（无元素ID）| 🟡 同 mac | `screen_windows.go` vs `screen_perceive_other.go`；UIA 失败回退 `perceiveNoUIA`（windows 也走 VLM-only）|
| get_ui_tree | ✅ 窗口+控件级（UIA；注册工具 `getUITreeEnhanced` 位于 `uiauto_windows.go`，`uitree_windows.go` 仅存窗口级辅助）| 🟡 窗口级（System Events）| 🟡 窗口级（wmctrl -lG）| `uitree_windows.go` vs `uitree_other.go(getUITreeLite)` |
| window_focus/maximize/restore/move/close | ✅ user32 | ✅ osascript | 🟡 wmctrl（X11）| `window_windows.go` vs `window_other.go` |
| desktop-auto 技能提示词 | ✅ UIA 版 | ✅ unix 版如实描述 | ✅ unix 版 | `internal/skill/builtins.go: builtinDesktopAutoBody`（GOOS 选择器）|
| 全局急停（E-stop）| ✅ RegisterHotKey，全组合键 | 🟡 修饰键弦+500ms 防误触（默认 Ctrl+Alt+Cmd+G）| 🟡 xinput 全组合键（X11；缺 xinput 则持续告警、触发器保持无效，循环不退出）| `estop_hotkey_{windows,unix,other}.go`、`estop_shared.go: estopHotkeyString` |
| 截图热键 | ✅ GetAsyncKeyState 全组合 | 🟡 osascript 修饰弦，沿触发+1s 冷却；需自动化权限（缺失一次性告警）| 🟡 xinput 修饰弦，沿触发；Wayland ❌ | `screenshot_hotkey_{windows,darwin,linux,other}.go` |
| 应用内急停快捷键 | ✅ Ctrl+Shift+\ | ✅ +Cmd | ✅ +Cmd | `desktop/frontend/src/App.tsx`（ctrlKey\|\|metaKey）|

### 2.2 终端与 Shell

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| 集成终端 PTY | ✅ ConPTY | ✅ creack/pty | ✅ creack/pty | `desktop/pty_{windows,unix,stub}.go`；docker/ssh 远程 tab 双端支持，`cd <root>` 已 shquote |
| 创建失败回退 | 前端回退 pipe 模式 + 本地化 toast（创建失败**与 PTY 中途退出**均触发）| 同 | 同 | `TerminalSession.tsx: onPtyUnavailable`、`TerminalPanel.tsx: fallbackToPipe`、locale `terminal.ptyFallback` |
| Shell 解析（agent 工具）| bash探测→Git Bash→pwsh/powershell | bash→sh→/bin/sh→errNoPOSIXShell 拒绝脚本 | 同 mac | `internal/sandbox/shell.go`（探测用 `bash -c true`）|
| Shell 解析（集成终端）| cmd.exe | `$SHELL`→/bin/bash→/bin/zsh→/bin/sh | 同 mac | `desktop/pty_unix.go: defaultShell` |
| Shell 进程树清理 | taskkill /T | Setpgid+kill(-pgid) | 同 | `bash_kill_{windows,other}.go`、`control/shell_kill_*` |
| 编码 | PowerShell UTF-8 序/chcp 65001 | locale UTF-8 | locale UTF-8 | `shell.go:20`、`hook.go:429-430` |

### 2.3 进程/沙箱/运行时

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| 进程树跟踪/清理 | Job Object（StartTracked）| Setpgid | Setpgid | `internal/proc/kill_*.go`；browserlaunch/lsp/plugin 均接入 |
| 隐藏窗口 | CREATE_NO_WINDOW | 无窗口概念 | 无 | `proc/hide_*.go`（HideWindow 桩对）|
| 沙箱 | ❌ 无（Job 只是跟踪）；boot 一次性 stderr 告警 | ✅ Seatbelt enforce | ✅ bubblewrap：功能探测（userns 禁用→Available=false）+ 临时/工具链缓存写授权（pip/npm/go install 可用）| `sandbox/seatbelt_darwin.go`、`seatbelt_other.go: bwrapWriteDirs/Available` |
| Python 解析 | py/python/uv | python3/uv（可运行性+≥3.10 探测；CLT 桩给出 xcode-select 指引）| 同 mac | `internal/runtime/runtime.go: resolvePython*` |
| uv | PATH→缓存→内置→`%LOCALAPPDATA%\Programs\uv`（`runtime.go: windowsUVDirs`）；下载 `.sha256` 校验；装后当进程生效（`uv_install.go: resetRuntimeResolution`）| 同（darwin 资产名）| 同（linux 资产名）| `internal/runtime/uv_install.go`、`internal/runtime/runtime.go` |
| HE/browser-use 边车 | ✅ uv 前缀参数已带上（发布版 bundled uv 可启动）| ✅ | ✅ | `desktop/{he,browseruse}_service.go: pyPrefix`、`desktop/sidecar_exec.go` |
| ppt-auto 依赖预装 | bat: py -3 优先+实跑验证 | sh: uv venv→venv→`--break-system-packages` 梯度+import 验证 | 同 mac | `internal/assets/pptauto/setup_python.{bat,sh}`、`SkillVersion` 历史含 51（setup_python 修复），当前值随变更递增见 `internal/assets/release.go` |
| 定时任务 OS 通知 | go-toast 长时 toast | Wails SendNotification | 同 | `desktop/notify_long.go` |
| SIGTERM/SIGINT 优雅关停 | （正常走 UI 退出）| ✅ system_quit swizzle | ✅ main.go signal → shutdownOnce | `desktop/main.go`、`app.go: shutdown` |

### 2.4 串口/运维主机面（internal/netdev 宿主侧）

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| 串口控制台 | ✅ DCB/CommTimeouts | ✅ termios（8N1、VMIN=0/VTIME=2）| ✅ 同 | `console_{windows,unix,other}.go`、`console_termios_*`；端口枚举：注册表 SERIALCOMM vs `/dev/ttyUSB*|ttyACM*|*.usb*` |
| Docker Engine 连接 | npipe | 探测 `~/.docker/run/docker.sock`→`/var/run/docker.sock`→colima | `/var/run/docker.sock` | `dockerapi.go: defaultDockerSocket` |
| ssh-agent | ✅ 命名管道（go-winio；需 OpenSSH Authentication Agent 服务）| ✅ `$SSH_AUTH_SOCK` unix socket | ✅ 同 | `transport/agent_{windows,unix}.go` |
| 多层发现主机视角 | 🟡 `ipconfig`/`route print -4`/`arp -a`（arp 可解析；if/route 表为空，专用解析器待做）| ✅ `ip -4 addr`/`ip route`/`ip neigh` | ✅ 同 | `layerdiscover.go: layerCommands`（drvKey=`linux-shell`/`windows-powershell`）|
| syslog/trap 接收 | ✅ | ✅（端口<1024 配置即拒绝）| ✅ 同 | `syslogrecv.go`、`traprecv.go`、`config/netdev.go` 两处端口校验 |
| nmap/netprobe | LookPath+`*_path` 覆盖 | 同 | 同 | `nmaporch.go`、`netprobeorch.go` |
| 远程主机助手发布 | 发布物含 hosts 包 | ✅（release.yml build-hosts 步骤；命名避开清单键）| ✅ | `.github/workflows/release.yml`、`desktop/remote_host_binary.go` |
| SSH 助手完整性校验 | n/a（WSL 字节比对）| SHA256 字节比对（原仅比大小）| 同 | `desktop/remote_ssh.go: fileSHA256` |
| RDP 带外打开 | mstsc | `open rdp://<addr>` 交给 Windows App | 提示本地客户端 | `desktop/netdev_app.go: NetDevOOBLaunch` windows 分支 |
| deeplink 开发护栏 | `FAIRPEER_DEV=1` 跳过 HKCU 协议注册（防开发版劫持已安装应用的 scheme）| ➖ | ➖ | `deeplink_windows.go` |
| 远程助手包签名 | hosts 包**不入更新清单、无 .minisig**——完整性由 provisioning 时的 sha256 比对保证 | 同 | 同 | `release.yml`（hosts 目录独立于 build/bin）|
| WSL 传输 | ✅ | ➖ | ➖ | `remote_wsl_{windows,other}.go`（诚实报错）|

### 2.5 更新与发布

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| 自更新 | ✅ 临时目录安装器→/S /D=原地安装→重启（PID 等待批处理）| ➖ 下载页兜底（Gatekeeper）| ✅ tar 包内 `fairpeer-desktop` 二进制换入+重启；`/usr`、`/opt` 系统安装→明确指引包管理器 | `updater.go: applyWindows/applyLinux`、`updater_app.go` |
| 更新清单 | manifest 键 `<goos>-<goarch>`；sigURL 与工件同 tag（`-sigs` 独立发布不存在）；version 由 tag 去 `desktop-` 前缀（裸 `v*` tag 时 version==tag，同样合法）；`matchPlatform` 另跳过 `.deb` 与 `-portable.`（人工下载变体，不得遮蔽更新键）| 同 | 同 | `desktop/cmd/sign/main.go: genManifest/matchPlatform`、`release.yml: publish` |
| 发布产物命名 | `fairpeer-desktop(.exe)`（wails outputfilename）为唯一真名；`release.yml` 按此搬运 | `.app` 同名 | tar 成员名必须 `fairpeer-desktop`（updater 解压约定）| `desktop/wails.json`、`release.yml: Package Artifact` |
| 签名验证 | minisign 公钥内嵌 `desktop/internal/update/verify.go`（README 示例已同步同一把钥）| 同 | 同 | `verify.go: publicKey` |

### 2.6 桌面壳体验

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| 窗口控制按钮 | ✅ 应用内 | ➖ 原生红绿灯 | ✅ 应用内（frameless 无原生装饰）| `AppChrome.tsx: platform !== "darwin"` |
| 托盘 | ✅ | ❌（NSStatusItem 需主线程，见 GAP-2）| 🟡 需 StatusNotifierWatcher（GNOME 无）| `tray_supported_*.go` |
| 关闭到托盘 | ✅ | 无托盘→回退真实退出+日志 | 同回退 | `app.go: beforeClose`、`backgroundWithoutTrayWarned` |
| deeplink `fairpeer://` | ✅ HKCU+argv | ✅ Info.plist CFBundleURLTypes + `Mac.OnUrlOpen`（kAEGetURL）| ✅ .desktop MimeType + `%u` argv | `deeplink_*.go`、`main.go: OnUrlOpen`、`build/darwin/Info.plist`、`build/linux/fairpeer.desktop` |
| 开机自启动 | ✅ HKCU Run | ✅ launchd agent（.app 经 `open -a`）| ✅ XDG autostart | `desktop/autostart*.go`、设置页 `settings.autostart*` |
| 全局急停/截图热键 | ✅ | 🟡/🟡（见 2.1）| 🟡/🟡 | 同 2.1 |
| 剪贴板读文本 | Get-Clipboard（ResolvePowerShell）| pbpaste | wl-paste→xclip→xsel | `desktop/clipboard_text.go` |
| 剪贴板图片 | PowerShell | osascript | wl-paste+xclip | `internal/control/attachments.go` |
| 文件管理器打开/揭示 | explorer /select | open / open -R | xdg-open→gio→kde-open5→kde-open（reveal 与 open 共链）| `open_workspace_*.go`、`compat_stubs.go`、`app.go: revealPath` |
| 崩溃取证 | 前端 ReportCrash | + main.go recover→app.log | 同 | `desktop/panic_log.go` |
| 系统信息（devinfo）| RtlGetVersion+注册表 | sw_vers+sysctl | os-release+/proc（arm64 回退 device-tree/lscpu）| `devinfo_{windows,darwin,linux}.go` |
| 系统代理 | IE/PAC/WPAD | scutil --proxy | gsettings（缺省回退 env）| `internal/sysproxy/system_*.go` |
| 密钥存储 | DPAPI | Keychain | Secret Service→machine→passphrase 链 | `internal/secret/*` |

### 2.7 办公能力

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| 现代格式（docx/xlsx/pptx/pdf/md/csv）| ✅ 纯 Go | ✅ | ✅ | `internal/rag/officedoc.go`、`internal/tool/builtin/docx*` |
| 旧格式 .doc/.xls/.ppt 预览 | ✅ PowerShell COM | ❌ 明确报错（GAP-3）| ❌ 同 | `officedoc.go: runOfficeCOM` |
| RAG 导入旧格式/.msg | 🟡 需 markitdown（脚本已自装，含 `--break-system-packages` 回退）| 🟡 同 | 🟡 同 | `store.go readDoc`、`doc_converter.py`（_install 自装）|
| 扫描版 OCR | ✅（`_ocr_page` 4/3 参 bug 已修；paddleocr<3 锁定；pip 自装含 PEP 668 回退）| ✅ | ✅ | `ocr_pdf.py`（根+内嵌两份）、`store.go decodeTextBytes` |
| OCR 边界 | `readPDFWithOCR` 固定 10 分钟超时（暂无配置覆盖），超大扫描件会整体失败 | 同 | 同 | `internal/rag/officedoc.go: readPDFWithOCR` |
| 日历 ICS 时区 | ✅ 内嵌 time/tzdata | ✅ | ✅ | `desktop/main.go`、`cmd/fairpeer/main.go`、`calendar/ics.go: SkippedNoStart` |
| PPT→PDF 导出 | ✅ COM | 🟡 soffice（mac bundle 路径待补，GAP-4）| ✅ soffice | `pptauto/scripts/export_pdf.py: find_libreoffice` |
| 模板扩展名匹配 | ✅ | ✅ EqualFold（大小写不敏感）| ✅ | `ppttemplate/template.go` |
| CSV 中文 Excel 兼容 | ✅ UTF-8 BOM | ✅ | ✅ | `document.go`（csv 分支 BOM + 读取剥离）|
| 生成文档字体 | Calibri/SimSun | ⚠️ 无本地字体时靠替代（GAP-6 文档化）| ⚠️ 同 | `docxwrite.go:1206` |
| 技能资产/示例 | ✅ SKILL.md 证据示例用 `<skill_dir>` 占位（原开发者 C:\ 路径已清除）；COM 脚本（analyze_template/export_previews）在非 Windows 明确报平台限制；生成项目不入 embed 树（gitignore `scripts/projects/`）| ✅ | ✅ | `internal/assets/pptauto/**`、`SkillVersion` ≥53 |

### 2.8 前端 UX（WebView2 / WKWebView / WebKitGTK）

| 能力 | Windows | macOS | Linux | 锚点 |
|---|---|---|---|---|
| 快捷键 | Ctrl | Ctrl/Cmd 双写（急停/终端开关/搜索等）| Ctrl | `App.tsx`、`Transcript.tsx`、`lib/keyboardShortcuts.ts`（显示 ⌘/Super 按平台）|
| 拖拽导入 | 原生 OnFileDrop | DOM 回退（webkitGetAsEntry→SavePastedFile）| 原生（GTK drag-data-received）+ DOM 回退 | `Composer.tsx`、`RagPanel.tsx: onPanelDrop`（RAG 面板 mac 已补）|
| 远程连接向导 | ✅ WSL 默认 | ✅ WSL 卡片隐藏、默认 SSH | ✅ 同 mac | `RemoteConnectWizard.tsx`（platform 门控 + ListWSLDistros 跳过）|
| 外部浏览器打开 | ✅ openExternal（BrowserOpenURL）| ✅ | ✅ | `PreviewPane.tsx`、`lib/bridge.ts: openExternal` |
| Loop 预设文案 | 中文 | ✅ 英文（labelEn/descEn）| ✅ 英文 | `LoopPanel.tsx`、`lib/loopPresets.ts` |
| 终端缩放 | ✅ addon-fit+ResizeObserver | ✅ | ✅ | `TerminalSession.tsx`（xterm DOM 渲染器，三平台一致）|
| CJK 字体 | Microsoft YaHei | PingFang SC | Noto Sans CJK SC/Source Han/WenQuanYi 已入栈 | `tokens.css --sans` 四栈、`workbench.css` |
| WebGL 图谱 | ✅ | ✅ | 弱 GPU 局部守护（PaneErrorBoundary+提示）| `RagPanel.tsx`、`cowork.ragGraphError` |
| Mermaid 主题 | 跟随亮/暗 | 同 | 同 | `mermaidLogic.ts: mermaidThemeFor` |
| 粘贴剪贴板文本 | web API+后端双路 | 后端 pbpaste（WKWebView 无 readText）| 后端 wl-paste | `SettingsPanel.tsx: pasteHooksJSON`、`bridge.ts ReadClipboardText` |
| 串口/套接字提示文案 | COM3 | /dev/cu.usbserial-* | 同 + docker sock 路径 | locales `ndv.sets.phConsolePort/phDockerSock` |

### 2.9 已知的"平台正确性"细节契约（防回归）

1. 附件正则分隔符无关（`\.fairpeer[\\/]+attachments`），屏幕工具发射路径 `filepath.ToSlash` — `internal/agent/agent.go: attachmentImageRe`、`screen_windows.go`。
2. `atomicWrite` 重命名前保留目标权限（unix 可执行位）— `internal/tool/builtin/atomic.go`。
3. 工作区围栏 windows 大小写折叠 — `confine.go: within/withinRel`。
4. symlink 失败回退 junction（目录）；错误文案含 Developer Mode/mode=copy 指引 — `installsource/apply.go: linkOrJunction`。
5. `expandHome` 认 `~\`；`USER`→`USERNAME` 回退 — `transport/sshconfig.go`、`transport/host.go`。
6. 变更提案 file/cert 步骤：draft 校验 + **approve 门复核**拒绝非 linux 目标 — `proposal.go: validateStep/approve`。
7. `quser`/`who` 会话计数滤表头 — `proposal.go: countUserLines`。
8. 陷阱/日志接收端口 <1024 拒绝 — `config/netdev.go`（两处）。
9. 远程终端 `cd <root>` 一律 `shquotePath`（docker/ssh 的 shell 行；WSL `wsl.exe --cd <root>` 为 argv 形式，**有意不加引号**，勿"统一"）— `pty_{unix,windows}.go`、`sidecar_exec.go`。
10. ICS 导入零起始时间事件计数并透出 — `calendar/ics.go: ParseResult.SkippedNoStart`。

---

## 三、遗留差距台账（按优先级）

### GAP-1（P1·功能级）screen_perceive 元素级接地（mac/Linux）
Windows=UIA+SoM；unix 仅 VLM 自由文本。设计：mac 经 System Events/AXUIElement 导出 role/name/position/size；linux 经 AT-SPI2 D-Bus（godbus，无 CGO）；把 `som_windows.go` 的标绘逻辑抽到共享 `som.go`。工作量 L。

### GAP-2（P1·体验级）macOS 托盘 + Dock 点击恢复
NSStatusItem 必须建于 Wails 所持主线程（直接 systray 会崩，`tray_supported_darwin.go` 注释 #3223）。设计：仿 `system_quit_darwin.m` swizzle AppDelegate，在 `applicationDidFinishLaunching` 建 NSStatusItem；同补 `applicationShouldHandleReopen:`（隐藏窗口的 Dock 恢复）。工作量 L。前置依赖：先做本条再做"mac 关闭到托盘"。

### GAP-3（P2）旧 Office 格式 unix 解析
`soffice --headless --convert-to docx|xlsx|pptx` 预步骤 → 复用现有 Go 解析器；`FindSoffice()` 探测（含 `/Applications/LibreOffice.app/...`）；注意 soffice 单实例（`-env:UserInstallation`）。工作量 M。`.msg` 仍需 markitdown（不可用→明确报错）。

### GAP-4（P2）PPT 导出 mac soffice 探测 + SKILL.md 纠偏
`find_libreoffice()` 补 mac bundle 路径；SKILL.md 移除"必须 PowerPoint/WPS"表述；补 headless CJK 字体说明（fonts-noto-cjk / brew font-noto-sans-cjk）。工作量 S。

### GAP-5（P2）Wayland 支持
输入（ydotool/wtype）、窗口（wlrctl/wlr-foreign-toplevel）、全局快捷键（XDG portal）、**GNOME-Wayland 截图（org.freedesktop.portal.Screenshot——现五工具链在 GNOME Wayland 全部失效）**。当前 X11-only+明确报错。工作量 L。

### GAP-6（P3）生成文档 CJK 字体替代
docxwrite docDefaults 加 `w:altName`（SimSun←Songti SC/Noto Serif CJK SC 等）。工作量 S。

### GAP-7（P2·发布）mac/Linux 桌面发布流水线
`release-desktop.yml`（被 `scripts/desktop-build.sh`、`desktop/README.md` 引用但不存在）：开发者签名/公证/dmg/nfpm deb + `cmd/sign` 签名清单 + canary `-X main.channel=` 注入。ci.yml 的"continuous release"（v0.1.6、未签名）应删除或修复。README 的 install.sh/brew 渠道需站点侧核实。
已落地（精读轮）：`cmd/sign matchPlatform` 跳过 `.zip`（applyWindows 以 NSIS 安装器执行资产，zip 会弄砖更新）——测试夹具同步为真实产物形态；ci.yml windows 腿本就带 `-nsis`，zip 仅为人工下载包。

### GAP-8（P3·仓库卫生）git 内 ~146MB 的 .exe/ELF 二进制
`git rm --cached`（desktop/test.exe 等 9 个）+ .gitignore 兜底。

### GAP-9（P3）窗口跨屏位置校验
`app.go: domReady`（启发式语句在函数内 ~1174 行）的 `X>=0 && Y>=0` 判断会把左副屏窗口重置居中；`runtime.ScreenGetAll` 已在同一函数中使用（取最大边界），按各屏矩形做包含性判断已近在咫尺。工作量 S。

### GAP-10（P3）睡眠/唤醒钩子
三平台均无；#3834（睡眠后 webview 卡死）已有注释佐证。darwin NSWorkspace swizzle / linux login1 PrepareForSleep（godbus 已有）/ windows WM_POWERBROADCAST。工作量 M。

### GAP-15（HIGH·深层场景）多显示器与 DPI 感知（三平台）
- Windows：全屏捕获/UIA 仅主屏 vs 前台窗口元素跨屏（标签错位、点错）；DPI 仅 SYSTEM_AWARE（`screen_windows.go: SetProcessDpiAwareness(1)`），混合 DPI 副屏坐标漂移。修：`SetProcessDpiAwarenessContext(-4)` + `GetDpiForMonitor` 按屏换算 + 前台屏捕获。
- macOS：`screencapture` 仅主屏；region clamp 拒绝负坐标副屏（`capture_darwin.go`）；Retina 探测假设单一缩放。修：`-D <n>` 按屏捕获 + 联合边界 clamp + 按屏缩放表。
- Linux：XWayland 分数缩放下无坐标补偿（mac 路径的 linux 孪生缺失）。修：GDK_SCALE/portal 探测后换算。
工作量 L-M（每平台独立）。

### GAP-16（HIGH·安全）远程主机传输默认明文 + TLS pin 静默失败
`desktop/remote_server.go`：非 TLS 分支把 token（及 host 回传的 API keys）过明文 TCP；`--tls` 仅是复选框不强制。pin 持久化失败已改为 fail-closed（本轮修复），但默认明文仍需产品决策：默认 TLS / 非 loopback 拒绝明文 token。工作量 S-M。

### GAP-17（P2）会话中途失效三连
1. macOS 屏幕录制权限被撤销后 `screencapture` 退出 0 但只出壁纸图——agent 拿到"看似合理的空屏"照常操作（`capture_darwin.go`）。修：壁纸基线哈希启发式 + 明确报权限。
2. config.toml 损坏时 `LoadForEdit` 静默回退默认值，下一次设置保存**用手写默认覆盖用户手工配置**（`config.go: LoadForEdit` + `edit.go: SaveTo`）。修：记录解析失败，SaveTo 拒绝或先备份。
3. rg 中途被删后 grep 永久报错，无原生扫描回退（`grep.go: runRipgrep` 启动时绑定）。修：ENOENT 时重新 LookPath 并回退原生扫描。

### GAP-18（P2）引导期能力可见性
`fairpeer doctor` 仅 CLI；桌面启动不探测，mac TCC 权限（Automation/Accessibility/Screen Recording）无任何探测代码，热键/感知失效只进 app.log；设置页无"环境诊断"。doctor 的 darwin 段为空（`desktopenv_other.go`）。修：`DoctorReport()` Wails 绑定 + 设置页环境页 + 启动警告一次性横幅；darwin 补 TCC 段、unix 补 helper LookPath 段（doctor 文案已可复用）。工作量 M。

### GAP-19（已修复·记录）unix 急停轮询自愈
本轮已修：probe 2s 超时（睡眠唤醒卡死自愈）、失败连续 40 次（~10s）退出轮询且告警一次性（原 4 行/秒刷屏）、设备过滤（mouse/touchpad/pointer/consumer）+ 上限 8（原 15 设备 60 forks/秒）；截图热键同样模式（`screenshot_hotkey_linux.go` 8 次失败退出）。残余建议：darwin 8 forks/s 常态轮询可改"仅运行活跃时 250ms、空闲 1s"。

### GAP-20（P2）长控制台会话 syncBuffer 无上限
`internal/netdev/session.go: syncBuffer` 在两条命令之间无限累积设备异步输出（终端 monitor 遗留/线路噪声），长会话 RSS 无界且 snapshot 15ms 全量拷贝 O(n²)。修：Write 内保留末尾 N MB（头部丢弃；emitLive 的前缀重同步分支已容忍）。工作量 S。

### GAP-21（P2·Linux）dbus 缺失时单实例锁失效
wails linux 单实例依赖 dbus session bus；SSH/systemd 拉起（无 DBUS_SESSION_BUS_ADDRESS）时锁静默不存在 → 双实例双写 config/UDP 端口冲突；且锁被占但 SendMessage 失败时第二个实例不退出（upstream v2.13 行为）。修：main.go 无 dbus 时回退 config 目录 flock。工作量 S。

### GAP-11（P3·需真机验证项）
- mac `pressKeyCombo` 的 cliclick 修饰键组合（`kp:` 不含修饰键，应 `kd:cmd t:s ku:cmd` 式）——已验证为疑似坏，改动需 mac 回归。
- estop 键码映射（xinput min-keycode 两种解释都匹配）与 Retina 探测在多显示器 mac 的退化路径。
- WKWebView 对 Finder 拖拽是否返回 entry（决定 DOM 回退是否必然触发）。
- darwin 真机出包（CGO systray overlay 桩仅编译验证）。

### GAP-12（P1·CJK/长路径场景）跨 OS 数据可移植性——工作区 slug 身份
`internal/config/config.go: WorkspaceSlug` 用**整条绝对路径**替换分隔符生成会话/记忆目录名（`memory/store.go: slugify` 同）：(a) 跨 OS 迁移（`C:\Users\bob\proj`→`/Users/bob/proj`）后旧 slug 目录变孤儿——会话/记忆 UI 不可见（可恢复但无入口）；(b) 无长度钳制，CJK 路径（3 字节/字）超过 ~80 字符时 `projects/<slug>` mkdir 超过 NAME_MAX(255B) → **会话静默不保存**；(c) windows/darwin 上大小写不同的同一路径生成重复项目（`desktop/tabs.go: normalizeProjectRoot` 不折叠大小写）。设计：slug=目录名+归一化路径短哈希（≤80 字节）+ 一次性旧 slug 迁移；`normalizeProjectRoot` 在 windows/darwin 折叠大小写。工作量 M。

### GAP-13（P2）RAG/表格的绝对路径身份
`internal/rag/extract.go`（JobRow.Path、FTS path 列）存绝对路径：OS/目录迁移后搜索结果带死路径、同目录重导入因 stat 键去重失效产生重复块与重复 LLM 花费。设计：以 `root_path+rel_path` 为身份键，打开时解析；或启动时按迁移映射重写。工作量 M。

### GAP-14（P2）tab 恢复的"幽灵工作区"
`desktop/tabs.go` 持久化绝对 WorkspaceRoot；OS 迁移/目录改名后 tab 仍以不存在目录 "Ready" 启动（`config.LoadForRoot` 不 stat 根），读写全部撞围栏错误。设计：恢复时 `!os.Stat(root)` → 回落 profile home 并提示（模式已存在于 `app.go` 外来根处理）。工作量 S-M。相关 P3：scheduler `output_dest` 绝对路径（迁移后 deliver_failed，可见非静默）、telemetry sidecar 只读路径残留（仅展示）。

---

## 四、验证协议

1. **编译矩阵**（每次跨平台改动后必跑）：
   - 根模块：`go build ./...` × {windows, linux, darwin}（当前窗口机：`GOOS=linux/darwin go build ./...`）
   - 桌面模块：`go build .` ×3（darwin 用 `-overlay ../scratch/darwin-overlay/overlay.json -o /dev/null .`）
   - `go vet .`（windows+linux）
2. **测试**：`go test ./...`（根，ubuntu CI 亦跑 → 不得有平台假设）；`cd desktop && go test .`；前端 `npm run typecheck && npx vite build && npx vitest run`（locale-parity 与 guides-drift 必绿）。
3. **发布自检**：`go run ./cmd/sign manifest <dir> <version> <tag>` 输出的键恰为 6 平台；`latest.json` 的 URL/Sig 指向同 tag 真实资产。
4. **真机清单**：GAP-11 列表 + 每次 unix 新能力的首次人工回归。

---

## 五、v1.0 抽象层方案的处置

v1.0 提议的 `internal/platform` 接口注册表方案**未实施也不再采用**：代码库以 40+ 组按文件构建标签实现了同等分离，且与 Wails/netdev driver 表等既有结构耦合自然；引入抽象层的迁移成本大于收益。若未来工具面继续膨胀，可按域评估（如 screen/input 域）局部抽象，但不做全局注册表。

---

## 六、验证基线与结论（2026-09-10）

**验证对象**：本文 v2.0 的全部断言。**基线**：当前工作树（两轮跨平台修复后、尚未提交——HEAD 仍为修复前状态，故与 HEAD 对比会看到大量差异，这是预期的）。

| 验证方 | 范围 | 结果 |
|---|---|---|
| 本人锚点核对（脚本化 grep + 人工读码） | 36 项核心断言 | 36 ✓（其中 2 项初判失败经人工读码确认为检查模式过严：`/S /D=` 为拼接构造、bwrap 探测为 argv 形式）|
| 验证代理 V1（§一/2.1/2.2/2.3 + GAP-1/5/10/11）| 41 条 | 38 ✓ / 3 ⚠ / 0 ✗ |
| 验证代理 V2（§2.4/2.5/2.6 + GAP-2/7/8/9）| 35 条 | 35 ✓ / 0 ⚠ / 0 ✗ |
| 验证代理 V3（§2.7/2.8/2.9 + GAP-3/4/6）| 44 条 | 43 ✓ / 1 ⚠ / 0 ✗ |
| **合计** | **~120 条** | **0 ✗（无与源码相悖的断言）**；⚠ 共 4 处，已全部修订入本版 |

**⚠ 修订记录**（验证发现 → 本文处置）：
1. §一#5 "CGO_ENABLED=0 全矩阵"从未被脚本固定 → 已在 Makefile `cross` 显式钉 `CGO_ENABLED=0` 并改写措辞（本轮顺手修复的真问题）。
2. §2.1 get_ui_tree windows 锚点：控件级实现 `getUITreeEnhanced` 在 `uiauto_windows.go`（非 `uitree_windows.go`）→ 已改锚点。
3. §2.2 Shell 解析行混淆了两条链 → 已拆分为"agent 工具 shell"（sandbox/shell.go）与"集成终端 shell"（pty_unix.go）两行。
4. GAP-9 行号 1166→1174（同函数内漂移），并按代码现状收紧前提（ScreenGetAll 已在用）。

**验证中发现并已顺手修复的源码问题**：`desktop/updater.go: manifestEndpoints` 的"Gitee 优先"过时注释（实际无 Gitee 端点）已改写为真实抓取顺序；`ocr_pdf.py`/`doc_converter.py`（各两份）的 pip 自装补 `--break-system-packages` PEP 668 回退。

**第三轮查遗漏（2026-09-11，3 路审计：从未覆盖的前端组件 / 跨 OS 数据可移植性 / 技能资产与边车痕迹）新增 23 项发现**：前端 9 项（WSL 向导门控、PreviewPane openExternal、keyPathPlaceholder、onboarding.privacy、voiceDeniedSystem、remote.description、Loop 预设英文、HistoryPanel 反斜杠、--mono-font 孤儿变量）已全部修复；资产 6 项（嵌入的演示项目移出 embed 树+gitignore、SKILL.md 开发者路径占位符化、COM 脚本平台守卫、cua-demo unix 变体、extract_test.py 参数化、README 配置路径三平台标注）已全部修复并升 SkillVersion=53；数据可移植性 2 项（RAG 绝对路径身份、tab 幽灵工作区）作为 GAP-13/14 入台账，slug 结构性问题（P1 for CJK）作为 GAP-12 入台账待实施。

**已知不可静态验证项**（列入 §三 GAP-11 真机清单）：estop 键码映射、Retina 多屏退化、WKWebView 拖拽 entry 行为、cliclick 修饰键组合、darwin CGO 出包。

**维护约定**：任何跨平台行为变更须同步更新 §二 对应行与 §三 差距台账；新增性能/行为契约先写进 §二 再实现（spec 先行）。

---

## 七、第 3 次五遍排查记录（2026-09-11，5 遍 × 3 子任务 = 15 角度）

| 遍 | 角度 | 新发现 | 已修 |
|---|---|---|---|
| 第 1 遍 | serve/SSE 协议层 · MCP/ACP/插件进程 · LSP/CodeGraph | 12 | 12（SSE Cancelled/SIGHUP/X-Accel/no-cache/banner、MCP PATH 增强、codegraph chmod+unsupported 报错、LSP 提示、docker cp sha256、pair code 拒绝采样、ESXi rm 危险级）|
| 第 2 遍 | 权限系统 · 信任域/护栏 · 密钥传输 | 19 | **权限 CR 绕过（High，安全）**、权限路径大小写绕过（High）、PowerShell 危险命令表、netdev PS 写表死条目、ESXi rm 危险级、TLS pin fail-closed；其余入 GAP-16 |
| 第 3 遍 | i18n 完整性 · 文档准确性 · 引导能力可见性 | ~30 | 文档 10 处过时声明已修（含 README 0x0C 控制字符——此前修复引入，已纠正）；i18n 硬编码/插值/Go 错误直出与 GAP-18 引导可见性入台账 |
| 第 4 遍 | CI/竞态/泄漏 · 新代码测试覆盖 · 构建可复现性 | 8 | **CI quality job webkit 缺失（High）已修**（apt + webkit2_41 tag）、内嵌 ocr_pdf.py 陈旧（High）已同步、pty closed 锁竞态已修、5 个 scratch main 加 ignore、gofmt/gitignore/死规则清理；测试覆盖缺口清单（13 项建议用例）已记录待补 |
| 第 5 遍 | 多屏/DPI/IME/轮询成本 · 中途失效 · 多实例/配对/语音 | 22 | estop unix 轮询三连（fork 风暴/告警刷屏/无超时）已修、xterm IME 守卫已修；多屏 DPI（GAP-15）、中途失效三连（GAP-17）、dbus 单实例（GAP-21）、syncBuffer（GAP-20）入台账 |

**结论**：五遍共 15 角度再发现 ~90 项，其中安全类 3 项 High（CR 绕过、路径大小写绕过、CI 空转）与构建类 2 项 High（CI webkit、内嵌脚本陈旧）已全部修复；其余按 GAP-15~21 入台账。§二 契约矩阵与 §三 台账已同步至当前代码状态。


---

## 八、逐字逐句精读轮记录（2026-09-12，5 遍 × 3 子任务）

与前几轮的模式扫描不同，本轮按"逐行通读指定文件"执行，聚焦两轮修复**新写入代码**的正确性。

**精读范围**：pty_{unix,windows,stub}.go · estop_{unix,shared,windows,other}.go + 截图热键互动 · autostart*.go · clipboard_text.go · panic_log.go · sidecar_exec.go · main.go（OnUrlOpen/信号/recover）· console_unix.go · permission（CR 修复 + 大小写折叠）· serve（wire/runServe/switchModel）· plugin/transport_stdio.go · codegraph/install.go · lsp/manager.go · remote_{docker,ssh}.go · pairing.go · driver/{esxi,hosts,driver}.go · ocr_pdf.py（根+内嵌，全文对照 officedoc.go 消费契约）· doc_converter.py · setup_python.{sh,bat} · release.yml · ci.yml · Makefile · wails.json · Info.plist · fairpeer.desktop · TerminalSession/TerminalPanel/DeviceTerminal.tsx · RemoteConnectWizard · SettingsPanel 变更区 · RagPanel · DashShell · NetDevLayout · loopPresets · misc.css。

**精读新发现并已修复**：
1. 【CRITICAL】共享解析器 parseHotkey 不认识 "PAUSE/BREAK" 与 "Cmd/command" —— unix/darwin 急停**默认组合键根本无法启动**（默默退出轮询）。已扩 keyToVK + 修饰符表（`screenshot_solve.go`）。
2. 【HIGH】unix PTYRead 在子进程退出后永远报告 alive（僵尸未收尸，terminal 挂死）—— 增加 reapIfExited（带 200ms 预算的 Wait）后再判定退出（`pty_unix.go`）。
3. 【HIGH】unix 急停轮询三连：helper 卡死无超时（挂死循环）、失败告警 4 行/秒刷屏、设备扇出无上限 —— 已修（probe 2s/3s 超时、40 次失败退避退出、键盘过滤+8 设备上限）。
4. 【MED】windows PTY：ssh `-p` 拼在 host 之后（被当远程命令）；argv 无引号拼装（空格路径全碎）；get() 读 closed 无锁；WriteFile 丢尾包 —— 全修。
5. 【MED】unix PTY：write 持锁可被卡死（PTYKill 永久阻塞）→ 锁内取句柄锁外写；resize 负值/超大值未钳制；TERM 去重；WorkspaceRoot 锁内拷贝。
6. 【MED】权限大小写折叠初版是死代码（isPathSubjectTool 引用不存在的 canonical 名）→ 改为路径形态检测（绝对/home/盘符），覆盖所有路径主题工具。
7. 【MED】BashDangerWarning 大小写敏感 → PS/cmd 大小写不敏感匹配（同时保护前缀授权拒绝逻辑）。
8. 【MED】serve switchModel 重建控制器绕过 notify sink → Server.SetNotifySink/effectiveSink（`cli.go` 接线）。
9. 【MED】ocr_pdf.py：Windows 每页临时 PNG 泄漏（句柄未 close 即 unlink）；paddleocr 3.x 已装时进程内降级不生效（sys.modules 陈旧）；paddlepaddle 未随 paddleocr 锁版本。
10. 【MED】DashShell Alt+P 永远无法**进入**投影模式（监听器仅投影中挂载）；Alt+Digit 未做 inField 门控；TerminalSession 零尺寸面板 fit 出 2×1 终端；缺 compositionend 宽限。
11. 【P3】estop windows 注册失败窗口泄漏（+设置开关竞态重试）、panic.log 与 app.log 截断冲突（已分离 panic 日志语义问题记录）、`wails.Run` 失败退出码 0（已 os.Exit(1)）、clipboard 的 wl-paste 按二进制存在而非会话类型选型（已改为 WAYLAND_DISPLAY 判定 + 回退链）、wails.json comments 乱码、serve index.html 补 cancelled 反馈、sidecar_exec 注释乱码。

**误报排除**：creack/pty "丢失"（代理 grep 了根模块 go.mod）——desktop/go.mod 依赖完好。

**回归**：根/桌面三平台构建 ✅、桌面 `go test .` ✅、spec 锚点 108/108 ✅、前端 tsc ✅。

**补跑与落地（同日）**：
- P4-2 逐行精读补跑（本人执行，因代理限速失败）：RemoteConnectWizard.tsx 全文——平台门控实现正确（WSL 卡非 Windows 禁用、默认回落 SSH、ListWSLDistros 跳过）；SettingsPanel AutostartField——乐观回写+后端确认+失败回弹，正确；estop 区域——三平台提示/默认键/输入启用，且与后端 parseHotkey（已接受 Cmd）一致；bridge.ts GetAutostart/SetAutostart mock 完整。无新 P1/P2；P3："coming soon" 文案用于 Windows 专属功能略误导。
- P3-1 七项 OCR 修复全部落地（根+内嵌同步）：Windows 每页临时 PNG 泄漏（句柄即关）、渲染异常清理窗口（doc/tmp 全路径 finally）、paddleocr≥3 进程内降级不生效（构造移入受保护路径，失败保留 pdfplumber 文本）、paddlepaddle 锁 <3.0、__version__ 缺失容错、早期致命错误镜像到 stderr（Go 侧可读）、stdout/stderr reconfigure 独立守卫。
- ci.yml zip/installer 契约：`cmd/sign matchPlatform` 跳过 `.zip`（含测试），从契约层杜绝 zip 弄砖更新；ci.yml 的 continuous-release 渠道整体仍列 GAP-7。
