package studio

import (
	"os/exec"
	"strings"
	"testing"
)

// runWrapped runs a wrapped script in a real shell, as bash does for the tool
// (bash -c, no input), and returns what the shell wrote.
func runWrapped(t *testing.T, script string) (string, string) {
	t.Helper()
	tag := scriptTag()
	cmd := exec.Command("bash", "-c", wrapScript(script, tag))
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("the wrapper itself failed: %v", err)
	}
	return string(out), tag
}

func TestWrapScriptReturnsTheStatusAsAValue(t *testing.T) {
	for _, tc := range []struct {
		name, script, out string
		code              int
	}{
		{"success", "echo hello", "hello", 0},
		{"failure", "echo bad; exit 3", "bad", 3},
		{"stderr joins stdout", "echo out; echo err >&2; false", "out\nerr", 1},
		{"no output", "true", "", 0},
		{"exit in the middle", "echo one; exit 7; echo two", "one", 7},
		{"last line a comment", "echo x # a comment", "x", 0},
		{"missing command", "no-such-command-here 2>/dev/null", "", 127},
		{"reads no input", "cat", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, tag := runWrapped(t, tc.script)
			out, code, err := parseScriptResult(text, tag)
			if err != nil {
				t.Fatalf("%v\n%q", err, text)
			}
			if strings.TrimSpace(out) != tc.out || code != tc.code {
				t.Fatalf("got %q status %d, want %q status %d (text %q)", out, code, tc.out, tc.code, text)
			}
		})
	}
}

func TestWrapScriptKeepsTheStatusLineWhenTheOutputIsLarge(t *testing.T) {
	text, tag := runWrapped(t, "yes aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa | head -c 300000; exit 4")
	out, code, err := parseScriptResult(text, tag)
	if err != nil {
		t.Fatal(err)
	}
	if code != 4 {
		t.Fatalf("status %d, want 4", code)
	}
	if len(out) > scriptOutputCap {
		t.Fatalf("output %d bytes, more than the cap %d", len(out), scriptOutputCap)
	}
	if len(out) < scriptOutputCap-100 {
		t.Fatalf("output %d bytes, much less than the cap %d", len(out), scriptOutputCap)
	}
}

func TestWrapScriptCannotBeFooledByItsOwnOutput(t *testing.T) {
	// The script prints a line like the status line, with a made-up status. The
	// tag is new for each check, so it is not the one that closes the output.
	text, tag := runWrapped(t, "echo 'LOCALAI_WAIT_STATUS_0000000000000000=0'; exit 5")
	_, code, err := parseScriptResult(text, tag)
	if err != nil || code != 5 {
		t.Fatalf("status %d, error %v", code, err)
	}
}

func TestParseScriptResultOfTextWithoutAStatusLine(t *testing.T) {
	const tag = "TAG="
	if _, _, err := parseScriptResult("partial\n[stopped after 120 seconds]", tag); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("a timeout must be a failed check: %v", err)
	}
	for _, text := range []string{"This host cannot run commands.", "This server cannot run commands: no scratch.", "Not run: this agent works in one folder."} {
		_, _, err := parseScriptResult(text, tag)
		if _, ok := err.(*errScriptUnavailable); !ok {
			t.Fatalf("%q must say that the script cannot run here, got %v", text, err)
		}
	}
	// A shell that stopped on a syntax error says its status after its message.
	out, code, err := parseScriptResult("bash: -c: line 4: syntax error\n[exit code 2]", tag)
	if err != nil || code != 2 || !strings.Contains(out, "syntax error") {
		t.Fatalf("a syntax error is a status: %q %d %v", out, code, err)
	}
	if _, _, err := parseScriptResult("something else", tag); err == nil {
		t.Fatal("text without a status is a failed check")
	}
}
