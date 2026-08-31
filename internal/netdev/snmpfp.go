package netdev

import (
	"context"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// snmpfp.go — F2's SNMP sysDescr fingerprint (spec §4.3.1). The polite-probe
// constitution applies in full: ONE v2c GET (sysDescr + sysName), no retry,
// no walk, only when the user configured a community. Failure is silence —
// a closed or filtered 161 says nothing and costs nothing further.
//
// Transport honesty: SNMP is UDP and cannot ride the SSH direct-tcpip tunnel,
// so this works from the position that can route to the target (direct-mode
// discovery, or the local segment). Tunnel-mode scans simply find no 161
// fingerprints unless the operator also has local reachability — the store
// records what was actually answered, never guesses.

// sysDescrOID / sysNameOID are the two MIB-2 system vars (both sit inside
// snmpAllowedOIDPrefixes' 1.3.6.1.2.1.1 subtree).
const (
	sysDescrOID = "1.3.6.1.2.1.1.1.0"
	sysNameOID  = "1.3.6.1.2.1.1.5.0"
)

// snmpFingerprint sends the single GET. Short timeout, one attempt.
func snmpFingerprint(ctx context.Context, ip, community string) (sysDescr, sysName string) {
	g := &gosnmp.GoSNMP{
		Target:    ip,
		Port:      161,
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   2 * time.Second,
		Retries:   0, // the constitution: single attempt, no retry
		MaxOids:   gosnmp.MaxOids,
		Context:   ctx,
	}
	if err := g.Connect(); err != nil {
		return "", ""
	}
	defer g.Conn.Close()
	res, err := g.Get([]string{sysDescrOID, sysNameOID})
	if err != nil || len(res.Variables) == 0 {
		return "", ""
	}
	for _, v := range res.Variables {
		if v.Type != gosnmp.OctetString {
			continue
		}
		s := strings.TrimSpace(string(v.Value.([]byte)))
		switch v.Name {
		case "." + sysDescrOID:
			sysDescr = s
		case "." + sysNameOID:
			sysName = s
		}
	}
	return sysDescr, sysName
}

// hintsFromSysDescr maps a sysDescr/platform string onto vendor/role hints.
// Pure function — the F2 word-seam shared by the SNMP path and the LLDP/CDP
// platform capture. Hints only fire on strong words; a wrong hint is worse
// than none.
func hintsFromSysDescr(desc string) (vendor, role string) {
	if strings.TrimSpace(desc) == "" {
		return "", ""
	}
	lower := strings.ToLower(desc)
	for _, t := range bannerVendorTokens {
		if t.vendor != "" && strings.Contains(lower, t.token) {
			vendor = t.vendor
			break
		}
	}
	if r, ok := roleFromWords(desc); ok {
		role = r
	}
	return vendor, role
}
