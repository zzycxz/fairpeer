package cli

// run_json.go — `fairpeer run --json` (CODEX_GAP_AUDIT G2,对标 codex exec
// --json)：把 run 的完整事件流以 JSONL（每行一个 JSON 对象）写到 stdout，
// 供脚本/CI/编排消费。线格式复用 eventwire 共享编解码器——与 serve SSE、
// desktop、remotehost 完全同一契约，Item/ExpertCollab/Resumed 全覆盖。
// 错误与人读提示保持走 stderr，stdout 只有机读行；结束时补一行 kind="result"
// 汇总（ok/最终文本/错误/会话文件），调用方无需自行拼装。

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/zzycxz/fairpeer/internal/event"
	"github.com/zzycxz/fairpeer/internal/eventwire"
)

type jsonlSink struct {
	mu sync.Mutex
	w  io.Writer

	// lastMessage 记录最后一条完整 assistant 文本（event.Message），
	// result 行从这里取；turnErr 记录 TurnDone 的错误。
	lastText string
	turnErr  error
}

func (s *jsonlSink) Emit(e event.Event) {
	w := eventwire.ToWire(e)
	b, err := json.Marshal(w)
	if err != nil {
		return // 线形状由 eventwire 保证可序列化；真失败降级不打断 run
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "%s\n", b)
	switch e.Kind {
	case event.Message:
		s.lastText = w.Text
	case event.TurnDone:
		s.turnErr = e.Err
	}
}

// writeResult 在 run 结束后补一行 result 汇总。ok 反映 Run 的错误状态；
// text 是最后一条 assistant 完整文本（可能为空——例如纯工具轮）。
func (s *jsonlSink) writeResult(ok bool, sessionPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	errText := ""
	if s.turnErr != nil {
		errText = s.turnErr.Error()
	}
	payload := map[string]any{
		"kind":        "result",
		"ok":          ok && s.turnErr == nil,
		"text":        s.lastText,
		"sessionPath": sessionPath,
	}
	if errText != "" {
		payload["error"] = errText
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "%s\n", b)
}
