package sbtest

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

func testNetwork(newBackend NewBackend) {
	It("no port handler means no network", func() {
		f := setup(newBackend, nil)
		need(f.b, "network")
		// Without curl the checks below would fail for the wrong reason, so
		// the guest must have it.
		r := f.run("command -v curl")
		Expect(r.code).To(Equal(0), "curl is missing from the guest: exit %d: %s%s", r.code, r.stdout, r.stderr)
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
			Expect(slices.Contains(c.codes, r.code)).To(BeTrue(), "%q: exit %d, want a connect failure %v: %+v", c.script, r.code, c.codes, r)
		}
	})
	It("the guest reaches a port handler", func() {
		f := setup(newBackend, map[int]sandbox.PortHandler{7000: EchoHandler})
		need(f.b, "network")
		got := f.mustRun(`bash -c 'exec 3<>/dev/tcp/127.0.0.1/7000; printf "ping\n" >&3; read -r line <&3; echo "$line"'`)
		Expect(got).To(Equal("ping\n"))
	})
	It("the proxy handler sees the host of an HTTPS request", func() {
		p := &ConnectProxy{}
		f := setup(newBackend, map[int]sandbox.PortHandler{3128: p.Handler})
		need(f.b, "network")
		r := f.run("curl -sS --max-time 20 https://example.com/")
		Expect(r.code).NotTo(Equal(0), "a denied request worked: %+v", r)
		Expect(p.Seen()).To(ContainElement("example.com:443"), "curl said %q", strings.TrimSpace(r.stderr))
	})
}
