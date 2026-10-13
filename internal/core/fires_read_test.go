package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// firesReadItem is one open item for the fire-record read tests.
func firesReadItem(id string) *Item {
	return NewItem(id, "t", UrgencyNormal, StatusOpen, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
}

func firesReadPlant(t *testing.T, s *FileStore, name, content string) string {
	t.Helper()
	dir := filepath.Join(s.root, firesSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const firesReadRecord = `{"v":1,"fire":"aaaaaaaa","sub":"bbbbbbbb","handler":"rec","event":"resolve","from":"open","to":"resolved","at":"2026-05-15T10:00:00Z","phase":"started","ts":"2026-05-15T10:00:00Z"}
{"v":1,"fire":"aaaaaaaa","sub":"bbbbbbbb","phase":"result","state":"ok","exit_code":0,"detail":null,"duration_ms":12,"ts":"2026-05-15T10:00:01Z","output":"hi\n"}
`

// TestLoadFiresLeavesOutWhatIsNotARecord: the record is a file on disk.
// Lines that are not record lines, a result that pairs with nothing, a
// second result for one fire and a repeated fire id are left out and
// counted, and the fires that are there are still read.
func TestLoadFiresLeavesOutWhatIsNotARecord(t *testing.T) {
	s := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000A", firesReadItem("ask-aaaa"))
	firesReadPlant(t, s, "ask-aaaa.jsonl", firesReadRecord+
		"not json\n"+
		`{"v":2,"fire":"cccccccc","sub":"bbbbbbbb","phase":"started"}`+"\n"+
		`{"v":1,"fire":"../../etc","sub":"bbbbbbbb","phase":"started"}`+"\n"+
		`{"v":1,"fire":"dddddddd","sub":"bbbbbbbb","phase":"result","state":"ok"}`+"\n"+
		`{"v":1,"fire":"aaaaaaaa","sub":"bbbbbbbb","phase":"result","state":"failed","exit_code":9}`+"\n"+
		`{"v":1,"fire":"aaaaaaaa","sub":"bbbbbbbb","handler":"other","phase":"started"}`+"\n"+
		"\n")
	fires, skipped, err := s.LoadFires("ask-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 6 {
		t.Errorf("skipped = %d, want 6", skipped)
	}
	if len(fires) != 1 {
		t.Fatalf("%d fires, want 1: %+v", len(fires), fires)
	}
	f := fires[0]
	if f.Item != "ask-aaaa" || f.Fire != "aaaaaaaa" || f.Handler != "rec" || f.State != FireOK ||
		f.ExitCode == nil || *f.ExitCode != 0 || f.DurationMs == nil || *f.DurationMs != 12 || f.Output != "hi\n" ||
		f.FinishedAt == nil || !f.FinishedAt.Equal(time.Date(2026, 5, 15, 10, 0, 1, 0, time.UTC)) {
		t.Errorf("fire = %+v", f)
	}
}

// TestLoadFiresDoesNotFollowOrFold: a symlink where the record should be
// is refused, and a file named for another spelling of the id is not this
// item's record, whether or not the filesystem folds case.
func TestLoadFiresDoesNotFollowOrFold(t *testing.T) {
	s := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000B", firesReadItem("ask-aaaa"), firesReadItem("ask-bbbb"))
	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.WriteFile(outside, []byte(firesReadRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.root, firesSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "ask-aaaa.jsonl")); err != nil {
		t.Fatal(err)
	}
	if fires, _, err := s.LoadFires("ask-aaaa"); err == nil {
		t.Errorf("a symlinked record was read: %+v", fires)
	}

	firesReadPlant(t, s, "ASK-BBBB.jsonl", firesReadRecord)
	fires, _, err := s.LoadFires("ask-bbbb")
	if err != nil || len(fires) != 0 {
		t.Errorf("a record under another spelling of the id was read: %v, %+v", err, fires)
	}
	ids, err := s.FiredItemIDs()
	if err != nil {
		t.Fatal(err)
	}
	// The symlink is listed by name (reading it is what refuses); the
	// other spelling is not an id.
	if len(ids) != 1 || ids[0] != "ask-aaaa" {
		t.Errorf("FiredItemIDs = %q, want [ask-aaaa]", ids)
	}
}

// TestHarvestFireRecordRefusals: a source record that cannot be read, and
// a target file that differs from a source record's name only in case,
// each refuse the harvest before anything is written.
func TestHarvestFireRecordRefusals(t *testing.T) {
	t.Run("unreadable source record", func(t *testing.T) {
		src := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000C", firesReadItem("ask-aaaa"))
		tgt := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000D")
		if err := os.MkdirAll(filepath.Join(src.root, firesSubdir, "ask-aaaa.jsonl"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Harvest(tgt, src, true)
		if !errors.Is(err, ErrUnreadableSource) {
			t.Fatalf("err = %v, want ErrUnreadableSource", err)
		}
		if ids, _ := tgt.ListIDs(); len(ids) != 0 {
			t.Errorf("target holds %q after a refused harvest", ids)
		}
		if ids, _ := src.ListIDs(); len(ids) != 1 {
			t.Errorf("source holds %q after a refused harvest", ids)
		}
	})
	t.Run("target file differing only in case", func(t *testing.T) {
		src := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000E", firesReadItem("ask-aaaa"))
		tgt := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000F")
		firesReadPlant(t, src, "ask-aaaa.jsonl", firesReadRecord)
		stray := firesReadPlant(t, tgt, "ASK-AAAA.jsonl", "theirs\n")
		_, err := Harvest(tgt, src, true)
		if !errors.Is(err, ErrStrayTarget) || !strings.Contains(err.Error(), "fires/ASK-AAAA.jsonl (source ask-aaaa)") {
			t.Fatalf("err = %v, want ErrStrayTarget naming fires/ASK-AAAA.jsonl", err)
		}
		if b, _ := os.ReadFile(stray); string(b) != "theirs\n" {
			t.Errorf("the stray was written: %q", b)
		}
		if ids, _ := tgt.ListIDs(); len(ids) != 0 {
			t.Errorf("target holds %q after a refused harvest", ids)
		}
	})
}

// TestHarvestCarriesOnlyRecordLines: what is not a fire record line in
// the source's record is not written into the target, and the record
// lines arrive as they were written.
func TestHarvestCarriesOnlyRecordLines(t *testing.T) {
	src := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000G", firesReadItem("ask-aaaa"), firesReadItem("ask-bbbb"))
	tgt := harvestSeed(t, t.TempDir(), "01HXFIRESREAD00000000000H")
	long := `{"v":1,"fire":"eeeeeeee","sub":"bbbbbbbb","phase":"result","state":"ok","output":"` + strings.Repeat("x", maxFireLine) + `"}`
	firesReadPlant(t, src, "ask-aaaa.jsonl", "#!/bin/sh\nrm -rf /\n"+firesReadRecord+
		`{"v":1,"fire":"aaaaaaaa","sub":"bbbbbbbb","phase":"neither"}`+"\n"+long+"\n")
	// A record with no record line in it still arrives as a file, empty.
	firesReadPlant(t, src, "ask-bbbb.jsonl", "nothing here is a record\n")
	if _, err := Harvest(tgt, src, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(tgt.root, firesSubdir, "ask-aaaa.jsonl"))
	if err != nil || string(got) != firesReadRecord {
		t.Errorf("carried record: %v\n got %q\nwant %q", err, got, firesReadRecord)
	}
	got, err = os.ReadFile(filepath.Join(tgt.root, firesSubdir, "ask-bbbb.jsonl"))
	if err != nil || len(got) != 0 {
		t.Errorf("carried record with no record lines: %v, %q", err, got)
	}
}
