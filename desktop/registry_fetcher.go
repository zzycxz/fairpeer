package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// registry_fetcher.go implements the remote-registry fetch (models.dev) and
// local-cache logic. It is the "live data" layer on top of the embed snapshot:
//
//   initRegistry() on startup → loadCache() → if stale, fetchRemote() → saveCache()
//   RefreshRegistry() on user click → fetchRemote() → saveCache()
//
// All failures degrade gracefully to the embed snapshot (registry.go).

// registryCacheFile is the on-disk cache format.
type registryCacheFile struct {
	UpdatedAt string             `json:"updated_at"` // RFC3339
	Templates []ProviderTemplate `json:"templates"`
}

// modelsDevResponse is the subset of models.dev/api.json we decode. Only the
// fields we need are defined; unknown fields are ignored (forward-compatible).
type modelsDevResponse map[string]modelsDevVendor

type modelsDevVendor struct {
	Name   string                    `json:"name"`   // display name
	API    string                    `json:"api"`    // base URL
	Env    []string                  `json:"env"`    // env var names
	Doc    string                    `json:"doc"`    // doc URL
	Models map[string]modelsDevModel `json:"models"` // model ID → detail
}

type modelsDevModel struct {
	Name       string         `json:"name"`
	Attachment bool           `json:"attachment"` // supports image/file input
	Reasoning  bool           `json:"reasoning"`
	Limit      modelsDevLimit `json:"limit"`
}

type modelsDevLimit struct {
	Context int `json:"context"` // max input+output tokens
	Output  int `json:"output"`
}

// initRegistry loads the registry on startup: try local cache first (fast),
// then asynchronously refresh from models.dev if stale. Non-blocking — the
// embed snapshot is used until the cache or remote data is ready.
func (a *App) initRegistry() {
	// Try local cache (synchronous, fast — just a file read).
	if ts, updated, ok := loadCache(); ok {
		globalRegistry.set(ts, updated)
	}
	// If cache is stale (or absent), refresh in the background.
	if globalRegistry.UpdatedAt().IsZero() || time.Since(globalRegistry.UpdatedAt()) > registryTTL {
		go func() {
			_ = a.fetchAndCache() // best-effort; errors are silent (embed snapshot stands)
		}()
	}
}

// fetchAndCache pulls from models.dev, merges with the embed snapshot, writes
// the local cache, and updates the in-memory registry. Returns nil on success.
func (a *App) fetchAndCache() error {
	ts, err := fetchRemote(context.Background(), modelsDevURL)
	if err != nil {
		return err
	}
	now := time.Now()
	globalRegistry.set(ts, now)
	if err := saveCache(ts, now); err != nil {
		// Cache write failed — registry is still updated in memory; just log.
		fmt.Fprintf(os.Stderr, "registry: cache write failed: %v\n", err)
	}
	return nil
}

// RefreshRegistry forces a fresh pull from models.dev (Settings button).
func (a *App) RefreshRegistry() error {
	return a.fetchAndCache()
}

// fetchRemote downloads the models.dev registry, filters to tracked vendors,
// merges with the embed snapshot (for role/display fields models.dev doesn't
// carry), and returns the combined template list.
func fetchRemote(ctx context.Context, url string) ([]ProviderTemplate, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("registry fetch: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry fetch: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB cap
	if err != nil {
		return nil, fmt.Errorf("registry read: %w", err)
	}

	var raw modelsDevResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("registry parse: %w", err)
	}

	// Start from the embed snapshot (carries DisplayName/DocURL/roles).
	snapshot := loadEmbedSnapshot()
	merged := make([]ProviderTemplate, 0, len(snapshot))
	seen := map[string]bool{}

	// Reverse lookup: our provider name → models.dev vendor ID.
	devIDForName := make(map[string]string, len(trackedVendors))
	for devID, ourName := range trackedVendors {
		devIDForName[ourName] = devID
	}

	// For each snapshot vendor, enrich with live data from models.dev if present.
	for _, snap := range snapshot {
		t := snap // copy
		devID := devIDForName[snap.Name]
		if v, ok := raw[devID]; ok {
			t = mergeRemote(snap, v)
		}
		merged = append(merged, t)
		seen[snap.Name] = true
	}

	_ = seen // (future: could append vendors present in remote but not snapshot)
	return merged, nil
}

// mergeRemote overlays live fields (BaseURL, APIKeyEnv, Models, ContextWindow,
// Vision) from the models.dev vendor onto the snapshot template (which carries
// DisplayName, DocURL, DefaultModel/FastModel/VisionModel roles). Snapshot
// wins for fields models.dev doesn't carry or that are empty.
func mergeRemote(snap ProviderTemplate, v modelsDevVendor) ProviderTemplate {
	t := snap
	if v.API != "" {
		t.BaseURL = v.API
	}
	if len(v.Env) > 0 && v.Env[0] != "" {
		t.APIKeyEnv = v.Env[0]
	}
	// Collect model IDs preserving the JSON map order is not guaranteed in Go
	// (map iteration is random). Sort for stable UI ordering.
	if len(v.Models) > 0 {
		models := make([]string, 0, len(v.Models))
		anyVision := false
		maxCtx := 0
		for id, m := range v.Models {
			models = append(models, id)
			if m.Attachment {
				anyVision = true
			}
			if m.Limit.Context > maxCtx {
				maxCtx = m.Limit.Context
			}
		}
		t.Models = models
		t.Vision = anyVision || snap.Vision // keep snapshot's vision flag if models.dev says no
		if maxCtx > 0 {
			t.ContextWindow = maxCtx
		}
	}
	return t
}

// loadCache reads the local cache file. Returns (templates, updatedAt, ok).
func loadCache() ([]ProviderTemplate, time.Time, bool) {
	path := registryCachePath()
	if path == "" {
		return nil, time.Time{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	var cache registryCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, time.Time{}, false
	}
	updated, err := time.Parse(time.RFC3339, cache.UpdatedAt)
	if err != nil {
		return nil, time.Time{}, false
	}
	if len(cache.Templates) == 0 {
		return nil, time.Time{}, false
	}
	return cache.Templates, updated, true
}

// saveCache writes the local cache file.
func saveCache(ts []ProviderTemplate, updatedAt time.Time) error {
	path := registryCachePath()
	if path == "" {
		return fmt.Errorf("registry: config dir unavailable")
	}
	cache := registryCacheFile{
		UpdatedAt: updatedAt.Format(time.RFC3339),
		Templates: ts,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
