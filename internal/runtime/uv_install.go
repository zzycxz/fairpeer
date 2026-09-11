package runtime

// uv_install.go implements automatic uv binary download (mirrors codegraph's
// install.go pattern). When ResolveUV finds no uv on PATH or bundle, and
// auto-install is enabled (default), Install downloads the platform-correct
// binary from GitHub releases, SHA256-verifies it, and places it in a cache
// directory so subsequent ResolveUV calls find it.
//
// The downloaded uv is ~15MB — a single static binary with no dependencies.
// It can install Python (`uv python install`), manage venvs (`uv venv`), and
// run tools (`uvx`). By bundling or auto-installing just uv, FairPeer gains
// the full Python ecosystem without shipping Python itself.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// UVVersion is the pinned uv release version to download.
const UVVersion = "0.5.11"

// uvRepo is the GitHub repo for download URLs.
const uvRepo = "astral-sh/uv"

// uvCacheDir returns the cache directory for the downloaded uv binary.
// Uses the OS cache dir (same convention as codegraph).
func uvCacheDir() string {
	cache, err := os.UserCacheDir()
	if err != nil || cache == "" {
		// Fallback to home/.fairpeer/cache.
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".fairpeer", "cache", "uv", UVVersion)
	}
	return filepath.Join(cache, "fairpeer", "uv", UVVersion)
}

// Install downloads uv to the cache directory. Safe to call from a goroutine
// at startup. Idempotent: if the binary already exists in cache, returns nil.
// The downloaded archive is SHA256-verified against the release's published
// .sha256 sidecar (skipped with a warning when the sidecar is absent), and a
// successful install resets the package-level uv/python resolution caches so
// the current process picks up the fresh install immediately.
func Install(ctx context.Context) error {
	cacheDir := uvCacheDir()
	exeName := uvNames()[0] // "uv" or "uv.exe"
	target := filepath.Join(cacheDir, exeName)

	// Already installed?
	if isExec(target) {
		return nil
	}

	// Download URL: https://github.com/astral-sh/uv/releases/download/0.5.11/uv-x86_64-pc-windows-msvc.zip
	// or uv-aarch64-apple-darwin.tar.gz etc.
	assetName, ext, err := uvAssetName()
	if err != nil {
		return err
	}
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		uvRepo, UVVersion, assetName)

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("uv install: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("uv install: download %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("uv install: HTTP %d for %s", resp.StatusCode, downloadURL)
	}

	// Read into memory (uv binary is ~15MB).
	body, err := io.ReadAll(io.LimitReader(resp.Body, 30<<20))
	if err != nil {
		return fmt.Errorf("uv install: read body: %w", err)
	}

	// SHA256-verify the ARCHIVE against astral's published checksum before it
	// is trusted (fulfills the docstring's long-standing claim). The sidecar
	// rides next to the asset as <asset-url>.sha256 and hashes the archive
	// bytes, so the comparison happens pre-extraction. A 404 (offline mirror,
	// release re-cut without checksums) skips verification with a warning
	// instead of failing, so mirrored setups keep working.
	want, err := fetchUVChecksum(ctx, downloadURL+".sha256")
	if err != nil {
		return fmt.Errorf("uv install: fetch checksum: %w", err)
	}
	if want == "" {
		slog.Warn("uv install: no published SHA256 found, skipping checksum verification", "url", downloadURL)
	} else if got := sha256Hex(body); got != want {
		return fmt.Errorf("uv install: SHA256 mismatch: want %s, got %s", want, got)
	}

	// Extract the uv binary from the archive.
	binary, err := extractUV(body, ext)
	if err != nil {
		return fmt.Errorf("uv install: extract: %w", err)
	}

	// Write to cache.
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("uv install: mkdir %s: %w", cacheDir, err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return fmt.Errorf("uv install: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("uv install: rename: %w", err)
	}

	// The binary is in place: drop this process's cached uv/python resolution
	// (likely "not found" — that's why Install ran) so the very next
	// ResolveUV/ResolvePython picks up the fresh download instead of waiting
	// for the next launch.
	resetRuntimeResolution()

	return nil
}

// fetchUVChecksum downloads and parses a published .sha256 sidecar
// ("<hex>[  <filename>]"). Any non-200 status — 404 above all — yields ("",
// nil): the caller skips verification with a warning rather than failing, so
// releases (or mirrors) that don't publish checksums keep installing.
func fetchUVChecksum(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", err
	}
	// Standard checksums(1) format is "<hex>  <name>"; tolerate bare hex and
	// any whitespace, and normalize case (uppercase digests exist in the wild).
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return "", nil
	}
	return strings.ToLower(fields[0]), nil
}

// uvAssetName returns the download asset filename and archive type for the
// current platform. Every released uv triple is mapped explicitly — no default
// that quietly claims linux-gnu for an unrelated GOOS — so an unsupported
// platform surfaces as a clear error instead of a bogus download URL that
// 404s (or worse, a binary built for the wrong ABI).
func uvAssetName() (name, ext string, err error) {
	goos := runtime.GOOS
	arch := runtime.GOARCH

	// Map Go arch to uv's naming.
	var archName string
	switch arch {
	case "amd64":
		archName = "x86_64"
	case "arm64":
		archName = "aarch64"
	default:
		return "", "", fmt.Errorf("uv auto-install unsupported on %s/%s", goos, arch)
	}

	switch goos {
	case "windows":
		return fmt.Sprintf("uv-%s-pc-windows-msvc.zip", archName), "zip", nil
	case "darwin":
		return fmt.Sprintf("uv-%s-apple-darwin.tar.gz", archName), "tar.gz", nil
	case "linux":
		return fmt.Sprintf("uv-%s-unknown-linux-gnu.tar.gz", archName), "tar.gz", nil
	default:
		return "", "", fmt.Errorf("uv auto-install unsupported on %s/%s", goos, arch)
	}
}

// extractUV extracts the uv binary from a zip or tar.gz archive.
// uv releases contain the binary at the root (zip) or under uv-<ver>-<arch>/
// (tar.gz).
func extractUV(body []byte, ext string) ([]byte, error) {
	switch ext {
	case "zip":
		return extractFromZip(body)
	case "tar.gz":
		return extractFromTarGz(body)
	default:
		return nil, fmt.Errorf("unsupported archive type %q", ext)
	}
}

// extractFromZip finds uv.exe / uv inside a zip archive (bytes in memory).
func extractFromZip(body []byte) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	uvExe := uvNames()[0]
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if strings.EqualFold(base, uvExe) && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("uv binary %q not found in zip", uvExe)
}

// extractFromTarGz finds uv inside a tar.gz archive.
func extractFromTarGz(body []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	uvExe := uvNames()[0]
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		base := filepath.Base(hdr.Name)
		if strings.EqualFold(base, uvExe) && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("uv binary %q not found in tar.gz", uvExe)
}

// cachedUV checks if uv was previously downloaded to the cache directory.
func cachedUV() (string, bool) {
	cacheDir := uvCacheDir()
	for _, name := range uvNames() {
		p := filepath.Join(cacheDir, name)
		if isExec(p) {
			return p, true
		}
	}
	return "", false
}

// sha256Hex computes the SHA-256 hex digest of data (compared against the
// release's published .sha256 sidecar by Install).
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
