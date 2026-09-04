package builtin

// browserdevtools.go — a slice of F12 for the console session: page console
// messages + network request list, buffered per session and served to the
// ops workbench's bottom pane (控制台 / 网络). Collection rides the same
// CDP event stream the recorder uses; the console session only — agent
// sessions stay lean.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// ConsoleLogEntry is one page console message (or exception).
type ConsoleLogEntry struct {
	Type string `json:"type"` // log|warning|error|info|debug|exception
	Text string `json:"text"`
	Time int64  `json:"time"` // unix millis
}

// NetEntry is one finished (or failed) network request.
type NetEntry struct {
	Method  string `json:"method"`
	URL     string `json:"url"`
	Status  string `json:"status"` // "200" / "404" / "FAIL"
	ResType string `json:"res_type,omitempty"`
	Time    int64  `json:"time"`
}

const devToolsBufCap = 200

type devToolsState struct {
	mu   sync.Mutex
	logs []ConsoleLogEntry
	net  []NetEntry
	// pending maps requestID → method/URL/start for in-flight requests.
	pending map[network.RequestID]NetEntry
}

func (d *devToolsState) pushLog(e ConsoleLogEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logs = append(d.logs, e)
	if len(d.logs) > devToolsBufCap {
		d.logs = d.logs[len(d.logs)-devToolsBufCap:]
	}
}

func (d *devToolsState) finishNet(id network.RequestID, status, resType string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, ok := d.pending[id]
	if !ok {
		return
	}
	delete(d.pending, id)
	entry.Status, entry.ResType = status, resType
	entry.Time = time.Now().UnixMilli()
	d.net = append(d.net, entry)
	if len(d.net) > devToolsBufCap {
		d.net = d.net[len(d.net)-devToolsBufCap:]
	}
}

// initDevToolsListener arms console + network capture for one console
// session. Called from ConsoleOpen; listeners stay for the session's life
// (chromedp listeners cannot be removed individually) and simply append into
// the per-session buffers.
func initDevToolsListener(s *browserSession) {
	if s.devTools == nil {
		s.devTools = &devToolsState{pending: map[network.RequestID]NetEntry{}}
	}
	d := s.devTools
	chromedp.ListenTarget(s.ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			var b strings.Builder
			for _, arg := range e.Args {
				if arg.Value != nil {
					var v json.RawMessage
					_ = json.Unmarshal(arg.Value, &v)
					b.Write(arg.Value)
				} else if arg.Description != "" {
					b.WriteString(arg.Description)
				} else {
					b.WriteString(arg.Type.String())
				}
				b.WriteByte(' ')
			}
			text := strings.TrimSpace(b.String())
			if len(text) > 300 {
				text = text[:300] + "…"
			}
			d.pushLog(ConsoleLogEntry{Type: e.Type.String(), Text: text, Time: time.Now().UnixMilli()})
		case *runtime.EventExceptionThrown:
			if e.ExceptionDetails != nil {
				text := e.ExceptionDetails.Text
				if e.ExceptionDetails.Exception != nil && e.ExceptionDetails.Exception.Description != "" {
					text += " " + e.ExceptionDetails.Exception.Description
				}
				if len(text) > 300 {
					text = text[:300] + "…"
				}
				d.pushLog(ConsoleLogEntry{Type: "exception", Text: text, Time: time.Now().UnixMilli()})
			}
		case *network.EventRequestWillBeSent:
			d.mu.Lock()
			d.pending[e.RequestID] = NetEntry{Method: e.Request.Method, URL: e.Request.URL}
			d.mu.Unlock()
		case *network.EventResponseReceived:
			status := ""
			if e.Response != nil {
				status = itoa(int(e.Response.Status))
			}
			resType := string(e.Type)
			d.finishNet(e.RequestID, status, resType)
		case *network.EventLoadingFailed:
			d.finishNet(e.RequestID, "FAIL", string(e.Type))
		}
	})
	// Enable the domains (one-shot; errors are non-fatal — the pane just
	// stays empty).
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	_ = chromedp.Run(ctx, runtime.Enable(), network.Enable())
}

func itoa(n int) string { return strconv.Itoa(n) }

// ConsoleDevTools returns the console session's buffered logs + network
// entries (newest last) for the workbench's bottom pane.
func ConsoleDevTools() ([]ConsoleLogEntry, []NetEntry, error) {
	s, err := consoleSession()
	if err != nil {
		return nil, nil, err
	}
	d := s.devTools
	if d == nil {
		return []ConsoleLogEntry{}, []NetEntry{}, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	logs := append([]ConsoleLogEntry(nil), d.logs...)
	net := append([]NetEntry(nil), d.net...)
	return logs, net, nil
}
