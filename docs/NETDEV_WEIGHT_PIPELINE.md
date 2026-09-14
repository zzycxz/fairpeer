# 权重三段式：登记-校验-分发落地件（F2，MODEL_DEPLOY_SPEC §3.2）

- 日期：2026-09-14（0.2.5 批③ F2 收尾）
- 原则：模型权重几十~几百 GB **不经运维台搬运**——SSH 通道既不稳也不该承载。
  运维台的角色是【登记】（runbook/提案描述里锁定路径与 manifest）+【校验】（只读
  步 sha256sum 对账）+【分发提供脚本模板】（本文件，客户通道执行）。

## 一、三段分工

| 段 | 谁做 | 运维台做什么 |
|---|---|---|
| 分发 | 客户通道（hf download / ModelScope / 内网 HTTP / NFS / 摆渡） | 提供本文件§二的脚本模板（file-upload 上传 ≤200MB + 提案 cli 执行；断点续传在脚本内） |
| 校验 | 运维台只读步 | `sha256sum config.json model.safetensors.index.json`（内置模板已含）+ 全量对账按本脚本 `--verify` 产物 |
| 登记 | 运维台 | 权重目录路径 + manifest 哈希写入部署 runbook 描述；`--model` 指向本地路径 + `HF_HUB_OFFLINE=1` |

## 二、分发脚本模板（hf 权重 + 断点续传 + manifest 产物）

```bash
#!/usr/bin/env bash
# weight-fetch.sh — 权重分发脚本模板（F2）。file-upload 上传本脚本（≤200MB
# 限制内），提案 cli 步执行：`bash weight-fetch.sh <repo_id> <dest_dir>`。
# 断点续传：hf download 原生支持（已下载分片跳过）；中断后重跑同一命令即续。
set -euo pipefail
REPO="$1"; DEST="${2:-/data/models/$(basename "$REPO")}"

mkdir -p "$DEST"
# 下载（断点续传原生）；HF_ENDPOINT 可指向镜像（如 https://hf-mirror.com）
HF_HUB_ENABLE_HF_TRANSFER=1 hf download "$REPO" \
  --local-dir "$DEST" --local-dir-use-symlinks False

# manifest 产物：全量 sha256 清单（运维台校验段/验收段的比对基准）
cd "$DEST"
find . -type f \( -name '*.safetensors' -o -name '*.json' -o -name '*.txt' \) \
  -print0 | sort -z | xargs -0 sha256sum > manifest.sha256
echo "== manifest ==" && cat manifest.sha256
```

要点：
1. **断点续传在脚本内**（hf download 分片级续传），运维台不管理传输状态。
2. **manifest.sha256 是校验段的接口**：`--verify` 对账即
   `sha256sum -c manifest.sha256`（读表内形态，可作提案/割接只读步）。
3. ModelScope 通道把 `hf download` 换成 `modelscope download --repo_id` 同构。

## 三、与内置模板/值班手册的接线

- `RBB-deploy-vllm-standalone` / `RBB-deploy-vllm-ascend` 的「权重对账（F2 校验段）」
  步核对 config.json + index 清单；全量对账用本脚本产出的 manifest.sha256
  （`sha256sum -c manifest.sha256`）。
- 哈希不一致 → **停用该副本再重下**（值班手册簇 4）；不带病上线。
- 磁盘写满是常见伴生成因（簇 4）：分发前 `df -h <dest>` 读步内置在模板前置。
