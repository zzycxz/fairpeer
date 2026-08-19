// check-radius-tokens.mjs — build guard: single-value literal `border-radius:
// Npx` is not allowed; use the --radius-sm/md/lg/pill tokens instead
// (ui-redesign-spec §4-A1). Allowed: var(...) tokens, 0, 50%, inherit, and
// multi-value corner shorthand (e.g. `14px 14px 4px 14px`).
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, "..");

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

const decl = /border-radius\s*:\s*([^;]+);/g;
let failed = false;

for (const file of files) {
  const rel = path.relative(frontendRoot, file).replace(/\\/g, "/");
  const source = fs.readFileSync(file, "utf8");
  let match;
  while ((match = decl.exec(source)) !== null) {
    const value = match[1].trim().replace(/\s+/g, " ");
    if (value.startsWith("var(")) continue;
    if (value === "0" || value === "50%" || value === "inherit") continue;
    if (value.includes(" ")) continue; // multi-value corner shorthand
    failed = true;
    const line = source.slice(0, match.index).split(/\r?\n/).length;
    console.error(`${rel}:${line}: border-radius must use a --radius-* token, got ${value}`);
  }
}

if (failed) process.exit(1);
console.log(`radius token check passed: ${files.length} file(s)`);
