package relaycmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelay/relaycmd"
)

func TestRelayCmd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "relaycmd suite")
}

func parse(args ...string) *relaycmd.Command {
	GinkgoHelper()
	var c relaycmd.Command
	k, err := kong.New(&c)
	Expect(err).ToNot(HaveOccurred())
	_, err = k.Parse(append([]string{"--data-dir", GinkgoT().TempDir()}, args...))
	Expect(err).ToNot(HaveOccurred())
	return &c
}

var _ = Describe("relay command", func() {
	It("has the defaults of a gated relay", func() {
		cfg, err := parse().Config()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Open).To(BeFalse())
		Expect(cfg.RateLimit).To(BeZero())
		Expect(cfg.Port).To(Equal(p2prelay.DefaultPort))
	})

	It("limits an open relay unless --rate-limit=0", func() {
		cfg, err := parse("--open").Config()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RateLimit).To(Equal(p2prelay.DefaultOpenRateLimit))
		cfg, err = parse("--open", "--rate-limit=0").Config()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RateLimit).To(BeZero())
	})

	It("health check succeeds only while something listens on the port", func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		c := parse("--healthcheck", "--port", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
		Expect(c.Execute(context.Background(), relaycmd.LogSettings{})).To(Succeed())
		Expect(ln.Close()).To(Succeed())
		Expect(c.Execute(context.Background(), relaycmd.LogSettings{})).ToNot(Succeed())
	})

	It("prints only the JSON banner on standard output with --json", func() {
		c := parse("--json", "--listen", "/ip4/127.0.0.1/tcp/0")

		stdout, stdoutDone := capture(&os.Stdout)
		stderr, stderrDone := capture(&os.Stderr)
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() { errc <- c.Execute(ctx, relaycmd.LogSettings{}) }()
		// The banner is written once the relay runs; give it time to log too.
		time.Sleep(2 * time.Second)
		cancel()
		var err error
		Eventually(errc).WithTimeout(15 * time.Second).Should(Receive(&err))
		out, errOut := stdout(), stderr()
		stdoutDone()
		stderrDone()
		relaycmd.SetLogger(relaycmd.LogSettings{}, false)

		Expect(err).ToNot(HaveOccurred())
		lines := strings.Split(strings.TrimSpace(out), "\n")
		Expect(lines).To(HaveLen(1), "stdout was: %q", out)
		var banner p2prelay.Banner
		Expect(json.Unmarshal([]byte(lines[0]), &banner)).To(Succeed())
		Expect(banner.PeerID).ToNot(BeEmpty())
		Expect(errOut).To(ContainSubstring("relay: started"))
		Expect(errOut).ToNot(ContainSubstring("caller"))
	})
})

// capture redirects *f to a pipe. The first function returns what was
// written so far and closes the writer; the second restores *f.
func capture(f **os.File) (func() string, func()) {
	old := *f
	r, w, err := os.Pipe()
	Expect(err).ToNot(HaveOccurred())
	*f = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, r); close(done) }()
	read := func() string {
		_ = w.Close()
		<-done
		return buf.String()
	}
	return read, func() { *f = old }
}
