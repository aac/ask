package version

import (
	"runtime/debug"
	"testing"
)

func vcs(rev, when, modified string) *debug.BuildInfo {
	bi := &debug.BuildInfo{Main: debug.Module{Path: "github.com/aac/ask", Version: "(devel)"}}
	for k, v := range map[string]string{"vcs.revision": rev, "vcs.time": when, "vcs.modified": modified} {
		if v != "" {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
		}
	}
	return bi
}

func TestFormat(t *testing.T) {
	const rev = "54537ac0123456789abcdef0123456789abcdef0"
	cases := []struct {
		name   string
		binary string
		bi     *debug.BuildInfo
		want   string
	}{
		{"ldflags stamp wins over build info", "v0.3.0+54537ac", vcs(rev, "2026-08-11T13:00:50Z", "false"), "v0.3.0+54537ac"},
		{"clean checkout", "dev", vcs(rev, "2026-08-11T13:00:50Z", "false"), "dev+54537ac01234 2026-08-11T13:00:50Z"},
		{"dirty checkout", "dev", vcs(rev, "2026-08-11T13:00:50Z", "true"), "dev+54537ac01234.dirty 2026-08-11T13:00:50Z"},
		{"revision without time", "dev", vcs(rev, "", ""), "dev+54537ac01234"},
		{"go1.24 pseudo-version defers to vcs form", "dev", func() *debug.BuildInfo {
			bi := vcs(rev, "2026-08-11T13:00:50Z", "true")
			bi.Main.Version = "v0.0.0-20260811130050-54537ac01234+dirty"
			return bi
		}(), "dev+54537ac01234.dirty 2026-08-11T13:00:50Z"},
		{"go install at a tagged version", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}}, "v0.3.0"},
		{"no vcs stamp falls back to dev", "dev", vcs("", "", ""), "dev"},
		{"no build info falls back to dev", "dev", nil, "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := format(c.binary, c.bi); got != c.want {
				t.Errorf("format() = %q, want %q", got, c.want)
			}
		})
	}
}

// Two builds of different commits must report different versions — the
// property that makes a stale install visible.
func TestFormatDistinguishesBuilds(t *testing.T) {
	a := format("dev", vcs("1111111111111111", "2026-08-01T00:00:00Z", "false"))
	b := format("dev", vcs("2222222222222222", "2026-09-01T00:00:00Z", "false"))
	if a == b || a == "dev" || b == "dev" {
		t.Fatalf("builds not distinguishable: %q vs %q", a, b)
	}
}
