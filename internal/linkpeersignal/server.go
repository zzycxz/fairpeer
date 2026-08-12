package linkpeersignal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Server wires the pair store, session store, rate limiters, audit, and metrics
// behind the HTTP/WS surface. One Server instance handles all endpoints.
type Server struct {
	cfg      Config
	pairs    *PairStore
	sessions *SessionStore
	ipRL     *RateLimiter // per-IP on /pair/*
	devRL    *RateLimiter // per-devId on /pair/*
	audit    *Audit
	metrics  *Metrics
	startup  time.Time
	upgrader websocket.Upgrader
}

func NewServer(cfg Config, audit *Audit) *Server {
	return &Server{
		cfg:      cfg,
		pairs:    NewPairStore(cfg.Pair),
		sessions: NewSessionStore(cfg.Session),
		ipRL:     NewRateLimiter(cfg.Pair.MaxFailPerIPPerHour),
		devRL:    NewRateLimiter(cfg.Pair.MaxPerDevPerHour),
		audit:    audit,
		metrics:  NewMetrics(),
		startup:  time.Now(),
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
}

// realIP honors X-Forwarded-For from the reverse proxy (Caddy). Without this,
// rate limiting would see only the proxy's internal IP and be useless.
//
// SECURITY (SIGNAL_SPEC §13.5): XFF is ONLY trusted when the request comes
// from a trusted proxy (127.0.0.1 / ::1 / docker 172.x). If K's port were
// exposed directly, a client could forge XFF to bypass IP rate limiting.
func realIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" && isTrustedProxy(r.RemoteAddr) {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	host := r.RemoteAddr
	for i := 0; i < len(host); i++ {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

// isTrustedProxy returns true if the direct peer is localhost or a Docker
// internal address (172.16-31.x / 10.x / 192.168.x). Caddy runs alongside K
// in docker-compose, so the upstream is always one of these.
func isTrustedProxy(remoteAddr string) bool {
	host := remoteAddr
	for i := 0; i < len(host); i++ {
		if host[i] == ':' {
			host = host[:i]
			break
		}
	}
	return host == "127.0.0.1" || host == "::1" ||
		strings.HasPrefix(host, "172.") || strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.")
}

// --- /pair/register ---

type pairRegisterReq struct {
	Code string `json:"code"`
	DevS string `json:"devS"`
	PubS string `json:"pubS"`
	FpS  string `json:"fpS"`
}

func (s *Server) handlePairRegister(w http.ResponseWriter, r *http.Request) {
	ip := realIP(r)
	if !s.ipRL.Allow(ip) {
		s.metrics.RateLimit("ip")
		writeErr(w, 429, "rate_limited")
		return
	}
	var req pairRegisterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request")
		return
	}
	if !s.devRL.Allow(req.DevS) {
		s.metrics.RateLimit("dev")
		writeErr(w, 429, "rate_limited")
		return
	}
	pubS, err := base64.StdEncoding.DecodeString(req.PubS)
	if err != nil {
		writeErr(w, 400, "bad_pub")
		return
	}
	pairID, err := s.pairs.Register(req.Code, req.DevS, pubS, req.FpS)
	if err != nil {
		s.metrics.Pair(errResult(err))
		s.audit.Error("pair_register", req.DevS, err)
		switch {
		case errors.Is(err, ErrCodeConflict):
			writeErr(w, 409, "code_conflict")
		case errors.Is(err, ErrCapacityFull):
			writeErr(w, 503, "capacity_full")
		case errors.Is(err, ErrFpMismatch):
			writeErr(w, 400, "fp_mismatch")
		default:
			writeErr(w, 500, "internal")
		}
		return
	}
	s.audit.PairRegister(req.DevS, ip)
	s.metrics.Pair("register")
	writeJSON(w, 200, map[string]any{
		"pairId":    pairID,
		"expiresAt": time.Now().Add(time.Duration(s.cfg.Pair.CodeTTL) * time.Second).Unix(),
	})
}

// --- /pair/exchange ---

type pairExchangeReq struct {
	PairID string `json:"pairId"`
	Code   string `json:"code"`
	DevC   string `json:"devC"`
	PubC   string `json:"pubC"`
	FpC    string `json:"fpC"`
}

func (s *Server) handlePairExchange(w http.ResponseWriter, r *http.Request) {
	ip := realIP(r)
	if !s.ipRL.Allow(ip) {
		s.metrics.RateLimit("ip")
		writeErr(w, 429, "rate_limited")
		return
	}
	var req pairExchangeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request")
		return
	}
	pubC, err := base64.StdEncoding.DecodeString(req.PubC)
	if err != nil {
		writeErr(w, 400, "bad_pub")
		return
	}
	pubS, fpS, err := s.pairs.Exchange(req.PairID, req.Code, req.DevC, pubC, req.FpC)
	if err != nil {
		s.metrics.Pair(errResult(err))
		s.audit.PairExchange(req.DevC, ip, false)
		switch {
		case errors.Is(err, ErrPairNotFound):
			writeErr(w, 404, "pair_not_found")
		case errors.Is(err, ErrPairExpired):
			writeErr(w, 410, "pair_expired")
		case errors.Is(err, ErrPairLocked):
			writeErr(w, 423, "pair_locked")
		case errors.Is(err, ErrCodeMismatch):
			writeErr(w, 401, "code_mismatch")
		case errors.Is(err, ErrFpMismatch):
			writeErr(w, 400, "fp_mismatch")
		default:
			writeErr(w, 500, "internal")
		}
		return
	}
	s.audit.PairExchange(req.DevC, ip, true)
	s.metrics.Pair("exchange")
	// 推 pair_exchange 给 S（devS）：让 S 知道 C 已 exchange，可 confirm。
	// K 不存业务，只中转配对通知（PROTOCOL §3.1）。
	if p, ok := s.pairs.Get(req.PairID); ok {
		notice, _ := json.Marshal(map[string]any{
			"type": "pair_exchange", "to": p.DevS,
			"pairId": req.PairID,
			"pubC": base64.StdEncoding.EncodeToString(p.PubC),
			"fpC":  p.FpC, "devC": p.DevC,
		})
		s.sessions.SendTo(p.DevS, notice)
	}
	// NOTE: no sessionToken — WS auth is fully stateless (PROTOCOL §4.1 v1.1).
	writeJSON(w, 200, map[string]any{
		"pubS": base64.StdEncoding.EncodeToString(pubS),
		"fpS":  fpS,
	})
}

// --- /pair/confirm (S calls after desktop user clicks confirm) ---

func (s *Server) handlePairConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PairID string `json:"pairId"`
		DevS   string `json:"devS"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request")
		return
	}
	p, ok := s.pairs.Get(req.PairID)
	if !ok || p.DevS != req.DevS {
		writeErr(w, 404, "pair_not_found")
		return
	}
	if err := s.pairs.Confirm(req.PairID); err != nil {
		writeErr(w, 410, "pair_expired")
		return
	}
	s.metrics.Pair("confirm")
	writeJSON(w, 200, map[string]any{
		"pubC": base64.StdEncoding.EncodeToString(p.PubC), "fpC": p.FpC, "devC": p.DevC,
	})
}

// --- /session/ws ---

func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := verifyAuth(
		q.Get("dev"), q.Get("ts"), q.Get("pub"), q.Get("sig"),
		time.Duration(s.cfg.Session.OfferTSSkew)*time.Second,
	); err != nil {
		s.metrics.Error("4401")
		http.Error(w, "auth_failed", http.StatusUnauthorized)
		return
	}
	devID := q.Get("dev")
	c, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	pc := &PeerConn{DevID: devID, conn: c}
	pc.touch()
	if !s.sessions.Register(pc) {
		// 达 MaxPeers 上限：拒绝新连接（§11.2⑤）。
		s.metrics.Error("4513")
		_ = c.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(1013, "capacity_full"))
		_ = c.Close()
		return
	}
	s.audit.WSConnect(devID, realIP(r))
	s.metrics.WSConnect()
	defer func() {
		s.sessions.Unregister(devID)
		s.metrics.WSDisconnect()
		s.audit.WSDisconnect(devID)
		_ = c.Close()
	}()

	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			return
		}
		if len(raw) > s.cfg.Session.WSMaxMsgBytes {
			continue // oversize: drop silently, don't kill the link
		}
		if !pc.allowMsg(s.cfg.Session.WSMsgPerSecPerDev) {
			// 超速：关闭连接（§6 per-dev 速率限制）
			s.metrics.Error("4404")
			_ = c.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(CloseRateLimited, "rate_limited"))
			return
		}
		pc.touch()
		msgType, _, rerr := s.sessions.Route(pc, raw)
		if rerr != nil {
			s.audit.Error("route", devID, rerr)
			continue
		}
		s.metrics.WsMsg(msgType)
	}
}

// --- /healthz ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"online":    s.sessions.OnlineCount(),
		"uptime":    int(time.Since(s.startup).Seconds()),
		"goroutines": runtime.NumGoroutine(),
		"heap_mb":   m.HeapAlloc / (1024 * 1024),
	})
}

// BroadcastShutdown 给所有在线 WS 客户端发 server_shutdown（§13.2）。
func (s *Server) BroadcastShutdown(retryAfter int) {
	s.sessions.BroadcastShutdown(retryAfter)
}

// Routes returns the HTTP mux.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pair/register", s.handlePairRegister)
	mux.HandleFunc("/pair/exchange", s.handlePairExchange)
	mux.HandleFunc("/pair/confirm", s.handlePairConfirm)
	mux.HandleFunc("/session/ws", s.handleSessionWS)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/metrics", s.metrics.WriteHTTP)
	return mux
}

// Sweep runs all periodic cleanup (main wires a 30s ticker).
func (s *Server) Sweep() {
	s.pairs.Sweep()
	s.sessions.SweepIdle(time.Duration(s.cfg.Session.IdleTimeout) * time.Second)
	s.ipRL.Sweep()
	s.devRL.Sweep()
}

// OnlinePeers exposes the current online-device count (for healthz + tests +
// metrics consumers that want a point-in-time read without /metrics parsing).
func (s *Server) OnlinePeers() int { return s.sessions.OnlineCount() }

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func errResult(err error) string {
	switch {
	case errors.Is(err, ErrPairNotFound):
		return "not_found"
	case errors.Is(err, ErrPairLocked):
		return "locked"
	case errors.Is(err, ErrPairExpired):
		return "expired"
	case errors.Is(err, ErrCodeMismatch):
		return "code_mismatch"
	case errors.Is(err, ErrCodeConflict):
		return "code_conflict"
	case errors.Is(err, ErrCapacityFull):
		return "capacity_full"
	case errors.Is(err, ErrFpMismatch):
		return "fp_mismatch"
	default:
		return "error"
	}
}
