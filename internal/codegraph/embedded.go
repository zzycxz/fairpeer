//go:build codegraph_embed

package codegraph

import _ "embed"

// The embedded runtime bundle: the CodeGraph release asset for THIS build's
// platform (zip on Windows, tar.gz elsewhere), placed at
// internal/codegraph/assets/codegraph_runtime.bin by the release pipeline
// before `go build -tags codegraph_embed`. Release builds then install the
// runtime with ZERO network — the "can't download on first run" failure class
// disappears entirely for shipped binaries.
//
// The bytes go through the same SHA256 table as downloaded assets, so a
// mismatched pipeline fetch (wrong platform/version) is caught at install time
// and falls back to the mirror chain instead of extracting garbage.
//
//go:embed assets/codegraph_runtime.bin
var embeddedRuntimeBundle []byte

func embeddedBundle() ([]byte, bool) {
	return embeddedRuntimeBundle, len(embeddedRuntimeBundle) > 0
}
