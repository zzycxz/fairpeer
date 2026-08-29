package builtin

import "testing"

// The mirror sink is a process-global; save/restore in every test so parallel
// packages never observe a leaked test sink.
func withPanelSink(t *testing.T, fn func(BrowserPanelFrame)) {
	t.Helper()
	prev := browserPanelSink
	browserPanelSink = fn
	t.Cleanup(func() { browserPanelSink = prev })
}

func TestEmitBrowserPanelNilSinkNoop(t *testing.T) {
	withPanelSink(t, nil) // must not panic
	EmitBrowserPanel(BrowserPanelFrame{Kind: "frame", Source: "tool"})
}

func TestEmitBrowserPanelForwardsFrame(t *testing.T) {
	var got []BrowserPanelFrame
	withPanelSink(t, func(f BrowserPanelFrame) { got = append(got, f) })

	EmitBrowserPanel(BrowserPanelFrame{Kind: "status", Source: "tool", Phase: "start", Text: "Chrome"})
	EmitBrowserPanel(BrowserPanelFrame{Kind: "frame", Source: "auto", Image: "data:image/png;base64,QQ"})

	if len(got) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(got))
	}
	if got[0].Phase != "start" || got[0].Text != "Chrome" {
		t.Errorf("start frame mismatch: %+v", got[0])
	}
	if got[1].Source != "auto" || got[1].Image == "" {
		t.Errorf("auto frame mismatch: %+v", got[1])
	}
}
