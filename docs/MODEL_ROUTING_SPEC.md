# 模型理解路由层规格（MODEL ROUTING SPEC）

> 状态：生效（2026-08-21 定稿）。本层是**非智能体**的：对"模型/端点是什么"的全部理解由字符串规则与配置声明完成，零 LLM 参与、零运行时探测。

## 1. 两键正交模型

这层回答两个**不同**的问题，各自用对的键。要统一的是它们的位置与文档，不是把键合并：

| 问题 | 键 | 为什么 |
|---|---|---|
| 这个**模型**需要什么行为纠正（提示词补充）？ | 模型 ID（家族嗅探） | 失效模式是模型属性（qwen 工具调用格式、glm 并行丢调用…），与端点无关 |
| 这个**端点**说什么请求方言（线格式）？ | BaseURL 主机名（+ `reasoning_protocol` 显式覆盖） | 方言是网关/供应商端点的属性；同一模型经不同网关可说不同方言 |

`reasoning_protocol` 配置 = 人工覆盖逃生口：显式声明可以强制方言，覆盖主机名嗅探（网关/代理场景唯一解法）。

## 2. 五个站点及职责

| # | 站点 | 文件 | 职责 | 键 |
|---|---|---|---|---|
| ① | 家族嗅探 → 提示词 | `internal/instruction/family.go` | 模型 ID → 家族 → 手术式提示补充 | 模型 ID（前缀精确 + token 边界） |
| ② | 推理协议声明 | `internal/config/effort.go` | `reasoning_protocol` 归一/校验、effort 统一词表（low/medium/high + 旧值迁移） | 配置声明 |
| ③ | 线格式方言 | `internal/provider/openai/{openai,host}.go`、`anthropic/` | MiniMax thinking.type vs reasoning_effort；Anthropic 扩展思考 + 签名回放 | BaseURL 主机名（`IsMiniMax` 精确匹配）+ ②的显式覆盖 |
| ④ | 目录分类 | `internal/config/config.go` `IsLikelyChatModel` | 非对话模型过滤（喂选择器） | 模型名 token 黑名单 |
| ⑤ | 引用解析兜底 | `internal/config/config.go` `ResolveModelWithFallback` | ref → default_model → 首个可用供应商；新鲜安装不落 keyless 预设 | 配置 |

## 3. 不变量（受保护约束，改动需过对应测试）

1. **非智能体**：本层任何路径不得调用 LLM。需要"理解内容"的分类（如 plan 模式判定 `auto_plan_classifier.go`）属于其上的智能体层，不在此。
2. **Build 期一次定型**：家族嗅探与提示补充只在 `boot.Build`（boot.go ForModel 注入点）执行一次，会话内稳定——这是前缀缓存稳定性的前提。
3. **切模型 = 全量重建**：`SetModelForTab`/`switchModel` 走 `boot.Build` 重建控制器，提示随之重算。不得引入"只改模型名不重建"的路径，否则不变量 2 被破坏。
4. **addon 只对实测失效模式开**：识别一个家族 ≠ 给它 addon。新增 addon 需要先有实测失效记录（qwen/glm/deepseek/kimi 四个的由来）。
5. **协议错字必须报错**：未知 `reasoning_protocol` 在构建期报错并列出合法值（`auto|openai|minimax|none`），不得静默降级为 auto。
6. **端点方言匹配保持精确主机名**：`IsMiniMax` 只认 minimaxi.com 系主机（刻意不认 `minimax` 拼写，防未来 minimax 品牌网关误撞）；代理/网关场景的解法是显式 `reasoning_protocol="minimax"`，不是放宽嗅探。

## 4. 家族嗅探匹配规则（2026-08-21 收紧）

两级匹配，替代旧的子串 Contains（`glmw` 误判 glm 之类）：

1. **供应商前缀**：`qwen/`、`deepseek/`、`z.ai/`、`moonshot(ai)/`、`minimax(i)/`、`openai/`、`anthropic/` 精确前缀，优先于 token。
2. **token 边界**：按 `-_. :/` 切词后整 token 相等（`GLM-4.6` → `glm`✓；`glmw-v2` → 单 token `glmw`✗）。**数字后缀融合规则**：`qwen3-max` 这类"键+数字"单 token 接受（key 后紧跟数字才算，字母后缀不算）。

家族表：qwen(qwen/qwq)、deepseek、glm、kimi、minimax、gpt(gpt/chatgpt/o1/o3/o4)、anthropic(claude)。未知 → `""`（不加 addon，安全默认）。

## 5. 注册表与行为层的边界

models.dev 注册表（desktop 包，四层降级）的每模型 `Reasoning` 标志保留到 `ProviderTemplate.ReasoningModels`——**仅供 UI 展示**。行为层（①②③）不读注册表数据：注册表在 desktop、行为层在 internal，依赖方向不可反转；且嗅探需要零网络零缓存的确定性。未来若要让行为层用注册表数据，需先把数据下沉到 internal 层的静态快照，另行评审。

## 6. 已知取舍记录

- `IsLikelyChatModel` 是**黑名单制**：新非对话模态出现时需手工补 token（最近一次：grok-imagine / sora 的 `imagine`/`video`）。维护提示已写在函数头注释。
- `ResolveModelWithFallback` 对"新鲜安装 vs 陈旧页签引用"区别对待（keyless 本地预设前者绝不自动选、后者允许降级），语义见函数注释与 `model_fallback_test.go`。
- 历史修复层（`provider/normalize.go` 工具调用契约修补）与本层相邻但独立：它修的是**会话历史**的线安全，不是模型理解。
