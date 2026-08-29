package netdev

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// pushRecorder captures bot pushes so the aggregation test can count them.
type pushRecorder struct {
	mu    sync.Mutex
	texts []string
}

func (p *pushRecorder) Push(_ context.Context, _ string, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.texts = append(p.texts, text)
	return nil
}

// Same-source findings inside the window must merge to ONE push + ONE
// summary; the first finding of a fresh source always pushes immediately.
func TestNotifyAggregationWindow(t *testing.T) {
	rec := &pushRecorder{}
	SetNotifyPusher(rec)
	defer SetNotifyPusher(nil)
	notifyMu.Lock()
	savedURL, savedMin, savedDst := notifyURL, notifyMinSev, notifyBotDst
	notifyURL, notifyMinSev, notifyBotDst = "", "info", "feishu:test"
	notifyMu.Unlock()
	defer func() {
		notifyMu.Lock()
		notifyURL, notifyMinSev, notifyBotDst = savedURL, savedMin, savedDst
		notifyMu.Unlock()
	}()

	f1 := &Finding{ID: "F1", Title: "接口掉线", Severity: SeverityWarning, Devices: []string{"sw1"}, Source: "alert:r1:sw1", Status: "active"}
	f2 := &Finding{ID: "F2", Title: "接口掉线", Severity: SeverityWarning, Devices: []string{"sw1"}, Source: "alert:r1:sw1", Status: "active"}
	f3 := &Finding{ID: "F3", Title: "别的告警", Severity: SeverityCritical, Devices: []string{"sw2"}, Source: "alert:r2:sw2", Status: "active"}

	notifyFindingAsync(f1)
	notifyFindingAsync(f2) // same source within window → suppressed
	notifyFindingAsync(f3) // different source → immediate

	deadline := time.Now().Add(2 * time.Second)
	for {
		rec.mu.Lock()
		n := len(rec.texts)
		rec.mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec.mu.Lock()
	n2 := len(rec.texts)
	texts2 := append([]string(nil), rec.texts...)
	rec.mu.Unlock()
	if n2 != 2 {
		t.Fatalf("expected 2 pushes (f1 + f3; f2 suppressed), got %d: %v", n2, texts2)
	}
	// Push goroutines race: order is not guaranteed, content-as-a-set is.
	joined := strings.Join(texts2, " | ")
	if !strings.Contains(joined, "接口掉线") || !strings.Contains(joined, "别的告警") {
		t.Errorf("unexpected push content: %v", texts2)
	}

	// Expire the aggregation timers so the summary flush fires (window-close
	// digest with the merged count), then check it mentions 共 2 条.
	aggMu.Lock()
	for _, st := range aggStateBySource {
		if st.timer != nil {
			st.timer.Reset(10 * time.Millisecond)
		}
	}
	aggMu.Unlock()
	deadline = time.Now().Add(2 * time.Second)
	for {
		rec.mu.Lock()
		n := len(rec.texts)
		rec.mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec.mu.Lock()
	final := append([]string(nil), rec.texts...)
	rec.mu.Unlock()
	if len(final) < 3 || !strings.Contains(strings.Join(final, " | "), "共 2 条") {
		t.Errorf("summary digest missing: %v", final)
	}
}
