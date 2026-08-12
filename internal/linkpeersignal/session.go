package linkpeersignal

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Custom WS close codes (PROTOCOL §13.1). 4xxx range is application-defined.
const (
	CloseBadSig      = 4402
	CloseBadTS       = 4403
	CloseRateLimited = 4404
)

var errAuth = errors.New("auth_failed")

// PeerConn is one live WS to a device (either an S long-link or a C on-demand).
type PeerConn struct {
	DevID    string
	conn     *websocket.Conn
	writeMu  sync.Mutex // serialize writes — gorilla/websocket forbids concurrent WriteMessage
	lastSeen atomic.Int64
	// per-dev 消息速率限制（§6 ws_msg_per_sec_per_dev）：固定 1s 窗口。
	rlMu     sync.Mutex
	rlWindow time.Time
	rlCount  int
}

// allowMsg 实现简单的固定窗口限流（每秒 limit 条）。超限返回 false，由调用方
// 决定 drop 还是 close。防止恶意设备灌爆信令中转。
func (pc *PeerConn) allowMsg(limit int) bool {
	if limit <= 0 {
		return true
	}
	pc.rlMu.Lock()
	defer pc.rlMu.Unlock()
	now := time.Now()
	if now.Sub(pc.rlWindow) >= time.Second {
		pc.rlWindow = now
		pc.rlCount = 0
	}
	pc.rlCount++
	return pc.rlCount <= limit
}

func (pc *PeerConn) send(raw []byte) error {
	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	return pc.conn.WriteMessage(websocket.TextMessage, raw)
}

func (pc *PeerConn) touch() { pc.lastSeen.Store(time.Now().UnixMilli()) }

// SessionStore is the online peers table + message router. Stateless routing
// target: messages are forwarded by "to" field, never broadcast.
type SessionStore struct {
	mu    sync.RWMutex
	peers map[string]*PeerConn // devId → conn
	cfg   SessionConfig
}

func NewSessionStore(cfg SessionConfig) *SessionStore {
	return &SessionStore{peers: map[string]*PeerConn{}, cfg: cfg}
}

// verifyAuth implements the STATELESS WS authentication (PROTOCOL §4.1 v1.1):
//   devId must equal base32(SHA256(pub)[:10])  — self-consistency
//   Ed25519.Verify(pub, devId||ts, sig)        — proves private-key possession
//   |now - ts| < skew                          — freshness window
//
// K stores NO state to validate this. A restarted K accepts any legitimately
// key-holding device immediately; pairing legality is enforced later by S at
// handshake (PROTOCOL §5.4), not by K.
//
// pub and sig travel in URL query strings, so they use URL-safe base64
// (URLEncoding, no padding). Standard base64 would corrupt on '+' which HTTP
// parsers turn into a space. JSON-body fields (pair register/exchange) use
// StdEncoding instead — see server.go.
//
// On any failure the same generic error is returned — we do NOT distinguish
// identity-mismatch / bad-sig / bad-ts to avoid leaking which check failed
// (device enumeration protection).
func verifyAuth(devIDParam, tsStr, pubB64, sigB64 string, skew time.Duration) error {
	uEnc := base64.URLEncoding.WithPadding(base64.NoPadding)
	pub, err := uEnc.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errAuth
	}
	if subtle.ConstantTimeCompare([]byte(devID(pub)), []byte(devIDParam)) != 1 {
		return errAuth
	}
	sig, err := uEnc.DecodeString(sigB64)
	if err != nil {
		return errAuth
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(devIDParam+tsStr), sig) {
		return errAuth
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return errAuth
	}
	now := time.Now().Unix()
	skewS := int64(skew.Seconds())
	if now-ts > skewS || ts-now > skewS {
		return errAuth
	}
	return nil
}

func (s *SessionStore) Register(pc *PeerConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 在线 WS 硬上限（§11.2⑤）：达上限且非重连则拒绝，防内存耗尽。
	if s.cfg.MaxPeers > 0 && s.peers[pc.DevID] == nil && len(s.peers) >= s.cfg.MaxPeers {
		return false
	}
	s.peers[pc.DevID] = pc
	return true
}

func (s *SessionStore) Unregister(devID string) {
	s.mu.Lock()
	delete(s.peers, devID)
	s.mu.Unlock()
}

// Route delivers raw bytes to the peer named in the message's "to" field.
// Returns the parsed type (for metrics) and whether delivery succeeded.
// Keepalive (kp/kp_ack) is local and not forwarded. Offline peers produce an
// "unavailable" reply to the sender. O(1) map lookup — never broadcasts.
func (s *SessionStore) Route(from *PeerConn, raw []byte) (msgType string, delivered bool, err error) {
	var hdr struct {
		Type string `json:"type"`
		To   string `json:"to"`
	}
	if e := json.Unmarshal(raw, &hdr); e != nil {
		return "", false, e
	}
	msgType = hdr.Type
	if hdr.Type == "kp" || hdr.Type == "kp_ack" {
		return msgType, true, nil
	}
	if hdr.To == "" {
		return msgType, false, errors.New("missing to")
	}
	s.mu.RLock()
	to := s.peers[hdr.To]
	s.mu.RUnlock()
	if to == nil {
		na, _ := json.Marshal(map[string]any{"type": "unavailable", "reason": "peer_offline", "to": hdr.To})
		_ = from.send(na)
		return msgType, false, nil
	}
	if e := to.send(raw); e != nil {
		return msgType, false, e
	}
	return msgType, true, nil
}

// SendTo delivers raw bytes to a specific peer by devID. Used by K to push
// pair_exchange notices to S (server.go handlePairExchange). Returns false if
// the peer is offline.
func (s *SessionStore) SendTo(devID string, raw []byte) bool {
	s.mu.RLock()
	to := s.peers[devID]
	s.mu.RUnlock()
	if to == nil {
		return false
	}
	return to.send(raw) == nil
}

// BroadcastShutdown 给所有在线 peer 发 server_shutdown 通知（§13.2），让客户端
// 立即按 retry_after 重连，而不是等 WS 自然断 + backoff 才察觉。
func (s *SessionStore) BroadcastShutdown(retryAfter int) {
	msg, _ := json.Marshal(map[string]any{"type": "server_shutdown", "retry_after": retryAfter})
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, pc := range s.peers {
		_ = pc.send(msg)
	}
}

// SweepIdle reaps peers unseen within timeout. Caller runs it periodically.
func (s *SessionStore) SweepIdle(timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-timeout).UnixMilli()
	for dev, pc := range s.peers {
		if pc.lastSeen.Load() < cutoff {
			delete(s.peers, dev)
		}
	}
}

func (s *SessionStore) OnlineCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.peers)
}
