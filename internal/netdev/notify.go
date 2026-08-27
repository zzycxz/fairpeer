package netdev

// notify.go — Finding 通知出口 v1（NETDEV_SPEC_V2 §5.2）：通用 webhook 的
// 异步 JSON POST（各 IM 的自定义机器人/网关都能吃）。飞书/钉钉/企微原生
// 模板、SMTP、升级链随后续批；deep_link 字段已带 fairpeer:// 深链。脱敏：
// POST 的是 Finding 本身（标题/设备/摘要），detail 截断——不含原始回显。

import (
	"bytes"
	"fmt"
	"encoding/json"
	"log/slog"
	"net/smtp"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

var (
	notifyMu     sync.Mutex
	notifyURL    string
	notifyMinSev string
	notifyFmt    string // generic | feishu | dingtalk | wecom
	notifySMTP   *smtpConfig
)

type smtpConfig struct {
	host, user, pass, from string
	port                   int
	to                     []string
}

// EnsureNotifier wires the webhook from config (idempotent; SharedManager
// calls it on every config load so settings changes go live without restart).
func EnsureNotifier(cfg *config.Config) {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	if cfg == nil || cfg.NetDev.NotifyWebhook == "" {
		notifyURL, notifyMinSev = "", ""
		return
	}
	notifyURL = strings.TrimSpace(cfg.NetDev.NotifyWebhook)
	notifyMinSev = strings.TrimSpace(cfg.NetDev.NotifyMinSeverity)
	if notifyMinSev == "" {
		notifyMinSev = "warning"
	}
	notifyFmt = strings.TrimSpace(cfg.NetDev.NotifyFormat)
	notifySMTP = nil
	if h := strings.TrimSpace(cfg.NetDev.NotifySMTPHost); h != "" && len(cfg.NetDev.NotifySMTPTo) > 0 {
		pass := ""
		if env := strings.TrimSpace(cfg.NetDev.NotifySMTPPassEnv); env != "" {
			if v, ok, _ := secretGetter(SecretKindPassword, env); ok {
				pass = v
			}
		}
		notifySMTP = &smtpConfig{host: h, user: cfg.NetDev.NotifySMTPUser, pass: pass, from: cfg.NetDev.NotifySMTPFrom, port: cfg.NetDev.NotifySMTPPort, to: cfg.NetDev.NotifySMTPTo}
		if notifySMTP.port == 0 {
			notifySMTP.port = 587
		}
	}
}

var sevRank = map[string]int{"info": 0, "warning": 1, "critical": 2}

// notifyFindingAsync fires the webhook for a new Finding when configured and
// the severity clears the bar. Best-effort: failures log, never block.
func notifyFindingAsync(f *Finding) {
	notifyMu.Lock()
	url, min, fmt_, smc := notifyURL, notifyMinSev, notifyFmt, notifySMTP
	notifyMu.Unlock()
	if url == "" || f == nil {
		return
	}
	if sevRank[f.Severity] < sevRank[min] {
		return
	}
	go func() {
		detail := f.Detail
		if len(detail) > 500 {
			detail = detail[:500] + "…"
		}
		if smc != nil {
			go smtpSendFinding(smc, f, detail)
		}
		text := fmt.Sprintf("[fairpeer 运维] %s（%s，%s）%s\n%s", f.Title, f.Severity, strings.Join(f.Devices, "、"), "fairpeer://finding/"+f.ID, detail)
		var body []byte
		var err error
		switch fmt_ {
		case "feishu":
			body, err = json.Marshal(map[string]any{"msg_type": "text", "content": map[string]string{"text": text}})
		case "dingtalk", "wecom":
			body, err = json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": text}})
		default:
			body, err = json.Marshal(map[string]any{
				"source":    "fairpeer-netdev",
				"kind":      "finding",
				"id":        f.ID,
				"title":     f.Title,
				"severity":  f.Severity,
				"devices":   f.Devices,
				"detail":    detail,
				"deep_link": "fairpeer://finding/" + f.ID,
				"created_at": f.CreatedAt.Format(time.RFC3339),
			})
		}
		if err != nil {
			return
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			slog.Warn("netdev notify webhook failed", "err", err)
			return
		}
		_ = resp.Body.Close()
	}()
}


// smtpSendFinding mails the finding (plain text, same truncation as the
// webhook). Best-effort like the webhook: failures log, never block.
func smtpSendFinding(c *smtpConfig, f *Finding, detail string) {
	subject := fmt.Sprintf("[fairpeer 运维] %s（%s，%s）", f.Title, f.Severity, strings.Join(f.Devices, "、"))
	body := fmt.Sprintf("%s\n\n%s\n%s", subject, detail, "fairpeer://finding/"+f.ID)
	msg := []byte("To: " + strings.Join(c.to, ",") + "\r\n" +
		"From: " + c.from + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		body)
	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	var auth smtp.Auth
	if c.user != "" {
		auth = smtp.PlainAuth("", c.user, c.pass, c.host)
	}
	err := smtp.SendMail(addr, auth, c.from, c.to, msg)
	if err != nil {
		slog.Warn("netdev notify smtp failed", "err", err)
	}
}
