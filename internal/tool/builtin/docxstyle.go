package builtin

// docxstyle.go: unit conversion helpers for DocStyle's size/spacing/indent
// fields. The goal is to let the LLM pass HUMAN-READABLE units ("12pt",
// "2cm", "0.5in") instead of raw half-points/twips, which it computes
// incorrectly (a model that wants 12pt will not reliably know to emit 24
// half-points).
//
// All converters return half-points (for sizes) or twips (for indents/
// spacing) — the OOXML native units — so the XML emitters in docxwrite.go
// consume them directly. Bare integers are accepted as the legacy unit
// (half-points for size, character-count for indent) for backward compat.

import (
	"fmt"
	"strconv"
	"strings"
)

// parseSize converts a Size value (int OR string) to half-points.
//   - int 24         → 24 (legacy: already half-points, 24=12pt)
//   - string "12pt"  → 24
//   - string "12"    → 24 (bare number treated as points, the intuitive default)
//   - string "0.5cm" → 28 (0.5 cm ≈ 14.17pt ≈ 28.3 half-points)
//
// The bare-string-number-as-points choice (not half-points) is deliberate:
// when a model writes size:"12" it means 12pt, not 12 half-points (6pt). This
// diverges from the legacy int form, but the int form is kept for back-compat
// with existing callers that pass half-points directly.
func parseSize(v any) (int, error) {
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		return int(t), nil
	case string:
		return parseLengthToHalfPoints(t)
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("size: unsupported type %T", v)
}

// parseLengthToHalfPoints parses "12pt"/"2cm"/"0.5in"/"12" → half-points.
// A bare number (no unit suffix) is interpreted as POINTS (the most common
// human intent), so "12" → 24 half-points.
func parseLengthToHalfPoints(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	// Try suffix-based parsing first.
	if pt, ok := parseUnitSuffix(s, "pt", 2); ok { // 1pt = 2 half-points
		return pt, nil
	}
	if hp, ok := parseUnitSuffix(s, "halfpt", 1); ok { // explicit half-points
		return hp, nil
	}
	if hp, ok := parseUnitSuffix(s, "cm", 56.6929); ok { // 1cm = 28.35pt = 56.69 half-points
		return hp, nil
	}
	if hp, ok := parseUnitSuffix(s, "in", 144); ok { // 1in = 72pt = 144 half-points
		return hp, nil
	}
	// Bare number → points (×2 for half-points).
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return int(n * 2), nil
	}
	return 0, fmt.Errorf("unsupported size unit %q (use pt/cm/in or a bare number = points)", s)
}

// parseSpacing converts a spacing string ("12pt"/"0.5cm") to twips (1/20 pt).
// Used for space_before/space_after. Bare numbers are treated as points.
func parseSpacing(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if tw, ok := parseUnitSuffix(s, "pt", 20); ok { // 1pt = 20 twips
		return tw, nil
	}
	if tw, ok := parseUnitSuffix(s, "twip", 1); ok { // explicit twips
		return tw, nil
	}
	if tw, ok := parseUnitSuffix(s, "cm", 566.929); ok { // 1cm = 28.35pt = 566.9 twips
		return tw, nil
	}
	if tw, ok := parseUnitSuffix(s, "in", 1440); ok { // 1in = 1440 twips
		return tw, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return int(n * 20), nil // bare = points
	}
	return 0, fmt.Errorf("unsupported spacing unit %q (use pt/cm/in/twip)", s)
}

// parseIndent converts an Indent value (int OR string) to twips or a char-unit
// sentinel. Supports "2char" (character units, the 公文 standard) and pt/cm/in.
//   - int 2          → legacy: treated as character count → -200 (firstLineChars)
//   - string "2char" → -200 (firstLineChars sentinel; caller emits w:firstLineChars)
//   - string "36pt"  → 720 (twips; caller emits w:firstLine)
//   - string "1cm"   → 567 (twips)
//
// Negative return = character units (magnitude = chars × 100); positive = twips.
func parseIndent(v any) (int, error) {
	switch t := v.(type) {
	case int:
		// Legacy int form was character count (公文). Preserve that semantics.
		if t > 0 {
			return -t * 100, nil
		}
		return 0, nil
	case int64:
		if t > 0 {
			return -int(t) * 100, nil
		}
		return 0, nil
	case float64:
		if t > 0 {
			return -int(t) * 100, nil
		}
		return 0, nil
	case string:
		return parseIndentString(t)
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("indent: unsupported type %T", v)
}

func parseIndentString(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	lower := strings.ToLower(s)
	if idx := strings.Index(lower, "char"); idx > 0 {
		numStr := strings.TrimSpace(s[:idx])
		if n, err := strconv.ParseFloat(numStr, 64); err == nil {
			return -int(n * 100), nil
		}
	}
	if tw, ok := parseUnitSuffix(s, "pt", 20); ok {
		return tw, nil
	}
	if tw, ok := parseUnitSuffix(s, "cm", 566.929); ok {
		return tw, nil
	}
	if tw, ok := parseUnitSuffix(s, "in", 1440); ok {
		return tw, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return -int(n) * 100, nil // bare number = character units (legacy semantics)
	}
	return 0, fmt.Errorf("unsupported indent unit %q (use char/pt/cm/in)", s)
}

// parseUnitSuffix tries to strip `suffix` from s, parse the remaining number,
// and multiply by `perUnit` (the target-unit value per 1 of this unit). Returns
// (value, true) on success.
func parseUnitSuffix(s, suffix string, perUnit float64) (int, bool) {
	lower := strings.ToLower(s)
	if !strings.HasSuffix(lower, strings.ToLower(suffix)) {
		return 0, false
	}
	numStr := strings.TrimSpace(s[:len(s)-len(suffix)])
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, false
	}
	return int(n * perUnit), true
}
