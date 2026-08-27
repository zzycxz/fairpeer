// loopPresets.ts — Loop Engineering built-in cases (docs/loop-engineering-spec.md
// §3). Each preset fills the LoopPanel form and can be freely edited before
// starting. Storage mirrors the preference-preset mechanism (built-in list +
// user copies), persisted by the panel.
import type { LoopConfig } from "./types";

export interface LoopPreset {
  id: string;
  icon: string; // lucide component name hint
  labelZh: string;
  labelEn: string;
  descZh: string;
  descEn: string;
  config: Omit<LoopConfig, "id">;
}

export const LOOP_PRESETS: LoopPreset[] = [
  {
    id: "preset-test-fix",
    icon: "flask",
    labelZh: "测试修复循环",
    labelEn: "Test-fix loop",
    descZh: "跑测试→修失败→重跑,直到全绿",
    descEn: "Run tests, fix failures, rerun until green",
    config: {
      name: "测试修复循环",
      goal: "修复当前测试套件中的失败用例。每轮挑一个失败原因修复,直到验收命令全绿。禁止修改测试来迁就实现,除非测试本身确有错误(需说明理由)。",
      sensorCommand: "",
      verifyCommand: "npm test",
      exploratory: false,
      autonomy: "L2",
      maxRounds: 15,
      maxTokens: 600_000,
      intervalSeconds: 0,
      commandAllowlist: [],
    },
  },
  {
    id: "preset-debt-sweep",
    icon: "broom",
    labelZh: "债务清扫",
    labelEn: "Debt sweep",
    descZh: "lint/类型警告逐个清零",
    descEn: "Sweep lint/type warnings to zero",
    config: {
      name: "债务清扫",
      goal: "持续清理代码库中的 lint 警告与类型问题。每轮从传感器输出中挑最严重的一项修复,保持行为不变。",
      sensorCommand: "npm run lint 2>&1 | tail -80",
      verifyCommand: "npm run lint",
      exploratory: true,
      autonomy: "L2",
      maxRounds: 30,
      maxTokens: 600_000,
      intervalSeconds: 0,
      commandAllowlist: [],
    },
  },
  {
    id: "preset-coverage",
    icon: "gauge",
    labelZh: "覆盖率爬坡",
    labelEn: "Coverage climb",
    descZh: "每轮给一个未覆盖分支补测试",
    descEn: "Cover one uncovered branch per round",
    config: {
      name: "覆盖率爬坡",
      goal: "提升测试覆盖率。每轮挑选一个未覆盖的分支/函数,编写针对性单元测试;只许加测试,不许改实现(发现实现缺陷时记录并跳过)。",
      sensorCommand: "",
      verifyCommand: "npm test",
      exploratory: true,
      autonomy: "L2",
      maxRounds: 20,
      maxTokens: 600_000,
      intervalSeconds: 0,
      commandAllowlist: [],
    },
  },
  {
    id: "preset-deps",
    icon: "package",
    labelZh: "依赖升级循环",
    labelEn: "Dependency upgrades",
    descZh: "逐个升级→跑测试→修 break",
    descEn: "Upgrade one dep at a time, fix breaks",
    config: {
      name: "依赖升级循环",
      goal: "逐个升级过期依赖。每轮选择一个可安全升级的依赖执行升级并修复破坏;major 版本升级需格外保守,无法安全处理时记录并跳过。",
      sensorCommand: "npm outdated",
      verifyCommand: "npm test",
      exploratory: true,
      autonomy: "L2",
      maxRounds: 15,
      maxTokens: 600_000,
      intervalSeconds: 0,
      commandAllowlist: [],
    },
  },
  {
    id: "preset-docs",
    icon: "book",
    labelZh: "文档追补",
    labelEn: "Docs catch-up",
    descZh: "给无注释的公共 API 补文档",
    descEn: "Document uncommented public APIs",
    config: {
      name: "文档追补",
      goal: "为缺少文档注释的公共导出补齐注释。只添加注释与文档,不修改任何逻辑。",
      sensorCommand: "",
      verifyCommand: "npm run build",
      exploratory: false,
      autonomy: "L2",
      maxRounds: 15,
      maxTokens: 600_000,
      intervalSeconds: 0,
      commandAllowlist: [],
    },
  },
  {
    id: "preset-inspect",
    icon: "radar",
    labelZh: "只读巡检",
    labelEn: "Read-only inspection",
    descZh: "零写入,只出问题报告(L1)",
    descEn: "Zero writes, report only (L1)",
    config: {
      name: "只读巡检",
      goal: "全面巡检当前仓库:测试状态、lint、类型、可疑模式、技术债热点。产出一份问题报告与优先级建议,不做任何修改。",
      sensorCommand: "",
      verifyCommand: "",
      exploratory: false,
      autonomy: "L1",
      maxRounds: 3,
      maxTokens: 100_000,
      intervalSeconds: 0,
      commandAllowlist: [],
    },
  },
];

export function presetToConfig(preset: LoopPreset): LoopConfig {
  return { ...preset.config, id: `${preset.id}-${Date.now()}` };
}
