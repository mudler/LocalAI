package session_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

func TestCapTextCutsOnARuneBoundary(t *testing.T) {
	s := "abé世\U0001F600" // 1+1+2+3+4 bytes
	for n := 0; n <= len(s)+2; n++ {
		got := session.CapText(s, n)
		if len(got) > n && n < len(s) {
			t.Errorf("CapText(%d) has %d bytes", n, len(got))
		}
		if !utf8.ValidString(got) || !strings.HasPrefix(s, got) {
			t.Errorf("CapText(%d) = %q", n, got)
		}
	}
	if got := session.CapText("short", 100); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := session.CapText("é", 1); got != "" {
		t.Errorf("a cut inside a rune must drop the rune: %q", got)
	}
	if got := session.CapText("abc", -1); got != "" {
		t.Errorf("a negative n must give an empty string: %q", got)
	}
	if got := session.CapText("abc", -1); got != "" {
		t.Errorf("a negative n must give an empty string: %q", got)
	}
}

func TestSummarizePicksTheFirstUsefulArgument(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{`{"command":"ls -l","path":"x"}`, "ls -l"},
		{`{"path":"home/a.txt","content":"long"}`, "home/a.txt"},
		{`{"pattern":"*.md"}`, "*.md"},
		{`{"content":"x"}`, ""},
		{`not json`, ""},
		{``, ""},
	} {
		if got := session.Summarize(json.RawMessage(tc.args)); got != tc.want {
			t.Errorf("Summarize(%s) = %q, want %q", tc.args, got, tc.want)
		}
	}
	long := `{"command":"` + strings.Repeat("x", 500) + `"}`
	if got := session.Summarize(json.RawMessage(long)); len(got) != 200 {
		t.Errorf("long summary has %d bytes, want 200", len(got))
	}
}
