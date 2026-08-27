package netdev

// report.go — 报告族 v1 补齐（NETDEV_SPEC_V2 §5.5）：周报 + 凭证盘点。
// 数据全部来自既有面（审计 JSONL / Findings / 设备清单 + 密钥库状态）。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// AuditEntry mirrors the JSONL line shape (read-side only).
type auditEntryLite struct {
	Time    time.Time `json:"time"`
	Device  string    `json:"device"`
	Command string    `json:"command"`
	Class   string    `json:"class"`
}

func readAuditSince(since time.Time) []auditEntryLite {
	f, err := os.Open(AuditPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []auditEntryLite
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		var e auditEntryLite
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Time.After(since) {
			out = append(out, e)
		}
	}
	return out
}

// WeeklyReport builds the weekly ops digest (markdown).
func (m *Manager) WeeklyReport() string {
	week := time.Now().Add(-7 * 24 * time.Hour)
	entries := readAuditSince(week)
	reads, writes, refusals := 0, 0, 0
	for _, e := range entries {
		switch e.Class {
		case "read":
			reads++
		case "write", "proposal-write", "proposal-rollback":
			writes++
		case "guardrail":
			refusals++
		}
	}

	fs, _ := ListFindings()
	var fresh []*Finding
	sev := map[string]int{}
	for _, f := range fs {
		if f.CreatedAt.After(week) {
			fresh = append(fresh, f)
			sev[f.Severity]++
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].CreatedAt.After(fresh[j].CreatedAt) })

	var b strings.Builder
	b.WriteString("# 运维周报\n\n")
	b.WriteString(fmt.Sprintf("统计窗口：近 7 天（%s ~ %s）\n\n", week.Format("01-02 15:04"), time.Now().Format("01-02 15:04")))
	b.WriteString(fmt.Sprintf("- 命令总量：%d（读 %d / 写路径 %d / 护栏拦截 %d）\n", len(entries), reads, writes, refusals))
	b.WriteString(fmt.Sprintf("- 新增 Finding：%d（critical %d / warning %d / info %d）\n", len(fresh), sev["critical"], sev["warning"], sev["info"]))
	b.WriteString(fmt.Sprintf("- 纳管设备：%d 台\n\n", len(m.cfg.NetDev.Devices)))

	b.WriteString("## 本周 Top Finding\n\n")
	for i, f := range fresh {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("- …另有 %d 条\n", len(fresh)-10))
			break
		}
		b.WriteString(fmt.Sprintf("- **%s**（%s，%s）：%s\n", f.Title, f.Severity, strings.Join(f.Devices, "、"), firstLine(f.Detail)))
	}
	return b.String()
}

// CredentialInventory builds the credential-health page (markdown): every
// device's auth surface + which secrets are actually set.
func (m *Manager) CredentialInventory() string {
	var rows []string
	for _, d := range m.cfg.NetDev.Devices {
		var parts []string
		switch {
		case d.Kind == "k8s":
			set := false
			if d.K8s != nil && d.K8s.KubeconfigEnv != "" {
				_, set, _ = secretGetter(SecretKindKubeconfig, d.K8s.KubeconfigEnv)
			}
			parts = append(parts, "kubeconfig:"+setOrMiss(set))
		case d.Kind == "firewall":
			set := false
			if d.Fw != nil && d.Fw.ApiTokenEnv != "" {
				_, set, _ = secretGetter(SecretKindAPIToken, d.Fw.ApiTokenEnv)
			}
			parts = append(parts, "api-token:"+setOrMiss(set))
		case d.Kind == "docker":
			parts = append(parts, "socket")
		default:
			if d.PasswordEnv != "" {
				_, set, _ := secretGetter(SecretKindPassword, d.PasswordEnv)
				parts = append(parts, "密码:"+setOrMiss(set))
			}
			if d.IdentityFile != "" {
				parts = append(parts, "密钥文件")
			}
			if d.SNMP != nil && d.SNMP.CommunityEnv != "" {
				_, set, _ := secretGetter(SecretKindPassword, d.SNMP.CommunityEnv)
				parts = append(parts, "SNMP:"+setOrMiss(set))
			}
			if d.PasswordEnv == "" && d.IdentityFile == "" && !d.UseSSHConfig {
				parts = append(parts, "⚠ 无凭证")
			}
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %s |", d.Name, d.Vendor, strings.Join(parts, " / ")))
	}
	var b strings.Builder
	b.WriteString("# 凭证盘点\n\n| 设备 | 类型 | 凭证面 |\n|---|---|---|\n")
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n\n「未配置」= 声明了密钥名但密钥库无值（设置 → 运维 补录）；「⚠ 无凭证」= 既无密码也无私钥，连接将失败。\n")
	return b.String()
}

func setOrMiss(set bool) string {
	if set {
		return "✓"
	}
	return "未配置"
}
