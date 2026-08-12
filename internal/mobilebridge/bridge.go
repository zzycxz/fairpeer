package mobilebridge

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"

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
	tabSub  map[string]map[string]bool  // tabId → set[connId]
	ctx     context.Context
}

// NewBridge wires everything but does NOT connect yet (Start does that).
func NewBridge(cfg Config, sPriv ed25519.PrivateKey, sPub ed25519.PublicKey, store KeyStore, exec CommandExecutor, audit *Audit) *Bridge {
	b := &Bridge{
		cfg: cfg, sPriv: sPriv, sPub: sPub, devS: DevID(sPub),
		store: store, exec: exec, audit: audit,
		conns: map[string]*Conn{},
		byDev: map[string]*Conn{},
		tabSub: map[string]map[string]bool{},
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
	return b.signal.Run(ctx)
}

// StartPairing kicks off a new pairing session (Wails UI calls this).
func (b *Bridge) StartPairing() (code, qrURL string, err error) {
	return b.pairing.StartPairing()
}

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
	router := NewCommandRouter(msg.From, b.exec, b.cfg.DefaultPermissions(), b.audit)
	router.SetSubscribeHook(func(tab string) { b.setSubscription(msg.ConnID, tab) })
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
	})
	conn.SetOnClose(func(c *Conn) {
		b.mu.Lock()
		delete(b.conns, connID)
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

	// create PeerConnection with the configured ICE servers
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: toICEServers(b.cfg),
	})
	if err != nil {
		b.audit.Error("newpc", msg.From, err)
		conn.close()
		return
	}
	// forward our ICE candidates back to C via K
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		_ = b.signal.Send(SignalMsg{
			Type: "ice", ConnID: connID,
			From: b.devS, To: msg.From,
			Cand: c.ToJSON().Candidate,
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
	_ = b.signal.Send(SignalMsg{
		Type: "answer", ConnID: connID,
		From: b.devS, To: msg.From,
		SDP: answerSDP,
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
	b.mu.Unlock()
	for _, c := range conns {
		_ = c.SendEvent(wireEventJSON)
	}
}

// toICEServers builds the pion ICEServers from config (STUN always; TURN opt-in).
func toICEServers(cfg Config) []webrtc.ICEServer {
	servers := []webrtc.ICEServer{}
	for _, s := range cfg.STUNServers {
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
