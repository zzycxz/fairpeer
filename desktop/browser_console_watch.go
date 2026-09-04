package main

// browser_console_watch.go — the download-and-judge tail of the ops browser:
//
//   - BrowserConsoleResolveTimeRange: phrase → literal range (editor preview).
//   - BrowserConsoleAnalyzeDownload: exported workbook → LLM alert-triage
//     report (fast model via consoleProviderChat; char-budgeted table).
//   - BrowserConsoleWatchStart/Stop/State: a Go-side recurring round that
//     runs a browser-flow skill (rolling "最近 N 分钟" window), waits for the
//     Excel export download, and AI-triages it — the standing-watch scenario
//     ("每隔5分钟轮询下载判断"). Rounds serialize with manual trial runs via
//     consoleGate; a busy gate marks the round skipped instead of queueing.
//
// Rounds are NOT persisted — the watch lives and dies with the app session.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

// --- time range preview -------------------------------------------------------

// BrowserConsoleResolveTimeRange resolves a whole-value time-range phrase
// ("最近5分钟") into the literal "start - end" string; "" when the text is not
// a recognized phrase (the editor then passes it through unchanged).
func (a *App) BrowserConsoleResolveTimeRange(text string) (string, error) {
	if r, ok := builtin.ResolveTimeRange(time.Now(), strings.TrimSpace(text)); ok {
		return r, nil
	}
	return "", nil
}

// --- AI triage of an exported workbook ----------------------------------------

// BrowserDownloadAnalysis is the AI alert-triage result for one exported file.
type BrowserDownloadAnalysis struct {
	File             string   `json:"file"`
	Rows             int      `json:"rows"`       // total data rows in the workbook
	ShownRows        int      `json:"shown_rows"` // rows actually sent to the model
	Report           string   `json:"report"`     // markdown triage report
	CompromisedHosts []string `json:"compromised_hosts,omitempty"`
	AttentionCount   int      `json:"attention_count,omitempty"`
	Severity         string   `json:"severity,omitempty"`
}

// alertVerdict is the machine-readable conclusion parsed from the report's
// trailing ```json block — the notification gate ("确认失陷主机才通知")
// keys off it instead of scraping prose. The schema is shared by both
// analysis kinds: 失陷主机 (alerts) and 关键发现 (generic) coalesce into
// Findings; 需关注告警数/需关注条数 coalesce into AttentionCount.
type alertVerdict struct {
	CompromisedHosts []string `json:"失陷主机"`
	KeyFindings      []string `json:"关键发现"`
	AttentionAlerts  int      `json:"需关注告警数"`
	AttentionItems   int      `json:"需关注条数"`
	Severity         string   `json:"最高等级"`
	Notify           bool     `json:"需通知"`
	Reason           string   `json:"通知理由"`
}

// findings returns the round's confirmed-compromised hosts (alerts kind) or
// key findings (generic kind) — the gate for the "compromised" notify policy.
func (v alertVerdict) findings() []string {
	if len(v.CompromisedHosts) > 0 {
		return v.CompromisedHosts
	}
	return v.KeyFindings
}

// attention returns the attention-worthy count for either schema spelling.
func (v alertVerdict) attention() int {
	if v.AttentionAlerts > 0 {
		return v.AttentionAlerts
	}
	return v.AttentionItems
}

// verdictJSONRe grabs the LAST fenced json block in the report (the verdict
// contract mandates it at the end; earlier fences could be data samples).
var verdictJSONRe = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")

func parseAlertVerdict(report string) (alertVerdict, bool) {
	matches := verdictJSONRe.FindAllStringSubmatch(report, -1)
	if len(matches) == 0 {
		return alertVerdict{}, false
	}
	var v alertVerdict
	if err := json.Unmarshal([]byte(matches[len(matches)-1][1]), &v); err != nil {
		return alertVerdict{}, false
	}
	return v, true
}

// analyzeTableCharBudget bounds the workbook text sent to the model (~tokens):
// big exports shrink row-wise until the table fits.
const analyzeTableCharBudget = 60_000

// Analysis kinds: the watch is skill-agnostic — a security-alert export gets
// the SIEM triage prompt, any other export gets a generic table analysis,
// and "none" just downloads without burning model tokens.
const (
	watchAnalysisAlerts  = "alerts"
	watchAnalysisGeneric = "generic"
	watchAnalysisNone    = "none"
)

// BrowserConsoleAnalyzeDownload runs AI analysis on a downloaded export
// (.xlsx / .csv). analysisKind: "alerts" (SIEM 告警研判，默认) or "generic"
// (通用表格研判). The table is char-budgeted; the report is markdown plus a
// machine-readable verdict block for notification routing.
func (a *App) BrowserConsoleAnalyzeDownload(path, analysisKind string) (BrowserDownloadAnalysis, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return BrowserDownloadAnalysis{}, fmt.Errorf("缺少文件路径")
	}
	if _, err := os.Stat(path); err != nil {
		return BrowserDownloadAnalysis{}, fmt.Errorf("文件不存在: %w", err)
	}
	if analysisKind != watchAnalysisGeneric {
		analysisKind = watchAnalysisAlerts
	}
	table, total, shown, err := workbookTableForLLM(path)
	if err != nil {
		return BrowserDownloadAnalysis{}, err
	}
	prompt := alertTriageSystemPrompt
	if analysisKind == watchAnalysisGeneric {
		prompt = genericTableAnalysisPrompt
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	report, err := a.consoleProviderChat(ctx, prompt,
		"以下是导出的表格（第一行为表头，数据行共 "+fmt.Sprint(total)+" 行，本次研判基于前 "+fmt.Sprint(shown)+" 行）：\n\n"+table)
	if err != nil {
		return BrowserDownloadAnalysis{}, fmt.Errorf("AI 研判失败: %w", err)
	}
	out := BrowserDownloadAnalysis{File: filepath.Base(path), Rows: total, ShownRows: shown, Report: report}
	if v, ok := parseAlertVerdict(report); ok {
		out.CompromisedHosts, out.AttentionCount, out.Severity = v.findings(), v.attention(), v.Severity
	}
	return out, nil
}

// workbookTableForLLM reads the workbook as a table and shrinks the row count
// until the text fits the char budget (min 50 rows). Returns the table, the
// workbook's total data-row count, and the row count actually included.
func workbookTableForLLM(path string) (string, int, int, error) {
	const startRows = 1000
	table, total, err := builtin.ReadWorkbookAsTable(path, startRows)
	if err != nil {
		return "", 0, 0, err
	}
	shown := startRows
	for len(table) > analyzeTableCharBudget && shown > 50 {
		shown = shown * analyzeTableCharBudget / len(table)
		if shown < 50 {
			shown = 50
		}
		table, total, err = builtin.ReadWorkbookAsTable(path, shown)
		if err != nil {
			return "", 0, 0, err
		}
	}
	return table, total, shown, nil
}

// alertTriageSystemPrompt is the standing instruction for export triage.
const alertTriageSystemPrompt = `你是网络安全告警研判助手。用户提供一份从态势感知/SIEM平台导出的告警表格（markdown 表格，第一行是表头）。

任务：逐条研判，判断哪些告警真实可疑、需要优先处置，哪些疑似误报或低优先级。

输出 markdown，固定结构：
## 概览
数据条数、时间跨度、告警类型/等级分布（1-3 句；若基于截断数据须注明"基于前 N 行"）
## 需关注告警
| 行号 | 时间 | 源/目的IP | 告警名称 | 研判理由 | 建议动作 |
只列值得关注的告警；若没有，写"本轮无需要关注的告警"并简述原因
## 疑似误报
简列行号与理由（内网互访、备份扫描、已知业务行为等模式）
## 建议动作
按优先级给出 2-5 条具体动作

规则：
- 只依据表格中的数据研判，严禁臆造不存在的行、IP 或告警
- 横向移动、可疑外联、爆破成功、特权提升、数据外传类模式优先关注
- 相同源/目的的重复告警合并研判，在行号列列出全部相关行号
- 结论拿不准时明确说"需人工复核"及缺什么信息

` + "报告正文结束后，必须另起一行输出一个 ```json 代码块（机器可读结论，用于通知路由），格式示例：\n" +
	"```json\n" +
	`{"失陷主机": ["10.0.0.5"], "需关注告警数": 3, "最高等级": "high", "需通知": true, "通知理由": "SSH 爆破成功且出现外联"}` + "\n" +
	"```\n" + `
字段要求：
- 失陷主机: 仅列"有明确失陷证据（爆破成功+异常外联/提权/落盘等）"的 IP 数组；没有确凿证据就给空数组，宁可漏报不可误报
- 需关注告警数: 「需关注告警」表格的行数（整数）
- 最高等级: critical|high|medium|low|none（本次需关注告警中的最高）
- 需通知: 是否建议立即通知安全负责人（布尔）
- 通知理由: 一句话`

// genericTableAnalysisPrompt is the skill-agnostic counterpart: any exported
// table (inventory, orders, logs, monitor data…) gets a structured read with
// the same verdict contract, so watch notifications work for every skill.
const genericTableAnalysisPrompt = `你是数据分析助手。用户提供一份导出的表格（markdown 表格，第一行是表头）——内容类型未知（可能是清单、日志、监控数据、工单等），先通过表头和样本行理解数据结构，再研判。

任务：识别表格中值得关注的行——异常值、越限、缺失、重复、可疑模式、与常理不符的条目——并给出可执行的结论。

输出 markdown，固定结构：
## 概览
数据结构（列含义）、条数、时间跨度（如有）、整体状况（1-3 句；若基于截断数据须注明"基于前 N 行"）
## 需关注条目
| 行号 | 关键列值 | 关注原因 | 建议动作 |
只列值得关注的行；若没有，写"本轮无需关注的条目"并简述原因
## 数据质量
缺失/重复/格式异常简述（无则写"无明显问题"）
## 建议动作
按优先级给出 2-5 条具体动作

规则：
- 只依据表格中的数据研判，严禁臆造不存在的行或值
- 结论拿不准时明确说"需人工复核"及缺什么信息

` + "报告正文结束后，必须另起一行输出一个 ```json 代码块（机器可读结论，用于通知路由），格式示例：\n" +
	"```json\n" +
	`{"关键发现": ["订单A0032金额异常"], "需关注条数": 2, "最高等级": "high", "需通知": true, "通知理由": "发现两笔金额异常订单"}` + "\n" +
	"```\n" + `
字段要求：
- 关键发现: 确凿、值得立即处理的发现数组（一两句话内）；没有确凿发现给空数组，宁可漏报不可误报
- 需关注条数: 「需关注条目」表格的行数（整数）
- 最高等级: critical|high|medium|low|none（本次关注条目中的最高）
- 需通知: 是否建议立即通知负责人（布尔）
- 通知理由: 一句话`

// --- standing watch -----------------------------------------------------------

// BrowserConsoleWatchStep is one step's outcome inside a watch round.
type BrowserConsoleWatchStep struct {
	Index  int    `json:"index"`
	Type   string `json:"type"`
	Status string `json:"status"` // running|waiting|done|failed
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// BrowserConsoleWatchRound is one completed (or skipped) watch cycle.
type BrowserConsoleWatchRound struct {
	StartedAt    string                    `json:"started_at"`
	FinishedAt   string                    `json:"finished_at,omitempty"`
	TimeRange    string                    `json:"time_range,omitempty"`
	Status       string                    `json:"status"` // running|done|failed|skipped
	Skipped      bool                      `json:"skipped,omitempty"`
	Note         string                    `json:"note,omitempty"`
	Error        string                    `json:"error,omitempty"`
	DownloadName string                    `json:"download_name,omitempty"`
	DownloadPath string                    `json:"download_path,omitempty"`
	Rows         int                       `json:"rows,omitempty"`
	Analysis     string                    `json:"analysis,omitempty"`
	Steps        []BrowserConsoleWatchStep `json:"steps,omitempty"`
	// Machine-readable triage verdict (parsed from the analysis report's
	// trailing JSON block) + the notification channels actually sent.
	CompromisedHosts []string `json:"compromised_hosts,omitempty"`
	AttentionCount   int      `json:"attention_count,omitempty"`
	Severity         string   `json:"severity,omitempty"`
	Notified         []string `json:"notified,omitempty"` // im|email|system
	NotifyError      string   `json:"notify_error,omitempty"`
}

// BrowserConsoleWatchNotify is a watch's delivery policy: WHICH rounds reach
// out (OnEvent) and through WHICH channels (IM bot chat / email / in-app).
// IMChatID carries the gateway dest ("platform:chatId", QQ 群
// "qq:chatType:chatId"); EmailAccount selects the configured SMTP sender
// (empty = default account).
type BrowserConsoleWatchNotify struct {
	OnEvent      string `json:"on_event"` // compromised|attention|always|never
	IMChatID     string `json:"im_chat_id,omitempty"`
	IMChatName   string `json:"im_chat_name,omitempty"`
	Email        string `json:"email,omitempty"`         // recipient
	EmailAccount string `json:"email_account,omitempty"` // sender account from settings
	System       bool   `json:"system,omitempty"`
}

// BrowserConsoleWatchConfig is a standing watch's full configuration — the
// shape the panel submits AND the persisted browser_watch.json record.
// Analysis picks the post-download treatment: "alerts" (SIEM 告警研判),
// "generic" (通用表格研判), "none" (仅下载，不耗模型).
type BrowserConsoleWatchConfig struct {
	Skill       string                    `json:"skill"`
	IntervalSec int                       `json:"interval_sec"`
	AnchorMin   string                    `json:"anchor_min,omitempty"` // "HH:MM" grid alignment; empty = floor(start, minute)
	Analysis    string                    `json:"analysis,omitempty"`   // alerts|generic|none (default alerts)
	Notify      BrowserConsoleWatchNotify `json:"notify"`
}

// BrowserConsoleWatchState is the watch's live configuration and tail.
type BrowserConsoleWatchState struct {
	Active      bool                       `json:"active"`
	Skill       string                     `json:"skill,omitempty"`
	IntervalSec int                        `json:"interval_sec,omitempty"`
	Anchor      string                     `json:"anchor,omitempty"`  // RFC3339
	NextAt      string                     `json:"next_at,omitempty"` // RFC3339
	Analysis    string                     `json:"analysis,omitempty"`
	Notify      *BrowserConsoleWatchNotify `json:"notify,omitempty"`
	LastRound   *BrowserConsoleWatchRound  `json:"last_round,omitempty"`
	Rounds      []BrowserConsoleWatchRound `json:"rounds,omitempty"`
}

// BrowserConsoleWatchEvent rides the "browser:watch" channel.
type BrowserConsoleWatchEvent struct {
	Type  string                    `json:"type"` // "state" | "round"
	State *BrowserConsoleWatchState `json:"state,omitempty"`
	Round *BrowserConsoleWatchRound `json:"round,omitempty"`
}

// watchHistoryMax bounds the retained rounds per app session.
const watchHistoryMax = 50

// watchMinIntervalSec is the floor for the round interval — below this the
// export+download tail (20s+) would overlap the next tick permanently.
const watchMinIntervalSec = 60

type consoleWatch struct {
	mu          sync.Mutex
	active      bool
	cfg         BrowserConsoleWatchConfig
	stop        chan struct{}
	anchor      time.Time                  // wall-clock grid origin
	nextAt      time.Time
	rounds      []BrowserConsoleWatchRound // newest first
	lastEnd     time.Time                  // end of the window the last round actually typed
}

var consoleWatchState consoleWatch

// watchMaxCatchup bounds how far back a catch-up window reaches: after a long
// pause nobody wants a 3-hour export; the residual gap is noted instead.
const watchMaxCatchup = 30 * time.Minute

// watchAlignedWindow derives one round's window from its SCHEDULED grid
// instant: [scheduled-interval, scheduled] — closed whole-minute intervals
// (start 22:38:00 → round 1 covers 22:33:00–22:38:00, round 2 at 22:43:00
// covers 22:38:00–22:43:00), fully decoupled from execution latency because
// the interval is already closed when the round fires. lastEnd reconciles
// skips: a stretch back to lastEnd (capped) covers gaps, a clamp forward
// removes overlap.
func watchAlignedWindow(scheduled, lastEnd time.Time, interval time.Duration) (start, end time.Time, catchup bool) {
	end = scheduled
	start = scheduled.Add(-interval)
	if lastEnd.IsZero() {
		return start, end, false
	}
	switch {
	case lastEnd.Equal(start):
		// Exactly contiguous — the normal grid case.
	case lastEnd.After(start):
		// Previous round already covered past this grid start (long catch-up,
		// re-run): clamp forward, overlap is never queried twice.
		start = lastEnd
	default:
		// Gap — skipped/failed rounds never typed their window. Stretch back
		// to lastEnd (capped) so no interval is silently lost.
		if end.Sub(lastEnd) > watchMaxCatchup {
			start = end.Add(-watchMaxCatchup)
		} else {
			start = lastEnd
		}
		catchup = true
	}
	return start, end, catchup
}

// nextWatchFire returns the first grid point anchor+N*interval strictly
// after `after` — wall-clock aligned firing (22:38 anchor, 5m interval fires
// at 22:43:00, 22:48:00…), immune to Ticker drift and slow-round skew.
func nextWatchFire(anchor time.Time, interval time.Duration, after time.Time) time.Time {
	if interval <= 0 {
		return after.Add(time.Minute)
	}
	n := after.Sub(anchor)/interval + 1
	if n < 1 {
		n = 1
	}
	return anchor.Add(time.Duration(n) * interval)
}

func (a *App) emitWatch(ev BrowserConsoleWatchEvent) {
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "browser:watch", ev)
	}
}

func (a *App) emitWatchStateLocked() {
	st := a.watchStateLocked()
	a.emitWatch(BrowserConsoleWatchEvent{Type: "state", State: &st})
}

// watchStateLocked snapshots the watch state; caller holds the mutex.
func (a *App) watchStateLocked() BrowserConsoleWatchState {
	st := BrowserConsoleWatchState{
		Active:      consoleWatchState.active,
		Skill:       consoleWatchState.cfg.Skill,
		IntervalSec: consoleWatchState.cfg.IntervalSec,
		Analysis:    consoleWatchState.cfg.Analysis,
		Rounds:      consoleWatchState.rounds,
	}
	if consoleWatchState.active {
		notify := consoleWatchState.cfg.Notify
		st.Notify = &notify
	}
	if len(consoleWatchState.rounds) > 0 {
		r := consoleWatchState.rounds[0]
		st.LastRound = &r
	}
	if consoleWatchState.active && !consoleWatchState.nextAt.IsZero() {
		st.NextAt = consoleWatchState.nextAt.Format(time.RFC3339)
	}
	if consoleWatchState.active && !consoleWatchState.anchor.IsZero() {
		st.Anchor = consoleWatchState.anchor.Format(time.RFC3339)
	}
	return st
}

// BrowserConsoleWatchStart starts (replacing any prior) the standing watch
// from a full config. The schedule is wall-clock aligned: AnchorMin "22:38"
// pins the grid to that minute (fires at 22:43:00, 22:48:00…); empty floors
// the start instant to the minute and fires round 1 immediately for the
// just-closed interval. The config persists to browser_watch.json and
// auto-resumes on the next app start. Notify routes rounds to the IM bot /
// email / in-app toast per its OnEvent policy. Skills containing human/ask
// steps are rejected — nobody watches a parked banner at 3am.
func (a *App) BrowserConsoleWatchStart(cfgIn BrowserConsoleWatchConfig) error {
	cfg := cfgIn
	cfg.Skill = strings.TrimSpace(cfg.Skill)
	if cfg.Skill == "" {
		return fmt.Errorf("缺少技能名")
	}
	if cfg.IntervalSec < watchMinIntervalSec {
		cfg.IntervalSec = watchMinIntervalSec
	}
	switch cfg.Analysis {
	case watchAnalysisGeneric, watchAnalysisNone, watchAnalysisAlerts:
	default:
		cfg.Analysis = watchAnalysisAlerts
	}
	interval := time.Duration(cfg.IntervalSec) * time.Second
	raw, err := a.BrowserConsoleReadSkill(cfg.Skill)
	if err != nil {
		return err
	}
	flowSteps, _, perr := parseUserFlowSkill(raw)
	if perr != nil {
		return fmt.Errorf("技能不是可执行的 browser-flow: %w", perr)
	}
	for i, st := range flowSteps {
		if st.Type == "human" || st.Type == "ask" {
			return fmt.Errorf("第 %d 步是人工/询问步骤——巡检技能必须可无人值守执行", i+1)
		}
	}

	// Anchor: explicit "HH:MM" pins the grid; empty = start floored to minute.
	anchor := time.Now().Truncate(time.Minute)
	immediate := true
	if am := strings.TrimSpace(cfg.AnchorMin); am != "" {
		parsed, aerr := time.ParseInLocation("15:04", am, time.Local)
		if aerr != nil {
			return fmt.Errorf("巡检时间 %q 无效（应为 HH:MM，如 22:38）", am)
		}
		now := time.Now()
		anchor = time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location())
		immediate = anchor.After(now)
	}
	cfg.IntervalSec = int(interval.Seconds())

	consoleWatchState.mu.Lock()
	if consoleWatchState.active {
		close(consoleWatchState.stop)
	}
	consoleWatchState.active = true
	consoleWatchState.cfg = cfg
	consoleWatchState.stop = make(chan struct{})
	consoleWatchState.anchor = anchor
	consoleWatchState.nextAt = anchor
	consoleWatchState.lastEnd = time.Time{}
	stop := consoleWatchState.stop
	a.emitWatchStateLocked()
	consoleWatchState.mu.Unlock()
	savePersistedBrowserWatch(persistedBrowserWatch{Config: cfg, Active: true})

	go func() {
		// Round 1: immediately for the just-closed interval — only when the
		// anchor IS the start minute. An explicit past anchor starts at the
		// next grid point; a future one fires when it arrives.
		if immediate {
			a.runWatchRound(cfg, anchor)
		}
		for {
			next := nextWatchFire(anchor, interval, time.Now())
			consoleWatchState.mu.Lock()
			consoleWatchState.nextAt = next
			a.emitWatchStateLocked()
			consoleWatchState.mu.Unlock()
			timer := time.NewTimer(time.Until(next))
			select {
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
				a.runWatchRound(cfg, next)
			}
		}
	}()
	return nil
}

// BrowserConsoleWatchStop halts the standing watch (the in-flight round, if
// any, runs to completion — it holds the console gate). The config persists
// with active=false so a later start (or the panel) can rebuild it.
func (a *App) BrowserConsoleWatchStop() error {
	consoleWatchState.mu.Lock()
	defer consoleWatchState.mu.Unlock()
	if !consoleWatchState.active {
		return nil
	}
	close(consoleWatchState.stop)
	consoleWatchState.active = false
	consoleWatchState.nextAt = time.Time{}
	cfg := consoleWatchState.cfg
	a.emitWatchStateLocked()
	go savePersistedBrowserWatch(persistedBrowserWatch{Config: cfg, Active: false})
	return nil
}

// BrowserConsoleWatchState snapshots the watch for the panel.
func (a *App) BrowserConsoleWatchState() BrowserConsoleWatchState {
	consoleWatchState.mu.Lock()
	defer consoleWatchState.mu.Unlock()
	return a.watchStateLocked()
}

// runWatchRound executes one poll-download-judge cycle for the grid instant
// `scheduled`. The console gate is try-entered: a manual trial run (or an
// over-running previous round) marks this round skipped rather than queueing
// behind the browser. The window is the closed grid interval
// [scheduled-interval, scheduled] (reconciled against lastEnd for skips), so
// execution latency cannot skew the boundaries — the interval is already
// closed when the round fires. lastEnd advances only when the window
// actually reached the page, anchoring contiguous coverage.
func (a *App) runWatchRound(cfg BrowserConsoleWatchConfig, scheduled time.Time) {
	now := time.Now()
	round := BrowserConsoleWatchRound{
		StartedAt: now.Format(time.RFC3339),
		Status:    "running",
	}
	// The window that actually hit the page (resolved at step-run time) and
	// its end instant — zero until the time-range step executes.
	var resolvedEnd time.Time
	finish := func() {
		round.FinishedAt = time.Now().Format(time.RFC3339)
		consoleWatchState.mu.Lock()
		// Advance coverage only when the window reached the page: a failure
		// before the time-range step (session open, parse error) leaves
		// lastEnd alone so the next round catch-ups over the gap; a failure
		// after it (export/download/triage) still advanced the query.
		if !resolvedEnd.IsZero() {
			consoleWatchState.lastEnd = resolvedEnd
		}
		consoleWatchState.rounds = append([]BrowserConsoleWatchRound{round}, consoleWatchState.rounds...)
		if len(consoleWatchState.rounds) > watchHistoryMax {
			consoleWatchState.rounds = consoleWatchState.rounds[:watchHistoryMax]
		}
		a.emitWatch(BrowserConsoleWatchEvent{Type: "round", Round: &round})
		a.emitWatchStateLocked()
		consoleWatchState.mu.Unlock()
	}

	resume, abort, ok := consoleGate.enter()
	if !ok {
		round.Status = "skipped"
		round.Skipped = true
		round.Note = "浏览器被试运行或上一轮占用，本轮跳过（数据由下一轮补漏窗口覆盖）"
		finish()
		return
	}
	defer consoleGate.leave()
	a.emitWatch(BrowserConsoleWatchEvent{Type: "round", Round: &round})

	raw, err := a.BrowserConsoleReadSkill(cfg.Skill)
	if err != nil {
		round.Status = "failed"
		round.Error = err.Error()
		finish()
		return
	}
	steps, params, err := parseUserFlowSkill(raw)
	if err != nil {
		round.Status = "failed"
		round.Error = fmt.Sprintf("解析技能失败: %v", err)
		finish()
		return
	}
	// The closed grid interval for this round, reconciled against the last
	// window that reached the page (skips stretch back, capped).
	consoleWatchState.mu.Lock()
	lastEnd := consoleWatchState.lastEnd
	consoleWatchState.mu.Unlock()
	winStart, winEnd, catchup := watchAlignedWindow(scheduled, lastEnd, time.Duration(cfg.IntervalSec)*time.Second)
	window := builtin.TimeRangeFromSpan(winEnd, winEnd.Sub(winStart))
	round.TimeRange = window
	if catchup {
		round.Note = fmt.Sprintf("补漏窗口：自上次查询点 %s 续读（期间有跳过/失败的轮次）", lastEnd.Format("15:04:05"))
	}
	// Bind the rolling window: named 时间范围 when referenced, else the sole
	// unbound ref (single-parameter export skills are the norm).
	if err := bindWatchWindow(steps, params, window); err != nil {
		round.Status = "failed"
		round.Error = err.Error()
		finish()
		return
	}
	if err := a.ensureConsoleOpen(); err != nil {
		round.Status = "failed"
		round.Error = err.Error()
		finish()
		return
	}

	// Live per-step feed into the round record + the watch event channel. The
	// onRange collector snapshots the window the moment it resolves, so the
	// round (and its UI card) shows what the platform actually queried.
	stepErr := ""
	emit := func(st BrowserConsoleTrialStatus) {
		if st.Index < 0 {
			if st.Status == "failed" {
				stepErr = st.Error
			}
			return
		}
		for len(round.Steps) <= st.Index {
			round.Steps = append(round.Steps, BrowserConsoleWatchStep{Index: len(round.Steps)})
		}
		rs := &round.Steps[st.Index]
		rs.Status = st.Status
		rs.Output = st.Output
		rs.Error = st.Error
		snapshot := round
		a.emitWatch(BrowserConsoleWatchEvent{Type: "round", Round: &snapshot})
	}
	onRange := func(r string) {
		round.TimeRange = r
		if _, end, ok := builtin.TimeRangeBounds(r); ok {
			resolvedEnd = end
		}
	}
	downloads := a.runConsoleSteps(steps, params, emit, resume, abort, false, onRange)
	if stepErr != "" {
		round.Status = "failed"
		round.Error = stepErr
		finish()
		return
	}

	// Judge the first spreadsheet export of the round.
	for _, d := range downloads {
		if ext := strings.ToLower(filepath.Ext(d.Path)); ext == ".xlsx" || ext == ".csv" {
			round.DownloadName, round.DownloadPath = d.Name, d.Path
			break
		}
	}
	if round.DownloadPath == "" {
		round.Status = "done"
		if round.Note == "" {
			round.Note = "本轮没有产生可研判的导出文件（.xlsx/.csv）"
		}
		finish()
		return
	}
	// Analysis is per-watch: alerts (SIEM 研判) / generic (通用表格) / none
	// (download only — no model spend, no notifications).
	if cfg.Analysis == watchAnalysisNone {
		round.Rows = -1 // unknown without parsing; UI shows the file only
		round.Status = "done"
		if round.Note == "" {
			round.Note = "本轮已下载（未开启研判）"
		}
		finish()
		return
	}
	analysis, aerr := a.BrowserConsoleAnalyzeDownload(round.DownloadPath, cfg.Analysis)
	if aerr != nil {
		round.Status = "failed"
		round.Error = fmt.Sprintf("下载成功但研判失败: %v", aerr)
		finish()
		return
	}
	round.Rows = analysis.Rows
	round.Analysis = analysis.Report
	round.CompromisedHosts = analysis.CompromisedHosts
	round.AttentionCount = analysis.AttentionCount
	round.Severity = analysis.Severity
	round.Status = "done"
	// Verdict-gated delivery: "确认失陷主机才通知" (alerts) / "发现关键结论才通知"
	// (generic) is OnEvent=compromised — the round only reaches the 负责人
	// when the analysis actually confirmed findings (or per policy).
	a.deliverWatchNotification(cfg, &round)
	finish()
}

// deliverWatchNotification evaluates the watch's notify policy against the
// round's verdict and pushes through the configured channels. Failures are
// recorded on the round (NotifyError), never fail the round itself.
func (a *App) deliverWatchNotification(cfg BrowserConsoleWatchConfig, round *BrowserConsoleWatchRound) {
	if !watchNotifyShould(cfg.Notify, alertVerdict{
		CompromisedHosts: round.CompromisedHosts,
		AttentionAlerts:   round.AttentionCount,
	}) || round.Status != "done" || round.Analysis == "" {
		return
	}
	generic := cfg.Analysis == watchAnalysisGeneric
	var sent, errs []string
	pushCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	summary := watchNotifySummary(cfg, round)
	subject := "【态势巡检】" + cfg.Skill
	if len(round.CompromisedHosts) > 0 {
		if generic {
			subject += fmt.Sprintf(" 关键发现 ×%d", len(round.CompromisedHosts))
		} else {
			subject += fmt.Sprintf(" 确认失陷主机 ×%d", len(round.CompromisedHosts))
		}
	} else if generic {
		subject += fmt.Sprintf(" 需关注条目 %d 条", round.AttentionCount)
	} else {
		subject += fmt.Sprintf(" 需关注告警 %d 条", round.AttentionCount)
	}
	if cfg.Notify.IMChatID != "" {
		if err := (schedulerIMPusher{app: a}).Push(pushCtx, cfg.Notify.IMChatID, summary); err != nil {
			errs = append(errs, "IM: "+err.Error())
		} else {
			sent = append(sent, "im")
		}
	}
	if cfg.Notify.Email != "" {
		if err := (schedulerEmailSender{}).Send(pushCtx, cfg.Notify.EmailAccount, cfg.Notify.Email, subject, summary); err != nil {
			errs = append(errs, "邮件: "+err.Error())
		} else {
			sent = append(sent, "email")
		}
	}
	if cfg.Notify.System {
		(schedulerNotifier{app: a}).Notify(subject, summary)
		sent = append(sent, "system")
	}
	round.Notified = sent
	if len(errs) > 0 {
		round.NotifyError = strings.Join(errs, "；")
	}
}

// watchNotifyShould maps the policy to a boolean for this round's verdict.
func watchNotifyShould(p BrowserConsoleWatchNotify, v alertVerdict) bool {
	switch p.OnEvent {
	case "always":
		return true
	case "attention":
		return v.attention() > 0 || len(v.findings()) > 0
	case "compromised":
		return len(v.findings()) > 0
	default: // "never" / unset
		return false
	}
}

// watchNotifySummary renders the compact push text for IM/email/toast.
func watchNotifySummary(cfg BrowserConsoleWatchConfig, round *BrowserConsoleWatchRound) string {
	generic := cfg.Analysis == watchAnalysisGeneric
	var b strings.Builder
	b.WriteString("【浏览器巡检】")
	if len(round.CompromisedHosts) > 0 {
		if generic {
			b.WriteString(fmt.Sprintf("发现关键结论 %d 项\n关键发现: %s\n", len(round.CompromisedHosts), strings.Join(round.CompromisedHosts, "、")))
		} else {
			b.WriteString(fmt.Sprintf("确认失陷主机 %d 台\n失陷主机: %s\n", len(round.CompromisedHosts), strings.Join(round.CompromisedHosts, "、")))
		}
	} else if generic {
		b.WriteString("发现需关注条目\n")
	} else {
		b.WriteString("发现需关注告警\n")
	}
	if round.TimeRange != "" {
		b.WriteString("窗口: " + round.TimeRange + "\n")
	}
	if generic {
		b.WriteString(fmt.Sprintf("需关注条目: %d 条", round.AttentionCount))
	} else {
		b.WriteString(fmt.Sprintf("需关注告警: %d 条", round.AttentionCount))
	}
	if round.Severity != "" {
		b.WriteString("（最高等级 " + round.Severity + "）")
	}
	if round.Rows >= 0 {
		b.WriteString("\n数据: " + round.DownloadName + fmt.Sprintf("（%d 行）", round.Rows))
	} else {
		b.WriteString("\n数据: " + round.DownloadName)
	}
	b.WriteString("\n完整研判报告见运维面板 → 浏览器 → 巡检")
	return b.String()
}

// bindWatchWindow fills the rolling time-range binding: prefer a ref named
// 时间范围; otherwise bind the single remaining unbound ref; more than one
// unbound ref is a configuration error the user must fix in the editor.
func bindWatchWindow(steps []BrowserConsoleStep, params map[string]string, window string) error {
	var refs []string
	seen := map[string]bool{}
	for _, st := range steps {
		for _, v := range []string{st.Target, st.URL, st.Text, st.Value, st.Expression, st.Condition} {
			for _, m := range paramRefRe.FindAllStringSubmatch(v, -1) {
				name := strings.TrimSpace(m[1])
				if !seen[name] {
					seen[name] = true
					refs = append(refs, name)
				}
			}
		}
	}
	sort.Strings(refs)
	var unbound []string
	for _, r := range refs {
		if _, ok := params[r]; !ok {
			unbound = append(unbound, r)
		}
	}
	if _, ok := params["时间范围"]; ok {
		params["时间范围"] = window
		return nil
	}
	switch len(unbound) {
	case 0:
		// Skill is fully self-contained (all {{refs}} have defaults, or none
		// exist) — the window stays informational only. The watch is generic:
		// not every poll exports a time-filtered slice (status pages,
		// dashboards, inventory dumps).
		return nil
	case 1:
		params[unbound[0]] = window
		return nil
	default:
		return fmt.Errorf("技能有多个未绑定参数（%s）——巡检需要唯一的时间窗口参数（推荐命名为 时间范围）", strings.Join(unbound, "、"))
	}
}

// parseUserFlowSkill loads a user skill's 步骤 table and frontmatter params
// into the desktop step shape the shared runner consumes.
func parseUserFlowSkill(raw string) ([]BrowserConsoleStep, map[string]string, error) {
	flows, err := builtin.ParseFlowTable(raw)
	if err != nil {
		return nil, nil, err
	}
	steps := make([]BrowserConsoleStep, 0, len(flows))
	for _, f := range flows {
		steps = append(steps, BrowserConsoleStep{
			Type: f.Type, Target: f.Target, URL: f.URL, Text: f.Text, Value: f.Value,
			Direction: f.Direction, Amount: f.Amount, Condition: f.Condition,
			TimeoutSec: f.TimeoutSec, Files: f.Files, Expression: f.Expression,
		})
	}
	return steps, parseSkillFrontmatterParams(raw), nil
}

// parseSkillFrontmatterParams extracts the `params: k=v, k2=v2` line — a
// comma-separated dialect distinct from run_skill arguments. Values stay
// raw: time phrases resolve lazily at the step that uses them.
func parseSkillFrontmatterParams(raw string) map[string]string {
	params := map[string]string{}
	fm := regexp.MustCompile(`(?m)^params:\s*(.+)$`).FindStringSubmatch(raw)
	if fm == nil {
		return params
	}
	for _, pair := range strings.Split(fm[1], ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(pair), "="); ok {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k != "" {
				params[k] = v
			}
		}
	}
	return params
}

// --- persistence + startup resume ---------------------------------------------

// persistedBrowserWatch is the browser_watch.json record: the config plus the
// on/off flag, so a stopped watch keeps its settings for one-click restart
// and an active one auto-resumes on the next app start.
type persistedBrowserWatch struct {
	Config BrowserConsoleWatchConfig `json:"config"`
	Active bool                      `json:"active"`
}

func browserWatchConfigPath() string {
	return filepath.Join(desktopConfigDir(), "browser_watch.json")
}

func loadPersistedBrowserWatch() (persistedBrowserWatch, error) {
	var p persistedBrowserWatch
	b, err := os.ReadFile(browserWatchConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return persistedBrowserWatch{}, fmt.Errorf("browser_watch.json 损坏: %w", err)
	}
	return p, nil
}

func savePersistedBrowserWatch(p persistedBrowserWatch) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(browserWatchConfigPath()), 0o755)
	_ = os.WriteFile(browserWatchConfigPath(), b, 0o644)
}

// resumeBrowserWatch restarts an active watch at app startup. Best-effort:
// a missing/unparseable skill simply logs — the panel still shows the config
// and the user can restart it from there.
func (a *App) resumeBrowserWatch() {
	p, err := loadPersistedBrowserWatch()
	if err != nil {
		fmt.Printf("[browser-watch] resume skipped: %v\n", err)
		return
	}
	if !p.Active || strings.TrimSpace(p.Config.Skill) == "" {
		return
	}
	if err := a.BrowserConsoleWatchStart(p.Config); err != nil {
		fmt.Printf("[browser-watch] resume failed for %q: %v\n", p.Config.Skill, err)
	}
}
