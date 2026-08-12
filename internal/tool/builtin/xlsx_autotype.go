package builtin

import (
	"strconv"
	"strings"
)

// isNumericLiteral reports whether s should be stored as a numeric cell rather
// than a text cell when auto-typing model-provided string values for xlsx.
//
// It accepts plain integers and decimals (including a leading "-", "+", and a
// single ".") so that SUM/AVERAGE formulas treat them as numbers. It rejects:
//   - leading-zero strings like "001" or "010000" (IDs / postal codes / phone
//     numbers must stay text — stripping the leading zeros silently corrupts
//     them, and Excel would otherwise drop the zeros on display);
//   - thousands-separated forms like "1,000" or "12,500.50" (ambiguous locale
//     parsing — let the caller use the structured `number` field instead);
//   - empty strings and anything strconv.ParseFloat rejects.
//
// "0" and "0.5" are accepted (single zero is a real number); the leading-zero
// guard only fires when there is a second digit that isn't a decimal point.
func isNumericLiteral(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Leading-zero guard: "01..." stays text (preserve IDs/postal codes), but
	// "0" alone and "0.xxx" are real numbers.
	if len(s) > 1 && s[0] == '0' && s[1] != '.' {
		return false
	}
	// Same for a leading sign: "-01..." stays text.
	if (s[0] == '-' || s[0] == '+') && len(s) > 2 && s[1] == '0' && s[2] != '.' {
		return false
	}
	// Thousands separators are locale-ambiguous — don't auto-parse them.
	if strings.ContainsRune(s, ',') {
		return false
	}
	// Reject Go underscore syntax ("1_000") and the ParseFloat special values
	// Inf/NaN — these would otherwise be stored as numbers (+Inf/NaN corrupt
	// cells in Excel, and "1_000" would silently lose the underscore).
	lower := strings.ToLower(s)
	if strings.ContainsRune(s, '_') ||
		lower == "inf" || lower == "-inf" || lower == "+inf" ||
		lower == "nan" {
		return false
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return false
	}
	return true
}

// numericValue parses s (already validated by isNumericLiteral) into a float64
// for SetCellValue. Caller MUST guard with isNumericLiteral first.
func numericValue(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// normalizeCriteria maps the schema-documented snake_case criteria values to
// the space-separated literals excelize's validator requires. Unknown values
// pass through unchanged (so a caller who already supplies the excelize form
// still works). See XLSXCondFmt.Criteria doc ("greater_than"|...).
func normalizeCriteria(criteria string) string {
	switch strings.ToLower(strings.TrimSpace(criteria)) {
	case "greater_than":
		return "greater than"
	case "less_than":
		return "less than"
	case "greater_than_or_equal":
		return "greater than or equal"
	case "less_than_or_equal":
		return "less than or equal"
	case "not_equal":
		return "not equal"
	default:
		// "equal", "between", already-spaced forms, etc.
		return criteria
	}
}
