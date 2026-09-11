# 品牌预设（brand-presets）

内置品牌色板目录。任务文本命中品牌关键词时，Step 0 用
`preflight.py <project_name> --preset <id>` 机械套用——跳过颜色识别，
配色/纪律规则/模板种子全部来自预设文件，不依赖 VLM 或提取启发式。

## 当前预设

| id | 品牌 | 主色 | 强调色 | 模板种子 |
|----|------|------|--------|----------|
| china-mobile | 中国移动 | #1084CD 主蓝 | #FF7F00 活力橙（≤5% 点状） | templates/default.pptx |

## 触发词

- china-mobile: 中国移动 / 中移 / 移动公司 / CMCC

## 文件结构

```json
{
  "id": "china-mobile",
  "keywords": ["中国移动", "中移", "移动公司", "CMCC"],
  "colors": { "brand": "...", "accent": "...", "line": "...", "...": "..." },
  "rules": { "color_usage": "...", "content_density": { } },
  "default_prompt_style": "……",
  "template_seed": "default.pptx"
}
```

- `colors` 整组覆盖 template_config.json 的 colors（preset 最高优先）
- `rules` 做浅合并（同名键覆盖）
- `template_seed`：`~/.fairpeer/ppt-template.pptx` 不存在时，从技能
  `templates/<seed>` 复制一份（存在用户自选模板则不动）

## 与"严禁凭主题名推断品牌色"的关系

SKILL.md 的禁令仍然成立：**查本目录的预设文件不是凭空推断**——预设是
已核实的品牌色值；预设目录里没有的品牌不得自行编色。
