// Pure logic tests for ToolCard state transitions.
//
// Run: tsx src/__tests__/toolcard-state.test.ts

import { getInitialOpenState, shouldKeepMounted } from "../components/toolcardLogic";

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

console.log("\ngetInitialOpenState");
eq(getInitialOpenState("running", true, false), true, "running + hasNested -> true");
eq(getInitialOpenState("running", false, true), true, "running + isShell -> true");
eq(getInitialOpenState("done", true, false), false, "done + hasNested -> false");
eq(getInitialOpenState("done", false, true), false, "done + isShell -> false");
eq(getInitialOpenState("running", false, false), false, "running + normal tool -> false");

console.log("\nshouldKeepMounted");
eq(shouldKeepMounted(false, false), false, "never opened -> false");
eq(shouldKeepMounted(false, true), true, "first open -> true");
eq(shouldKeepMounted(true, false), true, "closed after being open -> true");
eq(shouldKeepMounted(true, true), true, "kept open -> true");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
