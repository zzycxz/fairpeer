# 动画与过渡参数（`svg_to_pptx.py` 进阶）

> 默认不生成动画。仅在用户明确要求动画/过渡/旁白时参考本文件。

## 过渡效果（`-t/--transition`）

| 取值 | 效果 |
|------|------|
| `fade` | 淡入淡出（默认） |
| `push` / `wipe` / `split` / `strips` / `cover` | 对应切换效果 |
| `none` | 无过渡 |

配套：`--transition-duration 0.5`（秒）

## 元素入场动画（`-a/--animation`，默认 none）

需 `pptx_animations` 模块（缺失时优雅降级，不写入动画 XML）。

| 取值 | 效果 |
|------|------|
| `none` | 无（默认） |
| `fade` / `fly` / `zoom` / `appear` | 单一效果 |
| `auto` | 按 SVG group id 自动映射 |
| `mixed` / `random` | 轮换效果池 |

动画参数：`--animation-duration`、`--animation-trigger`（on-click/with-previous/after-previous）、`--animation-stagger`、`--animation-config <path>`

## 自动翻页

`--auto-advance 3.0`（秒，默认手动翻页）

## 旁白/录音

- `--recorded-narration <dir>` — 完整录制模式（每页一个音频文件，自动设翻页时间）
- `--narration-audio-dir <dir>` — 低级音频嵌入
- `--use-narration-timings` — 按音频时长设翻页
- `--narration-padding 0.5` — 旁白结束后追加秒数

## 示例

```bash
# 带过渡和级联动画
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir> \
  -t push --transition-duration 1.0 \
  -a auto --animation-trigger after-previous --animation-stagger 0.4

# 录制式旁白
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir> --recorded-narration audio
```

## 其它进阶参数

| 参数 | 作用 |
|------|------|
| `-o/--output <path>` | 显式输出路径 |
| `-s/--source <name>` | SVG 源目录 |
| `-f/--format <name>` | 画布格式 |
| `--only native\|legacy` | 只生成一种版本 |
| `--no-notes` | 关闭备注嵌入 |
| `--conversion-trace` | 诊断 JSON |
| `--cache-dir` / `--no-cache` | SVG→PNG 缓存控制 |
| `--workers <n>` | 并行 worker 数 |
