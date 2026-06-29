package cli

import "testing"

func TestEveryTopicHasContent(t *testing.T) {
	for k, v := range helpTopics {
		if len(v) < 200 {
			t.Errorf("help topic %q is too short (%d chars)", k, len(v))
		}
	}
}
