package linkpeersignal

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

// Metrics is a minimal Prometheus-text-format collector. M0-b avoids pulling
// in prometheus/client_golang to keep the dependency tree lean; the exposition
// format is hand-written and fully Prometheus-compatible. Migrate later if
// richer histogram/label support is needed.
//
// Cardinality rule (SIGNAL_SPEC §13.4): label values are LOW-cardinality
// enums only (type/code/result/dim) — never devId or IP.
type Metrics struct {
	mu            sync.Mutex
	wsMsgs        map[string]uint64
	pairTotal     map[string]uint64
	signalErrors  map[string]uint64
	rateLimitHits map[string]uint64
	wsConnections int64
}

func NewMetrics() *Metrics {
	return &Metrics{
		wsMsgs: map[string]uint64{}, pairTotal: map[string]uint64{},
		signalErrors: map[string]uint64{}, rateLimitHits: map[string]uint64{},
	}
}

func (m *Metrics) WsMsg(t string)      { m.mu.Lock(); m.wsMsgs[t]++; m.mu.Unlock() }
func (m *Metrics) Pair(result string)  { m.mu.Lock(); m.pairTotal[result]++; m.mu.Unlock() }
func (m *Metrics) Error(code string)   { m.mu.Lock(); m.signalErrors[code]++; m.mu.Unlock() }
func (m *Metrics) RateLimit(dim string) { m.mu.Lock(); m.rateLimitHits[dim]++; m.mu.Unlock() }
func (m *Metrics) WSConnect()          { m.mu.Lock(); m.wsConnections++; m.mu.Unlock() }
func (m *Metrics) WSDisconnect()       { m.mu.Lock(); m.wsConnections--; m.mu.Unlock() }

func (m *Metrics) WriteHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	writeCounter(w, "linkpeer_ws_msgs_total", "type", m.wsMsgs)
	writeCounter(w, "linkpeer_pair_total", "result", m.pairTotal)
	writeCounter(w, "linkpeer_signal_errors_total", "code", m.signalErrors)
	writeCounter(w, "linkpeer_ratelimit_hits_total", "dim", m.rateLimitHits)
	fmt.Fprintf(w, "# TYPE linkpeer_ws_connections gauge\nlinkpeer_ws_connections %d\n", m.wsConnections)
}

func writeCounter(w io.Writer, name, label string, vals map[string]uint64) {
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	for k, v := range vals {
		fmt.Fprintf(w, "%s{%s=%q} %d\n", name, label, k, v)
	}
}
