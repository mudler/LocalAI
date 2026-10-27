package sbtest

import (
	"slices"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

func testNetwork(t *testing.T, newBackend NewBackend) {
	t.Run("no port handler means no network", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		need(t, f.b, "network")
		// Without curl the checks below would fail for the wrong reason, so
		// the guest must have it.
		if r := f.run("command -v curl"); r.code != 0 {
			t.Fatalf("curl is missing from the guest: exit %d: %s%s", r.code, r.stdout, r.stderr)
		}
		// Curl exit codes: 5 could not resolve the proxy, 6 could not resolve the
		// host, 7 could not connect, 28 timed out. Any other failure, such as 127 or a bad argument, fails
		// the test. bash exits 1 when a /dev/tcp redirection fails.
		for _, c := range []struct {
			script string
			codes  []int
		}{
			{"curl -sS --max-time 10 http://example.com/", []int{5, 6, 7, 28}},
			{"curl -sS --max-time 10 --noproxy '*' http://example.com/", []int{5, 6, 7, 28}},
			{"bash -c 'exec 3<>/dev/tcp/127.0.0.1/3128'", []int{1}},
		} {
			r := f.run(c.script)
			if !slices.Contains(c.codes, r.code) {
				t.Fatalf("%q: exit %d, want a connect failure %v: %+v", c.script, r.code, c.codes, r)
			}
		}
	})
	t.Run("the guest reaches a port handler", func(t *testing.T) {
		f := setup(t, newBackend, map[int]sandbox.PortHandler{7000: EchoHandler})
		need(t, f.b, "network")
		got := f.mustRun(`bash -c 'exec 3<>/dev/tcp/127.0.0.1/7000; printf "ping\n" >&3; read -r line <&3; echo "$line"'`)
		if got != "ping\n" {
			t.Fatalf("echo = %q", got)
		}
	})
	t.Run("the proxy handler sees the host of an HTTPS request", func(t *testing.T) {
		p := &ConnectProxy{}
		f := setup(t, newBackend, map[int]sandbox.PortHandler{3128: p.Handler})
		need(t, f.b, "network")
		r := f.run("curl -sS --max-time 20 https://example.com/")
		if r.code == 0 {
			t.Fatalf("a denied request worked: %+v", r)
		}
		if seen := p.Seen(); !slices.Contains(seen, "example.com:443") {
			t.Fatalf("seen = %v, curl said %q", seen, strings.TrimSpace(r.stderr))
		}
	})
}
