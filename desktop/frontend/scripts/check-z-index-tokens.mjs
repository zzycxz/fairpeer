import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, "..");
const targets = process.argv.slice(2);
function listCssFiles(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...listCssFiles(p));
    else if (entry.name.endsWith(".css")) out.push(p);
  }
  return out;
}
const stylesDir = path.join(frontendRoot, "src", "styles");
const defaultFiles = ["src/styles.css",
  ...(fs.existsSync(stylesDir) ? listCssFiles(stylesDir).map((p) => path.relative(frontendRoot, p).split(path.sep).join("/")) : [])];
const files = targets.length > 0 ? targets : defaultFiles;
const zIndexDecl = /z-index\s*:\s*([^;]+);/g;
const tokenValue = /^var\(--z-[a-z0-9-]+\)$/;

let failed = false;

for (const file of files) {
  const fullPath = path.resolve(frontendRoot, file);
  const source = fs.readFileSync(fullPath, "utf8");
  let match;
  while ((match = zIndexDecl.exec(source)) !== null) {
    const value = match[1].trim().replace(/\s+/g, " ");
    if (tokenValue.test(value)) continue;
    failed = true;
    const line = source.slice(0, match.index).split(/\r?\n/).length;
    console.error(`${file}:${line}: z-index must use a --z-* token, got ${value}`);
  }
}

if (failed) {
  process.exit(1);
}

console.log(`z-index token check passed: ${files.join(", ")}`);
