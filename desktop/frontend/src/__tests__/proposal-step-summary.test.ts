// Pure logic tests for the structured-proposal step summaries (§7.1) — the
// text a human signs off on must be type-aware and never silently empty.
//
// Run: tsx src/__tests__/proposal-step-summary.test.ts

import { stepSummary, k8sRef, STEP_TYPE_LABEL } from "../components/netdev/proposalStepFormat";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

console.log("\nstepSummary — cli (back-compat: no type field)");
eq(
  stepSummary({ device: "sw1", commands: ["vlan 100", "description IoT"], applied: false }),
  "sw1: vlan 100; description IoT",
  "legacy cli step renders commands",
);

console.log("\nstepSummary — structured types");
eq(
  stepSummary({ device: "k8s-prod", type: "k8s-apply", commands: [], applied: false, yaml: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n" }),
  "k8s-prod [k8s-apply]: Deployment/web (apps/v1)",
  "k8s-apply renders the manifest ref",
);
eq(
  stepSummary({ device: "mysql-prod", type: "sql-migration", commands: [], applied: false, up_sql: "CREATE TABLE t (id INT);\n", down_sql: "DROP TABLE t;" }),
  "mysql-prod [sql-migration]: CREATE TABLE t (id INT); …（down: DROP TABLE t;）",
  "sql-migration shows first up line + down line",
);
eq(
  stepSummary({ device: "mysql-prod", type: "sql-migration", commands: [], applied: false, up_sql: "CREATE TABLE t (id INT);" }),
  "mysql-prod [sql-migration]: CREATE TABLE t (id INT); …（down: ⚠ 缺失）",
  "missing down script is VISIBLE (§7.1 not submittable)",
);
eq(
  stepSummary({ device: "host1", type: "file-upload", commands: [], applied: false, local_path: "/tmp/app.conf", remote_path: "/etc/app/app.conf" }),
  "host1 [file-upload]: /tmp/app.conf → /etc/app/app.conf",
  "file-upload renders local → remote",
);
eq(
  stepSummary({ device: "host1", type: "cert-replace", commands: [], applied: false, local_path: "/tmp/c.pem", remote_path: "/etc/tls/c.pem", reload_cmd: "systemctl reload nginx" }),
  "host1 [cert-replace]: /tmp/c.pem → /etc/tls/c.pem + reload systemctl reload nginx",
  "cert-replace includes the reload command",
);

console.log("\nk8sRef — degradation");
eq(k8sRef(undefined), "0 字节 YAML", "empty manifest degrades to a byte hint");
eq(k8sRef("apiVersion: v1\n"), "15 字节 YAML", "kind-less manifest degrades");

console.log("\nlabels");
eq(STEP_TYPE_LABEL["k8s-apply"], "k8s apply", "k8s-apply label");
eq(STEP_TYPE_LABEL["cli"], "CLI", "cli label");

if (failed > 0) {
  process.stdout.write(`\n${failed} FAILED, ${passed} passed\n`);
  process.exit(1);
}
process.stdout.write(`\nall ${passed} passed\n`);
