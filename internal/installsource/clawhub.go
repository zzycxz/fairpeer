package installsource

// clawhub.go is the REST API client for clawhub.ai, a community skill marketplace
// (SPEC v2 §3.4C). ClawHub provides a real HTTP API (list, search, download) with
// install counts — the richest of the default sources. The client is intentionally
// thin: list/search return CatalogEntry slices (the unified cross-source format),
// and content download reuses the existing fetchText → skillAction pipeline.
//
// API (verified against https://clawhub.ai/api/v1):
//   GET /api/v1/skills?per_page=N&cursor=...  → {items: [...], nextCursor}
//   GET /api/v1/search?q=...                  → {items: [...]}
//   GET /api/v1/skills/{slug}/file?path=SKILL.md → raw SKILL.md content
//
// Each item: slug, displayName, summary, description, topics[], stats{installs}, ...

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// clawhubItem is one skill entry in the ClawHub API response.
type clawhubItem struct {
	Slug        string   `json:"slug"`
	DisplayName string   `json:"displayName"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
	Stats       struct {
		Installs int `json:"installs"`
		Stars    int `json:"stars"`
	} `json:"stats"`
}

type clawhubListResponse struct {
	Items      []clawhubItem `json:"items"`
	NextCursor string        `json:"nextCursor"`
}

// clawhubList fetches a page of skills from ClawHub. Returns entries + the
// next-page cursor (empty when no more pages). Network errors are returned; the
// caller (FetchCatalog/SearchCatalog) treats them as "skip this source".
func (t *installSourceTool) clawhubList(ctx context.Context, perPage int, cursor string) ([]CatalogEntry, string, error) {
	u := "https://clawhub.ai/api/v1/skills"
	q := url.Values{}
	if perPage > 0 {
		q.Set("per_page", fmt.Sprintf("%d", perPage))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	body, err := t.fetchText(ctx, u)
	if err != nil {
		return nil, "", err
	}
	var resp clawhubListResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, "", newErr(ErrInvalidManifest, "clawhub list parse error: %v", err)
	}
	return clawhubItemsToCatalog(resp.Items), resp.NextCursor, nil
}

// clawhubSearchResult is one entry in the /api/v1/search response. The search
// API returns a DIFFERENT structure from the list API — results[] with nested
// install/native fields — so we need a separate type.
type clawhubSearchResult struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Summary     string `json:"summary"`
	OwnerHandle string `json:"ownerHandle"` // e.g. "ivangdavila" — avoids ambiguous-slug errors
	Downloads   int    `json:"downloads"`
	Install     struct {
		Kind      string `json:"kind"`
		Reference string `json:"reference"` // "owner/slug"
		SourceURL string `json:"sourceUrl"`
	} `json:"install"`
	Native struct {
		Skill struct {
			Stats struct {
				Installs int `json:"installs"`
				Stars    int `json:"stars"`
			} `json:"stats"`
			Topics []string `json:"topics"` // note: search API puts topics under native.skill, not top-level
		} `json:"skill"`
	} `json:"native"`
}

type clawhubSearchResponse struct {
	Results []clawhubSearchResult `json:"results"`
}

// clawhubSearch searches ClawHub server-side. Returns matching entries.
// The search API (/api/v1/search) returns {results: [...]} with a different
// structure than the list API (/api/v1/skills) — so we use a separate parser.
func (t *installSourceTool) clawhubSearch(ctx context.Context, query string) ([]CatalogEntry, error) {
	u := "https://clawhub.ai/api/v1/search?q=" + url.QueryEscape(query)
	body, err := t.fetchText(ctx, u)
	if err != nil {
		return nil, err
	}
	var resp clawhubSearchResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		// Fallback: try the list-style structure (some endpoints share schemas).
		var listResp clawhubListResponse
		if err2 := json.Unmarshal([]byte(body), &listResp); err2 == nil && listResp.Items != nil {
			return clawhubItemsToCatalog(listResp.Items), nil
		}
		return nil, newErr(ErrInvalidManifest, "clawhub search parse error: %v", err)
	}
	return clawhubSearchResultsToCatalog(resp.Results), nil
}

// clawhubSearchResultsToCatalog converts search API results to CatalogEntry.
// The search API's install.reference is "owner/slug"; the content URL uses the
// slug to fetch SKILL.md via the file endpoint.
func clawhubSearchResultsToCatalog(results []clawhubSearchResult) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(results))
	for _, r := range results {
		name := r.Slug
		if name == "" {
			name = slugify(r.DisplayName)
		}
		desc := r.Summary
		if len(desc) > 150 {
			desc = desc[:150] + "…"
		}
		installs := r.Downloads
		if installs == 0 {
			installs = r.Native.Skill.Stats.Installs
		}
		topics := r.Native.Skill.Topics
		if topics == nil {
			topics = []string{}
		}
		// Use ownerHandle/slug for the content URL to avoid AMBIGUOUS_SKILL_SLUG
		// errors when multiple authors use the same slug name.
		fullSlug := r.Slug
		if r.OwnerHandle != "" {
			fullSlug = r.OwnerHandle + "/" + r.Slug
		}
		out = append(out, CatalogEntry{
			Source:      "clawhub",
			Name:        name,
			Slug:        r.Slug,
			Author:      r.OwnerHandle,
			Description: desc,
			Topics:      topics,
			Installs:    installs,
			ContentURL:  clawhubContentURL(fullSlug),
			InstallRef:  clawhubContentURL(fullSlug),
		})
	}
	return out
}

// clawhubContentURL returns the URL to fetch a skill's SKILL.md from ClawHub.
func clawhubContentURL(slug string) string {
	return fmt.Sprintf("https://clawhub.ai/api/v1/skills/%s/file?path=SKILL.md", url.PathEscape(slug))
}

// clawhubItemsToCatalog converts ClawHub API items to the unified CatalogEntry
// format. The skill name is derived from the slug (ClawHub uses slug as the
// canonical identifier; displayName is often a human-readable title).
func clawhubItemsToCatalog(items []clawhubItem) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(items))
	for _, item := range items {
		name := item.Slug
		if name == "" {
			name = slugify(item.DisplayName)
		}
		desc := item.Summary
		if desc == "" {
			desc = item.Description
		}
		if len(desc) > 150 {
			desc = desc[:150] + "…"
		}
		topics := item.Topics
		if topics == nil {
			topics = []string{}
		}
		out = append(out, CatalogEntry{
			Source:      "clawhub",
			Name:        name,
			Slug:        item.Slug,
			Description: desc,
			Topics:      topics,
			Installs:    item.Stats.Installs,
			ContentURL:  clawhubContentURL(item.Slug),
			InstallRef:  clawhubContentURL(item.Slug),
		})
	}
	return out
}

// slugify turns a display name into a slug-like identifier (lowercase, hyphens).
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	// Keep only alphanumeric + hyphens.
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	result := b.String()
	result = strings.Trim(result, "-")
	if result == "" {
		return "unnamed"
	}
	return result
}
