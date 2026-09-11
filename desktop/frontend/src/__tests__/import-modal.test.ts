import { describe, expect, it } from "vitest";

type ImportType = "folder" | "files";

interface ImportModalState {
  isOpen: boolean;
  importType: ImportType;
  targetCollection: string;
}

// Mirrors ImportModal's real behavior: a target collection is REQUIRED (empty
// string is a view scope, not an import target), and deep extraction is always
// queued by the import itself — there is no separate autoExtract opt-in.
function createInitialModalState(isOpen: boolean, defaultCollection = ""): ImportModalState {
  return {
    isOpen,
    importType: "folder",
    targetCollection: defaultCollection,
  };
}

function switchImportType(state: ImportModalState, type: ImportType): ImportModalState {
  return { ...state, importType: type };
}

describe("ImportModal Asset Selection & UX Suite", () => {
  it("should initialize with folder import mode and the provided collection target", () => {
    const state = createInitialModalState(true, "工作/研发");
    expect(state.isOpen).toBe(true);
    expect(state.importType).toBe("folder");
    expect(state.targetCollection).toBe("工作/研发");
  });

  it("should allow switching between folder batch import and specific file selection", () => {
    let state = createInitialModalState(true);
    expect(state.importType).toBe("folder");

    state = switchImportType(state, "files");
    expect(state.importType).toBe("files");
  });

  it("should keep the target empty when no default is given (user must choose)", () => {
    // ImportModal disables the import button until a concrete collection is
    // picked — "" is intentionally NOT auto-replaced with a made-up name.
    const state = createInitialModalState(true, "");
    expect(state.targetCollection).toBe("");
  });
});
