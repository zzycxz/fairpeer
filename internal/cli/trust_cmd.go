package cli

// trust_cmd.go — `fairpeer trust [dir]` / `fairpeer trust -remove [dir]`
// (CODEX_GAP_AUDIT G3): the user-facing half of the project trust gate.
// Trusting a root lets ITS project hooks, [[plugins]] / .mcp.json MCP servers
// load; untrusted roots contribute nothing executable. The flag lives in the
// user-global trust store (~/.fairpeer/trust.json) — never in the project.
import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zzycxz/fairpeer/internal/hook"
	"github.com/zzycxz/fairpeer/internal/i18n"
)

func trustCommand(args []string) int {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "revoke trust for the directory instead of granting it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	if *remove {
		if err := hook.Untrust(abs, ""); err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("已撤销信任: %s\n", abs)
		return 0
	}
	if err := hook.Trust(abs, ""); err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	fmt.Printf("已信任: %s\n该目录的项目 hooks、[[plugins]] 与 .mcp.json MCP 服务器此后正常加载。\n", abs)
	return 0
}
