package cli

import (
	"strings"
	"testing"
)

// TestMCPTopicErrorCodes: `ask help mcp` lists the codes an MCP tool call
// can fail with — 2, 3, 4, 5, 7 (each is driven out of the real server in
// internal/mcp's TestErrorCodesReachableOverMCP) — and says 6 is not one.
// It also must not show isError on a success, which the wire omits.
func TestMCPTopicErrorCodes(t *testing.T) {
	_, after, found := strings.Cut(mcpTopic, "Error envelope")
	if !found {
		t.Fatal("mcp topic has no Error envelope section")
	}
	section, _, _ := strings.Cut(after, "Example transcript")
	for _, want := range []string{"\n    2  ", "\n    3  ", "\n    4  ", "\n    5  ", "\n    7  ", "never 6"} {
		if !strings.Contains(section, want) {
			t.Errorf("mcp topic's error-code list is missing %q", strings.TrimSpace(want))
		}
	}
	if strings.Contains(section, "\n    6  ") {
		t.Error("mcp topic lists 6 as an error code; a no-op is a success over MCP")
	}
	if strings.Contains(mcpTopic, `"isError": false`) || strings.Contains(mcpTopic, `"isError":false`) {
		t.Error("mcp topic shows isError:false on a success; the wire omits the key")
	}
}

func TestEveryTopicHasContent(t *testing.T) {
	for k, v := range helpTopics {
		if len(v) < 200 {
			t.Errorf("help topic %q is too short (%d chars)", k, len(v))
		}
	}
}

// TestMCPTopicShowsNoFixedVersion: serverInfo.version is the build's
// version string, so the example transcript must not print a literal
// number that goes stale at the next release.
func TestMCPTopicShowsNoFixedVersion(t *testing.T) {
	if !strings.Contains(mcpTopic, `"version":"<version>"`) {
		t.Error(`mcp topic's serverInfo example should read "version":"<version>"`)
	}
	if strings.Contains(mcpTopic, `"version":"0.`) || strings.Contains(mcpTopic, `"version":"1.`) {
		t.Error("mcp topic's serverInfo example carries a literal version number")
	}
}
