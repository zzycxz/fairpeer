//go:build !codegraph_embed

package codegraph

// embeddedBundle is the no-embed stub: dev/local builds don't carry the
// runtime asset, so installs fall back to the checksum-guarded mirror chain.
func embeddedBundle() ([]byte, bool) { return nil, false }
