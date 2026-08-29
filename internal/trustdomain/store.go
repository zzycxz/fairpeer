package trustdomain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// archiveFormat versions the on-disk layout; bump on breaking changes so a
// future Load can reject what it cannot interpret (same philosophy as
// rule_version for records).
const archiveFormat uint32 = 1

var ErrNoLedger = errors.New("trustdomain: no ledger on disk")

// Archive is the on-disk/exported form of a ledger (spec §16-5: 全链 + 检查点
// 签名(inside blocks) + 验证规则版本元数据). DomainID is the genesis hash —
// the domain's permanent identity; archives of different domains never mix.
type Archive struct {
	Format      uint32   `json:"format"`
	RuleVersion uint32   `json:"rule_version"`
	DomainID    string   `json:"domain_id"`
	Blocks      []*Block `json:"blocks"`
}

// Store persists a ledger under dir (file "ledger.json"). Save is
// atomic-ish: write to temp, then replace. The crash window between remove
// and rename on Windows leaves at worst a stale .tmp beside a missing main
// file — recoverable from a peer resync; a production deployment may swap
// this for the sqlite store without touching callers.
type Store struct {
	dir string
}

// OpenStore creates dir if needed and returns the store.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Path is the ledger file location.
func (s *Store) Path() string { return filepath.Join(s.dir, "ledger.json") }

// Save atomically-ish persists the chain.
func (s *Store) Save(c *Chain) error {
	data, err := ArchiveBytes(c)
	if err != nil {
		return err
	}
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Remove(s.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, s.Path())
}

// Load reads and — crucially — fully revalidates the ledger from disk.
// Local storage is untrusted like any other input (全员验证 invariant): a
// tampered, truncated or substituted file fails with the same located errors
// as a malicious peer would.
func (s *Store) Load() (*Chain, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoLedger
		}
		return nil, err
	}
	var ar Archive
	if err := json.Unmarshal(data, &ar); err != nil {
		return nil, fmt.Errorf("trustdomain: ledger file corrupt: %w", err)
	}
	if ar.Format != archiveFormat {
		return nil, fmt.Errorf("trustdomain: ledger format %d != supported %d", ar.Format, archiveFormat)
	}
	c, err := ValidateChain(ar.Blocks)
	if err != nil {
		return nil, err
	}
	if DomainID(c) != ar.DomainID {
		return nil, fmt.Errorf("trustdomain: archive domain ID mismatch (file claims %s)", ar.DomainID)
	}
	return c, nil
}

// ArchiveBytes serializes the chain for cold archival/export (§6.6 冷归档).
func ArchiveBytes(c *Chain) ([]byte, error) {
	ar := Archive{
		Format:      archiveFormat,
		RuleVersion: c.state.RuleVersion,
		DomainID:    DomainID(c),
		Blocks:      c.blocks,
	}
	return json.MarshalIndent(ar, "", " ")
}

// DomainID returns the genesis block hash — the chain's permanent identity.
func DomainID(c *Chain) string {
	return c.hashes[0].Hex()
}
