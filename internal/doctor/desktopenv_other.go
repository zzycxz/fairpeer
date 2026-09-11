//go:build !linux

package doctor

// collectDesktopEnv is the non-Linux stub: the domestic-OS desktop checks
// (webkit2gtk, display server, IME env, CAP_NET_RAW…) are Linux concerns, so
// other platforms report an empty section.
func collectDesktopEnv() DesktopEnvReport { return DesktopEnvReport{} }
