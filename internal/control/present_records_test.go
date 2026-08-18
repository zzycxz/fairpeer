package control

import (
	"path/filepath"
	"testing"

	"github.com/zzycxz/fairpeer/internal/agent"
	"github.com/zzycxz/fairpeer/internal/event"
	"github.com/zzycxz/fairpeer/internal/present"
)

// TestPresentRecordsSeedsFromSidecarAndTracksLiveEvents verifies the in-process
// read path PresentForTab uses: a controller that resumed an existing session
// must return the prior turns' sidecar records PLUS everything recorded since
// (reading the sidecar on disk instead would lag behind by the mid-turn
// autosave interval and drop the newest cards), and a controller without a
// recorder must report ok=false so the caller falls back to disk.
func TestPresentRecordsSeedsFromSidecarAndTracksLiveEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Prior process: persisted one notice card with rewrite version 3.
	prior := present.NewRecorder()
	prior.Append(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "prior turn notice"})
	prior.SyncBeforeSave(3, event.Event{})
	if err := prior.Save(present.PresentPath(path)); err != nil {
		t.Fatal(err)
	}

	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	sink := event.FuncSink(func(event.Event) {})
	c := New(Options{Executor: exec, SessionDir: dir, Present: true, Sink: sink})
	c.Resume(sess, path)

	records, ver, ok := c.PresentRecords()
	if !ok {
		t.Fatal("PresentRecords: expected ok=true for a Present-enabled controller")
	}
	if ver != 3 {
		t.Fatalf("rewrite version = %d, want 3 (seeded from sidecar header)", ver)
	}
	if len(records) != 1 || records[0].Text != "prior turn notice" {
		t.Fatalf("records = %+v, want the seeded prior-turn notice", records)
	}

	// A live event after the resume must show up immediately — the sidecar on
	// disk still holds only the prior record until the next snapshot.
	c.notice("live notice")
	records, _, _ = c.PresentRecords()
	if len(records) != 2 || records[1].Text != "live notice" {
		t.Fatalf("records after live event = %+v, want seeded + live notice", records)
	}

	// No recorder (CLI/headless): ok=false so callers fall back to disk.
	bare := New(Options{Sink: sink})
	if _, _, ok := bare.PresentRecords(); ok {
		t.Fatal("PresentRecords: expected ok=false when Present is disabled")
	}
}
