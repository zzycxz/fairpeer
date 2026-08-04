package installsource

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDefaultMarketSources confirms the 4 default sources are present and have
// correct types.
func TestDefaultMarketSources(t *testing.T) {
	sources := DefaultMarketSources()
	if len(sources) < 4 {
		t.Fatalf("expected at least 4 default sources, got %d", len(sources))
	}
	ids := map[string]bool{}
	for _, s := range sources {
		ids[s.ID] = true
	}
	for _, want := range []string{"builtin", "anthropics", "openai", "clawhub"} {
		if !ids[want] {
			t.Errorf("default source %q missing", want)
		}
	}
}

// TestBuiltinCatalog confirms the builtin catalog has entries and they all have
// a ContentURL (the install path) and a Name.
func TestBuiltinCatalog(t *testing.T) {
	if len(builtinCatalog) == 0 {
		t.Fatal("builtin catalog should not be empty")
	}
	for _, e := range builtinCatalog {
		if e.Name == "" {
			t.Error("builtin catalog entry missing Name")
		}
		if e.ContentURL == "" {
			t.Errorf("builtin entry %q missing ContentURL", e.Name)
		}
		if e.InstallRef == "" {
			t.Errorf("builtin entry %q missing InstallRef", e.Name)
		}
		if !strings.HasPrefix(e.ContentURL, "https://") {
			t.Errorf("builtin entry %q ContentURL should be HTTPS: %s", e.Name, e.ContentURL)
		}
	}
}

// TestCatalogMatches confirms the local search filter works on name/desc/topics.
func TestCatalogMatches(t *testing.T) {
	e := CatalogEntry{
		Name:        "code-review",
		Description: "Automated code review with security checks",
		Topics:      []string{"security", "quality"},
	}
	cases := map[string]bool{
		"code":      true,  // name match
		"review":    true,  // name match
		"security":  true,  // topic + description match
		"automated": true,  // description match
		"missing":   false, // no match
		"":          false, // empty query
	}
	for query, want := range cases {
		if got := catalogMatches(e, query); got != want {
			t.Errorf("catalogMatches(%q) = %v, want %v", query, got, want)
		}
	}
}

// TestClawhubContentURL confirms the content URL format for a slug.
func TestClawhubContentURL(t *testing.T) {
	got := clawhubContentURL("my-skill")
	want := "https://clawhub.ai/api/v1/skills/my-skill/file?path=SKILL.md"
	if got != want {
		t.Errorf("clawhubContentURL = %q, want %q", got, want)
	}
}

// TestClawhubItemsToCatalog confirms API items convert to the unified format.
func TestClawhubItemsToCatalog(t *testing.T) {
	items := []clawhubItem{
		{Slug: "code-review", DisplayName: "Code Review", Summary: "Review code", Topics: []string{"quality"}, Stats: struct {
			Installs int `json:"installs"`
			Stars    int `json:"stars"`
		}{Installs: 42}},
		{Slug: "pdf-tool", DisplayName: "PDF Tool", Summary: "Create PDFs"},
	}
	entries := clawhubItemsToCatalog(items)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "code-review" {
		t.Errorf("first entry name = %q, want code-review", entries[0].Name)
	}
	if entries[0].Installs != 42 {
		t.Errorf("first entry installs = %d, want 42", entries[0].Installs)
	}
	if entries[0].Source != "clawhub" {
		t.Errorf("source = %q, want clawhub", entries[0].Source)
	}
	if !strings.Contains(entries[0].ContentURL, "code-review") {
		t.Errorf("ContentURL should contain the slug: %s", entries[0].ContentURL)
	}
}

// TestSlugify confirms display names become valid slugs.
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Code Review":    "code-review",
		"PDF Generator!": "pdf-generator",
		"  spaces  ":     "spaces",
		"":               "unnamed",
		"UPPER_CASE":     "upper-case",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMarketplaceToCatalog confirms marketplace.json plugins convert to entries.
func TestMarketplaceToCatalog(t *testing.T) {
	mp := &claudeMarketplace{
		Plugins: []claudeMarketplacePlugin{
			{Name: "code-review", Description: "Code review skill", Skills: []string{"./skills/code-review"}},
			{Name: "deploy-tool", Description: "Deploy helper"},
		},
	}
	entries := marketplaceToCatalog("github.com/test/repo", "marketplace", mp)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// First plugin has a skills dir → name derived from dir.
	if entries[0].Name != "code-review" {
		t.Errorf("first name = %q, want code-review", entries[0].Name)
	}
	if !strings.Contains(entries[0].Description, "Code review") {
		t.Errorf("first desc = %q", entries[0].Description)
	}
	// ContentURL should point to raw.githubusercontent.com.
	if !strings.Contains(entries[0].ContentURL, "raw.githubusercontent.com") {
		t.Errorf("ContentURL should be a raw GitHub URL: %s", entries[0].ContentURL)
	}
}

// TestSkillMarketTool_BrowseBuiltin confirms browse works offline with the
// builtin source (no network needed).
func TestSkillMarketTool_BrowseBuiltin(t *testing.T) {
	tool := NewMarketToolFromOptions(Options{ProjectRoot: t.TempDir(), HomeDir: t.TempDir()})
	out, err := tool.Execute(nil, mustMarshalJSON(map[string]any{"action": "browse", "source": "builtin"}))
	if err != nil {
		t.Fatalf("browse builtin: %v", err)
	}
	if !strings.Contains(out, "Curated") {
		t.Errorf("browse output should contain source name 'Curated': %s", out)
	}
	// Should list at least one skill name from the builtin catalog.
	foundSkill := false
	for _, e := range builtinCatalog {
		if strings.Contains(out, e.Name) {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Errorf("browse output should list at least one builtin skill name")
	}
}

// mustMarshalJSON is a test helper.
func mustMarshalJSON(m map[string]any) []byte {
	b, _ := json.Marshal(m)
	return b
}
