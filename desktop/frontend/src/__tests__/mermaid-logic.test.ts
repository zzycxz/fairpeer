// Pure logic tests for Mermaid string preprocessing.
//
// Run: tsx src/__tests__/mermaid-logic.test.ts

import { extractMermaidTitle, sanitizeMermaidCode } from "../components/mermaidLogic";

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

console.log("\nextractMermaidTitle");
eq(extractMermaidTitle("---\ntitle: Simple Flowchart\n---\ngraph TD"), "Simple Flowchart", "Extracts standard title");
eq(extractMermaidTitle("graph TD\nA-->B"), "", "Returns empty when no title present");
eq(extractMermaidTitle("---\n title:  Spaced Title  \n---\ngraph TD"), "Spaced Title", "Trims whitespace from title");

console.log("\nsanitizeMermaidCode");
eq(sanitizeMermaidCode("graph TD\n  A-->B\n\n\n"), "graph TD\n  A-->B", "Strips trailing whitespace and empty lines");
eq(sanitizeMermaidCode("  sequenceDiagram\nAlice->Bob: Hello"), "sequenceDiagram\nAlice->Bob: Hello", "Trims leading whitespace");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
