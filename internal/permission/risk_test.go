package permission

import "testing"

// TestClassify covers all five classification paths plus the priority ordering
// (override > builtin > MCP > bash > readOnly).
func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		readOnly  bool
		overrides map[string]RiskClass
		want      RiskClass
	}{
		// Path 1: explicit override wins over everything.
		{"override beats builtin", "email_send", false, map[string]RiskClass{"email_send": RiskRead}, RiskRead},
		{"override beats MCP default", "mcp__srv__tool", false, map[string]RiskClass{"mcp__srv__tool": RiskRead}, RiskRead},
		{"override beats bash", "bash", false, map[string]RiskClass{"bash": RiskRead}, RiskRead},

		// Path 2: builtin table (email_send / rag_delete).
		{"builtin email_send is external", "email_send", false, nil, RiskExternal},
		{"builtin rag_delete is external", "rag_delete", false, nil, RiskExternal},
		// readOnly flag does NOT downgrade a builtin external tool — email_send
		// is outward even if a buggy tool declares itself readOnly.
		{"builtin external ignores readOnly flag", "email_send", true, nil, RiskExternal},

		// Path 3: MCP tools (mcp__ prefix) default to external (safe default).
		{"MCP tool defaults to external", "mcp__codegraph__context", false, nil, RiskExternal},
		{"MCP tool external even when readOnly true", "mcp__srv__read", true, nil, RiskExternal},

		// Path 4: bash → exec.
		{"bash is exec", "bash", false, nil, RiskExec},

		// Path 5: readOnly → read; else write_local.
		{"read-only builtin tool is read", "read_file", true, nil, RiskRead},
		{"non-read-only builtin write tool is write_local", "edit_file", false, nil, RiskWriteLocal},
		{"write_file is write_local", "write_file", false, nil, RiskWriteLocal},
		{"unknown read-only tool is read", "some_ro_tool", true, nil, RiskRead},
		{"unknown write tool is write_local", "some_rw_tool", false, nil, RiskWriteLocal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.tool, tt.readOnly, tt.overrides); got != tt.want {
				t.Errorf("Classify(%q, ro=%v) = %d, want %d", tt.tool, tt.readOnly, got, tt.want)
			}
		})
	}
}

// TestClassify_PriorityOrdering confirms the documented precedence holds when
// multiple signals conflict: override > builtin > MCP > bash > readOnly.
func TestClassify_PriorityOrdering(t *testing.T) {
	// A tool named like MCP but in the builtin table → builtin wins (it's a
	// known builtin even if oddly prefixed). Actually builtin is checked after
	// override but the names don't collide in practice; test the real precedence:
	// override > builtin.
	if got := Classify("email_send", false, map[string]RiskClass{"email_send": RiskRead}); got != RiskRead {
		t.Errorf("override should beat builtin: got %d, want %d (Read)", got, RiskRead)
	}
}

// TestIsExternal confirms the predicate Gate.Check consults.
func TestIsExternal(t *testing.T) {
	cases := map[string]bool{
		"email_send":     true,  // builtin external
		"rag_delete":     true,  // builtin external
		"mcp__srv__tool": true,  // MCP default external
		"read_file":      false, // read
		"edit_file":      false, // write_local
		"bash":           false, // exec (not external)
	}
	for tool, want := range cases {
		t.Run(tool, func(t *testing.T) {
			ro := false
			if tool == "read_file" {
				ro = true
			}
			if got := IsExternal(tool, ro, nil); got != want {
				t.Errorf("IsExternal(%q) = %v, want %v", tool, got, want)
			}
		})
	}
}

// TestIsExternal_OverrideDowngrade confirms a config override can downgrade an
// MCP tool from external to read (so a trusted read-only MCP server doesn't
// prompt every call). This is the per-server escape hatch.
func TestIsExternal_OverrideDowngrade(t *testing.T) {
	// Per-tool exact override.
	overrides := map[string]RiskClass{"mcp__trusted__get": RiskRead}
	if IsExternal("mcp__trusted__get", false, overrides) {
		t.Errorf("override to Read should make IsExternal false")
	}
	// A different tool on the same server still external (override is per-tool).
	if !IsExternal("mcp__trusted__post", false, overrides) {
		t.Errorf("un-overridden MCP tool should stay external")
	}
}

// TestClassify_MCPServerPrefixOverride confirms a per-server prefix key
// ("mcp__<server>__") covers ALL tools on that server, so [[plugins]] risk=read
// marks every tool on the server without naming them. This is the realistic
// config path (boot builds "mcp__<server>__" keys from PluginEntry.Risk).
func TestClassify_MCPServerPrefixOverride(t *testing.T) {
	// Server "codegraph" configured as read-only → all its tools are Read.
	overrides := map[string]RiskClass{"mcp__codegraph__": RiskRead}
	for _, tool := range []string{"mcp__codegraph__context", "mcp__codegraph__search", "mcp__codegraph__anything"} {
		if got := Classify(tool, false, overrides); got != RiskRead {
			t.Errorf("server-prefix override: %s = %d, want %d (Read)", tool, got, RiskRead)
		}
	}
	// A different server is unaffected (still external by default).
	if got := Classify("mcp__other__tool", false, overrides); got != RiskExternal {
		t.Errorf("unconfigured server should stay external, got %d", got)
	}
	// Exact-name override beats server-prefix override.
	overrides["mcp__codegraph__write"] = RiskWriteLocal
	if got := Classify("mcp__codegraph__write", false, overrides); got != RiskWriteLocal {
		t.Errorf("exact override should beat server prefix, got %d", got)
	}
}

// TestParseRiskClass covers the config-string → RiskClass mapping, including
// the safe-default (unknown → external) so a typo never silently downgrades.
func TestParseRiskClass(t *testing.T) {
	tests := []struct {
		in   string
		want RiskClass
	}{
		{"read", RiskRead},
		{"READ", RiskRead},
		{"read-only", RiskRead},
		{"write_local", RiskWriteLocal},
		{"write", RiskWriteLocal},
		{"exec", RiskExec},
		{"external", RiskExternal},
		{"outward", RiskExternal},
		{"", RiskExternal},     // empty → safe default
		{"typo", RiskExternal}, // unknown → safe default
		{"  Read  ", RiskRead}, // trimmed
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ParseRiskClass(tt.in); got != tt.want {
				t.Errorf("ParseRiskClass(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestBackwardCompat_EmailRagDeleteStillExternal is the regression guard: the
// old isIrreversibleOutwardTool(email_send) and isIrreversibleOutwardTool
// (rag_delete) both returned true. After refactoring into the risk table,
// IsExternal must keep returning true for both — headless mode's safety
// guarantee depends on it.
func TestBackwardCompat_EmailRagDeleteStillExternal(t *testing.T) {
	for _, tool := range []string{"email_send", "rag_delete"} {
		if !IsExternal(tool, false, nil) {
			t.Errorf("regression: %s must stay external (was isIrreversibleOutwardTool=true)", tool)
		}
	}
}
