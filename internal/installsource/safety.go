package installsource

// safety.go statically scans a skill's content for prompt-injection and
// malicious-payload patterns before it is installed (SPEC v2 §3.4A). Findings
// flow into the plan's RiskReasons — which the plan step already shows the
// user — so a risky skill surfaces its reasons with zero new UI and zero prompt
// additions (the model sees riskLevel/riskReasons in the install_source result,
// not in the system prompt).
//
// Adapted from rooster's skills/_loader.py AdvancedGuard concept (static
// pattern detection before loading untrusted skills), implemented as a pure
// Go function with no external deps. The patterns are deliberately
// conservative: a skill that legitimately discusses these patterns (a
// security-review skill) may trip a warning, which is why findings are
// classified info/warn/block rather than always refusing — the user still
// decides via the existing apply=true step.

import (
	"strings"
)

// SafetyFinding is one detected concern in a skill body.
type SafetyFinding struct {
	Level       string // "info" | "warn" | "block"
	Code        string // a stable identifier for the pattern family
	Description string // human-readable explanation shown in riskReasons
}

const (
	safetyInfo  = "info"
	safetyWarn  = "warn"
	safetyBlock = "block"
)

// scanSkillContent statically inspects a skill's text for injection / payload
// patterns. Returns findings sorted by severity (block first). Empty = clean.
//
// It only examines the body AFTER the YAML frontmatter (a skill's legitimate
// frontmatter may contain URLs/descriptions that aren't threats). It is a
// heuristic — the goal is to make suspicious skills LOUD in the plan, not to
// be a perfect sandbox (execution safety is the permission gate's job).
func scanSkillContent(content string) []SafetyFinding {
	body := stripFrontmatter(content)
	low := strings.ToLower(body)
	var findings []SafetyFinding

	// 1. Prompt-injection overrides: instructions that try to change the
	// agent's identity or ignore prior instructions. These are the highest-risk
	// because a skill is untrusted text the model reads.
	for _, p := range injectionPatterns {
		if strings.Contains(low, p.needle) {
			findings = append(findings, SafetyFinding{Level: p.level, Code: p.code, Description: p.desc})
		}
	}

	// 2. Hidden execution payloads: code-execution primitives embedded in a
	// skill that runs via bash. These don't prove malice (a debugging skill
	// mentions eval) but warrant a warning.
	for _, p := range payloadPatterns {
		if strings.Contains(low, p.needle) {
			findings = append(findings, SafetyFinding{Level: p.level, Code: p.code, Description: p.desc})
		}
	}

	// 3. Obfuscation signals: base64/hex blobs large enough to hide a payload.
	if hasLargeBase64Blob(body) {
		findings = append(findings, SafetyFinding{
			Level:       safetyWarn,
			Code:        "obfuscated_blob",
			Description: "contains a large base64-like blob that could hide an encoded payload",
		})
	}

	return sortFindings(findings)
}

// stripFrontmatter returns the body after a leading "---\n...\n---" YAML block.
// If there's no frontmatter, the whole content is returned (it's all body).
func stripFrontmatter(content string) string {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "---") {
		return content
	}
	// Drop the opening "---".
	rest := strings.TrimPrefix(s, "---")
	// Find the closing "---".
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return content // malformed frontmatter; scan everything
	}
	return strings.TrimSpace(rest[idx+4:])
}

type safetyPattern struct {
	needle string
	level  string
	code   string
	desc   string
}

// injectionPatterns are prompt-injection attempts. These earn a "block" level
// when found in a skill body — a legitimate skill never needs to override the
// agent's identity or prior instructions.
var injectionPatterns = []safetyPattern{
	{"ignore all previous instructions", safetyBlock, "inject_ignore_previous", "prompt-injection: 'ignore all previous instructions' override attempt"},
	{"ignore your previous instructions", safetyBlock, "inject_ignore_previous", "prompt-injection: 'ignore your previous instructions' override attempt"},
	{"ignore the above instructions", safetyBlock, "inject_ignore_previous", "prompt-injection: 'ignore the above instructions' override attempt"},
	{"you are now a", safetyWarn, "inject_identity_override", "attempts to override the agent's identity ('you are now a ...')"},
	{"disregard prior", safetyBlock, "inject_disregard_prior", "prompt-injection: 'disregard prior' override attempt"},
	{"new instructions: you must", safetyWarn, "inject_new_instructions", "embeds a new-instructions directive aimed at the model"},
	{"system prompt:", safetyWarn, "inject_system_prompt", "embeds a 'system prompt:' directive that could shadow the real system prompt"},
	{"<system>", safetyWarn, "inject_system_tag", "uses a <system> tag that could be confused with a system message"},
}

// payloadPatterns are code-execution / data-exfil primitives. These earn "warn"
// (not block) because some skills legitimately document them — but a skill
// that silently runs one is suspicious.
var payloadPatterns = []safetyPattern{
	{"eval(", safetyWarn, "payload_eval", "contains eval() — can execute arbitrary code if run"},
	{"os.system(", safetyWarn, "payload_os_system", "contains os.system() — runs a shell command if executed"},
	{"subprocess.popen", safetyWarn, "payload_subprocess", "contains subprocess.Popen — spawns a process if executed"},
	{"subprocess.run", safetyWarn, "payload_subprocess", "contains subprocess.run — spawns a process if executed"},
	{"__import__(", safetyWarn, "payload_import", "contains __import__() — dynamic import can load arbitrary modules"},
	{"curl http", safetyWarn, "payload_network_fetch", "contains a curl-to-http fetch — could exfiltrate data"},
	{"wget http", safetyWarn, "payload_network_fetch", "contains a wget-to-http fetch — could exfiltrate data"},
	{"invoke-webrequest", safetyWarn, "payload_network_fetch", "contains Invoke-WebRequest — could exfiltrate data"},
	{"/dev/tcp", safetyWarn, "payload_network_fetch", "contains a /dev/tcp redirect — a common exfiltration vector"},
	{"rm -rf /", safetyBlock, "payload_destructive", "contains 'rm -rf /' — a destructive command"},
	{"mkfs", safetyBlock, "payload_destructive", "contains 'mkfs' — a destructive filesystem command"},
}

// hasLargeBase64Blob reports whether the body contains a base64-ish run long
// enough to plausibly hide a payload (≥80 consecutive base64 chars). Short
// hashes/IDs are fine; a long opaque blob is a red flag.
func hasLargeBase64Blob(body string) bool {
	run := 0
	for _, r := range body {
		if isBase64Char(r) {
			run++
			if run >= 80 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

func isBase64Char(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') || r == '+' || r == '/' || r == '='
}

// sortFindings returns findings with the most severe first (block > warn > info).
func sortFindings(in []SafetyFinding) []SafetyFinding {
	if len(in) <= 1 {
		return in
	}
	// Stable sort by severity rank; within a level, keep discovery order.
	rank := func(level string) int {
		switch level {
		case safetyBlock:
			return 0
		case safetyWarn:
			return 1
		default:
			return 2
		}
	}
	out := append([]SafetyFinding(nil), in...)
	// Simple stable insertion sort (finding lists are tiny).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && rank(out[j].Level) < rank(out[j-1].Level); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// safetyFindingsToRiskReasons turns findings into risk-reason strings for the
// plan action. Block findings escalate the action's RiskLevel to high.
func safetyFindingsToRiskReasons(findings []SafetyFinding) []string {
	if len(findings) == 0 {
		return nil
	}
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Description)
	}
	return out
}

// safetyHasBlock reports whether any finding is block-level (the caller escalates
// RiskLevel to high in that case).
func safetyHasBlock(findings []SafetyFinding) bool {
	for _, f := range findings {
		if f.Level == safetyBlock {
			return true
		}
	}
	return false
}
