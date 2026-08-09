package builtin

import "testing"

// TestParseSize covers the unit converter for the Size field: int (legacy
// half-points) and string forms (pt/cm/in/bare).
func TestParseSize(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{24, 24},              // int = half-points (legacy back-compat)
		{"12pt", 24},          // points → half-points
		{"12", 24},            // bare number = points (NOT half-points)
		{"0.5cm", 28},         // 0.5cm ≈ 14.17pt ≈ 28.3 → 28
		{"1in", 144},          // 72pt = 144 half-points
		{"", 0},               // empty
		{nil, 0},
		{12.0, 12},            // float64 like int
	}
	for _, c := range cases {
		got, err := parseSize(c.in)
		if err != nil {
			t.Errorf("parseSize(%v) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseSize(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestParseSizeRejectsBadUnit verifies unsupported units return an error.
func TestParseSizeRejectsBadUnit(t *testing.T) {
	_, err := parseSize("12foo")
	if err == nil {
		t.Error("expected error for unsupported unit 'foo'")
	}
}

// TestParseSpacing covers space_before/space_after conversion to twips.
func TestParseSpacing(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"12pt", 240},   // 12 × 20
		{"12", 240},     // bare = points
		{"1in", 1440},
		{"0.5cm", 283},  // 0.5 × 566.9 ≈ 283
	}
	for _, c := range cases {
		got, err := parseSpacing(c.in)
		if err != nil {
			t.Errorf("parseSpacing(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseSpacing(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestParseIndentCharUnits verifies character units (int and "2char") return
// the negative sentinel (so pPrXML emits firstLineChars, the 公文 standard).
func TestParseIndentCharUnits(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{2, -200},       // int = character count (legacy)
		{"2char", -200}, // string char units
		{"2", -200},     // bare string number = char units (legacy semantics)
	}
	for _, c := range cases {
		got, err := parseIndent(c.in)
		if err != nil {
			t.Errorf("parseIndent(%v) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseIndent(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestParseIndentTwips verifies pt/cm/in indents return positive twips.
func TestParseIndentTwips(t *testing.T) {
	got, err := parseIndent("36pt")
	if err != nil {
		t.Fatal(err)
	}
	if got != 720 { // 36 × 20
		t.Errorf("36pt = 720 twips, got %d", got)
	}
}
