# 推理指标面：K3 映射表与告警阈值建议（批④ / F3+F14+K3）

- 日期：2026-09-14
- 载体：`internal/netdev/infermetrics.go`（F14 抓取通道 + K3 映射）、`GpuBoard.Services`（服务层区）
- 配套：值班手册簇 2（显存）/ SCENARIO_SPEC S4-2（告警联动）

## 一、通道语义（F14，端点登记制）

1. **登记**：设备配置 `metrics_ports = [8000]`（上限 4 端口/主机）+ 可选 `metrics_path`（默认 `/metrics`）。**登记即授权**——未登记的地址永不抓取。
2. **抓取**：健康轮询每轮（PollHealthOnce）在工作站直连 `GET http://<address>:<port><path>`——不走共享代理，10s 超时，best-effort（失败只留审计行与健康判定无关）。每端点每轮**一条**审计行（`GET http://…`，读类，降噪口径与 GPU 轮询一致）。
3. **解析**：Prometheus 文本 exposition 公共子集（`name{labels} value [ts]`，`#` 注释跳过，NaN/Inf 跳过，标签支持三种标准转义）。解析器与引擎无关——vLLM/SGLang/TGI/网关通用；**映射表按引擎前缀择行**（现收 `vllm:*`，新引擎加映射行即可，解析器不动）。
4. **落库**：映射产物写 series（`infer.*`，标签 `svc=<端口>`、`model=<model_name>`），复用既有 14 天滚动清理与 F11 分片。

## 二、K3 映射表（vllm:* → infer.*）

| vLLM 原始指标（Prometheus） | 类型 | series（infer.*） | 语义 | 建议告警阈值起步* |
|---|---|---|---|---|
| `vllm:gpu_cache_usage_perc` | gauge | `infer.kv_usage`（×100 百分数） | KV cache 利用率——扩副本/降 max-model-len 的第一信号 | `>= 90` warning，`>= 97` critical（for_rounds 3） |
| `vllm:num_requests_running` | gauge | `infer.running` | GPU 上并发执行数 | 容量视角参考值，一般不告警 |
| `vllm:num_requests_waiting` | gauge | `infer.queued` | 排队深度——过载最直接信号 | `>= 32` warning（for_rounds 3） |
| `vllm:num_preemptions_total` | counter | `infer.preemptions_rate`（次/分，轮内增量） | 抢占=KV 不足强逐请求——体验劣化根因 | `>= 1` warning，`>= 10` critical |
| `vllm:generation_tokens_total` | counter | `infer.tokens_rate`（tok/s，轮内增量） | 集群吞吐（单实例口径） | 容量基线参考；骤降对照 `e2e_ms` |
| `vllm:time_to_first_token_seconds_sum/_count` | histogram | `infer.ttft_ms`（轮内增量平均） | 首 token 延迟——交互体验核心 | `>= 2000` warning（交互型业务自行收紧） |
| `vllm:e2e_request_latency_seconds_sum/_count` | histogram | `infer.e2e_ms`（轮内增量平均） | 端到端延迟 | `>= 30000` warning（按业务 SLO） |

\* 起步值是**对话起点不是标准**——按业务 SLO 与压测基线调；`for_rounds` 防抖一律建议 ≥3（单轮毛刺不立案，语义同割接门 SustainSec）。

**派生语义纪律**：counter/histogram 不落累计值——累计值对值班没有可设阈的意义（单调递增的"预分配量"只会永远超阈值）。series 落的是轮内增量派生值；导出器重启（计数器回绕）当轮不产速率点，基线就地重置；进程重启首轮只有 gauge 点（缺历史基线，诚实缺采不造 0）。

## 三、消费面

1. **告警规则**（`[[netdev.alert_rules]]`）：`metric = "infer.kv_usage"` 等 7 项进枚举（config 校验硬失败兜底）；跨服务**取最大**（最忙实例触发）；`for_rounds` 全兼容。**冻结闸**：未登记 `metrics_ports` 的主机永不参与 infer.* 规则——与 GPU 规则的采样闸同哲学（"我方没数据"≠"条件满足"）。
2. **智算大屏服务层区**（GpuBoard.Services）：一行 = (主机, 端口, 模型)；gauge 列取最新点，延迟/吞吐列取 15 分钟窗均值；KV 占用 ≥80% 琥珀 / ≥90% 红；>5 分钟未更新给陈旧提示行。
3. **值班排查路径**：`infer.kv_usage` 高 → 值班手册簇 2（降 max-model-len / 降 gpu-mem-util / 扩 TP / 量化）或 `ops-scale-dp-replicas` 模板扩副本；`infer.preemptions_rate` 持续 >0 → 同簇 2 + 检查 `swap_space`。

## 四、扩展（新引擎/新组件）

- **SGLang / TGI / NIM**：在 `reduceSamples` 加映射行（解析器零改动）；命名继续 `infer.*` 语义对齐（SGLang 无逐项同名指标——`token_usage` ≈ kv_usage 等，映射时注明口径差异）。
- **网关层聚合**（LiteLLM）：登记其 `/metrics` 端点即得全池视角；与实例层同名 infer.* 指标并存时以 `svc` 标签区分。
- **F15 验收联动**：七步验收的压测判定线（busbw/TTFT 基线）落档后，验收模板的门命令可直接比对 series 历史。
