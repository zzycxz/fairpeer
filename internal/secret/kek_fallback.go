//go:build !windows && !darwin && !linux

package secret

// BSD/other platforms: no OS keystore integration. Passphrase if configured,
// else the degraded machine fallback — same chain as headless Linux.
func platformKekProviders() []kekProvider {
	return []kekProvider{passphraseKekProvider{}, machineKekProvider{}}
}
