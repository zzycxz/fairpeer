package config

// projectSecuritySnapshot pins the execution/security surfaces an untrusted
// project TOML must not override (G3 v1 scope): the statusline runs an
// arbitrary shell command, and Permissions/Sandbox are the approval gate and
// OS jail themselves. Model/provider overrides stay deferred (v2, data-exfil
// surface). Project hooks are gated separately by internal/hook trust.
type projectSecuritySnapshot struct {
	Statusline  StatuslineConfig
	Permissions PermissionsConfig
	Sandbox     SandboxConfig
}

func snapshotProjectSecurity(cfg *Config) projectSecuritySnapshot {
	return projectSecuritySnapshot{Statusline: cfg.Statusline, Permissions: cfg.Permissions, Sandbox: cfg.Sandbox}
}

func restoreProjectSecurity(cfg *Config, s projectSecuritySnapshot) {
	cfg.Statusline, cfg.Permissions, cfg.Sandbox = s.Statusline, s.Permissions, s.Sandbox
}
