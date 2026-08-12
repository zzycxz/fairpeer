package mobilebridge

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// pairCodeAlphabet omits confusable 0/O/1/I/L (31 chars; NOT a base32 encoding,
// just mod-31 selection for a human-readable 6-char code). Matches
// linkpeersignal.PairStore.GenCode — keep in sync via the pair-flow integration test.
const pairCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func genPairCode() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = pairCodeAlphabet[int(b[i])%len(pairCodeAlphabet)]
	}
	return string(b)
}

// ErrNoPending means no C is waiting at the given pairId when Confirm is called.
var ErrNoPending = errors.New("no pending pair")

// PendingPair is a C that has exchanged and is awaiting the desktop user's confirm.
type PendingPair struct {
	PairID    string
	DevC      string
	PubC      ed25519.PublicKey
	FpC       string
	CreatedAt time.Time
}

// Pairing drives the desktop side of pair/confirm/unpair against K and the
// local KeyStore. The Wails UI calls StartPairing/Confirm/Unpair; the
// SignalClient feeds OnExchange when K pushes a pair_exchange notice.
type Pairing struct {
	signalURL  string
	devID      string
	longPub    ed25519.PublicKey
	store      KeyStore
	httpc      *http.Client
	audit      *Audit
	onExchange func(pairID, devC, fpC string) // optional UI hook

	mu      sync.Mutex
	pending map[string]*PendingPair

	autoConfirm bool // 联调：OnExchange 后立即 Confirm（不等用户点允许）
}

func NewPairing(signalURL string, pub ed25519.PublicKey, store KeyStore, audit *Audit) *Pairing {
	return &Pairing{
		signalURL: signalURL,
		devID:     DevID(pub),
		longPub:   pub,
		store:     store,
		httpc:     &http.Client{Timeout: 15 * time.Second},
		audit:     audit,
		pending:   map[string]*PendingPair{},
	}
}

// SetOnExchange installs the UI callback fired when a C scans + exchanges.
func (p *Pairing) SetOnExchange(fn func(pairID, devC, fpC string)) { p.onExchange = fn }

// SetAutoConfirm enables automatic confirmation: OnExchange immediately POSTs
// /pair/confirm + persists C's key, so C's ClientHello (which follows fast)
// passes PeerPub. Desktop user still sees the pending→confirmed UI.
func (p *Pairing) SetAutoConfirm(v bool) { p.autoConfirm = v }

// StartPairing generates a code, registers with K, returns the QR payload
// (linkpeer://pair?...). The QR carries the fingerprint out-of-band so C can
// defeat a MITM'd K at the exchange step.
func (p *Pairing) StartPairing() (code, qrURL string, err error) {
	code = genPairCode()
	pubSB64 := b64(p.longPub)
	fp := Fingerprint(p.longPub)
	body, _ := json.Marshal(map[string]string{
		"code": code, "devS": p.devID, "pubS": pubSB64, "fpS": fp,
	})
	resp, err := p.httpc.Post(p.signalURL+"/pair/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		eb, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("register: %d %s", resp.StatusCode, eb)
	}
	var r struct {
		PairID    string `json:"pairId"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", err
	}
	if r.PairID == "" {
		return "", "", errors.New("register: empty pairId")
	}
	qrURL = fmt.Sprintf("linkpeer://pair?pid=%s&code=%s&fp=%s&dev=%s&relay=%s",
		r.PairID, code, fp, p.devID, p.signalURL)
	p.audit.PairStart(p.devID)
	return code, qrURL, nil
}

// OnExchange handles a pair_exchange notice pushed from K (delivered by the
// SignalClient). Records C as pending and fires the UI hook.
func (p *Pairing) OnExchange(msg SignalMsg) {
	pubC, err := b64d(msg.PubC)
	if err != nil || len(pubC) != ed25519.PublicKeySize {
		p.audit.Error("pair_exchange_bad_pubc", msg.DevC, err)
		return
	}
	pp := &PendingPair{
		PairID: msg.PairID, DevC: msg.DevC,
		PubC: ed25519.PublicKey(pubC), FpC: msg.FpC,
		CreatedAt: time.Now(),
	}
	p.mu.Lock()
	p.pending[msg.PairID] = pp
	auto := p.autoConfirm
	p.mu.Unlock()
	if p.onExchange != nil {
		p.onExchange(msg.PairID, msg.DevC, msg.FpC)
	}
	// 联调捷径：自动确认。C 扫码 exchange 后会在几百 ms 内连 WebRTC 发
	// ClientHello；手动确认来不及。异步 Confirm（POST + store.Set）通常比
	// ICE 协商快，所以 ClientHello 到达时 pubC 已落盘。
	if auto {
		go func() { _ = p.Confirm(msg.PairID) }()
	}
}

// Pending lists C's awaiting confirm (for the Wails UI).
func (p *Pairing) Pending() []PendingPair {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PendingPair, 0, len(p.pending))
	for _, pp := range p.pending {
		out = append(out, *pp)
	}
	return out
}

// Confirm: the desktop user accepted. POST /pair/confirm to K, then persist
// C's public key. After this, C can handshake (Pairing.PeerPub returns its key).
func (p *Pairing) Confirm(pairID string) error {
	p.mu.Lock()
	pp := p.pending[pairID]
	p.mu.Unlock()
	if pp == nil {
		return ErrNoPending
	}
	body, _ := json.Marshal(map[string]string{"pairId": pairID, "devS": p.devID})
	resp, err := p.httpc.Post(p.signalURL+"/pair/confirm", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("confirm: %d", resp.StatusCode)
	}
	if err := p.store.Set("mobilebridge.peer."+pp.DevC+".pub", []byte(pp.PubC)); err != nil {
		return err
	}
	p.mu.Lock()
	delete(p.pending, pairID)
	p.mu.Unlock()
	p.audit.PairConfirmed(pp.DevC, p.devID)
	return nil
}

// Reject drops a pending pair without persisting (desktop user declined).
func (p *Pairing) Reject(pairID string) {
	p.mu.Lock()
	delete(p.pending, pairID)
	p.mu.Unlock()
}

// Unpair deletes C's key and appends to the local revocation list. A revoked
// device's next handshake gets refused at hello_c verification (PROTOCOL §5.4).
func (p *Pairing) Unpair(devC string) {
	_ = p.store.Delete("mobilebridge.peer." + devC + ".pub")
	revoked, _ := p.store.Get("mobilebridge.revoked")
	entry := []byte(devC + ",")
	if !bytes.Contains(revoked, entry) {
		revoked = append(revoked, entry...)
		_ = p.store.Set("mobilebridge.revoked", revoked)
	}
	p.audit.Unpaired(devC)
}

// PeerPub returns C's stored public key if paired, else (nil,false).
func (p *Pairing) PeerPub(devC string) (ed25519.PublicKey, bool) {
	b, err := p.store.Get("mobilebridge.peer." + devC + ".pub")
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(b), true
}

// IsRevoked checks the local revocation list.
func (p *Pairing) IsRevoked(devC string) bool {
	revoked, _ := p.store.Get("mobilebridge.revoked")
	return bytes.Contains(revoked, []byte(devC+","))
}
