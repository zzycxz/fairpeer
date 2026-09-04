package netdev

// sftp.go — SFTP 只读下载（NETDEV_SPEC_V2 §6.2）：文件拉取方向（日志导出、
// 配置备份取回、证据收集）。上传方向只存在于变更步骤（file-upload），本文件
// 不存在任何写路径。路径白名单沿用 log_paths + /var/log。

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
)

const sftpMaxBytes = 200 << 20 // 200 MB hard cap

// SFTPDownload reads one file from a device over the existing SSH transport.
// Returns the content (bounded). The path must sit inside the device's
// log_paths whitelist or /var/log (§6.2: 路径浏览限白名单).
func (m *Manager) SFTPDownload(ctx context.Context, deviceName, remotePath string) ([]byte, error) {
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return nil, fmt.Errorf("device %q is not in the inventory", deviceName)
	}
	if !logPathAllowed(remotePath, LogAllowedRoots(d)) {
		return nil, fmt.Errorf("path %q is outside the device's whitelist (/var/log or log_paths — add it in 运维设置)", remotePath)
	}
	// v1: read via the sealed exec path (cat with log-whitelist override,
	// capped output) — the dedicated SFTP subsystem channel lands with the
	// frontend file browser; the seal semantics are identical either way.
	clean := path.Clean(remotePath)
	res := m.Exec(ctx, deviceName, "cat "+clean)
	if res.Refused {
		return nil, fmt.Errorf("%s", res.Refusal)
	}
	if res.IsError {
		return nil, fmt.Errorf("device error reading %s", clean)
	}
	out := []byte(res.Output)
	if len(out) > sftpMaxBytes {
		out = out[:sftpMaxBytes]
	}
	// Audit the download (§6.2 审计确认).
	_ = AppendAudit(Audit{
		Time:    time.Now(),
		Device:  deviceName,
		Command: "sftp-download " + clean,
		Class:   "read",
		Status:  AuditOK,
	})
	return out, nil
}

// SFTPBrowse lists a whitelisted directory (ls, sealed).
func (m *Manager) SFTPBrowse(ctx context.Context, deviceName, dirPath string) ([]string, error) {
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return nil, fmt.Errorf("device %q is not in the inventory", deviceName)
	}
	if !logPathAllowed(dirPath, LogAllowedRoots(d)) {
		return nil, fmt.Errorf("path %q is outside the whitelist", dirPath)
	}
	res := m.Exec(ctx, deviceName, "ls "+path.Clean(dirPath))
	if res.Refused || res.IsError {
		return nil, fmt.Errorf("%s", res.Refusal)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(res.Output), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}
