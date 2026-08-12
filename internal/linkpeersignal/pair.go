package linkpeersignal

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"sync"
	"time"
)

// Pair is one in-flight pairing session: created by S on register, completed
// when C exchanges and S confirms. TTL-enforced; never persisted to disk.
// K forgets everything on restart — by design (PROTOCOL §10.4).
//
// Public keys are stored as raw []byte (32-byte Ed25519). The HTTP layer is
// responsible for base64 encode/decode at the wire boundary — PairStore itself
// is encoding-agnostic, which keeps it testable without base64 plumbing.
type Pair struct {
	PairID      string
	Code        string
	DevS        string
	PubS        []byte
	FpS         string
	PubC        []byte // filled on successful exchange
	FpC, DevC   string
	FailedCount int
	Confirmed   bool
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

func (p *Pair) Expired(now time.Time) bool { return now.After(p.ExpiresAt) }

// Pair errors. The HTTP layer maps these to status + error codes (server.go).
var (
	ErrPairNotFound = errors.New("pair_not_found")
	ErrCodeMismatch = errors.New("code_mismatch")
	ErrPairLocked   = errors.New("pair_locked")
	ErrPairExpired  = errors.New("pair_expired")
	ErrCodeConflict = errors.New("code_conflict")
	ErrCapacityFull = errors.New("capacity_full")
	ErrFpMismatch   = errors.New("fp_mismatch")
)

// PairStore holds all in-flight pairs. Memory-only.
type PairStore struct {
	mu     sync.Mutex
	pairs  map[string]*Pair  // pairID → pair
	byCode map[string]string // code → pairID (uniqueness index)
	cfg    PairConfig
	now    func() time.Time
	rng    func(p []byte) (int, error)
}

func NewPairStore(cfg PairConfig) *PairStore {
	return &PairStore{
		pairs:  map[string]*Pair{},
		byCode: map[string]string{},
		cfg:    cfg,
		now:    time.Now,
		rng:    rand.Read,
	}
}

// GenCode generates a 6-char code from an alphabet with confusable chars
// (0,O,1,I,L) removed. ~29 bits entropy; combined with 60s TTL + 5-fail lock,
// online brute force is infeasible. Note: this alphabet is 31 chars (one short
// of base32) because it is for human-readable pair codes only, NOT a base32
// encoding — mod-31 selection is fine here.
func (s *PairStore) GenCode() (string, error) {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // 31 chars, 0/O/1/I/L removed
	b := make([]byte, 6)
	if _, err := s.rng(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

// Register creates a pair for S. Validates fp self-consistency (fpS must equal
// fingerprint(pubS)), code uniqueness, per-dev concurrency, and global capacity.
func (s *PairStore) Register(code, devS string, pubS []byte, fpS string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()

	if want := fingerprint(pubS); subtle.ConstantTimeCompare([]byte(fpS), []byte(want)) != 1 {
		return "", ErrFpMismatch
	}
	if len(s.pairs) >= s.cfg.MaxGlobal {
		return "", ErrCapacityFull
	}
	devCount := 0
	for _, p := range s.pairs {
		if p.DevS == devS && !p.Expired(s.now()) {
			devCount++
		}
	}
	if devCount >= s.cfg.MaxConcurrentPerDev {
		return "", ErrCapacityFull
	}
	if _, exists := s.byCode[code]; exists {
		return "", ErrCodeConflict
	}
	pid := make([]byte, 16)
	if _, err := s.rng(pid); err != nil {
		return "", err
	}
	pairID := b32.EncodeToString(pid)
	now := s.now()
	p := &Pair{
		PairID: pairID, Code: code,
		DevS: devS, PubS: pubS, FpS: fpS,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(s.cfg.CodeTTL) * time.Second),
	}
	s.pairs[pairID] = p
	s.byCode[code] = pairID
	return pairID, nil
}

// Exchange is called by C. Validates pairId existence + code (constant-time) +
// C's fp self-consistency. On success records C's keys and returns S's pub/fp.
//
// NOTE: we do NOT sweep at the top of Exchange — a pair that just expired must
// report ErrPairExpired (so the caller sees the right code), not
// ErrPairNotFound. Background sweep (main's ticker) handles long-expired pairs.
func (s *PairStore) Exchange(pairID, code, devC string, pubC []byte, fpC string) (pubS []byte, fpS string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pairs[pairID]
	if !ok {
		return nil, "", ErrPairNotFound
	}
	if p.Expired(s.now()) {
		s.deleteLocked(pairID)
		return nil, "", ErrPairExpired
	}
	if subtle.ConstantTimeCompare([]byte(p.Code), []byte(code)) != 1 {
		p.FailedCount++
		if p.FailedCount >= s.cfg.MaxFailPerPair {
			s.deleteLocked(pairID)
			return nil, "", ErrPairLocked
		}
		return nil, "", ErrCodeMismatch
	}
	if want := fingerprint(pubC); subtle.ConstantTimeCompare([]byte(fpC), []byte(want)) != 1 {
		p.FailedCount++
		return nil, "", ErrFpMismatch
	}
	p.PubC, p.FpC, p.DevC = pubC, fpC, devC
	return p.PubS, p.FpS, nil
}

// Confirm marks a pair confirmed by S (after the desktop user clicks confirm).
func (s *PairStore) Confirm(pairID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pairs[pairID]
	if !ok {
		return ErrPairNotFound
	}
	if p.Expired(s.now()) {
		s.deleteLocked(pairID)
		return ErrPairExpired
	}
	p.Confirmed = true
	return nil
}

// Get returns a snapshot of a pair (for S to read C's pub after exchange).
func (s *PairStore) Get(pairID string) (*Pair, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pairs[pairID]
	return p, ok
}

// Delete removes a pair.
func (s *PairStore) Delete(pairID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteLocked(pairID)
}

// Sweep purges expired pairs (called periodically by main's ticker, and
// opportunistically at the top of Register).
func (s *PairStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
}

func (s *PairStore) deleteLocked(pairID string) {
	if p, ok := s.pairs[pairID]; ok {
		delete(s.byCode, p.Code)
		delete(s.pairs, pairID)
	}
}

func (s *PairStore) sweepLocked() {
	now := s.now()
	for pid, p := range s.pairs {
		if p.Expired(now) {
			delete(s.byCode, p.Code)
			delete(s.pairs, pid)
		}
	}
}

// SetNow / SetRng for tests.
func (s *PairStore) SetNow(now func() time.Time)         { s.now = now }
func (s *PairStore) SetRng(rng func([]byte) (int, error)) { s.rng = rng }
