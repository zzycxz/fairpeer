package netdev

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestAppendAuditConcurrentChainStaysIntact covers the chain-hash race: the
// prev-read → hash → append → cache-update sequence must be one critical
// section. Computing the hash before taking the lock let two concurrent
// appends chain off the same prev, and VerifyAuditChain then reported
// tampering that never happened.
func TestAppendAuditConcurrentChainStaysIntact(t *testing.T) {
	SetAuditPath(filepath.Join(t.TempDir(), "audit.jsonl"))
	t.Cleanup(func() { SetAuditPath(""); auditLastHash = "" })

	const goroutines, perG = 8, 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := AppendAudit(Audit{
					Device: fmt.Sprintf("(t%d)", g), Command: fmt.Sprintf("op %d", i),
					Class: "read", Status: AuditOK,
				}); err != nil {
					t.Errorf("AppendAudit: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	st := VerifyAuditChain()
	if !st.OK {
		t.Fatalf("chain broken after concurrent appends: %+v", st)
	}
	if st.Total != goroutines*perG {
		t.Fatalf("total = %d, want %d", st.Total, goroutines*perG)
	}
}
