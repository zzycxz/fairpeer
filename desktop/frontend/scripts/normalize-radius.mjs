// normalize-radius.mjs — one-shot migration of literal `border-radius: Npx`
// declarations to the --radius-* design tokens (ui-redesign-spec §2.2 / §4-A1).
//
//   node scripts/normalize-radius.mjs            # dry-run: report only
//   node scripts/normalize-radius.mjs --write    # apply rewrites
//
// Mapping (token values live in styles.css :root):
//   2/3/4px → var(--radius-sm)   (6px)
//   5/6/7/8px → var(--radius-md) (10px)
//   10/11/12/14px → var(--radius-lg) (14px)
//   24/999px → var(--radius-pill) (999px, incl. pill-shaped floating bars)
// Left untouched: 50% (circles), 0, inherit, var() already, multi-value
// corners (e.g. `14px 14px 4px 14px` bubble corners) — listed in the report.
// The build guard (check-radius-tokens.mjs) rejects NEW single-value literals.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, "..");
const WRITE = process.argv.includes("--write");

const MAP = new Map([
  ["2", "--radius-sm"], ["3", "--radius-sm"], ["4", "--radius-sm"],
  ["5", "--radius-md"], ["6", "--radius-md"], ["7", "--radius-md"], ["8", "--radius-md"],
  ["10", "--radius-lg"], ["11", "--radius-lg"], ["12", "--radius-lg"], ["14", "--radius-lg"],
  ["24", "--radius-pill"], ["999", "--radius-pill"],
]);

function listCssFiles(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...listCssFiles(p));
    else if (entry.name.endsWith(".css")) out.push(p);
  }
  return out;
}

const files = fs.existsSync(path.join(frontendRoot, "src/styles"))
  ? listCssFiles(path.join(frontendRoot, "src/styles"))
  : [path.join(frontendRoot, "src/styles.css")];

// Single-value literal only: value + `;` right after (multi-value corners have
// a second length before the semicolon and don't match).
const SINGLE = /(\bborder-radius\s*:\s*)(\d+(?:\.\d+)?)px(\s*;)/g;
const ANY = /\bborder-radius\s*:\s*([^;]+);/g;

let totalRewrites = 0;
const skipped = [];

for (const file of files) {
  const rel = path.relative(frontendRoot, file).replace(/\\/g, "/");
  const source = fs.readFileSync(file, "utf8");
  const counts = {};
  let out = source.replace(SINGLE, (m, head, num, tail) => {
    const token = MAP.get(num);
    if (!token) {
      skipped.push(`${rel}: unmapped ${num}px left as-is`);
      return m;
    }
    counts[token] = (counts[token] || 0) + 1;
    return `${head}var(${token})${tail}`;
  });
  const n = Object.values(counts).reduce((a, b) => a + b, 0);
  totalRewrites += n;
  const detail = Object.entries(counts).map(([t, c]) => `${c}×${t.replace("--radius-", "")}`).join(" ");
  console.log(`${rel}: ${n ? `${n} rewrites (${detail})` : "clean"}`);
  if (WRITE && out !== source) fs.writeFileSync(file, out);

  // Report multi-value / percent corners (informational; guard allows them).
  let m;
  while ((m = ANY.exec(source)) !== null) {
    const v = m[1].trim();
    if (v.includes(" ") && !/^var\(/.test(v)) skipped.push(`${rel}: multi-value corner \`${v}\` kept`);
  }
}

console.log(`\n${WRITE ? "WROTE" : "DRY-RUN"}: ${totalRewrites} replacement(s) across ${files.length} file(s)`);
if (skipped.length) {
  console.log("Kept as-is (review list):");
  for (const s of skipped) console.log(`  ${s}`);
}
