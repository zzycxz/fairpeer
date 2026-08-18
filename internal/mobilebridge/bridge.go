package mobilebridge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// Bridge is the desktop-side mobilebridge entry object: it owns S's identity,
// the Pairing state machine, the SignalClient long-link to K, and the live
// Conn map. It implements SignalHandler (dispatching K messages) and exposes
// ForwardEvent for the tabEventSink injection point.
//
// Lifecycle: NewBridge → Start (connects to K) → runs until ctx done.
// The desktop layer (desktop/app.go) constructs it once at startup.
type Bridge struct {
	cfg     Config
	sPriv   ed25519.PrivateKey
	sPub    ed25519.PublicKey
	devS    string
	store   KeyStore
	exec    CommandExecutor
	pairing *Pairing
	signal  *SignalClient
	audit   *Audit

	mu      sync.Mutex
	conns   map[string]*Conn            // connId → Conn (one per C connection)
	byDev   map[string]*Conn            // devC → Conn (latest, for event routing)
	knockS  map[string]*net.UDPConn     // connId → 敲门用的 ICE 共享 UDP socket
	tabSub  map[string]map[string]bool  // tabId → set[connId]
	ringBuf map[string]*tabRing         // tabId → 事件环形缓冲（resync 增量同步，§11.2）
	ctx     context.Context

	onReady func(*Conn) // 全局 onReady hook（debug-server 注册，发测试 wireEvent）

	// resolveTab maps linkpeer's tab alias ("default"/"") to a real fairpeer tab
	// ID (UUID). Injected by the desktop layer; nil = use the alias as-is.
	resolveTab func(string) string
}

// tabRing 是每 tab 的事件环形缓冲（resync 增量同步用，§11.2）。纯内存，重启清空。
type tabRing struct {
	seq     uint64
	entries []ringEntry
}

type ringEntry struct {
	seq  uint64
	json []byte
}

const ringCap = 200 // 每 tab 保留最近 200 条事件

// NewBridge wires everything but does NOT connect yet (Start does that).
func NewBridge(cfg Config, sPriv ed25519.PrivateKey, sPub ed25519.PublicKey, store KeyStore, exec CommandExecutor, audit *Audit) *Bridge {
	b := &Bridge{
		cfg: cfg, sPriv: sPriv, sPub: sPub, devS: DevID(sPub),
		store: store, exec: exec, audit: audit,
		conns:   map[string]*Conn{},
		byDev:   map[string]*Conn{},
		knockS:  map[string]*net.UDPConn{},
		tabSub:  map[string]map[string]bool{},
		ringBuf: map[string]*tabRing{},
	}
	b.pairing = NewPairing(cfg.SignalURL, sPub, store, audit)
	// SignalClient needs a SignalHandler — Bridge itself.
	b.signal = NewSignalClient(cfg.SignalURL, sPub, sPriv, b, audit)
	return b
}

// Start connects the SignalClient to K and keeps it connected. Blocks until
// ctx is done. Call in a goroutine.
func (b *Bridge) Start(ctx context.Context) error {
	b.ctx = ctx
	b.pairing.SetAutoConfirm(b.cfg.AutoConfirm)
	return b.signal.Run(ctx)
}

// SetOnReady 注册全局 onReady hook：每个 Conn 握手完成时回调（debug-server 用它
// 在握手后发测试 wireEvent，模拟 fairpeer 下行）。
func (b *Bridge) SetOnReady(fn func(*Conn)) { b.onReady = fn }

// SetResolveTab injects the tab-alias resolver: linkpeer sends "default"/"" but
// fairpeer tabs are UUIDs. The desktop layer maps the alias to the active tab.
func (b *Bridge) SetResolveTab(fn func(string) string) { b.resolveTab = fn }

// SignalConnected reports whether the long-link to K is up. Surfaces K
// reachability in the settings panel so a stale/failed link is visible.
func (b *Bridge) SignalConnected() bool { return b.signal != nil && b.signal.Connected() }

// SignalURL returns the configured K base URL (for display in the panel).
func (b *Bridge) SignalURL() string { return b.cfg.SignalURL }

// KnockEnabled / KnockServer expose the UDP-knock config (settings panel).
func (b *Bridge) KnockEnabled() bool   { return b.cfg.UDPKnock }
func (b *Bridge) KnockServer() string { return b.cfg.KnockServer }

// StartPairing kicks off a new pairing session (Wails UI calls this).
func (b *Bridge) StartPairing() (code, qrURL string, err error) {
	return b.pairing.StartPairing()
}

// SetPairAddress 钉死二维码 relay 使用的网卡 IP（"" = 自动）。
func (b *Bridge) SetPairAddress(ip string) { b.pairing.SetPairAddress(ip) }

// ListPairNics 枚举「配对网卡」候选（含默认标记），设置面板下拉框用。
func (b *Bridge) ListPairNics() []NicInfo { return ListPairNics() }

// PendingPairings lists C's awaiting desktop confirm.
func (b *Bridge) PendingPairings() []PendingPair { return b.pairing.Pending() }

// ConfirmPairing accepts a C. Wails UI calls this after the user clicks confirm.
func (b *Bridge) ConfirmPairing(pairID string) error { return b.pairing.Confirm(pairID) }

// RejectPairing declines a C.
func (b *Bridge) RejectPairing(pairID string) { b.pairing.Reject(pairID) }

// Unpair removes + revokes a previously-paired C.
func (b *Bridge) Unpair(devC string) {
	b.pairing.Unpair(devC)
	// drop any active Conn from that device
	b.mu.Lock()
	conn := b.byDev[devC]
	b.mu.Unlock()
	if conn != nil {
		conn.close()
	}
}

// --- SignalHandler (K → S inbound) ---

func (b *Bridge) OnSignalMsg(msg SignalMsg) {
	b.audit.Info("signal_msg", "type", msg.Type, "from", msg.From, "to", msg.To, "connId", msg.ConnID)
	switch msg.Type {
	case "offer":
		b.handleOffer(msg)
	case "ice":
		b.handleICE(msg)
	case "pair_exchange":
		b.pairing.OnExchange(msg)
	case "unavailable":
		// our target wasn't online; nothing to do (we're S, we don't initiate)
	default:
		// unknown — ignore (forward compat)
	}
}

func (b *Bridge) handleOffer(msg SignalMsg) {
	if msg.ConnID == "" {
		return
	}
	// one Conn per connId; ignore duplicate offers on the same connId
	b.mu.Lock()
	if _, exists := b.conns[msg.ConnID]; exists {
		b.mu.Unlock()
		return
	}
	// S2: max_connections 强制（§11.3）—— 达上限拒绝新 C（第 N+1 个）
	if b.cfg.MaxConnections > 0 && len(b.conns) >= b.cfg.MaxConnections {
		b.mu.Unlock()
		b.audit.Info("max_connections_reached", "devC", msg.From,
			"current", len(b.conns), "max", b.cfg.MaxConnections)
		return
	}
	router := NewCommandRouter(msg.From, b.exec, b.cfg.DefaultPermissions(), b.audit)
	router.SetSubscribeHook(func(tab string) {
		origTab := tab
		if b.resolveTab != nil {
			tab = b.resolveTab(tab)
		}
		slog.Info("mobilebridge: subscribe_tab",
			"origTab", origTab, "resolvedTab", tab, "connID", msg.ConnID)
		b.setSubscription(msg.ConnID, tab)
	})
	router.SetListSessionsHook(func(sessions []SessionInfo) {
		// 回复会话列表给 C（加密 wireEvent，kind=sessions_list）。
		if conn := b.conns[msg.ConnID]; conn != nil {
			wire, _ := json.Marshal(map[string]any{"kind": "sessions_list", "sessions": sessions})
			_ = conn.SendEvent(wire)
		}
	})
	router.SetListModelsHook(func(models []ModelInfo) {
		if conn := b.conns[msg.ConnID]; conn != nil {
			wire, _ := json.Marshal(map[string]any{"kind": "models_list", "models": models})
			_ = conn.SendEvent(wire)
		}
	})
	router.SetNewTabHook(func(tabID string) {
		if conn := b.conns[msg.ConnID]; conn != nil {
			wire, _ := json.Marshal(map[string]any{"kind": "new_tab_result", "tabId": tabID})
			_ = conn.SendEvent(wire)
		}
	})
	router.SetResyncHook(func(tabID string, sinceSeq uint64) {
		if b.resolveTab != nil {
			tabID = b.resolveTab(tabID)
		}
		b.resyncTab(tabID, sinceSeq, msg.ConnID)
	})
	router.SetLoadSessionHook(func(tab string, history []map[string]any) {
		if conn := b.conns[msg.ConnID]; conn != nil {
			wire, _ := json.Marshal(map[string]any{"kind": "history_messages", "tab": tab, "messages": history})
			_ = conn.SendEvent(wire)
		}
	})
	router.SetOnErrorHook(func(code, errMsg string) {
		if conn := b.conns[msg.ConnID]; conn != nil {
			wire, _ := json.Marshal(map[string]any{"kind": "error", "code": code, "msg": errMsg})
			_ = conn.SendEvent(wire)
		}
	})
	conn, err := NewConn(b.sPriv, b.sPub, b.pairing, router, b.audit)
	if err != nil {
		b.mu.Unlock()
		b.audit.Error("newconn", msg.From, err)
		return
	}
	connID := msg.ConnID
	conn.SetOnReady(func(c *Conn) {
		b.mu.Lock()
		b.byDev[c.DevC()] = c
		b.mu.Unlock()
		if b.onReady != nil {
			b.onReady(c)
		}
	})
	conn.SetOnClose(func(c *Conn) {
		b.mu.Lock()
		delete(b.conns, connID)
		if kc, ok := b.knockS[connID]; ok {
			kc.Close() // 敲门 socket 随连接关闭
			delete(b.knockS, connID)
		}
		if c.DevC() != "" {
			if cur := b.byDev[c.DevC()]; cur == c {
				delete(b.byDev, c.DevC())
			}
		}
		b.removeSubscription(connID)
		b.mu.Unlock()
		b.audit.ConnClose(c.DevC())
	})
	b.conns[connID] = conn
	b.mu.Unlock()

	// create PeerConnection with the configured ICE servers.
	// UDPKnock 开启时：专用 UDP socket + UDPMux 注入 ICE——敲门包从 ICE
	// 同一端口发出，打开的 NAT 映射对 ICE connectivity check 直接生效
	//（换了 socket 敲门是无效的：NAT 映射按内网五元组）。
	pcCfg := webrtc.Configuration{ICEServers: toICEServers(b.cfg)}
	if b.cfg.UDPKnock {
		uc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
		if err != nil {
			b.audit.Error("knock_listen", msg.From, err)
			conn.close()
			return
		}
		se := webrtc.SettingEngine{}
		se.SetICEUDPMux(ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: uc}))
		pc, pcErr := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(pcCfg)
		if pcErr != nil {
			uc.Close()
			b.audit.Error("newpc", msg.From, pcErr)
			conn.close()
			return
		}
		// socket 生命周期挂在 knockS map（OnClose 时统一关闭）
		b.mu.Lock()
		b.knockS[connID] = uc
		b.mu.Unlock()
		b.attachPC(msg, conn, pc)
		return
	}
	pc, err := webrtc.NewPeerConnection(pcCfg)
	if err != nil {
		b.audit.Error("newpc", msg.From, err)
		conn.close()
		return
	}
	b.attachPC(msg, conn, pc)
}

// attachPC 接上候选转发/状态回调、发 answer——knock 与普通两条 PC 创建
// 路径的公共尾部。
func (b *Bridge) attachPC(msg SignalMsg, conn *Conn, pc *webrtc.PeerConnection) {
	connID := msg.ConnID
	// forward our ICE candidates back to C via K
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candJSON := c.ToJSON().Candidate
		// P2-2: 签名 ICE（防信令伪造）
		sigMsg := "ice|" + connID + "|" + b.devS + "|" + msg.From + "|" + candJSON
		sig := ed25519.Sign(b.sPriv, []byte(sigMsg))
		_ = b.signal.Send(SignalMsg{
			Type: "ice", ConnID: connID,
			From: b.devS, To: msg.From,
			Cand: candJSON,
			Sig: base64.URLEncoding.EncodeToString(sig),
		})
	})
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		b.audit.Info("ice_state", "devC", msg.From, "state", s.String())
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		b.audit.Info("pc_state", "devC", msg.From, "state", s.String())
	})
	conn.AttachPC(pc)

	answerSDP, err := conn.HandleOffer(msg.SDP)
	if err != nil {
		b.audit.Error("handleoffer", msg.From, err)
		conn.close()
		return
	}
	// P2-2: 签名 answer（防信令伪造）
	ansSigMsg := "answer|" + connID + "|" + b.devS + "|" + msg.From + "|" + answerSDP
	ansSig := ed25519.Sign(b.sPriv, []byte(ansSigMsg))
	_ = b.signal.Send(SignalMsg{
		Type: "answer", ConnID: connID,
		From: b.devS, To: msg.From,
		SDP:  answerSDP,
		Sig:  base64.URLEncoding.EncodeToString(ansSig),
	})
}

func (b *Bridge) handleICE(msg SignalMsg) {
	b.mu.Lock()
	conn := b.conns[msg.ConnID]
	b.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.AddICECandidate(msg.Cand)
	b.knockAt(msg.ConnID, msg.Cand)
}

// knockAt 单包敲门：收到 C 的 srflx 候选（C 的公网映射，经远程 STUN 探得）
// 时，从 ICE 共享 UDP socket 向其发 3 个敲门包。关键在从同一端口发出——
// NAT 映射按内网五元组，换 socket 敲门无效；共享后打开的映射对 ICE
// connectivity check 直接生效。对 cone NAT 有效；双对称 NAT 无解（§7）。
// 包体 = 魔数 LPKNOCK1|connId|nonce；C 的 ICE 对未知包静默丢弃，无副作用。
func (b *Bridge) knockAt(connID, candStr string) {
	b.mu.Lock()
	enabled := b.cfg.UDPKnock
	uc := b.knockS[connID]
	b.mu.Unlock()
	if !enabled || uc == nil {
		return
	}
	cand, err := ice.UnmarshalCandidate(candStr)
	if err != nil || cand.Type() != ice.CandidateTypeServerReflexive {
		return // 只敲 srflx（公网映射）；host 候选同网段无需敲门
	}
	ip := net.ParseIP(cand.Address())
	if ip == nil {
		return
	}
	addr := &net.UDPAddr{IP: ip, Port: cand.Port()}
	go func() {
		for i := 0; i < 3; i++ {
			nonce := make([]byte, 8)
			_, _ = rand.Read(nonce)
			pkt := append([]byte("LPKNOCK1|"+connID+"|"), nonce...)
			if _, err := uc.WriteToUDP(pkt, addr); err != nil {
				return // socket 已关（连接结束）
			}
			b.audit.Info("udp_knock", "connID", connID, "addr", addr.String())
			time.Sleep(250 * time.Millisecond)
		}
	}()
}

// SetKnock 运行时更新单包敲门开关/服务器（设置面板；影响之后新建的连接）。
func (b *Bridge) SetKnock(enabled bool, server string) {
	b.mu.Lock()
	b.cfg.UDPKnock = enabled
	b.cfg.KnockServer = server
	b.mu.Unlock()
}

// setSubscription records that connId wants events from tabId. Called when the
// C sends subscribe_tab. ForwardEvent consults this to route.
func (b *Bridge) setSubscription(connID, tabID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// remove from old tab
	for tab, set := range b.tabSub {
		if set[connID] {
			delete(set, connID)
			if len(set) == 0 {
				delete(b.tabSub, tab)
			}
		}
	}
	if b.tabSub[tabID] == nil {
		b.tabSub[tabID] = map[string]bool{}
	}
	b.tabSub[tabID][connID] = true
}

func (b *Bridge) removeSubscription(connID string) {
	for tab, set := range b.tabSub {
		if set[connID] {
			delete(set, connID)
			if len(set) == 0 {
				delete(b.tabSub, tab)
			}
		}
	}
}

// ForwardEvent is the injection point called by desktop/tabs.go's
// tabEventSink.Emit: it broadcasts the already-serialized wireEvent JSON to
// every Conn currently subscribed to tabId. Non-blocking — a slow/full Conn
// just drops that frame rather than stalling the Controller.
func (b *Bridge) ForwardEvent(tabID string, wireEventJSON []byte) {
	b.mu.Lock()
	subs := b.tabSub[tabID]
	conns := make([]*Conn, 0, len(subs))
	for connID := range subs {
		if c := b.conns[connID]; c != nil && c.IsEncrypted() {
			conns = append(conns, c)
		}
	}
	// 存入环形缓冲（resync 增量同步，§11.2）：重连的 C 发 resync 时从这里补拉。
	r := b.ringBuf[tabID]
	if r == nil {
		r = &tabRing{}
		b.ringBuf[tabID] = r
	}
	r.seq++
	r.entries = append(r.entries, ringEntry{seq: r.seq, json: wireEventJSON})
	if len(r.entries) > ringCap {
		r.entries = r.entries[len(r.entries)-ringCap:]
	}
	b.mu.Unlock()
	slog.Info("mobilebridge: forward_event",
		"tabID", tabID, "subscribers", len(subs), "targets", len(conns), "eventLen", len(wireEventJSON))
	if len(conns) == 0 {
		slog.Warn("mobilebridge: forward_event dropped — no subscribers for tab", "tabID", tabID)
	}
	for _, c := range conns {
		_ = c.SendEvent(wireEventJSON)
	}
}

// resyncTab 处理 C 的 resync 请求（§11.2）：从环形缓冲查 sinceSeq 之后的事件，
// 够则回投 resync_delta；缓存已超（重启/超出 200 条）则 resync_full（C 重新拉全量）。
func (b *Bridge) resyncTab(tabID string, sinceSeq uint64, connID string) {
	b.mu.Lock()
	r := b.ringBuf[tabID]
	conn := b.conns[connID]
	b.mu.Unlock()
	if conn == nil {
		return
	}
	if r == nil || len(r.entries) == 0 || r.entries[0].seq > sinceSeq {
		wire, _ := json.Marshal(map[string]any{"kind": "resync_full", "tab": tabID})
		_ = conn.SendEvent(wire)
		return
	}
	delta := make([]json.RawMessage, 0, len(r.entries))
	for _, e := range r.entries {
		if e.seq > sinceSeq {
			delta = append(delta, e.json)
		}
	}
	wire, _ := json.Marshal(map[string]any{"kind": "resync_delta", "tab": tabID, "events": delta})
	_ = conn.SendEvent(wire)
}

// NotifySessionListChanged 广播 session_list_changed 给所有在线加密 Conn（方案B）。
// fairpeer 端 tab 增删改时调，让 linkpeer 自动刷新会话列表，无需用户手动下拉。
func (b *Bridge) NotifySessionListChanged() {
	b.mu.Lock()
	conns := make([]*Conn, 0, len(b.conns))
	for _, c := range b.conns {
		if c.IsEncrypted() {
			conns = append(conns, c)
		}
	}
	b.mu.Unlock()
	wire, _ := json.Marshal(map[string]any{"kind": "session_list_changed"})
	for _, c := range conns {
		_ = c.SendEvent(wire)
	}
}

// toICEServers builds the pion ICEServers from config (STUN always; TURN opt-in).
func toICEServers(cfg Config) []webrtc.ICEServer {
	// 敲门依赖的远程 STUN 去重后并入——两端都要能探到 srflx，敲门才有目标。
	seen := map[string]bool{}
	var urls []string
	for _, s := range cfg.STUNServers {
		if !seen[s] {
			seen[s] = true
			urls = append(urls, s)
		}
	}
	if cfg.KnockServer != "" && !seen[cfg.KnockServer] {
		urls = append(urls, cfg.KnockServer)
	}
	servers := []webrtc.ICEServer{}
	for _, s := range urls {
		servers = append(servers, webrtc.ICEServer{URLs: []string{s}})
	}
	if cfg.TURNEnabled {
		for _, s := range cfg.TURNServers {
			servers = append(servers, webrtc.ICEServer{URLs: []string{s}})
		}
	}
	return servers
}

// ErrNotStarted is returned when Bridge operations are attempted before Start.
var ErrNotStarted = errors.New("bridge not started")

var _ SignalHandler = (*Bridge)(nil)
