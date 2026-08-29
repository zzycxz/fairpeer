// proposalStepFormat — 结构化提案步骤（NETDEV_SPEC_V2 §7.1）的纯展示逻辑：
// 类型标签、人审一行摘要、k8s manifest 一行引用。无 React/桥依赖，可直接
// 进 tsx 纯逻辑测试（src/__tests__/proposal-step-summary.test.ts）。

import type { NetDevProposalStep } from "../../lib/types";

export const STEP_TYPE_LABEL: Record<string, string> = {
  "": "CLI",
  cli: "CLI",
  "k8s-apply": "k8s apply",
  "sql-migration": "SQL 迁移",
  "file-upload": "文件上传",
  "cert-replace": "证书替换",
};

function firstLine(s?: string): string {
  return (s ?? "")
    .split("\n")
    .map((x) => x.trim())
    .find(Boolean) ?? "";
}

// stepSummary renders one step's change for the approve-confirm dialog — the
// human signs off on WHAT runs, type-aware.
export function stepSummary(s: NetDevProposalStep): string {
  switch (s.type) {
    case "k8s-apply":
      return `${s.device} [k8s-apply]: ${k8sRef(s.yaml)}`;
    case "sql-migration":
      return `${s.device} [sql-migration]: ${firstLine(s.up_sql)} …（down: ${firstLine(s.down_sql) || "⚠ 缺失"}）`;
    case "file-upload":
      return `${s.device} [file-upload]: ${s.local_path} → ${s.remote_path}`;
    case "cert-replace":
      return `${s.device} [cert-replace]: ${s.local_path} → ${s.remote_path} + reload ${s.reload_cmd}`;
	default:
		return `${s.device}: ${(s.commands ?? []).join("; ")}`;
  }
}

// k8sRef squeezes "Kind/name (apiVersion)" out of a manifest for one-line
// summaries; a parse failure degrades to a length hint.
export function k8sRef(yaml?: string): string {
  const kind = /^kind:\s*(\S+)/m.exec(yaml ?? "")?.[1];
  const name = /^\s+name:\s*(\S+)/m.exec(yaml ?? "")?.[1];
  const ver = /^apiVersion:\s*(\S+)/m.exec(yaml ?? "")?.[1];
  if (!kind) return `${(yaml ?? "").length} 字节 YAML`;
  return `${kind}/${name ?? "?"} (${ver ?? "?"})`;
}
