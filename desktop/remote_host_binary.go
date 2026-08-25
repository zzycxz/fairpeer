package main

// remote_host_binary.go — locating the prebuilt Linux CLI (the remote host
// binary) on the desktop side, shared by every transport. Release builds ship
// it in the hosts cache; dev builds look beside the running exe and in the
// repo tree. scripts/build-hosts.sh populates the cache.

import (
	"fmt"
	"os"
	"path/filepath"
)

// localLinuxHostBinaryFor finds the prebuilt Linux CLI for a GOARCH. arch is
// "amd64" or "arm64" (translate from uname -m before calling).
func localLinuxHostBinaryFor(arch string) (string, error) {
	var candidates []string
	if cache, err := os.UserCacheDir(); err == nil {
		candidates = append(candidates, filepath.Join(cache, "fairpeer", "hosts", "fairpeer-linux-"+arch))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(exe), "hosts", "fairpeer-linux-"+arch),
			filepath.Join(filepath.Dir(exe), "..", "..", "hosts", "fairpeer-linux-"+arch),
		)
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("Linux host binary not found (looked in %%LOCALAPPDATA%%\\fairpeer\\hosts and beside the desktop exe). Run scripts/build-hosts.sh (or `GOOS=linux GOARCH=%s go build -o <cache>/fairpeer/hosts/fairpeer-linux-%s ./cmd/fairpeer`) and retry", arch, arch)
}

// goarchFromUname maps `uname -m` output onto a GOARCH.
func goarchFromUname(machine string) string {
	switch machine {
	case "aarch64", "arm64":
		return "arm64"
	default:
		return "amd64"
	}
}
