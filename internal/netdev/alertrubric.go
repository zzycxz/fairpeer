package netdev

import "strings"

// Alert danger rubric — SCENARIO_SPEC S4-1/K2-3. The SINGLE authoritative
// scoring model for AI alert triage: the ops browser watch's 研判 prompt embeds
// these weights verbatim, the chat-side 研判 cites the same table via the
// netdev-help scenario card, and the night-shift gate (S4-2) bands rounds with
// AlertDangerBand. Change weights HERE and sync the three consumers.

// AlertDangerSignalWeights: signal name (as the model reports it) → weight.
// Weights sum to 100; both the Chinese and English signal spellings are
// accepted when summing a verdict.
var AlertDangerSignalWeights = map[string]int{
	"失陷确认": 40, "compromised": 40,
	"横向移动": 25, "lateral_movement": 25,
	"暴露critical资产": 20, "critical_exposure": 20,
	"可利用性": 15, "exploitability": 15,
}

// Danger bands: ≥Critical ⇒ critical (立即 IM+邮件), ≥Warning ⇒ warning
// (进日报聚合), else info (仅存档).
const (
	AlertDangerCriticalThreshold = 70
	AlertDangerWarningThreshold  = 40
)

// AlertDangerBand maps a 0-100 score onto critical|warning|info.
func AlertDangerBand(score int) string {
	switch {
	case score >= AlertDangerCriticalThreshold:
		return "critical"
	case score >= AlertDangerWarningThreshold:
		return "warning"
	default:
		return "info"
	}
}

// AlertDangerRank orders bands for threshold comparisons (info < warning <
// critical). Unknown bands rank as info (0) — a malformed verdict never
// escalates past the night gate on its own.
func AlertDangerRank(band string) int {
	switch strings.ToLower(strings.TrimSpace(band)) {
	case "critical":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}
