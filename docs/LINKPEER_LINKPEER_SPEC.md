# linkpeer 移动端（C）实现规范

> 状态：v1
> 日期：2026-08-11
> 范围：linkpeer（Flutter）App 的**实现层**规范——模块结构、状态机、数据流、安全、跨平台。
> 上游协议：[`LINKPEER_PROTOCOL.md`](./LINKPEER_PROTOCOL.md)。本规范是其 C 端实现的细化。

---

## 1. 职责边界

**C 做**：
- 扫码配对（指纹本地校验）。
- 发起 WebRTC 连接、握手、命令下发。
- 渲染 wireEvent 流（豆包/Gemini 样式）。
- 本地缓存历史（断网可看）。
- 多桌面管理（MVP 单活动连接）。

**C 不做**：
- ❌ 不跑模型、不存 API Key、不复制桌面文件。
- ❌ 不脱离桌面独立工作（无连接只能看缓存）。
- ❌ 不互通第三方 IM。

---

## 2. 模块架构（Flutter）

```
mobile/linkpeer/
└── lib/
    ├── main.dart
    ├── app.dart
    ├── core/
    │   ├── webrtc/            # PeerConnection、DataChannel、ICE 收集
    │   ├── crypto/            # Ed25519、X25519、AES-GCM、constant-time
    │   ├── signaling/         # WS 客户端（连 K）
    │   ├── pairing/           # 扫码、指纹校验
    │   ├── transport/         # NDJSON + AEAD 帧 + 分片重组
    │   └── connection/        # 状态机、重连（backoff+jitter）
    ├── data/
    │   ├── models/            # wireEvent Dart 镜像（对齐 Go proto）
    │   ├── session_store.dart # sqflite
    │   └── secure_store.dart  # flutter_secure_storage 封装
    ├── features/
    │   ├── chat/              # 对话 Tab（豆包样式）
    │   ├── office/            # 办公 Tab
    │   ├── pairing/           # 配对引导
    │   ├── settings/          # 设备/权限/诊断
    │   └── shared/            # 事件渲染组件
    └── platform/              # Android/iOS 差异（前台服务、Keychain）
```

---

## 3. 连接状态机（单连接）

```
IDLE ──配对成功──► PAIRED
PAIRED ──用户选 S / App 启动──► SIGNALING
        （WS 连 K 认证 + 发 offer）
SIGNALING ──DataChannel 开──► CONNECTED
SIGNALING ──unavailable/超时──► PAIRED（提示"桌面离线"）
CONNECTED ──Finished 通过──► ENCRYPTED
CONNECTED ──握手失败──► DISCONNECTED → backoff+jitter → SIGNALING
ENCRYPTED ──DC 断──► DISCONNECTED → backoff+jitter → SIGNALING
任意 ──用户解绑──► IDLE（删 pubS）
```

Riverpod 状态：`connectionProvider(devId)` 暴露当前状态 + 流。

---

## 4. 数据流

### 4.1 下行（S → C，渲染）

```
DataChannel.onMessage(密文帧)
  → transport.decode(s2c_key, seq) → 明文 NDJSON
    → seq 防重放检查
  → jsonDecode → wireEvent
  → chat/事件渲染器（按 kind 分发：text/reasoning/tool/approval/...）
  → 同步落 session_store（缓存）
```

### 4.2 上行（C → S，命令）

```
用户输输入/点按钮
  → 包装 cmd NDJSON
  → transport.encode(c2s_key, seq++) → 密文帧
  → DataChannel.send(密文帧)
    → flutter_webrtc DTLS 自动加密 → 网络
```

### 4.3 启动自动重连

```
App 启动
  → 读 devices 表
  → 若有默认 S 且 autoConnect：进 SIGNALING
  → backoff + jitter 重试，直到 ENCRYPTED 或用户切桌面
```

---

## 5. 配置

| 项 | 来源 | 默认 |
|---|---|---|
| 信令地址 | **扫码时带**（二维码 `relay`），持久化到该桌面记录 | 无硬编码 |
| 兜底信令 | App 内置官方公开信令 | `wss://signal.linkpeer.app`（占位） |
| STUN | 信令 `signal.toml [stun]` 下发 | 跟信令 |
| 主题/字体 | 系统 | 系统 |

二维码带信令地址 → 用户自建 K 也能用，不锁官方。

---

## 6. 安全要点（C 端）

| 项 | 实现 |
|---|---|
| 设备私钥 | `flutter_secure_storage`（iOS Keychain / Android Keystore，硬件保护） |
| 扫码指纹校验 | 本地算 `SHA256(pubS)` 与二维码 `fp` 比对，不符拒绝（PROTOCOL §3.2） |
| 比较用 constant-time | 防 fingerprint 时序攻击 |
| 会话密钥 | 每连接临时 X25519 派生，存内存，断即弃 |
| 不存明文业务 | session_store 缓存的是 wireEvent 流（已是渲染数据），非桌面原始文件 |
| 通知零知识 | 系统通知只用模板文案（"请求审批"），不含业务明文 |
| 解绑 | 删 pubS + sessionToken + 本地缓存 |

---

## 7. 跨平台差异

| 项 | Android | iOS |
|---|---|---|
| 私钥存储 | Keystore | Keychain |
| 后台保活 | 前台服务（ongoing notification）维持 DataChannel | **MVP 仅前台**（系统挂起即暂停），后期 APNs 静默唤醒 |
| 通知通道 | 分级 channel（审批/完成/错误） | 系统通知权限 |
| WebRTC | flutter_webrtc（Google libwebrtc） | 同 |
| 分发 | APK 侧载 + Play Store | App Store（Mac 可用后） |
| 合规 | — | `ITSAppUsesNonExemptEncryption=NO` |

### 7.1 后台策略（iOS 限制）

iOS 后台网络 socket 会被系统挂起。MVP 策略：
- 切后台 → 暂停接收（DataChannel 保持但不主动读）。
- 回前台 → 拉增量（按 `last_event_seq` 补拉缺失事件）。
- 后台审批通知：MVP 不支持（需 APNs，后期）。Android 支持（前台服务）。

---

## 8. 本地数据模型（sqflite）

见 FEATURES §6.2。核心表：`devices`（已配对桌面）、`session_cache`（会话缓存）、`settings`。

缓存策略：每会话最近 200 条事件，总配额 100MB LRU。增量同步键 `last_event_seq`。

---

## 9. 测试要点

| 层 | 测试 |
|---|---|
| core/crypto | Ed25519/X25519/AES-GCM 往返；constant-time 比较 |
| core/transport | seq 倒退丢帧；tag 错丢帧；分片重组；分方向密钥反射失败 |
| core/connection | 状态机转换；backoff+jitter；ICE restart |
| core/pairing | 扫码指纹不符拒绝；码过期；exchange 失败重试 |
| features/chat | wireEvent 各 kind 渲染；流式光标；审批交互 |
| widget | 豆包样式 UI 快照测试 |
| 集成 | mock S（Go 起 local 服务），验证配对 + 连接 + 命令往返 |

---

## 10. 与其他端的一致性

| 依赖 | 一致性 |
|---|---|
| S 端 | 握手消息字段（eph/nc/cid/sid/ts/sig）、帧格式、密钥派生（HKDF info 串）、wireEvent JSON 结构——字节级一致 |
| K 端 | `/pair/exchange` 字段、WS 认证（dev+ts+sig）、sessionToken 用法一致 |
| 验证 | `protocol-compat` CI：Go/Dart 双向解析 JSON 样本，字段不一致 fail；`data/models/` 每个 class 注释指向 Go struct |

---

## 11. v1.1 优化补充（基于复审）

### 11.1 App 生命周期与连接协调

| 事件 | 动作 |
|---|---|
| 冷启动 | 读 devices 表；若有默认 S 且 `autoConnect` → 进 SIGNALING |
| 切后台（Android） | 前台服务维持 DataChannel，继续接收（通知栏常驻） |
| 切后台（iOS） | 暂停读 DataChannel（系统挂起 socket）；记录 `last_seq`；状态→BACKGROUND |
| 回前台（iOS） | 按 `last_seq` 发 `resync` 补拉增量（§11.2）；状态→ENCRYPTED |
| 被系统杀 | 重启走冷启动；sqflite 缓存仍在，UI 从缓存恢复 |
| 网络断 | `connectivity_plus` 检测 → DISCONNECTED → backoff+jitter |
| 网络切换（WiFi↔4G） | 立即 ICE restart（§11.4） |

### 11.2 增量同步协议（重连补拉）

重连进入 ENCRYPTED 后，C 对当前订阅的 tab 发：
```json
{"t":"resync","tab":"<id>","since_seq":<last_seq>}
```
S 端：从内存环形缓冲（每 tab 最近 200 条事件 + seq）查 `since_seq` 之后的事件，分片回投。

| S 响应 | 含义 |
|---|---|
| `{"type":"resync_delta","events":[...]}` | 增量，从 since_seq 之后 |
| `{"type":"resync_full"}` | S 不再持有该 seq（重启/超缓存），C 重新拉全量 |

S 端 mobilebridge 维护每 tab 的 `ringBuffer[seq]event.Event`（内存，不持久，重启清）。

### 11.3 错误 UX 状态机

| 错误 | UI |
|---|---|
| 桌面离线（unavailable） | 顶栏"桌面离线"+ 重试按钮 + 诊断入口 |
| 打洞失败（host+srflx 都失败） | 弹窗"无法直连"+ ICE 候选类型 + 建议（切同 WiFi / 开 UPnP / 切网络）+ 诊断 |
| 握手失败 | "连接异常，重试中"（自动 backoff，不打扰） |
| 权限拒绝（forbidden） | toast"桌面未授权此操作" |
| 版本不符（version_mismatch） | 弹窗"请更新 linkpeer"+ 升级指引 |
| 网络断 | 顶栏"网络不可用" |
| 帧解密失败（多次） | "连接异常，重连" |

### 11.4 网络变化检测与 ICE restart

`connectivity_plus` 包监听网络类型变化（WiFi/4G/none）：
- 变化 → 立即 ICE restart：C 侧重新创建 PeerConnection（新 ufrag/pwd）→ 重收集候选 → 重发 offer（经 K）→ S 应答。
- 不等 DataChannel 自然断（NAT 映射失效后才察觉），主动切换更快。

### 11.5 离线命令队列

用户离线时输入的 `submit`：
- 入队（内存 + sqflite 持久），重连后按序补发。
- 队列上限 10 条，超出提示"网络恢复后再操作"。
- **审批类命令不排队**（approval/answer 有时效，过期失效，丢弃）。
- 队列状态 UI：输入框上方"待发送 (N)"小条。

### 11.6 流式渲染性能

- text/reasoning delta 用 Riverpod `StreamProvider`，**节流 16ms**（60fps）合并渲染，避免每 delta 触发重建。
- 长对话用 `ListView.builder`（虚拟列表，只渲染可见项）。
- diff/工具卡懒加载（点开才渲染详情）。
- 代码块用 `flutter_highlight` 预渲染，避免每帧解析。
- 大消息（>100KB）截断 + "展开全文"。

目标：稳定 55+ fps（ENGINEERING §1.1）。

### 11.7 App 锁（P1，生物识别）

- 首次启用：生物识别（Face ID/Touch ID/指纹）绑定。
- 自动锁超时：1/5/30 分钟（可选）。
- 实现：`local_auth` 包 + `flutter` 生命周期（切后台超时即锁）。
- 锁屏 UI：模糊背景（隐私屏幕），防多任务切换偷窥。
- 与配对/密钥无关（只锁 App 入口，不影响底层连接）。

### 11.8 Deep Link（配对唤起）

`linkpeer://pair?...` 由系统相机/扫码 App 识别 → 唤起 linkpeer → 直接进配对流程。
- Android：`intent-filter`（AndroidManifest.xml）
- iOS：URL Scheme（Info.plist）+ Universal Link（可选，后期）
- 已打开 App 时：直接跳配对页；未打开：冷启动后路由到配对页。

### 11.9 崩溃恢复

- App 崩溃 → 重启读 sqflite：devices、session_cache、settings 完整恢复。
- 上次活动连接的 tab + scroll 位置记入 settings，重启恢复到原位置。
- 崩溃日志本地落盘（`flutter_crashlytics` 或自实现），用户可主动导出诊断包。
