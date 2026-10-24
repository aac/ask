package core

import (
	"reflect"
	"testing"
)

func TestActDepCommands(t *testing.T) {
	got := ActDepCommands("ask-1a2b", []string{
		"act-3c89", "linear-eng-1234", "act-58f80e", "act-3c89",
		"act-", "act-xyz1", "act-3c89; rm -rf /", "ACT-3C89", " act-3c89",
	})
	want := []string{
		"act dep add act-3c89 --external ask-1a2b",
		"act dep add act-58f80e --external ask-1a2b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := ActDepCommands("ask-1a2b", nil); len(got) != 0 {
		t.Fatalf("no blocks: got %q", got)
	}
}
