package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// schemaverItemJSON returns a well-formed v1-shaped item file body with
// the given raw schema_version member (pass `"schema_version":"2",` or ""
// to omit the key) plus one key no v1 binary knows, so a rewrite would
// visibly drop it.
func schemaverItemJSON(id, member string) []byte {
	return []byte(`{
  ` + member + `
  "id": "` + id + `",
  "title": "from the future",
  "body": "",
  "urgency": "normal",
  "status": "open",
  "filed_by": null,
  "recipient": null,
  "tracker_ref": null,
  "verifier": null,
  "links": [],
  "blocks": [],
  "subscriptions": ["human:someone"],
  "resolution_note": null,
  "verification_output": null,
  "created_at": "2026-05-15T10:30:00Z",
  "resolved_at": null,
  "verified_at": null,
  "closed_at": null
}
`)
}

// schemaverWriteRaw places body at the store's path for id, bypassing Save.
func schemaverWriteRaw(t *testing.T, s *FileStore, id string, body []byte) string {
	t.Helper()
	p := s.itemPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSchemaverUnmarshalRejectsNonCurrent pins spec §1.1.1: any explicit
// non-empty schema_version other than "1" is a hard error, compared by
// exact string equality (so "01", "1.0" and " 1" are all refused).
// Replaces the former "preserve explicit value" test, which pinned the
// behaviour act-fc0ec7 reported as the bug.
func TestSchemaverUnmarshalRejectsNonCurrent(t *testing.T) {
	for _, v := range []string{"2", "1.1", "1.0", "01", " 1", "10", "v1"} {
		raw := schemaverItemJSON("ask-3c89", `"schema_version":"`+v+`",`)
		var it Item
		err := json.Unmarshal(raw, &it)
		if !errors.Is(err, ErrUnsupportedSchemaVersion) {
			t.Fatalf("schema_version %q: want ErrUnsupportedSchemaVersion, got %v", v, err)
		}
		if !strings.Contains(err.Error(), "schema_version") || !strings.Contains(err.Error(), `"`+v+`"`) {
			t.Fatalf("schema_version %q: error must name the field and the value, got %q", v, err)
		}
	}
}

// TestSchemaverUnmarshalAcceptsCurrentAndAbsent keeps the backward-compat
// half honest alongside the rejection: "1", "" and a missing key all load
// as "1".
func TestSchemaverUnmarshalAcceptsCurrentAndAbsent(t *testing.T) {
	for _, member := range []string{`"schema_version":"1",`, `"schema_version":"",`, ``} {
		var it Item
		if err := json.Unmarshal(schemaverItemJSON("ask-3c89", member), &it); err != nil {
			t.Fatalf("member %q: unexpected error %v", member, err)
		}
		if it.SchemaVersion != CurrentSchemaVersion {
			t.Fatalf("member %q: got schema_version %q", member, it.SchemaVersion)
		}
	}
}

// TestSchemaverUnmarshalRetypedFieldStillNamesVersion covers the case the
// marker exists for: a future schema retypes a field, so the typed decode
// fails before validation runs. The error must still be the version error,
// not a bare JSON type error.
func TestSchemaverUnmarshalRetypedFieldStillNamesVersion(t *testing.T) {
	raw := []byte(`{"schema_version":"2","id":"ask-3c89","title":{"text":"x"},"urgency":"normal","status":"open","created_at":"2026-05-15T10:30:00Z"}`)
	var it Item
	if err := json.Unmarshal(raw, &it); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("want ErrUnsupportedSchemaVersion, got %v", err)
	}
	// Same retyped field under the current marker stays a plain decode error.
	raw = bytes.Replace(raw, []byte(`"schema_version":"2"`), []byte(`"schema_version":"1"`), 1)
	err := json.Unmarshal(raw, &it)
	if err == nil || errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("v1 item with a bad field: want a plain decode error, got %v", err)
	}
}

// TestSchemaverStoreLoadAndListRefuse checks the store boundary: Load on a
// real file returns the version error naming the id (and does not call the
// file "corrupt"), and List does not present the item as a normal one —
// it reports it as skipped, carrying the same version error (spec §7.2).
func TestSchemaverStoreLoadAndListRefuse(t *testing.T) {
	s := tempStore(t)
	if err := s.Save(NewItem("ask-0001", "ok", UrgencyNormal, StatusOpen, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	schemaverWriteRaw(t, s, "ask-0002", schemaverItemJSON("ask-0002", `"schema_version":"2",`))

	_, err := s.Load("ask-0002")
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("Load: want ErrUnsupportedSchemaVersion, got %v", err)
	}
	if !strings.Contains(err.Error(), "ask-0002") || strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("Load error should name the id and not say corrupt: %q", err)
	}
	if _, err := s.Load("ask-0001"); err != nil {
		t.Fatalf("sibling v1 item must still load: %v", err)
	}
	items, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "ask-0001" {
		t.Fatalf("List: want only ask-0001, got %d items", len(items))
	}
	if len(skipped) != 1 || skipped[0].ID != "ask-0002" || !errors.Is(skipped[0].Err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("List: want ask-0002 skipped with ErrUnsupportedSchemaVersion, got %+v", skipped)
	}
}

// TestSchemaverSaveRefusesNonCurrent pins the write half: Save will not
// write under a marker this binary does not implement, and an existing
// file at that id is left byte-identical.
func TestSchemaverSaveRefusesNonCurrent(t *testing.T) {
	s := tempStore(t)
	want := schemaverItemJSON("ask-0002", `"schema_version":"2",`)
	p := schemaverWriteRaw(t, s, "ask-0002", want)

	it := NewItem("ask-0002", "overwrite attempt", UrgencyNormal, StatusOpen, time.Now().UTC())
	it.SchemaVersion = "2"
	if err := s.Save(it); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("Save: want ErrUnsupportedSchemaVersion, got %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file changed by a refused Save:\n%s", got)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("refused Save left a .tmp behind (stat err=%v)", err)
	}
}

// TestSchemaverHarvestRefusesAndKeepsSource: harvest re-marshals every
// source item, so it is a rewrite path too. A non-v1 source item fails the
// harvest and, even with clean=true, the source file is untouched.
func TestSchemaverHarvestRefusesAndKeepsSource(t *testing.T) {
	src, tgt := tempStore(t), tempStore(t)
	want := schemaverItemJSON("ask-0002", `"schema_version":"2",`)
	p := schemaverWriteRaw(t, src, "ask-0002", want)

	if _, err := Harvest(tgt, src, true); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("Harvest: want ErrUnsupportedSchemaVersion, got %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("source item gone after failed harvest: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("source item changed by failed harvest")
	}
	if ids, _ := tgt.ListIDs(); len(ids) != 0 {
		t.Fatalf("target received items from a refused harvest: %v", ids)
	}
}
