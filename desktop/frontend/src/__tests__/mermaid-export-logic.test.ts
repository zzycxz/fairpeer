// Pure logic tests for the mermaid-export helpers (fence replacement, SVG
// size parsing, filename sanitizing). The DOM/mermaid rasterizer itself
// (lib/mermaidExport.ts) needs a live webview and is not covered here.
//
// Run: tsx src/__tests__/mermaid-export-logic.test.ts

import {
  buildMermaidImageMarkdown,
  collectMermaidFences,
  parseMermaidSvgSize,
  replaceMermaidFences,
  safeMermaidFilename,
} from "../components/mermaidLogic";

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

console.log("\nparseMermaidSvgSize");
eq(
  parseMermaidSvgSize('<svg viewBox="0 0 512 384" width="100%" style="max-width: 512px;">').width,
  512,
  "viewBox wins over percent width",
);
eq(parseMermaidSvgSize('<svg viewBox="0 0 512 384">').height, 384, "viewBox height");
eq(parseMermaidSvgSize('<svg width="1234" height="567">').width, 1234, "explicit attrs used when no viewBox");
eq(parseMermaidSvgSize('<svg width="300px" height="150px">').width, 300, "px-suffixed attrs parsed");
eq(parseMermaidSvgSize('<svg viewBox="0,0,300,200">').height, 200, "comma-separated viewBox");
eq(parseMermaidSvgSize('<svg width="100%">').width, 800, "percent width falls back to default");
eq(parseMermaidSvgSize("<div>not an svg</div>").height, 600, "garbage falls back to default");

console.log("\nsafeMermaidFilename");
eq(safeMermaidFilename("部署架构图"), "部署架构图", "keeps plain text");
eq(safeMermaidFilename('a/b\\c:d*e?f"g<h>i|j'), "a-b-c-d-e-f-g-h-i-j", "strips filesystem-hostile characters");
eq(safeMermaidFilename(""), "mermaid-diagram", "empty title falls back");
eq(safeMermaidFilename("  spaced   title  "), "spaced title", "collapses whitespace");
eq(safeMermaidFilename("x".repeat(200)).length, 80, "caps length at 80");

console.log("\ncollectMermaidFences / replaceMermaidFences");
const simple = "before\n```mermaid\ngraph TD\nA-->B\n```\nafter";
const simpleFences = collectMermaidFences(simple);
eq(simpleFences.length, 1, "finds one mermaid fence");
eq(simpleFences[0].code, "graph TD\nA-->B", "captures inner chart source");
eq(
  replaceMermaidFences(simple, simpleFences, [buildMermaidImageMarkdown(simpleFences[0], "data:image/png;base64,QQ")]),
  "before\n![Mermaid diagram](data:image/png;base64,QQ)\nafter",
  "splices image markdown over the fence",
);

eq(replaceMermaidFences(simple, simpleFences, [null]), simple, "null replacement keeps the fence");

const leading = "```mermaid\ngraph TD\nA-->B\n```";
const leadingFences = collectMermaidFences(leading);
eq(
  replaceMermaidFences(leading, leadingFences, [buildMermaidImageMarkdown(leadingFences[0], "U")]),
  "![Mermaid diagram](U)",
  "fence at string start replaces cleanly",
);

const titled = "```mermaid\n---\ntitle: Deploy Flow\n---\ngraph TD\nA-->B\n```";
const titledFences = collectMermaidFences(titled);
eq(titledFences[0].title, "Deploy Flow", "extracts chart title");
eq(
  replaceMermaidFences(titled, titledFences, [buildMermaidImageMarkdown(titledFences[0], "U")]),
  "![Deploy Flow](U)",
  "uses the title as alt text",
);

const indented = "- item\n  ```mermaid\n  graph TD\n  A-->B\n  ```";
const indentedFences = collectMermaidFences(indented);
eq(
  replaceMermaidFences(indented, indentedFences, [buildMermaidImageMarkdown(indentedFences[0], "U")]),
  "- item\n  ![Mermaid diagram](U)",
  "preserves fence indentation",
);

const two = "```mermaid\nA-->B\n```\ntext\n```mermaid\nC-->D\n```";
const twoFences = collectMermaidFences(two);
eq(twoFences.length, 2, "finds multiple fences");
eq(
  replaceMermaidFences(two, twoFences, twoFences.map((f) => buildMermaidImageMarkdown(f, "U"))),
  "![Mermaid diagram](U)\ntext\n![Mermaid diagram](U)",
  "replaces multiple fences in order",
);

eq(collectMermaidFences("```js\nconsole.log(1)\n```").length, 0, "non-mermaid fences ignored");
eq(collectMermaidFences("```mermaid\nA-->B\n").length, 0, "unclosed fence ignored");
eq(collectMermaidFences("plain text, no fences").length, 0, "plain text has no fences");
eq(
  replaceMermaidFences("plain text", collectMermaidFences("plain text"), []),
  "plain text",
  "no fences returns input unchanged",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
