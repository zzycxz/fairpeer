package netdev

import (
	"path/filepath"
	"strings"
	"testing"
)

// humantty_recording_test — completion-spec §6 #5：录制列表/只读回放的
// 契约——落盘即 ANSI 剥离 + 脱敏，读取面限定录制目录。
func TestHumanTTYRecordingsRoundtrip(t *testing.T) {
	old := netdevStateDirOverr
	netdevStateDirOverr = t.TempDir()
	t.Cleanup(func() { netdevStateDirOverr = old })

	p1, err := saveHumanTTYRecording("sw1", []byte("\x1b[31madmin@sw1\x1b[0m display clock\r\n14:00:00 OK"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := saveHumanTTYRecording("sw2", []byte("admin@sw2 display version")); err != nil {
		t.Fatalf("save2: %v", err)
	}

	recs, err := ListHumanTTYRecordings()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 recordings, got %d", len(recs))
	}
	// 同秒内落盘的两条按 At 倒序 + 设备名归属正确即可（时间戳精度 1s）。
	devs := map[string]string{recs[0].Device: "", recs[1].Device: ""}
	if _, ok := devs["sw1"]; !ok {
		t.Fatalf("missing sw1 in %+v", recs)
	}
	if _, ok := devs["sw2"]; !ok {
		t.Fatalf("missing sw2 in %+v", recs)
	}

	txt, err := ReadHumanTTYRecording(p1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(txt, "\x1b[") {
		t.Fatalf("ANSI escape leaked into recording: %q", txt)
	}
	if !strings.Contains(txt, "display clock") {
		t.Fatalf("recording content missing: %q", txt)
	}

	// 目录外路径必须被拒——回放面不能变成任意文件读取。
	if _, err := ReadHumanTTYRecording(filepath.Join(t.TempDir(), "other.txt")); err == nil {
		t.Fatal("outside-dir path accepted")
	}
}
