package netdev

import (
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestFormatSnmpVarDecodesGBKOctetString pins the SNMP-side GBK handling:
// domestic Huawei/H3C agents emit GBK in sysDescr-style strings, and the
// formatter must route them through the same auto UTF-8→GBK decoder as the
// terminal path (gosnmp delivers OctetString values as []byte).
func TestFormatSnmpVarDecodesGBKOctetString(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("华为核心交换机"))
	if err != nil {
		t.Fatal(err)
	}
	got := formatSnmpVar(gosnmp.SnmpPDU{Name: "1.3.6.1.2.1.1.1.0", Type: gosnmp.OctetString, Value: gbk})
	if !strings.Contains(got, "华为核心交换机") {
		t.Errorf("GBK sysDescr mojibake: %q", got)
	}

	utf8 := formatSnmpVar(gosnmp.SnmpPDU{Name: "1.3.6.1.2.1.1.1.0", Type: gosnmp.OctetString, Value: []byte("plain-utf8")})
	if !strings.Contains(utf8, "plain-utf8") {
		t.Errorf("utf8 passthrough broken: %q", utf8)
	}

	num := formatSnmpVar(gosnmp.SnmpPDU{Name: "1.3.6.1.2.1.1.3.0", Type: gosnmp.Integer, Value: 7})
	if !strings.Contains(num, "= 7") {
		t.Errorf("non-octet rendering broken: %q", num)
	}
}
