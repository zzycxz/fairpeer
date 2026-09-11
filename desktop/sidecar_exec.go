package main

// sidecar_exec.go — shared helpers for python sidecars (he_service,
// browseruse_service) and remote terminal spawning: when the unified runtime
// resolver picks uv, the interpreter is invoked as `uv run python <script>` —
// the command name is uv's first prefix arg and the prefix rides ahead of the
// script.

import "strings"

// execCommandName picks the actual binary for an interpreter invocation with
// prefix args: `uv run python script.py` execs "uv", not "python".
func execCommandName(fallback string, prefix []string) string {
	if len(prefix) > 0 {
		return prefix[0]
	}
	return fallback
}

// execCommandArgs assembles argv: prefix args (e.g. ["run", "python"]) then
// the script and its flags.
func execCommandArgs(prefix []string, script string, rest ...string) []string {
	args := make([]string, 0, len(prefix)+1+len(rest))
	args = append(args, prefix...)
	args = append(args, script)
	args = append(args, rest...)
	return args
}

// shquotePath single-quotes a path for POSIX shell interpolation (docker
// exec ... sh -c / ssh command strings): spaces and metacharacters in a
// workspace root must not terminate the cd argument. An embedded quote gets
// the POSIX `'\”` treatment.
func shquotePath(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// quoteWin32CommandLine joins argv into a single Win32 command line using
// the CreateProcess quoting rules (backslashes before a quote double up).
// Used by the windows PTY spawn, where the joined string is re-parsed by the
// child's argv splitter — bare spaces inside an argument would split it.
func quoteWin32CommandLine(argv []string) string {
	quote := func(arg string) string {
		if arg != "" && !strings.ContainsAny(arg, " \t\"") {
			return arg
		}
		var b strings.Builder
		b.WriteByte('"')
		slashes := 0
		for _, r := range arg {
			switch r {
			case '\\':
				slashes++
				b.WriteString("\\\\")
			case '"':
				b.WriteString(strings.Repeat("\\", slashes*2+1))
				b.WriteByte('"')
				slashes = 0
			default:
				slashes = 0
				b.WriteRune(r)
			}
		}
		b.WriteString(strings.Repeat("\\", slashes*2))
		b.WriteByte('"')
		return b.String()
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = quote(a)
	}
	return strings.Join(out, " ")
}
