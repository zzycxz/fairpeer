package trustdomain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Hash is a SHA-256 digest. Blocks and audit anchors are identified by these.
type Hash [32]byte

// ZeroHash is the parent of the genesis block.
var ZeroHash Hash

func (h Hash) Hex() string { return hex.EncodeToString(h[:]) }

func (h Hash) MarshalText() ([]byte, error) { return []byte(h.Hex()), nil }
func (h *Hash) UnmarshalText(b []byte) error {
	raw, err := hex.DecodeString(string(b))
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("trustdomain: bad hash %q", string(b))
	}
	copy(h[:], raw)
	return nil
}

// Checkpoint is an m-of-n admin co-signature over a past block hash
// (spec §5.5). It is the fork-arbiter: the chain containing the most recent
// valid checkpoint wins; length only breaks ties.
type Checkpoint struct {
	TargetHeight uint64     `json:"target_height"`
	TargetHash   Hash       `json:"target_hash"`
	Sigs         []Approval `json:"sigs"` // admins over checkpointMaterial
}

func checkpointMaterial(height uint64, target Hash) []byte {
	m := make([]byte, 0, 8+32)
	var h [8]byte
	for i := 0; i < 8; i++ {
		h[i] = byte(height >> (56 - 8*i))
	}
	m = append(m, h[:]...)
	m = append(m, target[:]...)
	return m
}

// Block is one ledger block. Any mutation of its fields invalidates Hash().
type Block struct {
	Height      uint64      `json:"height"`
	PrevHash    Hash        `json:"prev_hash"`
	Records     []*Record   `json:"records"`
	Proposer    []byte      `json:"proposer"` // member public key
	ProposerSig []byte      `json:"proposer_sig"`
	Checkpoint  *Checkpoint `json:"checkpoint,omitempty"`
}

// RecordsHash folds every record's canonical JSON into one digest.
func (b *Block) RecordsHash() Hash {
	h := sha256.New()
	for _, rec := range b.Records {
		rh := sha256.Sum256(mustJSON(rec))
		h.Write(rh[:])
	}
	return Hash(h.Sum(nil))
}

// blockHeader is the canonical preimage of Block.Hash: the full header as
// one JSON object — unambiguous field boundaries, deterministic field order.
type blockHeader struct {
	Height      uint64      `json:"height"`
	PrevHash    Hash        `json:"prev_hash"`
	RecordsHash Hash        `json:"records_hash"`
	Proposer    []byte      `json:"proposer"`
	ProposerSig []byte      `json:"proposer_sig"`
	Checkpoint  *Checkpoint `json:"checkpoint,omitempty"`
}

func (b *Block) blockMaterial() []byte {
	return mustJSON(blockHeader{
		Height:      b.Height,
		PrevHash:    b.PrevHash,
		RecordsHash: b.RecordsHash(),
		Proposer:    b.Proposer,
		ProposerSig: b.ProposerSig,
		Checkpoint:  b.Checkpoint,
	})
}

// Hash returns the block's digest.
func (b *Block) Hash() Hash { return sha256.Sum256(b.blockMaterial()) }

// proposerMaterial is what the proposing member signs: their endorsement of
// exactly this height, parent, and record set.
func (b *Block) proposerMaterial() []byte {
	rh := b.RecordsHash()
	m := make([]byte, 0, 8+32+32)
	var h [8]byte
	for i := 0; i < 8; i++ {
		h[i] = byte(b.Height >> (56 - 8*i))
	}
	m = append(m, h[:]...)
	m = append(m, b.PrevHash[:]...)
	m = append(m, rh[:]...)
	return m
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// All types in this package marshal infallibly (structs, ints,
		// byte slices); a failure means a programming error upstream.
		panic("trustdomain: marshal: " + err.Error())
	}
	return b
}
