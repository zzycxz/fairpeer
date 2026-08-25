package main

import (
	"path/filepath"
	"strings"
)

// RemoteRef identifies a remote workspace a tab's controller lives against.
// P1 delivers WSL; the shape is deliberately transport-neutral so Docker/SSH/
// Server reuse the same session plumbing.
type RemoteRef struct {
	// Kind selects the transport: "wsl" (P1). "docker" | "ssh" | "server" are
	// reserved for follow-up work packages.
	Kind string `json:"kind"`
	// Target names the connection target within the kind: WSL distro name,
	// container name/id, "user@host:port", or server URL.
	Target string `json:"target"`
	// User optionally selects the remote-side user (WSL distro user; SSH
	// username).
	User string `json:"user,omitempty"`
	// KeyPath is an SSH private key path (not secret — the path only).
	KeyPath string `json:"keyPath,omitempty"`
	// Label is the human display name (e.g. "Ubuntu · /home/me/proj").
	Label string `json:"label,omitempty"`
}

// wslUncPrefixes are the Windows UNC roots a WSL distro is reachable through.
var wslUncPrefixes = []string{`\\wsl$\`, `\\wsl.localhost\`}

// wslUncRoot converts a Windows UNC path under \\wsl$\<distro>\... into the
// Linux-side path, plus the distro name. ok=false when path is not a WSL UNC
// path.
func wslUncRoot(winPath string) (distro, linuxRoot string, ok bool) {
	for _, prefix := range wslUncPrefixes {
		if !strings.HasPrefix(winPath, prefix) {
			continue
		}
		rest := strings.TrimPrefix(winPath, prefix)
		distro, rest, found := strings.Cut(rest, `\`)
		if !found || distro == "" || rest == "" {
			return "", "", false
		}
		return distro, "/" + strings.ReplaceAll(rest, `\`, "/"), true
	}
	return "", "", false
}

// wslDistroUNC converts a Linux-side path back to its Windows UNC form.
func wslDistroUNC(distro, linuxPath string) string {
	if !strings.HasPrefix(linuxPath, "/") {
		linuxPath = "/" + linuxPath
	}
	return `\\wsl$` + string(filepath.Separator) + distro + strings.ReplaceAll(linuxPath, "/", `\`)
}

// remoteProjectSlugKey disambiguates remote projects from local roots in the
// projects index and session slugs (config.WorkspaceSlug flattens local paths;
// remote entries prefix the kind+target so they can never collide).
func remoteProjectSlugKey(ref RemoteRef, remoteRoot string) string {
	target := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "@", "-").Replace(ref.Target)
	user := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(ref.User)
	parts := []string{"remote", ref.Kind, target}
	if user != "" {
		parts = append(parts, user)
	}
	flat := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-").Replace(strings.Trim(remoteRoot, "/"))
	parts = append(parts, flat)
	return strings.ToLower(strings.Join(parts, "-"))
}
