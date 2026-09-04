package main

// remote_wsl_types.go — wslDistro is platform-neutral so the non-Windows stub
// (remote_wsl_other.go) can still satisfy ListWSLDistros's signature; the
// Windows-only implementation lives in remote_wsl_windows.go.

// wslDistro is one installed distro.
type wslDistro struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Version int    `json:"version"`
	Default bool   `json:"default"`
}
