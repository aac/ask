package cli

import "testing"

func TestRunUnknownCommand(t *testing.T) {
	code := Run([]string{"xyzzy"})
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

func TestRunHelp(t *testing.T) {
	if code := Run([]string{"help"}); code != 0 {
		t.Fatalf("help exit: %d", code)
	}
}

func TestRunVersion(t *testing.T) {
	if code := Run([]string{"version"}); code != 0 {
		t.Fatalf("version exit: %d", code)
	}
}
