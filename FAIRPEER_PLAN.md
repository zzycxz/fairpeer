# FairPeer 改造工作计划（v3 · 代码核验整合版）

> **从 MoMAPeer（中国移动九天专属）→ FairPeer（公网通用 AI 编程助手）**
> 本计划基于 v2 深度调研，并整合了**两轮逐行源码核验**的修正（现状描述失真、致命链、架构原则纠偏）。所有"现状"声明均带 `文件:行号` 证据，截至 2026-08-02。
> **附件归档：** `docs/assets/` —— 见文末《附件清单》。

---

## 🎯 项目目标

**FairPeer** 是面向公网用户的通用 AI 编程助手，基于 MoMAPeer 二次开发（**clean start：新建独立仓库、切断 git 历史、与 momapeer 各自独立维护**），目标：

1. **完全去中国移动专属元素** — 品牌、API 端点、邮箱、模型推荐、系统提示词全面脱敏
2. **全面对接公网主流 LLM** — 11 厂商直连 + 7 个 Coding Plan 聚合平台
3. **新增主流邮箱支持** — 从 139 邮箱扩展到 Gmail/Outlook/QQ/163 等
4. **PPT 模板智能化** — 移除中国移动配色硬编码，新增模板底色自动检测
5. **品牌焕新** — 新名称、新图标（✅ 已完成）、新 README banner

---

## ⚠️ v3 相对 v2 的关键修正（务必先读）

v2 plan 方向正确，但**对"现状"的描述有 8 处失真**，且漏掉了**会让"改完即报废"的三条致命链**和一个**违反项目架构原则的根本错误**。v3 已全部修正：

| # | v2 的说法（错） | 代码实际（`证据`） | v3 处理 |
|---|---|---|---|
| 1 | 「替换 40+ Go 文件 import」 | 实际 **384 个** .go 文件 import 该路径 | WP-0.2 工时重估为 1-1.5 天 |
| 2 | 要清理 `.agents/` 目录 | fairpeer 仓库内**不存在** `.agents/`（在上级目录） | 从 WP-0.1 移除 |
| 3 | `internal/provider/host.go` 有 `IsMoMA` | 实际在 `internal/provider/openai/host.go:35` | 修正路径 |
| 4 | `internal/provider/openai.go` 有 `BuiltinMoMAModels` | 实际在 `internal/config/config.go:1413` | 修正路径 |
| 5 | `ProviderEntry` 无 per-provider default | **已有** `Default`(`:1130`)+`DefaultModel()`+`Vision bool`(`:1158`)+`Thinking/Effort/ReasoningProtocol/ModelsURL/Price` | WP-3.1 改为"补 fast_model、能力注册表重构"，非"新增字段" |
| 6 | 批评「全局 `VisionModel`」 | 代码**根本无此字段**；截图走 `[cowork] screenshot_vlm_model`(`config.go:550`) | 删除该批评 |
| 7 | `templates/template_config.json` 硬编码颜色 | 实际是 `pptauto/template_config.json`（无 templates 子层），硬编码超 5 处 | 修正路径 |
| 8 | 「移除 139 硬编码域名判断」 | 代码无 `if host=="139.com"` 分支，139 走通用 TLS/charset 自动机制 | WP-5.3 改为"注释清理"，非逻辑改动 |

**三条致命链（v2 完全漏掉，v3 单列 Phase）：**
- 🔴 **更新链**：`desktop/updater.go` 7 处硬编码 `zzycxz/momapeer`，改名=老用户永久断更 → 新增 **WP-1.6 更新链迁移**
- 🔴 **VLM/能力链**：`boot.go:666` 九天无条件兜底 + `MoMAVisionModels`/`MoMAThinkingModels` 硬编码白名单（`openai.go:202,236`）→ 拆九天后图片理解全废、新厂商 vision 全失效 → 新增 **WP-2.6 VLM 链重做 + WP-3.9 能力注册表重构**
- 🔴 **数据迁移链**：`.momapeer/` 目录名 20+ 处硬编码 + 前端 8 个 localStorage key → 改名=老用户数据全丢 → 新增 **WP-1.7 用户数据迁移**

**架构原则纠偏：** momapeer.md:43 明确"新增行为加在 controller，不加前端，五入口继承"。v2 把 90% 改造加在桌面端 React 组件。v3 新增 **WP-3.0 重写 `config.Default()`**（五前端根）+ **WP-3.8 五前端 coding_plan 适配**。

---

## 📊 深度调研结论（保留 v2 有效部分）

### 1. 各厂商 LLM API 端点差异

**核心发现：Coding Plan 是中国厂商特色商业模式，通常是模型聚合平台（类似九天 MoMA），一个 Key 可调多家模型。**

| 厂商 | 普通 Base URL | Coding Plan 端点 | Key 是否通用 |
|------|--------------|-----------------|-------------|
| **通义千问** | `dashscope.aliyuncs.com/compatible-mode/v1` | `coding.dashscope.aliyuncs.com/v1` | ❌ 独立 Key (`sk-sp-`) |
| **DeepSeek** | `api.deepseek.com` | 无（纯按量） | ✅ |
| **火山引擎** | `ark.cn-beijing.volces.com/api/v3` | 有（Coding/Agent Plan 订阅） | 共用端点 |
| **智谱 AI** | `open.bigmodel.cn/api/paas/v4` | `z.ai` 独立平台 | ❌ 独立平台 |
| **MiniMax** | `api.minimaxi.com/v1` | 无独立端点（订阅 Key） | ✅ 同端点 |
| **Moonshot** | `api.moonshot.cn/v1` | 同端点+不同模型 | ✅ |
| **MiMo** | `api.xiaomimimo.com/v1` | 无 | ✅ |
| **Anthropic** | `api.anthropic.com` | 无（Messages API 统一） | ✅ |
| **OpenAI** | `api.openai.com/v1` | Codex 独立产品线 | ✅ |
| **阶跃星辰** | `api.stepfun.com` | 同端点（Step Plan 订阅） | ✅ |
| **讯飞 MaaS** | `spark-api-open.xf-yun.com/v1` | 同端点（Coding/Token Plan） | ✅ |

### 2. 每厂商三类模型配置（2026-08）

| # | 厂商 | Default | Vision | Fast |
|---|------|---------|--------|------|
| 1 | 通义千问 | `qwen3.7-max` (1M) | `qwen3.7-plus` (1M) | `qwen3.6-flash` (1M) |
| 2 | DeepSeek | `deepseek-v4-pro` (1M) | `deepseek-v4-pro` (1M) | `deepseek-v4-flash` (1M) |
| 3 | 火山引擎 | `doubao-seed-evolving` (1M) | `doubao-seed-2.1-turbo` (256K) | `doubao-seed-2.1-turbo` (256K) |
| 4 | 智谱 AI | `GLM-5.2` (1M¹) | `GLM-5V-Turbo` (200K) | `GLM-4.7-Flash` (200K, 免费) |
| 5 | MiniMax | `MiniMax-M3` (1M) | `MiniMax-M3` (1M) | `MiniMax-M2.5` (200K) |
| 6 | Moonshot | `kimi-k3` (1M) | `kimi-k3` (1M) | `kimi-k2.6` (256K) |
| 7 | MiMo | `mimo-v2.5-pro` (1M) | `mimo-v2.5` (1M) | `mimo-v2.5` (1M) |
| 8 | 阶跃星辰 | `step-3.7-flash` (256K) | `step-3.7-flash` (256K) | `step-3.5-flash` (256K) |
| 9 | 讯飞 MaaS | `GLM-5.2` (256K²) | `Qwen3.5-397B-A17B` (128K²) | `Qwen3.6-35B-A3B` (128K²) |
| 10 | Anthropic | `claude-sonnet-5` (1M) | `claude-sonnet-5` (1M) | `claude-haiku-4-5` (200K) |
| 11 | OpenAI | `gpt-5.6-terra` (1.05M) | `gpt-5.6-terra` (1.05M) | `gpt-5.6-luna` (1.05M) |

> ¹ GLM-5.2 需 `glm-5.2[1m]` 后缀激活 1M；² 讯飞平台侧做上下文缩减
> 注意：`kimi-k2.5` / `moonshot-v1` 系列 **2026-08-31 下线**。模型每季度变化，**型号不应硬编码**（见 WP-3.9 能力注册表）。

### 3. Coding Plan 聚合平台（7 个）

| 聚合平台 | 可调用模型（跨厂商） |
|---------|---------------------|
| 通义千问 Coding | qwen3.7-plus, qwen3.6-plus, kimi-k2.5, GLM-5, MiniMax-M2.5, qwen3-coder-plus, GLM-4.7 |
| 智谱 z.ai | GLM-5.2, GLM-5.1, GLM-5（内置免费 MCP） |
| 火山引擎 Coding | doubao-seed-evolving, doubao-2.1-pro, DeepSeek V4 Pro/Flash |
| 百度千帆 | GLM-5, MiniMax-M2.5, Kimi-K2.5, DeepSeek-V4（⚠️ 已转 Token Plan） |
| 腾讯云 TokenHub | hunyuan-2.0, GLM-5, Kimi-K2.5, MiniMax-M2.5（¥39/月起） |
| 阶跃星辰 Step Plan | step-3.7-flash-2603, step-2 |
| 讯飞 MaaS Coding | 星火 X2 + GLM-5/5.1, Kimi-K2.5/K2.6, MiniMax-M2.5, DeepSeek-V4, Qwen（统一 ID `astron-code-latest`） |

---

## 🏗️ 工作计划（共 8 个 Phase，含 Phase -1 根因盘点）

> Phase 顺序经核验重排：**module 改名 → 品牌焕新（含图标✅/更新链/数据迁移）→ 九天解绑（含 VLM 链）→ 架构重构（含 Default()/能力注册表/五前端）→ PPT → 邮件 → 文档 → 测试发布**。

### Phase 0: 准备与 module 改名（Day 1）

#### WP-0.1: clean start 独立仓库
- [ ] 删除 `test-clone/`、`test-clone-2/`（完整仓库克隆副本，含独立 .git）
- [ ] 保留 `.zcode/`（IDE 配置，无害）—— **不删除 `.agents/`**（v2 误列，本项目无此目录）
- [ ] 移除 `.git` 历史，`git init` 新建 fairpeer 独立仓库（clean start，用户已确认）
- [ ] 首次提交作为基线

#### WP-0.2: Go 模块路径重命名 ⚠️ 工时 ×10
- [ ] `go.mod`: `module github.com/zzycxz/momapeer` → `github.com/zzycxz/fairpeer`
- [ ] **全局替换 import 路径：实际 384 个 .go 文件**（v2 误估 40+）。用 `find . -name "*.go" -not -path "./test-clone*" | xargs sed -i 's|github.com/zzycxz/momapeer|github.com/zzycxz/fairpeer|g'`
- [ ] **额外硬编码点**（非 import，sed 不覆盖）：
  - `cmd/e2ebench/diff.go:406` — `prefix := "github.com/zzycxz/momapeer/"`（编译期字符串，path 匹配用）
  - `cmd/momapeer-plugin-example/main.go` — 示例插件路径
- [ ] 确认 `go build ./...` 通过（预计需修若干包级引用）
- [ ] **预估 1-1.5 天**（非 v2 的 2h）

#### WP-0.3: 27 个 `MOMAPEER_*` 环境变量决策
代码硬编码 27 个 `MOMAPEER_*` env（`MOMAPEER_CACHE_DIR`/`MOMAPEER_LANG`/`MOMAPEER_THEME`/`MOMAPEER_PROXY_PASSWORD`/`MOMAPEER_DEV`/`MOMAPEER_DESKTOP_DISABLE_WEBVIEW2_GPU` 等）。
- [ ] 决策：重命名为 `FAIRPEER_*` + 老变量名**兼容期读取**（半年）
- [ ] grep 全部位置：`grep -rhoE "MOMAPEER_[A-Z_]+"` 逐个替换
- [ ] 更新 `.env.example`、文档中的环境变量引用

---

### Phase 1: 品牌焕新（Day 2-3）

#### WP-1.1: 构建配置品牌替换（7 个文件）
- `desktop/wails.json` — name/productName/companyName/copyright（当前全 momapeer）
- `.goreleaser.yaml` — project_name/binary/homebrew 仓库/description/homepage
- `scripts/installer.nsi` — APP_NAME/APP_PUBLISHER（当前 `APP_VERSION "0.5.6"`，见 WP-7.0）
- `desktop/build/linux/momapeer.desktop` — Name/Exec/Icon
- `desktop/build/linux/nfpm.yaml` — name/vendor/homepage
- `npm/momapeer/package.json` — name/description/keywords
- `scripts/desktop-build.sh` — APPNAME/BINNAME

#### WP-1.2: Go 后端品牌替换
- `desktop/main.go` — 窗口标题、ProgramName
- `desktop/app.go` — singleInstanceID（`desktop/single_instance.go:12` `MOMAPEER_DEV`）
- `desktop/frontend/index.html` — `<title>`
- `desktop/frontend/package.json` — name

#### WP-1.3: 前端 UI 品牌替换
- `StartupSplash.tsx:74` / `Welcome.tsx:61` / `OnboardingOverlay.tsx:46` — "MoMA" 文本和 alt
- `App.tsx:107` — localStorage key（见 WP-1.7）
- `locales/en.ts`（30 处）/ `locales/zh.ts`（33 处）— MoMA/momapeer/九天 引用
- `lib/bridge.ts:1057,1060,2686` — 硬编码 14 模型 moma provider mock（额外隐藏点）

#### WP-1.4: 图标替换 ✅ 已完成（2026-08-02）
**已完成 8 个图标，附件见 `docs/assets/`。** 处理方案：原图 1560×1561 非正方形 → 中心切边 1560×1560 master → 各位置缩放。

| 文件 | 尺寸 | 状态 |
|------|------|------|
| `desktop/build/appicon.png` | 536×536 | ✅ |
| `desktop/build/windows/icon.ico` | ICO 16-256 | ✅ |
| `desktop/frontend/public/favicon.ico` | ICO 16-256 | ✅ |
| `desktop/frontend/src/assets/logo.png` | 536×536 | ✅ |
| `desktop/frontend/src/assets/logo-symbol.png` | 980×980 | ✅ |
| `desktop/frontend/src/assets/welcome-hero.png` | 980×980 | ✅ |
| `docs/favicon.png` | 536×536 | ✅ |
| `docs/logo.png` | 2000×400 横版 banner（Fair+Peer wordmark） | ✅ |

#### WP-1.5: CI/CD 品牌替换（⚠️ 实际 4 个 workflow，但内部细节多）
- `.github/workflows/release-desktop.yml` — `REPO="zzycxz/momapeer"`(:288)、release name(:201,213)、6 个 `momapeer-*` artifact 名、`cmd/sign` manifest 生成器(:261)
- `.github/workflows/release.yml` — **当前 CLI release 是 DISABLED 状态**(:11-13，缺 homebrew tap repo + token)；有 Cloudflare R2 mirror job(:42-90)；示例 `v1.4.0`(:55) 与 plan v1.0.0 矛盾
- `.github/workflows/sync-gitee.yml` — 整条 Gitee 镜像 workflow（中国用户加速），改名后需重测 `git-filter-repo` LFS 魔法
- `.github/workflows/ci.yml` — **无品牌字面量**（v2 误列，无需改）

#### WP-1.6: 更新链迁移 🔴 致命链（v2 完全漏掉）
`desktop/updater.go` 7 处硬编码 + 发布端，**改名=双向断裂**：
- 客户端：`updater.go:43,45-46,50-51,121,131,135-138` — `zzycxz/momapeer` GitHub + Gitee URL
- 发布端：`release-desktop.yml:288`、`sync-gitee.yml`、`.goreleaser.yaml:44`（homebrew tap `homebrew-momapeer`）
- [ ] 客户端 URL 与发布 repo **原子切换**到 `zzycxz/fairpeer`
- [ ] **老 momapeer 仓库留一个最终 release 指向 fairpeer**（迁移通告），否则老客户端永久断更
- [ ] Gitee 镜像 repo 同步改名（中国用户更新依赖）
- [ ] homebrew tap 仓库 `homebrew-momapeer` → `homebrew-fairpeer`，迁移 cask

#### WP-1.7: 用户数据迁移 🔴 致命链（v2 一笔带过）
`.momapeer/` 目录名 **20+ 处硬编码** + 前端 8 个 localStorage key，改名=数据全丢：
- 目录常量**两处同步改**：`internal/config/config.go:1989`（`userDirname="momapeer"`）+ `internal/secret/store.go:39`
- `userDir()` 已有改名逻辑（`:1992-2018`），但**新旧名都写死 "momapeer"**，改常量后才生效，需测
- [ ] 改 `userDirname` 常量 `"momapeer"` → `"fairpeer"`
- [ ] 改 `userDir()` oldPath 为 `"momapeer"`，实现 `momapeer→fairpeer` 自动 rename
- [ ] 前端 8 个 localStorage key 迁移：`App.tsx:107`、`composerHistory.ts:13`、`fontFamily.ts:7`、`displayMode.ts:3`、`layoutPreferences.ts:17`、`i18n.tsx:24`、`textSize.ts:7`、`theme.ts:45-46`（一次性读取老 key 写入新 key）
- [ ] `internal/agent/agent.go:1613` — attachments 正则 `.momapeer/attachments/`
- [ ] `internal/assets/release.go:49,107` — ppt-auto skill 解压路径 `~/.momapeer/skills/`
- [ ] `internal/config/mcpjson.go:73` — legacy 读 `~/.momapeer/config.json`（老 MCP 配置）

---

### Phase 2: 九天平台解绑（Day 4-5）

#### WP-2.1: 重构 internal/jiutian/（⚠️ 8 个调用点，v2 只列 3 个）
`internal/jiutian/` → `internal/apihelper/` 或 `internal/platform/`。实际被 **8 个生产文件** import：
- `internal/boot/boot.go:34` + `:302` `jiutian.SetBaseDomain()`（启动注入全局域名）
- `internal/rag/jiutian_extractor.go:28`
- `internal/tool/builtin/jiutian_api.go` + `ragembed_jiutian.go` + `vlm.go`
- `desktop/rag_app.go` + `scheduler_llm.go` + `scheduler_app.go`
- `internal/provider/openai/openai.go:5`
- [ ] 移除 `internal/jiutian/api.go:24` `defaultBaseURL = "https://jiutian.10086.cn/..."`，改配置驱动

#### WP-2.2: 九天多模态工具通用化（⚠️ 含字面量 URL）
- `image_understand`/`image_generate`/`video_understand` 改可配置
- [ ] **`jiutian_api.go:55`** `jiutianUploadFile` POST 到硬编码 `https://jiutian.10086.cn/.../fs/uploadFile`（**不走 BaseURL**）
- [ ] **`jiutian_multimodal.go:158`** `downloadURL := "https://jiutian.10086.cn/.../fs/getFile?key=%s"`（**字面量，不走 BaseURL**）
- [ ] 这两处必须单独改，`SetBaseDomain` 无效

#### WP-2.3: 移除九天域名检测
- `internal/provider/openai/host.go:35` — `IsMoMA()`（注意 `openai/` 子包路径）
- `internal/provider/openai/openai.go` — MoMA thinking 协议分支
- `desktop/settings_app.go:229` — `if host == "jiutian.10086.cn"` + `:863` 默认 BaseURL

#### WP-2.4: 清理九天配置默认值
- `momapeer.example.toml:66` — `[jiutian]` 配置段（image_understand/generate/video 三开关）
- `.env.example:4` — `JIUTIAN_API_KEY`（标为 Required，默认 provider）
- `desktop/settings_app.go` — 移除九天默认 provider

#### WP-2.5: 清理脚本和基准测试（⚠️ v2 漏列）
- `benchmarks/context-maintenance-e2e/main.go:25,31` — 直连九天 baseURL + `JIUTIAN_API_KEY`
- `cmd/cua-replay/main.go:58,63` — `momaBaseURL` + 默认 model `qwen3.6-27b`
- `cmd/e2ebench/main.go:62` — `bin := "momapeer"`；`diff.go:406` — module 路径前缀（见 WP-0.2）
- `scripts/backfill-issue-labels.mjs:60,73` — fetch 九天 + `moma API`
- `scripts/test_kimi_audio.py:68`、`scripts/cua-demo.md:24,29`、`scripts/cua-test.md:23`
- `browseruse_server.py`、`scripts/package.ps1`、`scripts/desktop-build.sh`、`scripts/desktop_bridge.py`

#### WP-2.6: VLM 链兜底重做 🔴 致命链（v2 完全漏掉）
`internal/boot/boot.go:666-673` **无条件以九天结尾**：
```go
// Terminal fallback: 九天 always closes the chain.
chain = append(chain, builtin.VLMBackend{Kind: builtin.VLMBackendJiutian, ...})
```
- [ ] 九天一拆，`CallVLM` 兜底没了 → 截图识别、图片理解全废
- [ ] 改为「**最后一个用户配置的 vision provider**」作为兜底，或允许空链（无 vision 时降级文本描述）

---

### Phase 3: 多厂商 LLM 架构重构（Day 6-9）⭐ 核心

> **覆盖：11 直连 Provider + 7 Coding Plan。架构原则：改 controller/config 层，五前端继承。**

#### WP-3.0: 重写 `config.Default()` 🔴 五前端根（v2 完全漏掉）
`internal/config/config.go:1430` `func Default()` 是**所有五个前端的起点**（flag > project > user > Default），现在把九天焊死：
- `:1496-1497` 默认只装 `moma` provider，挂 `JIUTIAN_API_KEY` → 公网用户没 key，agent 起不来
- `:1380-1384` `DefaultSystemPrompt = "You are momapeer ... always say you are momapeer"`（**身份声明，Phase 1 漏了**）
- `:368` `DefaultFastTaskModel = "qwen/qwen3.6-35b"`（九天）
- `:1613` `ScreenshotVLMModel = "qwen/qwen3.5-397b-a17b"`（九天）
- [ ] 重写 Default()：去掉 moma provider，改为「无默认 provider + 引导 setup」或预置 11 家
- [ ] `DefaultSystemPrompt` 改 FairPeer 身份
- [ ] `DefaultFastTaskModel`/`ScreenshotVLMModel` 改配置推导
- [ ] **这是 Phase 3 前置**，不先做这个，五前端全坏

#### WP-3.1: Provider 配置升级 — per-provider 三类模型角色 + 连接时探测
**现状（v2 误判"无 default"，实际已有）：** `ProviderEntry`（`config.go:1123-1165`）已有 `Default`/`Vision bool`/`Thinking`/`Effort`/`ReasoningProtocol`/`SupportedEfforts`/`ModelsURL`/`Price` 等字段。`FetchModels`（`fetch.go:24`）已支持连接时探测可用型号。

**混合方案（已决策）：型号探测 + 角色预置**
- [ ] **补 `fast_model string` 字段**（唯一缺失的角色字段）
- [ ] **`Vision bool` 升级为可选 `vision_model string`**（从开关变模型名，更精确）
- [ ] `default_model` 字段已存在（`Default string`），无需新增
- [ ] **连接时探测型号清单**：用户填 API key → 点连接 → 调 `FetchModels` 拉 key 实际可用型号（前端展示完整列表供选）
- [ ] **角色（default/vision/fast）用 plan §2 预置值预填**，用户可在探测到的型号列表里覆盖
- [ ] 涉及：`internal/config/config.go`、`desktop/settings_app.go` `ProviderView`、`fairpeer.example.toml`

#### WP-3.2: 全局模型角色推导（保留全局 + 推导，非对立）
全局 `DefaultModel`(`config.go:43`)/`FastTaskModel`/`SubagentModel`/`PlannerModel`(`AgentConfig`) 是**互补**不是互斥。
- [ ] 全局角色 = 用户显式 > 推导自第一个 provider 的对应角色字段
- [ ] **真正问题**：全局角色没关联 provider，切 provider 后角色失效 → 改推导逻辑
- [ ] ⚠️ 代码**无全局 `VisionModel` 字段**（v2 误批），截图走 `[cowork] screenshot_vlm_model`，无需动

#### WP-3.3: Coding Plan 支持（✅ 已决策：复用 ProviderEntry，双标志维度）

**关键洞察：Coding Plan 的本质是「额度类型」，不是「是否聚合」。** 同一厂商通常给两个接口 URL，背后是两套独立额度池：

| 接口类型 | 扣额度 | 例子 |
|---------|--------|------|
| 普通端点 | token 额度（按量） | 通义 `dashscope.aliyuncs.com` |
| Coding 端点 | Coding Plan 订阅额度 | 通义 `coding.dashscope.aliyuncs.com`（¥200/月） |

两个端点**可能调同一模型**（如 `qwen3.7-plus` 在两边都能调，但扣不同额度）。**DeepSeek/MiMo/Anthropic/OpenAI 是特例——无第二个端点。**"聚合"只是某些 Coding 端点的附带特征（通义 Coding 能调 kimi/GLM），不是本质——阶跃 Step Plan 主要调自家 step 系列但仍是 Coding Plan。

**决策：复用 `ProviderEntry` + 双正交标志。** 同厂商双端点 = 两个 ProviderEntry（不同 base_url + 不同 api_key），限流天然分桶（`rate_limit.go:79` 按 `baseURL|apiKey`，不同即不同桶）。

| 标志 | 语义 | 作用 |
|------|------|------|
| `coding_only` | 走 Coding 订阅额度 | 额度类型提示 + 限制仅编码工具调用（对应厂商使用限制） |
| `aggregator` | 能调多家厂商模型 | UI 视觉分组（聚合平台分区），非路由分支 |

四种现实形态：
- 普通按量（DeepSeek/MiMo/Anthropic/OpenAI）：`coding_only=false, aggregator=false`
- 普通聚合：`coding_only=false, aggregator=true`（理论形态）
- Coding 自家（阶跃 Step Plan）：`coding_only=true, aggregator=false`
- Coding 聚合（通义 Coding/智谱 z.ai/火山 Coding/讯飞 MaaS）：`coding_only=true, aggregator=true`

```toml
# 通义千问 Coding Plan（聚合形态）
[[providers]]
name = "qwen-coding"
kind = "openai"
base_url = "https://coding.dashscope.aliyuncs.com/v1"
api_key_env = "QWEN_CODING_API_KEY"     # sk-sp- 前缀，独立于普通 DashScope Key
coding_only = true
aggregator = true
default_model = "qwen3.7-plus"
vision_model = "qwen3.7-plus"
models = ["qwen3.7-plus", "qwen3.6-plus", "kimi-k2.5", "GLM-5", "MiniMax-M2.5", "qwen3-coder-plus", "GLM-4.7"]

# DeepSeek（无 Coding Plan，纯按量）
[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"
# coding_only 和 aggregator 均默认 false
default_model = "deepseek-v4-pro"
```

- [ ] `ProviderEntry` 新增 `CodingOnly bool` + `Aggregator bool` 两字段（默认均 false）
- [ ] `coding_only=true` 的 provider：UI 提示"消耗订阅额度"，可选限制仅编码工具调用
- [ ] `aggregator=true` 的 provider：ModelSwitcher/SettingsPanel 做"聚合平台"视觉分区
- [ ] 同厂商双端点（通义/火山/智谱/讯飞等）拆成两个 ProviderEntry
- [ ] 7 个 Coding Plan + 11 直连全部写成此形态（见 §2/§3 表）
- [ ] 省掉 `CodingPlanEntry` 结构/独立路由层/独立 UI tab（v2 方案的设计冗余）

#### WP-3.4: 公网 Provider + Coding Plan 配置模板
- [ ] `fairpeer.example.toml` 预置 11 厂商 `[[providers]]`（每个含 default/vision/fast/context_window）
- [ ] 7 个聚合平台（按 WP-3.3 决策形态）
- [ ] 引用格式：`provider/model`（如 `qwen-coding/kimi-k2.5`）

#### WP-3.5: 移除内置 MoMA 模型清单 + 能力白名单（改为 per-provider 角色字段）
**与 WP-3.1 混合方案衔接：** 硬编码白名单负责判定能力，现改为由 per-provider `vision_model`/`fast_model` 角色字段 + 预置配置决定（已决策）。型号走 FetchModels 探测，能力走配置声明。
- [ ] `internal/config/config.go:1413` `BuiltinMoMAModels`（14 个，注意在 config.go 非 openai.go）
- [ ] **`internal/provider/openai/openai.go:202` `MoMAThinkingModels`**（13 个 thinking 白名单）
- [ ] **`internal/provider/openai/openai.go:236` `MoMAVisionModels`**（4 个 vision 白名单）
- [ ] **`internal/provider/openai/openai.go:243` `ModelSupportsVision`** — 改为读 provider 的 `vision_model` 角色字段（而非查硬编码表）
- [ ] `internal/config/effort.go:31-37` `modelReasoningCapabilities` — 从 MoMAThinkingModels 派生 → 改为读 provider 能力配置
- [ ] `effort.go:12` `ReasoningProtocolMoMA = "moma"`

#### WP-3.6: Provider 设置页重构（桌面端）
- [ ] 移除"九天域名"输入框 + Screenshot VLM 硬编码（`SettingsPanel.tsx:2051,4850` 两处各 2 个 qwen option）
- [ ] 每个 Provider 卡片显示三类角色
- [ ] API Key 输入 + 连通性测试按钮（基础设施已有 `settings_app.go:968 FetchProviderModels`、`app.go:5593 probeProviderKey`）
- [ ] ModelSwitcher 改按能力分组（vision/fast/reasoning）而非厂商前缀（当前 `ModelSwitcher.tsx:16-24` 硬编码 7 个前缀分类）

#### WP-3.7: Onboarding 引导页重构（桌面端）
- [ ] 移除 `jiutian.10086.cn` 链接
- [ ] 改为 11 厂商选择网格 + API Key 获取链接 + 免费额度提示
- [ ] 支持"跳过，稍后配置"

#### WP-3.8: 五前端 coding_plan/model 适配 🔴（v2 完全漏掉，违反架构原则）
v2 改造 90% 在桌面端，但 CLI/serve/bot/acp 共享 controller——**必须同步**：
- [ ] **CLI/TUI**：`internal/cli/cli.go:686` `interactiveSetup`、`:828` `selectEnabledProviders`/`withBuiltinFamilies`（硬编码 MoMA 家族）、`/model`（`model.go:85`）、`/provider`（`provider.go:42`）需认 `coding_plans`
- [ ] **serve (HTTP/SSE)**：`internal/serve/serve.go:67` 标题生成器硬编码 `ResolveModel("moma/qwen/qwen3.6-35b")` → 改读 `cfg.Agent.FastTaskModel`；`switchModel:87` 认 coding_plan
- [ ] **IM Bot**：`bot/qq/gateway.go:195` `Browser:"momapeer"`、`feishu.go:503` `WithSource("momapeer")`、`weixin.go:495` `client_id:"momapeer-..."`（**发给 IM 平台的设备标识，改名要同步**）；bot 默认 model 落 Default() moma（WP-3.0 解决）
- [ ] **ACP**：`acp.go:66` 测试断言 `"name":"momapeer"`、`protocol.go` 多处（评估是否破坏协议兼容）

#### WP-3.9: 能力注册表重构 🔴 致命链（v2 漏，与 WP-3.1 同步）
模型能力（vision/thinking/function-calling）由两个**硬编码 MoMA 名单**决定，新厂商模型全判"无能力"：
- [ ] `MoMAVisionModels`/`MoMAThinkingModels` → per-provider 能力字段或运行时探测
- [ ] 否则给智谱配 `GLM-5.2` 作 vision_model，运行时 `ModelSupportsVision` 仍判"无 vision"，走降级链
- [ ] 厂商识别：当前只 `IsMoMA`/`IsMiniMax`（`host.go:35,45`），其余 9 家无 host 识别

#### WP-3.10: provider 健康探测 + 可选 failover
当前 `retry.go:148-209` 只同 provider 内重试，无跨 provider 切换；启动从不探测（要发第一条消息才发现 key 配错）。
- [ ] 启动后台并发健康探测所有已配置 provider（复用 `FetchProviderModels`）
- [ ] 可选自动 fallback（主 provider 持续失败 → 切备用）
- [ ] 配置校验 `Validate`（`config.go:2342`）只查选中 model → 改为遍历所有 provider 汇总健康面板

---

### Phase 4: PPT 模板智能化（Day 10-11）

#### WP-4.1: PPT 模板颜色自动检测 — XML 解析层
新增 `internal/ppttemplate/color_extractor.go`，从 PPTX 解析 `theme1.xml` 的 `clrScheme`（12 色方案）+ slideMaster 背景 + slide srgbClr 统计。
- [ ] 处理 `srgbClr` 直接值与 `schemeClr` 引用映射（bg1→lt1, tx1→dk1, accent1-6）
- [ ] 代码现状：`scripts/analyze_template.py` 仅导出 PNG 背景图，**无任何 clrScheme/theme1.xml 解析**（确认为真实缺口）

#### WP-4.2: VLM 兜底层（XML 不足时）
- [ ] 调 `vision_model` 分析导出的背景图，输出 3-4 主色调 JSON
- [ ] ⚠️ **用户无 vision provider 时断链** → 需纯 fallback（如中性蓝灰默认配色），不能强依赖 VLM
- [ ] 复用 `internal/tool/builtin/vlm.go` 基础设施

#### WP-4.3: PPT 技能配色系统重构
- [ ] `internal/assets/pptauto/template_config.json`（⚠️ **无 templates/ 子层**，v2 路径错）— 移除 `#1084CD`(×3)/`#D9EAF7`/`#EAF5FC`/`#FF7F00`(×2)/`#8CC63F`（远超 v2 说的"5 处"）
- [ ] `internal/assets/pptauto/SKILL.md:156` — 移除"中国移动商务风格补充约束"整段 + `:158` "中国移动 PPT 要求"
- [ ] `internal/assets/pptauto/references/layout_templates.md` — 移除硬编码颜色
- [ ] `internal/assets/pptauto/check_svg.py` — 移除默认颜色 fallback
- [ ] `internal/ppttemplate/template.go` — Theme 字段支持动态值

#### WP-4.4: PPT 技能新增"模板分析"Step 0
SKILL.md 生成流程前置：读模板 → extract_template_colors() → VLM 兜底 → 输出主色调 → 后续 SVG/DrawingML 必须用提取色。

#### WP-4.5: PPT 内置模板去品牌化
- [ ] `templates/中国移动模板.pptx` → `templates/default.pptx`（`template_config.json:104` `template` 字段引用）
- [ ] 确保 default.pptx 的 theme.xml 中性配色

---

### Phase 5: 邮件系统扩展（Day 12）

#### WP-5.1: 新增主流邮箱配置示例
| 邮箱 | SMTP | IMAP |
|------|------|------|
| Gmail | smtp.gmail.com:587(STARTTLS) | imap.gmail.com:993(SSL) |
| Outlook | smtp.office365.com:587(STARTTLS) | outlook.office365.com:993(SSL) |
| QQ | smtp.qq.com:465(SSL) | imap.qq.com:993(SSL) |
| 163 | smtp.163.com:465(SSL) | imap.163.com:993(SSL) |
| 126 | smtp.126.com:465(SSL) | imap.126.com:993(SSL) |
| Yahoo | smtp.mail.yahoo.com:587(STARTTLS) | imap.mail.yahoo.com:993(SSL) |
| iCloud | smtp.mail.me.com:587(STARTTLS) | imap.mail.me.com:993(SSL) |

#### WP-5.2: 邮箱服务商自动检测
- [ ] 按邮箱地址后缀自动推荐 SMTP/IMAP 配置，设置页自动填充

#### WP-5.3: 移除 139 邮箱特殊处理（⚠️ v2 误判，实际是注释清理）
代码**无 `if host=="139.com"` 分支**。139 特殊处理走通用机制：
- [ ] `internal/tool/builtin/email_imap.go:78-91` TLS RSA 回退（通用，注释提 139）— **保留逻辑，泛化注释**
- [ ] `:173-287` GBK charset 自动检测（通用）— **保留**
- [ ] 真正硬编码 139.com 的只是注释和 example.toml 示例账号（`:245-273`）— 清理注释

---

### Phase 6: 文档和配置更新（Day 13）

#### WP-6.1: README + 文档全量去品牌（⚠️ v2 只提 README）
- [ ] 中英文 README 重写（引用新 `docs/logo.png` 横版 banner）
- [ ] 新增：11 厂商支持矩阵 + 快速开始 + API Key 获取指引
- [ ] **`docs/` 下 24 个 .md 含 momapeer/moma/九天**，包括 GUIDE/SPEC/EXPERT_GUIDE/OFFICE_GUIDE 等，逐一处理
- [ ] `docs/MOMA_MODEL_MATRIX.md` 引用 `BuiltinMoMAModels`/`MoMAThinkingModels`/`MoMAVisionModels` 三处代码（WP-3.5 改后同步）
- [ ] `docs/RELEASING.md` npm dist-tag `momapeer@canary` 等
- [ ] `momapeer.md` 记忆文件改名 `fairpeer.md` + 内容更新（当前版本历史只到 v0.1.6，与 CHANGELOG 0.5.15 脱节）

#### WP-6.2: 配置文件示例更新
- [ ] `fairpeer.example.toml` 完整重写（11 providers + 7 聚合平台）
- [ ] `.env.example` — 11 厂商 + 7 聚合平台 API Key 环境变量（约 19 个 Key）
- [ ] ⚠️ **API Key 安全存储**：当前 19 个 key 明文写 `credentials` 文件（`config/dotenv.go:40`、`migrate.go:271`）。`internal/secret/` **已有完整 DPAPI/AES-GCM 加密**，却只用于邮箱密码 → LLM key 改走 `secret.Store`（`ConnectKey`/`upsertDotEnv` 改实现）

#### WP-6.3: 文档站更新
- [ ] 新增《各 LLM 厂商接入指南》《Coding Plan 配置指南》《PPT 模板自定义指南》《邮箱配置指南》

---

### Phase 7: 测试与发布（Day 14-15）

#### WP-7.0: 版本号统一（⚠️ v2 漏，当前三处错位）
当前版本号**三处不一致**：CHANGELOG `0.5.15` / README badge `0.5.6` / installer `0.5.6` / `momapeer.md` 只到 `v0.1.6`。
- [ ] 先对齐四处版本号
- [ ] 决定 1.0.0 合理性（配置破坏性变更：移除 BuiltinMoMAModels + 改 Default() → 老 config 的 `default_model="moma/..."` 解析失败 = major，1.0.0 合理）

#### WP-7.1: 编译验证
- [ ] `go build ./...` / `go vet ./...` / `gofmt` / `wails build`
- [ ] **配置 schema 单测 + provider 路由单测**（v2 只有端到端，高风险架构改动需单测）

#### WP-7.2: 功能验证
- [ ] 至少 3 家 LLM 厂商连通性（含 Coding Plan 端点）
- [ ] PPT 模板颜色检测（换模板后自动提取）
- [ ] 邮件收发（至少 3 种邮箱）
- [ ] **更新链端到端**：模拟老客户端查 fairpeer repo（WP-1.6）
- [ ] **数据迁移**：模拟老 `.momapeer/` 升级（WP-1.7）

#### WP-7.3: 首版发布
- [ ] 打标签 `v1.0.0`（注意 `release.yml:11-13` CLI release 当前 DISABLED，需先恢复 homebrew tap repo + token）
- [ ] GitHub Release + 6 平台安装包（Windows/macOS/Linux × amd64/arm64）
- [ ] **老 momapeer 仓库留迁移通告 release**（WP-1.6）

---

## 📋 优先级与工时（v3 重估，较 v2 ×2-3）

| 优先级 | 工作包 | 预估 | 说明 |
|--------|--------|------|------|
| P0 | WP-0.1~0.3 准备 + module 改名 + 27 env | **2 天** | 384 文件 import（v2 误估 2h） |
| P0 | WP-1.1~1.7 品牌焕新 | **2 天** | 含更新链/数据迁移致命链；图标✅已完成 |
| P0 | WP-2.1~2.6 九天解绑 | **2 天** | 含 VLM 链重做；8 调用点 |
| **P0** | **WP-3.0~3.5 架构核心** | **3 天** | Default() + 能力注册表 + 移除白名单 |
| P1 | WP-3.6~3.10 UI + 五前端 + 健康探测 | **2 天** | 含 CLI/serve/bot/acp 适配 |
| P1 | WP-4.1~4.5 PPT 智能化 | **2 天** | XML 解析 + VLM 兜底 + 去品牌 |
| P1 | WP-5.1~5.3 邮件扩展 | **1 天** | |
| P2 | WP-6.1~6.3 文档更新 | **1 天** | 24 个 md + Key 加密 |
| P2 | WP-7.0~7.3 测试发布 | **2 天** | 版本号统一 + 单测 + 更新链验证 |
| — | WP-1.4 图标 | **✅ 已完成** | |

**总计：~17 工作日（v2 的 ~3 倍，因修正了工时低估 + 新增致命链 WP）**

---

## 🔧 决策结果（2026-08-02 已拍板）

| 决策项 | 结论 | 依据 |
|--------|------|------|
| **Coding Plan 架构** | ✅ **复用 `ProviderEntry` + 双标志（`coding_only`/`aggregator`）**（不搞独立 `[[coding_plans]]`） | Coding Plan 本质是「额度类型」非「是否聚合」：同厂商双端点=两个 ProviderEntry（不同 base_url+key），限流天然分桶。`aggregator` 只做 UI 分组。"聚合"是附带特征，阶跃 Step Plan 调自家模型但仍是 Coding Plan |
| **Coding Plan 额度** | ✅ **`coding_only` 标志区分订阅额度 vs token 额度** | 同模型可能在两个端点都能调（如 qwen3.7-plus），但扣不同额度。DeepSeek/MiMo/Anthropic/OpenAI 无 Coding 端点（特例） |
| **模型型号来源** | ✅ **连接时实时探测**（FetchModels 拉取 key 实际可用清单） | 型号每季度变，探测拿实时数据不过期；`fetch.go` 已现成，支持 `/models` + `/v1/models` 多形态 |
| **模型角色来源** | ✅ **plan §2 预置配置**（default/vision/fast 预填，用户可覆盖） | 能力（vision/thinking）无标准 API 端点，靠预置声明最稳；探测只能列型号名，列不出"哪个是视觉模型" |
| **27 env 变量** | ✅ **重命名 `FAIRPEER_*` + 半年兼容期** | 保留 `MOMAPEER_*` = 品牌永久残留，违背去品牌初衷。实测 13 个是测试专用、8 个生产用户面 |
| **license** | ✅ **MIT，允许去品牌再发布** | `LICENSE` 是 MIT，Copyright `2026 momapeer Contributors`。MIT 明确允许修改/再发布/sublicense/改名。执行时版权行改 `FairPeer Contributors` |

---

## 🔧 技术决策记录

| 决策项 | 方案 | 理由 |
|--------|------|------|
| Git 历史 | clean start，移除 .git | 用户确认，与 momapeer 各自独立维护 |
| 旧图标备份 | 删除（不留在仓库） | fairpeer 独立，momapeer 另有维护 |
| Go 模块路径 | `github.com/zzycxz/fairpeer` | 同一 GitHub 用户 |
| 九天多模态工具 | 保留为可配置通用工具 | 不丢能力，端点配置驱动 |
| 内置 MoMA 模型 | 移除，配置驱动 | 公网无需九天模型 |
| **模型角色** | **per-provider + 全局推导** | per-provider 声明能力，全局选默认（互补） |
| **能力判定** | **能力字段/运行时探测**（非型号硬编码） | 型号易过期，能力才稳定 |
| **PPT 颜色** | **XML 解析 + VLM 兜底 + 纯色 fallback** | 三保险，不依赖单一手段 |
| 邮箱 TLS 回退 | 保留并泛化注释 | 兼容老旧邮箱，已是通用机制 |

---

## 📎 附件清单（`docs/assets/`）

| 文件 | 用途 |
|------|------|
| `fairpeer-logo-master.png`（1560×1560，1690KB） | 正方形 logo master，重新生成任意方形图标的源头 |
| `fairpeer-banner-original.jpg`（2000×400，78KB） | 横版 banner 原图（README 用 `docs/logo.png` 的 PNG 版） |

> 需新尺寸时：`python -c "from PIL import Image; Image.open('docs/assets/fairpeer-logo-master.png').resize((N,N), Image.LANCZOS).save('目标.png')"`
