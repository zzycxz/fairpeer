package main

// fileStatus mapping tests (R1-2): partial must surface when a done job has
// failed chunks — the production database showed files at 1/9 chunks claiming
// "已抽取".

import (
	"testing"

	"github.com/zzycxz/fairpeer/internal/rag"
)

func TestFileStatusMapping(t *testing.T) {
	cases := []struct {
		name string
		job  rag.JobRow
		want string
	}{
		{"pending → queued", rag.JobRow{Status: rag.JobPending}, "queued"},
		{"extracting", rag.JobRow{Status: rag.JobExtracting}, "extracting"},
		{"done clean", rag.JobRow{Status: rag.JobDone, DoneChunks: 3, FailedChunks: 0}, "enriched"},
		{"done with failures → partial", rag.JobRow{Status: rag.JobDone, DoneChunks: 3, FailedChunks: 2}, "partial"},
		{"done zero chunks → indexed", rag.JobRow{Status: rag.JobDone, DoneChunks: 0}, "indexed"},
		{"error", rag.JobRow{Status: rag.JobError}, "error"},
		{"cancelled", rag.JobRow{Status: rag.JobCancelled}, "cancelled"},
		{"unknown → indexed", rag.JobRow{Status: "weird"}, "indexed"},
	}
	for _, tc := range cases {
		if got := fileStatus(tc.job); got != tc.want {
			t.Errorf("%s: fileStatus = %q, want %q", tc.name, got, tc.want)
		}
	}
}
