package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	"golang.org/x/mod/semver"

	"github.com/zzycxz/fairpeer/desktop/internal/update"
	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netclient"
	"github.com/zzycxz/fairpeer/internal/proc"
)

// updater.go is the transport-free core of the desktop auto-updater: manifest
// fetch, version comparison, signed download, and per-platform apply/relaunch. It
// has no Wails dependency so the logic is unit-tested directly; updater_app.go is
// the thin Wails binding that wires these into App methods and progress events.

// Manifest endpoints — GitHub releases as the sole source.
const (
	ghReleasesBase = "https://github.com/zzycxz/fairpeer/releases"
	httpTimeout    = 15 * time.Second
)

// manifestEndpoints returns the manifest URLs in fetch order. When
// [network] update_base_url is configured it REPLACES the GitHub/ghproxy
// endpoints entirely — air-gapped deployments must never touch github.com —
// and the mirror layout (…/latest/download/latest.json, canary equivalent)
// is the operator's contract. Otherwise: GitHub Releases first, then the
// ghproxy.net mirror as fallback.
func manifestEndpoints() []string {
	if base := updateBaseOverride(); base != "" {
		base = strings.TrimRight(base, "/")
		if channel == "canary" {
			return []string{base + "/download/canary/latest.json"}
		}
		return []string{base + "/latest/download/latest.json"}
	}
	if channel == "canary" {
		return []string{
			ghReleasesBase + "/download/canary/latest.json",
			"https://ghproxy.net/" + ghReleasesBase + "/download/canary/latest.json",
		}
	}
	return []string{
		ghReleasesBase + "/latest/download/latest.json",
		"https://ghproxy.net/" + ghReleasesBase + "/latest/download/latest.json",
	}
}

// updateBaseOverride reads the configured self-update mirror base. A config
// load failure means "no override" — the default endpoints already handle
// their own errors downstream.
func updateBaseOverride() string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	return cfg.NetworkUpdateBaseURL()
}

// downloadPage is the human-facing releases page shown when self-update is
// unavailable (macOS) or the manifest omits its own link.
func downloadPage() string {
	if channel == "canary" {
		return ghReleasesBase + "/tag/canary"
	}
	return ghReleasesBase + "/latest"
}

// UpdateInfo is the CheckUpdate result that drives the frontend's update banner.
type UpdateInfo struct {
	Available     bool   `json:"available"`
	Current       string `json:"current"`
	Latest        string `json:"latest"`
	Notes         string `json:"notes"`
	CanSelfUpdate bool   `json:"canSelfUpdate"` // win/linux true; macOS false (unsigned → manual download)
	DownloadURL   string `json:"downloadUrl"`   // human-facing releases page (macOS path / fallback link)
	AssetSize     int64  `json:"assetSize"`     // running platform's artifact size, for the progress bar
	Err           string `json:"err,omitempty"` // set when the check itself failed (both endpoints down)
}

// updateProgress is the payload of the "updater:progress" Wails event emitted
// throughout ApplyUpdate.
type updateProgress struct {
	Phase    string `json:"phase"` // downloading | verifying | applying | done | error
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Err      string `json:"err,omitempty"`
}

func httpClient() (*http.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return netclient.NewHTTPClient(cfg.NetworkProxySpec(), netclient.TransportOptions{})
}

// canSelfUpdate reports whether in-place update is possible. macOS is excluded:
// without a Developer ID signature + notarization, swapping the .app and relaunching
// trips Gatekeeper, so macOS falls back to a manual download.
func canSelfUpdate() bool { return runtime.GOOS != "darwin" }

// normalizeVersion canonicalizes a version to semver "vX.Y.Z". It reports ok=false
// for the un-injected "dev" build (and anything not valid semver), so a dev build
// never prompts to update.
func normalizeVersion(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return "", false
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	return semver.Canonical(v), true
}

// fetchManifest pulls latest.json from the release endpoints and decodes it.
// The returned error is deliberately short — it surfaces verbatim in the UI
// ("更新失败：…"), where a full per-endpoint URL chain is noise. The complete
// failure detail (every URL + transport error) goes to the log instead.
func fetchManifest(ctx context.Context, c *http.Client) (*update.Manifest, error) {
	var errs []string

	for _, url := range manifestEndpoints() {
		b, err := fetchBytes(ctx, c, url)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", url, err))
			continue
		}
		var m update.Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			errs = append(errs, fmt.Sprintf("%s decode: %v", url, err))
			continue
		}
		return &m, nil
	}
	slog.Warn("update: manifest fetch failed", "endpoints", len(errs), "errors", errs)
	return nil, fmt.Errorf("update: manifest fetch failed (%d endpoints unreachable; see log for detail)", len(errs))
}

// evaluate compares the running version against the manifest and builds the
// frontend-facing result. Pure (no I/O) so the comparison is unit-tested.
func evaluate(current string, m *update.Manifest) UpdateInfo {
	page := m.DownloadPage
	if page == "" {
		page = downloadPage()
	}
	info := UpdateInfo{
		Current:       current,
		Latest:        m.Version,
		Notes:         m.Notes,
		CanSelfUpdate: canSelfUpdate(),
		DownloadURL:   page,
	}
	cur, okCur := normalizeVersion(current)
	latest, okLatest := normalizeVersion(m.Version)
	if !okLatest {
		info.Err = "manifest has no valid version"
		return info
	}
	// A dev/invalid running version never auto-prompts.
	if okCur && semver.Compare(latest, cur) > 0 {
		info.Available = true
	}
	if a, ok := m.Asset(); ok {
		info.AssetSize = a.Size
	}
	return info
}

// fetchBytes GETs a URL fully into memory.
func fetchBytes(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// download fetches url into memory, invoking onProgress as bytes arrive. total is
// the expected size for the progress denominator (overridden by Content-Length).
func download(ctx context.Context, c *http.Client, url string, total int64, onProgress func(received, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	var buf bytes.Buffer
	pr := &progressReader{r: resp.Body, total: total, onProgress: onProgress}
	if _, err := io.Copy(&buf, pr); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// progressReader reports cumulative bytes read, throttled so the event channel
// isn't flooded.
type progressReader struct {
	r          io.Reader
	received   int64
	total      int64
	lastEmit   int64
	onProgress func(received, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.received += int64(n)
	// Emit roughly every 256 KiB, and always on the final read (io.EOF).
	if p.onProgress != nil && (p.received-p.lastEmit >= 256<<10 || err == io.EOF) {
		p.lastEmit = p.received
		p.onProgress(p.received, p.total)
	}
	return n, err
}

// checkSHA256 verifies data's digest matches the lowercase-hex want.
func checkSHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, want) {
		return fmt.Errorf("update: sha256 mismatch: got %s want %s", got, want)
	}
	return nil
}

// extractBinary pulls a single named regular file out of a .tar.gz blob.
func extractBinary(targz []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && (h.Name == name || strings.HasSuffix(h.Name, "/"+name)) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("update: %q not found in archive", name)
}

// applyLinux replaces the running binary with the one inside the downloaded
// tar.gz; the caller relaunches afterwards. A binary under a system prefix
// (/usr, /opt — the deb/rpm layout) is root-owned, so the in-place swap would
// fail with EACCES; refuse with a clear pointer to the package manager
// instead of a raw permission error after a 100 MB download.
func applyLinux(targz []byte) error {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		if strings.HasPrefix(exe, "/usr/") || strings.HasPrefix(exe, "/opt/") {
			return fmt.Errorf("update: this copy was installed system-wide (%s) — update it with your package manager (apt/rpm), or download the portable build from the releases page", exe)
		}
	}
	bin, err := extractBinary(targz, "fairpeer-desktop")
	if err != nil {
		return err
	}
	return selfupdate.Apply(bytes.NewReader(bin), selfupdate.Options{})
}

// applyWindows stages the downloaded NSIS installer (a temp file OUTSIDE the
// install dir — the asset is an installer, never the app binary, so it must
// not be swapped over the running exe) behind a batch wrapper that waits for
// this process to exit, runs the installer silently (/S) pinned to the
// running app's own directory (/D=, unquoted final token per NSIS), then
// relaunches the app. The caller shuts down and exits so the installer can
// replace the binary in place (issue #3217) — this also covers upgrades from
// builds that predate the registry InstallLocation.
func applyWindows(newExe []byte) error {
	currentExe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(currentExe); err == nil {
		currentExe = resolved
	}

	tmp, err := os.CreateTemp("", "fairpeer-update-*.exe")
	if err != nil {
		return err
	}
	instPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(instPath, newExe, 0o755); err != nil {
		return err
	}

	// Wait for the running app by PID before installing: a silent installer
	// hitting the locked exe would fail outright. tasklist|find matches the
	// PID digits, so it is locale-independent.
	pid := os.Getpid()
	// NSIS takes /D= as the verbatim rest of the line — keep it last and
	// unquoted; emit it only when the dir resolved.
	instLine := `start /wait "" "` + instPath + `" /S`
	if dir := currentInstallDir(); dir != "" {
		instLine += ` /D=` + dir
	}
	batPath := filepath.Join(os.TempDir(), "fairpeer-update.bat")
	bat := fmt.Sprintf(`@echo off
:wait
timeout /t 1 /nobreak >nul
tasklist /FI "PID eq %d" | find "%d" >nul
if not errorlevel 1 goto wait
%s
del "%s" >nul 2>&1
start "" "%s"
del "%%~f0" >nul 2>&1
`, pid, pid, instLine, instPath, currentExe)
	if err := os.WriteFile(batPath, []byte(bat), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", batPath)
	proc.HideWindow(cmd)
	return cmd.Start()
}

// currentInstallDir is the directory of the running executable — the location a
// Windows update must overwrite. Empty when it can't be resolved, in which case
// the installer falls back to its own InstallDir logic.
func currentInstallDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

// relaunch starts a fresh copy of the (just-replaced) executable.
func relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Start()
}
