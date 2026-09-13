package netdev

import "testing"

// layerCommands drives multi-layer discovery per host driver; the case labels
// must match drvKey's actual values ("linux-shell"/"windows-powershell" from
// driver.Driver.Key()) — a label mismatch silently disables host vantages.
func TestLayerCommandsHostVantages(t *testing.T) {
	ifb, routes, arp, ok := layerCommands("linux-shell")
	if !ok || ifb != "ip -4 addr" || routes != "ip route" || arp != "ip neigh" {
		t.Fatalf("linux-shell = %q/%q/%q/%v", ifb, routes, arp, ok)
	}
	ifb, routes, arp, ok = layerCommands("windows-powershell")
	if !ok || ifb != "ipconfig" || routes != "route print -4" || arp != "arp -a" {
		t.Fatalf("windows-powershell = %q/%q/%q/%v", ifb, routes, arp, ok)
	}
	// Aliases keep working.
	if _, _, _, ok = layerCommands("linux"); !ok {
		t.Fatal("linux alias lost")
	}
	if _, _, _, ok = layerCommands(""); !ok {
		t.Fatal("empty alias lost")
	}
	// Switch vendors keep their tables.
	if _, _, _, ok = layerCommands("huawei-vrp"); !ok {
		t.Fatal("huawei-vrp lost")
	}
	if _, _, _, ok = layerCommands("cisco-ios"); !ok {
		t.Fatal("cisco-ios lost")
	}
}
