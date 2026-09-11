package main

// buildImportMessage must surface skipped files instead of losing them
// silently (spec R2-5 / import feedback): a folder import where one file
// fails to parse previously reported only the successes.

import (
	"strings"
	"testing"
)

func TestBuildImportMessageIncludesSkipped(t *testing.T) {
	msg := buildImportMessage(3, 9, 15000, []string{"扫描件.pdf (read pdf: timeout)"})
	for _, want := range []string{"已导入 3 个文件", "约 18 次调用", "预计", "1 个文件无法解析", "扫描件.pdf"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestBuildImportMessageSummarizesManySkips(t *testing.T) {
	msg := buildImportMessage(0, 0, 0, []string{"a.pdf", "b.pdf", "c.pdf", "d.pdf", "e.pdf"})
	if !strings.Contains(msg, "等 5 个") || !strings.Contains(msg, "5 个文件无法解析") {
		t.Errorf("many-skip summary wrong: %q", msg)
	}
	if !strings.Contains(msg, "0 个文件") {
		t.Errorf("zero-import message wrong: %q", msg)
	}
}
