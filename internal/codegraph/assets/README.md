# codegraph embedded-runtime staging dir

Release builds (`go build -tags codegraph_embed`, wired in
`.github/workflows/release.yml`) embed the CodeGraph runtime from
`codegraph_runtime.bin` in this directory, so shipped binaries install it with
zero network. The file is fetched per-platform by CI right before the build —
never commit it (see .gitignore).

Local/dev builds omit the tag and fall back to the SHA256-guarded mirror
chain, so this directory being empty locally is expected.
