package netdev

import "regexp"

// Redact masks credential-bearing text in device output before it reaches the
// model context or any on-disk store (NETDEV_SPEC §8.1 / Appendix B-3). The
// patterns target the password/secret/community/key families across Huawei
// VRP and Cisco IOS configuration syntax: a redacted line keeps its structure
// (so the model can still reason about the config) and loses only the secret
// token. This runs on every netdev_exec output — the raw text lives only in
// the in-memory session buffer.
var redactPatterns = []*regexp.Regexp{
	// ── passwords ──────────────────────────────────────────────────────────
	// Huawei: password {cipher|simple|irreversible-cipher} <secret>.
	regexp.MustCompile(`(?im)^(\s*(?:\S+\s+)*password\s+(?:cipher|simple|irreversible-cipher)\s+)\S+`),
	// Huawei aaa: local-user <u> password [mode] <secret>.
	regexp.MustCompile(`(?im)^(\s*local-user\s+\S+\s+password(?:\s+(?:cipher|simple|irreversible-cipher))?\s+)\S+`),
	// Cisco: password [alg] <secret> (line-level).
	regexp.MustCompile(`(?im)^(\s*password(?:\s+(?:0|5|7|md5|sha256|sha512|scrypt))?\s+)\S+`),
	// Cisco: enable secret/password [alg] <secret>.
	regexp.MustCompile(`(?im)^(\s*enable\s+(?:secret|password)(?:\s+(?:0|4|5|7|8|9|md5|sha256|scrypt))?\s+)\S+`),
	// Cisco: username <u> [privilege N|role R] password|secret [alg] <secret>.
	regexp.MustCompile(`(?im)^(\s*username\s+\S+\s+(?:(?:privilege|role|view)\s+\S+\s+)*(?:password|secret)(?:\s+(?:0|5|7|8|9|md5|sha256|scrypt))?\s+)\S+`),

	// ── SNMP ───────────────────────────────────────────────────────────────
	// Communities: huawei `snmp-agent community {read|write} <x>`; cisco
	// `snmp-server community <x> <acl>` (community is the first token).
	regexp.MustCompile(`(?im)^(\s*snmp-agent\s+community\s+(?:read|write)\s+)\S+`),
	regexp.MustCompile(`(?im)^(\s*snmp-server\s+community\s+)\S+`),
	// Huawei SNMPv3: snmp-agent usm-user v3 <user> [group] authentication-mode|privacy-mode <alg> <key>.
	regexp.MustCompile(`(?im)^(\s*snmp-agent\s+usm-user\s+(?:v3\s+)?\S+(?:\s+\S+)*?\s+(?:authentication-mode|privacy-mode)\s+(?:md5|sha-?\d*|hmac-sha\d*|des|3des|aes-?\d*)\s+)\S+`),

	// ── shared keys ────────────────────────────────────────────────────────
	// radius/tacacs shared-key [cipher|simple|alg] <secret>.
	regexp.MustCompile(`(?im)^(\s*(?:radius-server|tacacs-server)\s+(?:shared-)?key(?:\s+(?:0|7|cipher|simple))*\s+)\S+`),
	// `radius-server host <ip> auth-port N key [7] <secret>` / isakmp pre-shared-key.
	regexp.MustCompile(`(?im)^(\s*radius-server\s+host\s+\S+(?:\s+\S+\s+\d+)*\s+key(?:\s+\d+)?\s+)\S+`),
	// Standalone pre-shared-key lines (inside crypto/bgp/wlan blocks; context
	// lives on earlier lines, so the pattern must not depend on it):
	// pre-shared-key [address A|hostname H] [key] [mode] <secret>.
	regexp.MustCompile(`(?im)^(\s*pre-shared-key(?:\s+(?:address|hostname)\s+\S+)*(?:\s+key)?(?:\s+(?:cipher|simple|encrypted|plain|\d+))*\s+)\S+`),
	// key-chain key-string [encrypt|alg] <secret>.
	regexp.MustCompile(`(?im)^(\s*key-string(?:\s+(?:0|7|encrypt\s+\S+))*\s+)\S+`),
	// Generic authentication/master keys — mode token REQUIRED so ordinary
	// tokens are never eaten as the secret (isis/bfd authentication-key …).
	regexp.MustCompile(`(?im)^(\s*\S*(?:authentication-key|master-key)\S*(?:\s+\S+)*?\s+(?:cipher|simple|encrypted|\d+)\s+)\S+`),
	// WLAN pass-phrases.
	regexp.MustCompile(`(?im)^(\s*\S*(?:wpa|psk|preshared)\S*(?:\s+\S+)*?\s+(?:pass-phrase|key|psk)\s+)\S+`),

	// ── misc ───────────────────────────────────────────────────────────────
	// Cisco key config-key password-encrypt <key>.
	regexp.MustCompile(`(?im)^(\s*key\s+config-key\s+password-encrypt\s+)\S+`),
	// Public/private key blobs (base64 bodies) in device config.
	regexp.MustCompile(`(?im)^(\s*\S*(?:rsa|dsa|ecdsa|ssh)\S*\s+\S*(?:key|local-key-pair|peer-public-key|public-key)\S*(?:\s+\S+)*?\s+)[A-Za-z0-9+/=]{40,}`),
}

// redactedToken replaces the captured secret.
const redactedToken = "<redacted>"

// Redact applies every pattern; later patterns see earlier replacements, so a
// line can only lose more, never regain, secret material.
func Redact(s string) string {
	for _, re := range redactPatterns {
		s = re.ReplaceAllString(s, "${1}"+redactedToken)
	}
	return s
}
