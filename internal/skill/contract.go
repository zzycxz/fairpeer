// Package skill — contract.go: the findings-first output contract predicate
// shared by boot's runner (SKILL_ORCHESTRATION_SPEC §11-L4) and CI scenario
// tests. A subagent whose toolface can file findings is bound by "立案先行才
// 作答"; the runner verifies it with the findings-store generation counter and
// this predicate decides whether that check applies.
package skill

// RequiresFindingsContract reports whether a subagent skill is bound by the
// findings-first output contract: its AllowedTools include the findings-filing
// tool, so its answer must be backed by freshly filed evidence.
func RequiresFindingsContract(sk Skill) bool {
	for _, t := range sk.AllowedTools {
		if t == "netdev_finding" {
			return true
		}
	}
	return false
}
