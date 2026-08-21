package mobilebridge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// b32 is the Crockford base32 alphabet (matches linkpeersignal/codec.go —
// single source of truth is PROTOCOL §2.4; cross-checked by test vectors).
var b32 = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// DevID = base32(SHA256(pub)[:10]). The self-consistency invariant K uses for
// stateless WS auth: anyone can recompute it from pub, so K never stores it.
func DevID(pub []byte) string {
	h := sha256.Sum256(pub)
	return b32.EncodeToString(h[:10])
}

// Fingerprint = base32(SHA256(pub)[:8]). Human-comparable; verified out-of-band
// (C compares it against the QR code locally to defeat MITM at pairing time).
func Fingerprint(pub []byte) string {
	h := sha256.Sum256(pub)
	return b32.EncodeToString(h[:8])
}

// GenerateLongTerm creates a fresh Ed25519 keypair for device identity.
func GenerateLongTerm() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// ConstantTimeEqual guards signature/fingerprint comparisons against timing
// oracles. Use this anywhere secret-derived bytes are compared.
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// b64 / b64d are standard base64 for JSON message fields (handshake hellos,
// commands). Note: WS-auth URL params use URLEncoding instead — see signal_client.
func b64(b []byte) string  { return base64.StdEncoding.EncodeToString(b) }
func b64d(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// b64uAny decodes URL-safe base64 tolerating missing padding — the inbound
// signal sig field: C signs with no-padding encoding, S's own outbound uses
// padded URLEncoding (P2-2 double-side sig verify).
func b64uAny(s string) ([]byte, error) {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return base64.URLEncoding.DecodeString(s)
}

// Random fills n bytes from crypto/rand.
func Random(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(rand.Reader, b)
	return b, err
}

// DeriveSessionKeys runs HKDF-SHA256 over the X25519 shared secret to produce
// two direction-split AES-256 keys: c2s (C→S) and s2c (S→C). Splitting
// direction prevents reflection attacks (an attacker can't bounce S's own
// frames back at it). Salt = nc‖ns binds both nonces into the key.
func DeriveSessionKeys(shared, nc, ns []byte) (c2s, s2c []byte) {
	salt := make([]byte, 0, len(nc)+len(ns))
	salt = append(salt, nc...)
	salt = append(salt, ns...)
	return deriveKey(shared, salt, []byte("linkpeer v1 c2s")),
		deriveKey(shared, salt, []byte("linkpeer v1 s2c"))
}

func deriveKey(shared, salt, info []byte) []byte {
	out := make([]byte, 32)
	r := hkdf.New(sha256.New, shared, salt, info)
	if _, err := io.ReadFull(r, out); err != nil {
		panic("hkdf: " + err.Error()) // only on bad reader; sha256 never is
	}
	return out
}

// ECDHShared computes the X25519 shared secret from our ephemeral private key
// and the peer's ephemeral public key bytes.
func ECDHShared(ephPriv *ecdh.PrivateKey, peerEphPub []byte) ([]byte, error) {
	if len(peerEphPub) != 32 {
		return nil, errors.New("ephemeral public key must be 32 bytes")
	}
	pub, err := ecdh.X25519().NewPublicKey(peerEphPub)
	if err != nil {
		return nil, err
	}
	return ephPriv.ECDH(pub)
}

// NewAEAD wraps AES-256-GCM under a 32-byte session key.
func NewAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("session key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// GenerateEphemeral returns a fresh X25519 keypair for one handshake.
func GenerateEphemeral() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}
