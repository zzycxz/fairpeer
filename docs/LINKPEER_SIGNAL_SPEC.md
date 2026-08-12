# 云端信令（K）实现规范

> 状态：v1
> 日期：2026-08-11
> 范围：`linkpeer-signal` 服务的**实现层**规范——模块结构、端点、路由、限速、监控、部署、容量。
> 上游协议：[`LINKPEER_PROTOCOL.md`](./LINKPEER_PROTOCOL.md)。本规范是其 K 端实现的细化。

---

## 1. 职责边界

**K 做**：
- 配对撮合（pairId / code / 公钥交换）。
- 会话寻址（peers 在线表）。
- SDP/ICE 候选路由转发（按 `to` 字段 O(1) 精确投递）。
- 限速、监控、健康检查。

**K 不做（硬边界）**：
- ❌ 不验业务消息签名（无密钥，两端自验）。
- ❌ 不存业务内容、SDP 明文、密钥。
- ❌ 不持久化（内存态，重启清空）。
- ❌ 不做 TURN 中继（STUN only）。

---

## 2. 模块架构

```
cmd/linkpeer-signal/
├── main.go              # 启动、配置加载、优雅关闭、信号处理
├── server.go            # HTTP + WS 服务、路由
├── pair.go              # 配对状态机: pairId/code/TTL/失败锁/唯一性
├── session.go           # WS 会话: peers 表、消息路由、僵尸清理
├── ratelimit.go         # 按 devId/IP 限速（token bucket）
├── metrics.go           # Prometheus 指标暴露 /metrics
├── audit.go             # 元数据日志（脱敏，7 天轮转）
├── config.go            # signal.toml 加载
└── internal/
    └── proto/           # 复用 internal/mobilebridge/proto/types.go（或独立镜像）
```

---

## 3. 端点接口

| 方法 路径 | 用途 | 认证 | 限速 |
|---|---|---|---|
| `POST /pair/register` | S 注册配对 | 无 | devId 5/h、IP 200/h |
| `POST /pair/exchange` | C 换公钥 | pairId+code | 同上 |
| `GET /session/ws` | 双向信令 WS | dev+ts+sig(Ed25519) | WS 50 msg/s/devId |
| `GET /healthz` | 存活探针 | 无 | 无 |
| `GET /metrics` | Prometheus | 无（内网或 CF 防护） | 无 |

### 3.1 `/pair/register`（S → K）

```jsonc
// req
{ "code":"7KQ9XM", "pubS":"...", "fpS":"ABCD-EFGH-JKMN", "devS":"..." }
// resp 200
{ "pairId":"...", "expiresAt":1723352061 }
// resp 409 (code 冲突)
{ "error":"code_conflict" }   // S 端重新生成 code 重试
```

服务端校验：`fpS == base32(SHA256(pubS)[:8])` 自洽；同 devS 60s 内 ≤3 active pair；code 全局唯一（冲突 409）；全局 active pair ≤50000（超拒 503 `capacity_full`）。

### 3.2 `/pair/exchange`（C → K）

```jsonc
// req
{ "pairId":"...", "code":"7KQ9XM", "pubC":"...", "fpC":"...", "devC":"..." }
// resp 200
{ "pubS":"...", "fpS":"...", "sessionToken":"...", "tokenTtl":2592000 }
// resp 4xx
{ "error":"pair_not_found" | "code_mismatch" | "pair_locked" | "pair_expired" }
```

服务端校验：pairId 存在未过期；code 匹配；failedCount < 5。任一不符 failedCount++，到 5 锁。exchange 成功后**不立即删除 pairId**（等 S 端双向确认，或 60s TTL 自然过期）。

### 3.3 `/session/ws`（双向 WS）

URL：`GET /session/ws?dev=<id>&ts=<unix_s>&sig=<base64 Ed25519_Sign(L, dev||ts)>`

认证：K 用该 dev 配对时记录的 pub 验签（`sessionToken` 也可作为辅助），ts 在 ±60s 内。失败关 4401/4402/4403。

WS 消息（JSON，透传不验签）：
```jsonc
{ "type":"offer"|"answer"|"ice"|"bye"|"kp"|"unavailable",
  "from":"...", "to":"...", "ts":..., "sig":"...", "sdp":"...", "cand":"..." }
```

---

## 4. 内部数据结构（内存态）

```go
type Server struct {
    mu sync.RWMutex
    pairs map[string]*Pair        // pairId → 配对会话
    peers map[string]*PeerConn    // devId → WS 连接（在线表）
    paired map[string]map[string]bool  // devId → 已配对对端集合（可选软校验）
    tokens map[string]*Token      // sessionToken → devId+exp
    rl *RateLimiter               // 限速器
    metrics *Metrics
}

type Pair struct {
    Code, DevS, PubS, FpS string
    PubC, FpC, DevC string   // exchange 后填
    FailedCount int
    CreatedAt, ExpiresAt int64
    Confirmed bool           // S 端双向确认后置 true
}

type PeerConn struct {
    DevId string
    Ws *websocket.Conn
    WriteMu sync.Mutex       // 串行写
    LastSeen atomic.Int64
}
```

---

## 5. 路由逻辑（核心，O(1)）

```go
func (s *Server) route(from *PeerConn, raw []byte) {
    var m struct{ To string `json:"to"` }
    json.Unmarshal(raw, &m)
    s.mu.RLock(); to := s.peers[m.To]; s.mu.RUnlock()
    if to == nil {
        s.send(from, `{"type":"unavailable","reason":"peer_offline"}`)
        return
    }
    to.WriteMu.Lock()
    to.Ws.WriteMessage(TextMessage, raw)
    to.WriteMu.Unlock()
    s.metrics.WsMsgs.WithLabelValues(m.Type).Inc()
}
```

非广播，精确投递。5000 规模单次 <10μs。

---

## 6. 限速策略（5000 规模，防误伤共享 NAT）

| 维度 | 上限 | 实现 |
|---|---|---|
| devId `/pair/*` | 5/小时 | token bucket per devId |
| IP `/pair/*` | 200/小时 | token bucket per IP |
| pairId 失败 | 5 次锁 | pairs[pairId].FailedCount |
| WS msg | 50/s/devId | 滑动窗口 |
| 全局 active pair | 50000 | 硬上限，超 503 |

---

## 7. 监控指标（Prometheus `/metrics`）

见 PROTOCOL §18。核心：`linkpeer_peers_online`、`linkpeer_pairs_active`、`linkpeer_ws_connections`、`linkpeer_signal_errors_total{code}`、`linkpeer_ratelimit_hits_total{dim}`。

5000 规模必须有 Grafana 仪表盘 + 告警（ENGINEERING §3.2、§10.11）。

---

## 8. 安全要点（K 端）

| 项 | 实现 |
|---|---|
| 无状态 | 重启清空，配对持久性在两端本地（不依赖 K） |
| 配对码唯一性 | register 时 code 冲突返回 409，S 重试 |
| 配对码 TTL | 60s 后台清扫协程主动清 |
| WS 签名认证 | dev+ts+sig(Ed25519)，ts ±60s |
| 全局内存上限 | active pair 50000、peers 表上限（防失控） |
| 日志脱敏 | 只记 devId/时间/错误码；不记 code 全文/SDP/IP/业务 |
| 日志保留 | 7 天轮转 |
| 不验业务签名 | K 无密钥，业务签名两端自验（PROTOCOL §4.2） |
| DDoS 防护 | Cloudflare 前置（支持 WS），CF Rate Limit + K 限速双保险 |
| 拒绝服务是上限 | K 被攻破只能 DoS，不能泄露（PROTOCOL §11.1） |

---

## 9. 部署（docker-compose，5000 规模）

见 PROTOCOL §10 完整配置。关键：
- **VPS 规格**：4C8G 起步（5000 并发 WS）。
- **fd 调优**：`LimitNOFILE=65535`（signal + Caddy 都设）。
- **组件**：signal（Go）+ coturn（STUN only）+ Caddy（TLS 反代）。
- **冗余**：standby 实例 + DNS 切换（TTL 60s），RTO ~90s。
- **DDoS**：Cloudflare 前置。

### 9.1 coturn → pion/turn 调优（后期）

M0 用 coturn（成熟稳妥）。后期可评估 **pion/turn**（纯 Go）嵌入信令服务同进程：
- 优点：省一个容器、统一部署、统一监控、二进制合一。
- 风险：pion/turn 稳定性需评估（生产用户量少于 coturn）。
- 切换时机：M3 后，看 M0-M3 的 coturn 运维数据决定。

---

## 10. 容量（5000 规模）

详见 ENGINEERING §10。要点：
- 7500 并发 WS（5000 S 长连 + 2500 C 峰值）。
- 内存 ~400MB、CPU 稳态 <10%、流量 <100GB/月。
- 演进：5000 单实例 → 2万垂直扩 → 2万以上 Redis pub/sub + L4 LB。

---

## 11. 测试要点

| 层 | 测试 |
|---|---|
| pair.go | code 冲突 409；TTL 过期；失败 5 次锁；全局上限 503 |
| session.go | 路由 O(1)；offline 回 unavailable；僵尸清理；WS 认证 4401/4402/4403 |
| ratelimit.go | devId/IP 限速生效；超限 429 |
| metrics.go | 指标正确暴露 |
| 集成 | mock S + mock C，验证配对 + 路由完整流程 |
| 压测 | 7500 并发 WS + 400 msg/s 稳态，验证 CPU/内存 |
| 安全 | 配对码爆破（脚本批量，验证锁）；WS 伪造签名（验证拒） |

---

## 12. 与其他端的一致性

| 依赖 | 一致性 |
|---|---|
| S 端 | `/pair/register` 字段、WS 认证格式、消息路由（to 字段）一致——`proto/types.go` 单一事实源 |
| C 端 | `/pair/exchange` 字段、sessionToken 用法一致 |
| 验证 | K 不依赖 S/C 的协议细节（只透传 JSON），但消息字段必须与 `proto/types.go` 一致；CI 协议兼容测试覆盖 |

---

## 13. v1.1 优化补充（基于复审）

### 13.1 WS 认证改为完全无状态（重要优化）

原设计 WS 认证依赖 `sessionToken`（K 内存），K 重启 token 全失效。复审发现可优化为**完全无状态认证**，去掉 token：

```
GET /session/ws?dev=<devId>&ts=<unix_s>&pub=<base64公钥>&sig=<base64 Sign(L, devId||ts)>

K 验证（零状态）:
  1. devId == base32(SHA256(pub)[:10])?    // 身份自洽（devId 由公钥派生）
  2. Ed25519.Verify(pub, devId||ts, sig)?  // 持有对应私钥
  3. |now - ts| < 60s?                      // 防重放窗口
  全过 → 允许 WS，登记 peers[devId] = conn
```

**好处**：
- K 完全无状态，重启零影响（任何已配对设备重连即过，无需 token）。
- 配对合法性不靠 K，由 S 端握手拒绝（§5.4）兜底——K 只验"你持有 devId 对应的私钥"，不验"你配对过谁"。
- 去掉 `/pair/exchange` 响应里的 `sessionToken` 字段；去掉 `tokens` 内存表。

**同步更新**：PROTOCOL §4.1、§3.2（exchange 响应去掉 token）、C 端 spec（不再存/用 token）。

### 13.2 优雅关闭流程

收到 SIGTERM：
1. `listener.Close()`（停止接受新连接）
2. 给所有活动 WS 广播 `{"type":"server_shutdown","retry_after":5}`
3. 等 5s 让客户端 graceful close
4. 强制关闭剩余 WS
5. flush 日志、退出

客户端收到 `server_shutdown` → 立即 backoff+jitter 重连（不等 DNS）。配合 §10.7 连接风暴防护。

### 13.3 结构化日志格式

JSON 单行，固定字段：
```json
{"ts":"2026-08-11T14:23:01Z","lvl":"info","evt":"ws_connect","dev":"K7QM...9XC","ip":"1.2.3.4","msg":"...","err":""}
```

| 字段 | 说明 |
|---|---|
| ts, lvl, evt, msg, err | 标准 |
| dev | devId 截断（前 4 + 后 3） |
| ip | 仅限速/审计用，可配置脱敏 |
| **绝不** | code 全文、pub、sdp、cand、业务内容 |

### 13.4 Prometheus cardinality 控制

label 只用低基数枚举：
- ✅ `type={offer,answer,ice,bye,kp,unavailable}`、`code={4401,4402,4403,4404,...}`、`result={success,fail,expire,locked}`
- ❌ **绝不用 devId / IP 做 label**（高基数撑爆 Prometheus 内存）

devId/IP 维度的统计走日志，不进指标。

### 13.5 反向代理 IP 透传

Caddy 转发设 `X-Forwarded-For`。K 的 `ratelimit.go` 用 `realIP(r)` helper 提取（优先 XFF，回退 `RemoteAddr`），保证限速看到真实客户端 IP 而非 Caddy 内网 IP。

Caddyfile 已默认正确转发 XFF（reverse_proxy 自动加）。K 信任链：仅信任来自 Caddy 的 XFF（通过检查 peer 是 `127.0.0.1` 或 docker 内网）。

### 13.6 健康检查细节

`GET /healthz` 检查项：
- 进程存活（goroutine 不死锁，用 `runtime.NumGoroutine` 上限告警）
- 内存不超阈值（`runtime.MemStats` 上限，如 2G）
- 返回 `{"ok":true,"online":N,"uptime":T}`

5000 规模下 `/healthz` 是 Cloudflare / standby 探活的关键。

### 13.7 配置热更新

`signal.toml` 改动**需重启**（不热加载）。无状态服务重启快（<5s + 客户端重连），不值得引入热更新复杂度。改配置走：编辑文件 → `docker-compose restart signal`。
