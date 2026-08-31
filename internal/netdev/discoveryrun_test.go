package netdev

import (
	"path/filepath"
	"testing"
)

func TestDiscoveryRunStateRoundtrip(t *testing.T) {
	dir := t.TempDir()
	oldRun := discoveryRunOver
	discoveryRunOver = filepath.Join(dir, "run.json")
	t.Cleanup(func() { discoveryRunOver = oldRun })

	run := &DiscoveryRunState{
		ID: "r1", Vantage: "CORE-1", Cidrs: []string{"10.20.0.0/24", "10.21.0.0/24", "10.22.0.0/24"},
		DoneCidrs: []string{"10.20.0.0/24"}, Status: "paused", StartedAt: "t0",
	}
	if err := SaveDiscoveryRun(run); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDiscoveryRun()
	if err != nil || got == nil {
		t.Fatalf("load: %v %v", got, err)
	}
	rem := got.Remaining()
	if len(rem) != 2 || rem[0] != "10.21.0.0/24" || rem[1] != "10.22.0.0/24" {
		t.Fatalf("remaining = %v", rem)
	}
	// A fully done run resumes to nothing.
	got.DoneCidrs = got.Cidrs
	if rem := got.Remaining(); len(rem) != 0 {
		t.Fatalf("done run remaining = %v", rem)
	}
}

func TestDeviceLayerLedgerAndGuard(t *testing.T) {
	dir := t.TempDir()
	oldLedger := deviceLayersOver
	deviceLayersOver = filepath.Join(dir, "layers.json")
	t.Cleanup(func() { deviceLayersOver = oldLedger })

	if err := RecordDeviceLayer("CORE-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := RecordDeviceLayer("PROMO-1", 1); err != nil {
		t.Fatal(err)
	}
	m := LoadDeviceLayers()
	if m["CORE-1"] != 0 || m["PROMO-1"] != 1 {
		t.Fatalf("ledger = %v", m)
	}

	// Guard: layer-0 vantage discovers layer 1 (ok at max_hops 2); a layer-2
	// vantage would produce layer 3 → refused with the knob named.
	if err := layerGuard(2, 0, "CORE-1"); err != nil {
		t.Errorf("layer-0 vantage must pass: %v", err)
	}
	if err := layerGuard(2, 2, "DEEP-3"); err == nil {
		t.Error("layer-2 vantage at max_hops=2 must refuse")
	}
	// Effective config: 0→2 default, clamp 1..4.
	for in, want := range map[int]int{0: 2, 1: 1, 4: 4, 9: 4} {
		if got := maxHopsEffective(in); got != want {
			t.Errorf("maxHopsEffective(%d) = %d, want %d", in, got, want)
		}
	}
}
