package connectors_test

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

func TestSplitKeepsPartsUnderTheLimitAndWholeWords(t *testing.T) {
	if got := connectors.Split("  ", 10); got != nil {
		t.Fatalf("blank = %v", got)
	}
	if got := connectors.Split("short", 10); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short = %v", got)
	}
	text := strings.Repeat("alpha beta\n", 50)
	for _, p := range connectors.Split(text, 40) {
		if len([]rune(p)) > 40 || strings.HasPrefix(p, "lpha") || strings.HasSuffix(p, "alph") {
			t.Fatalf("bad part %q", p)
		}
	}
	// No space or break: cut at the limit, on a character boundary.
	got := connectors.Split(strings.Repeat("é", 25), 10)
	if len(got) != 3 || len([]rune(got[0])) != 10 || len([]rune(got[2])) != 5 {
		t.Fatalf("parts = %q", got)
	}
}
