package agent

import "testing"

// TestPartialJSONString (upgrade spec 3-3): the streaming preview extractor
// must decode escapes incrementally and tolerate every truncation point —
// mid-escape, mid-\uXXXX, before the opening quote, and after the closing
// quote (complete value).
func TestPartialJSONString(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"key not arrived", `{"patch`, "", false},
		{"colon not arrived", `{"patchText"`, "", false},
		{"quote not arrived", `{"patchText":`, "", false},
		{"empty so far", `{"patchText":"`, "", true},
		{"plain prefix", `{"patchText":"*** Begin Patch\n*** Add File: a.go`, "*** Begin Patch\n*** Add File: a.go", true},
		{"escaped newline decoded", `{"patchText":"line1\nline2\t`, "line1\nline2\t", true},
		{"escaped quote decoded", `{"patchText":"say \"hi\"`, `say "hi"`, true},
		{"dangling backslash", `{"patchText":"abc\`, "abc", true},
		{"incomplete unicode escape", `{"patchText":"x\u4e2`, "x", true},
		{"complete value", `{"patchText":"done","other":1}`, "done", true},
		{"other key first", `{"x":1,"patchText":"v`, "v", true},
	}
	for _, c := range cases {
		got, ok := partialJSONString(c.raw, "patchText")
		if ok != c.ok || got != c.want {
			t.Errorf("%s: partialJSONString(%q) = (%q,%v), want (%q,%v)", c.name, c.raw, got, ok, c.want, c.ok)
		}
	}
}
