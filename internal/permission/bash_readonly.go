package permission

import "strings"

// readOnlyBashCommands is the set of commands considered read-only — they
// don't modify filesystem state, network state, or process state. Each
// entry is the first word of a bash command (lowercased). Commands not in
// this set that might also be read-only (e.g. "git log") are handled
// separately by isReadOnlyBashSubject.
var readOnlyBashCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"ls": true, "find": true, "locate": true, "which": true, "whereis": true, "type": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true,
	"echo": true, "printf": true,
	"pwd": true, "cd": true, "whoami": true, "id": true, "uname": true, "hostname": true,
	"date": true, "env": true, "printenv": true,
	"wc": true, "sort": true, "uniq": true, "cut": true, "tr": true,
	"stat": true, "file": true, "du": true, "df": true,
	"ps": true, "top": true, "htop": true,
	"diff": true, "cmp": true, "comm": true,
	// awk and sed are intentionally excluded — they can execute arbitrary
	// code (e.g. awk 'system("cmd")') and must require user approval.
	"man": true, "info": true, "help": true,
	"true": true, "false": true, "test": true, "[": true,
	"basename": true, "dirname": true, "realpath": true, "readlink": true,
}

// readOnlyBashPrefixes are command prefixes where the second word
// determines read-only status. Each maps to the set of read-only
// subcommands.
var readOnlyBashPrefixes = map[string]map[string]bool{
	"git": {
		"log": true, "status": true, "diff": true, "show": true,
		"tag":   true,
		"blame": true, "grep": true, "ls-files": true, "ls-tree": true,
		"rev-parse": true, "rev-list": true, "describe": true, "reflog": true,
		"shortlog": true, "whatchanged": true, "cherry": true,
		"cat-file": true, "for-each-ref": true, "name-rev": true,
	},
	"go": {
		"vet": true, "doc": true, "list": true,
		"version": true, "env": true,
	},
	"npm": {
		"ls": true, "list": true, "view": true, "info": true,
		"outdated": true, "audit": true,
	},
	// cargo check/doc are deliberately absent: both compile dependencies, and
	// compilation executes dependency build.rs scripts — arbitrary code, not a
	// read. cargo search/version stay read-only.
	"cargo": {
		"search": true, "version": true,
	},
	"docker": {
		"ps": true, "images": true, "inspect": true, "logs": true,
		"stats": true, "info": true, "version": true,
	},
	"kubectl": {
		"get": true, "describe": true, "logs": true, "explain": true,
		"api-resources": true, "api-versions": true,
	},
}

// isReadOnlyBashSubject returns true when a bash command is a known
// read-only operation. The subject is the JSON arg value extracted by
// Subject() — for bash it is the raw command string.
func isReadOnlyBashSubject(subject string) bool {
	cmd := strings.TrimSpace(subject)
	if cmd == "" {
		return false
	}
	if containsShellSyntax(cmd) {
		return false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	base := strings.ToLower(fields[0])

	// Check single-word read-only commands.
	if readOnlyBashCommands[base] {
		return !hasUnsafeReadOnlyArgs(base, fields[1:])
	}

	// Check prefix commands (git log, go vet, etc.).
	if len(fields) > 1 {
		if sub, ok := readOnlyBashPrefixes[base]; ok {
			subcmd := strings.ToLower(fields[1])
			return sub[subcmd] && !hasUnsafePrefixArgs(base, subcmd, fields[2:])
		}
	}
	return false
}

func containsShellSyntax(cmd string) bool {
	// \r counts: strings.Fields treats it as whitespace (so the read-only
	// verb is extracted cleanly) while PowerShell treats CR as a statement
	// separator — "ls\rRemove-Item …" would otherwise pass as read-only.
	return strings.ContainsAny(cmd, ";|&<>\n`\r") || strings.Contains(cmd, "$(")
}

func hasUnsafeReadOnlyArgs(base string, args []string) bool {
	switch base {
	case "find":
		// -exec/-execdir/-delete execute or remove; -fprint*/-fls write files.
		return hasAnyArg(args, "-exec", "-execdir", "-delete", "-fls") ||
			hasArgWithPrefix(args, "-fprint")
	case "sed":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-i") || strings.HasPrefix(arg, "--in-place") {
				return true
			}
		}
	case "sort":
		return hasArgWithPrefix(args, "-o") || hasAnyArg(args, "--output") || hasArgWithPrefix(args, "--output=")
	case "env":
		// env is a meta-executor: `env VAR=x <cmd>` runs <cmd>. Only a bare
		// env — optionally with flags and VAR=value assignments — is
		// read-only; any other word is a command for env to execute.
		consumesOperand := false // -u/--unset take a NAME operand
		for _, arg := range args {
			if consumesOperand {
				consumesOperand = false
				continue
			}
			if arg == "-u" || arg == "--unset" {
				consumesOperand = true
				continue
			}
			if arg == "-S" || arg == "--split-string" {
				return true // consumes a COMMAND operand (NEW-15)
			}
			if strings.HasPrefix(arg, "-S") || strings.HasPrefix(arg, "--split-string=") {
				return true // glued form: -S'cmd' / --split-string=cmd execute
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if i := strings.Index(arg, "="); i > 0 {
				continue // VAR=value assignment
			}
			return true
		}
	case "rg":
		// --pre runs an arbitrary preprocessor command over every match file.
		return hasArgWithPrefix(args, "--pre")
	case "hostname":
		// `hostname <name>` sets the system hostname (root). Query flags
		// (-f/-i/-d/-s/-a) are read-only (PERM-3); a non-flag operand sets,
		// and -F/--file reads the name to set FROM a file.
		for _, arg := range args {
			if arg == "-F" || strings.HasPrefix(arg, "--file") {
				return true
			}
			if !strings.HasPrefix(arg, "-") {
				return true
			}
		}
		return false
	case "date":
		// `date -s`/`--set` sets the system clock (root).
		return hasArgWithPrefix(args, "-s") || hasArgWithPrefix(args, "--set")
	case "less":
		// `less -o FILE`/`--LOG-FILE=FILE` copies output to a file.
		return hasArgWithPrefix(args, "-o") || hasArgWithPrefix(args, "--LOG-FILE")
	case "tail":
		// tail -f/-F never exits — in an unattended pipe it hangs the tool
		// until timeout (PERM-4).
		return hasArgWithPrefix(args, "-f") || hasArgWithPrefix(args, "-F") ||
			hasAnyArg(args, "--follow") || hasArgWithPrefix(args, "--follow=")
	case "top", "htop":
		// Interactive full-screen mode hangs an unattended pipe; batch mode
		// (-b) produces one snapshot and exits (PERM-4).
		return !hasArgWithPrefix(args, "-b") && !hasAnyArg(args, "--batch")
	}
	return false
}

func hasUnsafePrefixArgs(base, subcmd string, args []string) bool {
	switch base {
	case "git":
		switch subcmd {
		case "diff", "show", "log", "whatchanged":
			// whatchanged is the log family and inherits --output (PERM-2).
			return hasAnyArg(args, "--output") || hasArgWithPrefix(args, "--output=")
		case "tag":
			// Bare `git tag` lists tags; any argument creates/deletes/moves
			// a ref (git tag v1, git tag -d x, git tag -f v1 commit).
			return len(args) > 0
		case "reflog":
			// Bare `git reflog` (and `git reflog show`) is read-only;
			// delete/expire/drop destroy history (drop landed in git 2.43 —
			// PERM-1); `reflog show --output=X` writes a file like log does.
			return hasAnyArg(args, "delete", "expire", "drop") ||
				hasAnyArg(args, "--output") || hasArgWithPrefix(args, "--output=")
		}
	case "go":
		if subcmd == "env" {
			return hasAnyArg(args, "-w", "-u")
		}
	case "npm":
		if subcmd == "audit" {
			// `npm audit fix` installs packages and runs their lifecycle
			// scripts — code execution, not a read. Plain `npm audit` (with
			// flags like --json/--omit=dev) only reports; fix, --fix and
			// fix-force are all the same operation (NEW-14: the word-form
			// check alone let `npm audit --fix` through).
			return hasAnyArg(args, "fix") || hasArgWithPrefix(args, "fix") || hasArgWithPrefix(args, "--fix")
		}
	case "docker":
		// stats streams forever without --no-stream; logs -f follows — both
		// hang the turn while whitelisted (NEW-15).
		if subcmd == "stats" {
			return !hasAnyArg(args, "--no-stream")
		}
		if subcmd == "logs" {
			return hasAnyArg(args, "-f", "--follow")
		}
	case "kubectl":
		if subcmd == "get" || subcmd == "logs" || subcmd == "top" {
			return hasAnyArg(args, "-w", "--watch", "--watch-only", "-f", "--follow")
		}
	}
	return false
}

func hasArgWithPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func hasAnyArg(args []string, unsafe ...string) bool {
	for _, arg := range args {
		for _, candidate := range unsafe {
			if arg == candidate {
				return true
			}
		}
	}
	return false
}

// dangerousBashPatterns are glob-like patterns that match destructive
// commands. Used only for a UI warning — the deny list is the actual
// enforcement mechanism.
var dangerousBashPatterns = []struct {
	pattern string
	label   string
}{
	{"rm -rf*", "recursive delete"},
	{"rm -r *", "recursive delete"},
	{"rm -fr*", "recursive delete"},
	{"git push*--force*", "force push"},
	{"git push*-f*", "force push"},
	{"git reset --hard*", "hard reset"},
	{"git clean -f*", "force clean"},
	{"chmod 777*", "world-writable"},
	{"chmod -R 777*", "world-writable recursive"},
	{"chown *", "ownership change"},
	{"sudo *", "superuser"},
	{"mkfs*", "filesystem format"},
	{"dd if=*", "raw device write"},
	{"fdisk*", "partition table"},
	{"> /dev/*", "device overwrite"},
	// PowerShell / cmd equivalents (Windows hosts without bash): without
	// these, BashDangerWarning stays empty AND BashCommandPrefix would create
	// destructive prefix grants that bash-land refuses.
	{"Remove-Item *-Recurse*", "recursive delete"},
	{"rm -Recurse*", "recursive delete"},
	{"rd /s*", "recursive delete"},
	{"del /q /s*", "recursive delete"},
	{"del /s /q*", "recursive delete"},
	{"format*", "filesystem format"},
	{"Format-Volume*", "filesystem format"},
	{"Set-ExecutionPolicy*", "execution policy change"},
	{"iex *", "remote script execution"},
	{"Invoke-Expression*", "remote script execution"},
	{"reg add*", "registry write"},
	{"reg delete*", "registry delete"},
}

// BashDangerWarning returns a short label if subject matches a known
// dangerous pattern, or "" when the command looks safe. This is a visual
// hint only — the Policy rules are the authority.
func BashDangerWarning(subject string) string {
	// PowerShell/cmd are case-insensitive ("RD /Q /S" == "rd /q /s"); fold
	// both sides so the danger warning and the prefix-grant refusal fire.
	s := strings.ToLower(strings.TrimSpace(subject))
	for _, d := range dangerousBashPatterns {
		if matchGlob(d.pattern, s) {
			return d.label
		}
	}
	return ""
}
