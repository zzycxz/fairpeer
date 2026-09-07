// Builtin preference presets — mirrors memory.defaultPresets on the Go side
// (internal/memory/presets.go), one list per profile mode. Two frontend
// consumers share them: the browser-dev bridge mock (which picks by the active
// mock tab's profile) and the preset panel's "restore built-ins" action (which
// re-adds any deleted builtin by id). Keep both sides in sync; ids must match
// exactly. Note the backend seeds these with NOTHING active — a fresh user's
// prompt stays untouched until they pick one.

import type { ProfilePreset } from "./types";

export type PresetMode = "cowork" | "dev" | "netdev";

export const BUILTIN_COWORK_PRESETS: ProfilePreset[] = [
  {
    id: "reduce-ai",
    name: "减少AI味",
    content:
      "行文自然口语化，像人写的。禁用“首先/其次/最后/总之”式模板结构与空洞排比；少用“赋能、抓手、闭环”这类词；句长要有变化，直接给结论，不堆砌形容词，少用感叹号。",
    builtin: true,
  },
  {
    id: "strict-excel",
    name: "严格Excel匹配",
    content:
      "处理 Excel 时严格忠于原表：只改动明确要求的单元格，不擅自增删行列、不改格式与公式；输出中引用的数值、表头必须与原表逐字一致；拿不准就先问，不要猜。",
    builtin: true,
  },
  {
    id: "concise-summary",
    name: "少描述只给总结",
    content:
      "回复尽量短：直接给结果和关键结论，省略过程描述与步骤解释；优先用列表/表格呈现，能一句话说清的不展开。",
    builtin: true,
  },
];

export const BUILTIN_DEV_PRESETS: ProfilePreset[] = [
  {
    id: "minimal-diff",
    name: "最小改动",
    content:
      "只改任务要求的代码，不顺手重构、不改无关的格式与命名；改动范围之外的代码保持原样；能用小改动解决就不用大方案。",
    builtin: true,
  },
  {
    id: "match-style",
    name: "贴合现有风格",
    content:
      "新代码向周围代码看齐：命名、注释密度、错误处理、导入分组都模仿同文件/同包的既有写法，不引入新依赖和新风格，除非我明确要求。",
    builtin: true,
  },
  {
    id: "universal-code",
    name: "普遍适用",
    content:
      "写的代码要普遍适用，不针对当前一个用例写死：优先标准库和惯用写法，不依赖特定机器或环境；不硬编码绝对路径、密钥、账号；公共逻辑抽成可复用函数；处理常见边界（空输入、异常路径、超大输入）；注意跨平台差异（路径分隔符、编码、大小写）。",
    builtin: true,
  },
  {
    id: "explain-more",
    name: "多解释",
    content:
      "每次改动都说明：改了什么、为什么这么改、有什么影响；关键路径给出简要讲解，帮我理解这套代码库，不要只给结论。",
    builtin: true,
  },
];

// builtinPresetsFor returns the factory list for a mode, defaulting to dev.
export const BUILTIN_NETDEV_PRESETS: ProfilePreset[] = [
  {
    id: "evidence-first",
    name: "证据先行",
    content:
      "每个结论都附证据：引用设备名、时间与具体数值（命令输出/告警条目/日志行）；没有证据就明确说“未验证”，不要推断；给处置建议时区分“已确认”与“待核实”。",
    builtin: true,
  },
  {
    id: "careful-readonly",
    name: "谨慎只读",
    content:
      "默认只做只读诊断；任何写操作先说明影响面与回滚方案再等我确认；不确定的命令先解释它的作用和风险；涉及危险命令（重启/删除类）必须单独点名提醒。",
    builtin: true,
  },
  {
    id: "triage-with-score",
    name: "研判带评分",
    content:
      "告警研判按权威评分表输出：失陷确认=40、横向移动=25、暴露critical资产=20、可利用性=15（命中求和），并给分级（≥70 critical/40-69 warning/<40 info）与处置优先级；研判结论附命中信号，证据不足的信号不计分。",
    builtin: true,
  },
  {
    id: "concise-ops-report",
    name: "简洁汇报",
    content:
      "汇报直接给结论与影响面，按“结论→证据→建议动作”三段组织；告警类给分级和处置优先级；不堆过程描述，多设备状态用表格。",
    builtin: true,
  },
];

export function builtinPresetsFor(mode: PresetMode): ProfilePreset[] {
  if (mode === "cowork") return BUILTIN_COWORK_PRESETS;
  if (mode === "netdev") return BUILTIN_NETDEV_PRESETS;
  return BUILTIN_DEV_PRESETS;
}
