package trustdomain

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
)

const identityFile = "identity.key"

// errBadIdentityFile guards against truncated/corrupted key material.
var errBadIdentityFile = errors.New("trustdomain: identity.key is corrupt or truncated")

// LoadOrCreateIdentity returns this host's long-term domain identity,
// creating it on first use. The key file stores the 32-byte Ed25519 seed
// with 0600 permissions. Production deployments should place DataDir on
// appropriate storage and may migrate custody into secret.Store
// (DPAPI/Keychain/Secret Service — spec §17); the file format stays
// compatible (raw seed bytes).
func LoadOrCreateIdentity(dataDir string) (*Identity, error) {
	path := filepath.Join(dataDir, identityFile)
	if seed, err := os.ReadFile(path); err == nil {
		if len(seed) != ed25519.SeedSize {
			return nil, errBadIdentityFile
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return &Identity{Public: priv.Public().(ed25519.PublicKey), Private: priv}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	id, err := GenerateIdentity()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	seed := id.Private.Seed()
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		return nil, err
	}
	return id, nil
}

// IdentityKeyPath exposes where the identity lives (diagnostics UI).
func IdentityKeyPath(dataDir string) string {
	return filepath.Join(dataDir, identityFile)
}
