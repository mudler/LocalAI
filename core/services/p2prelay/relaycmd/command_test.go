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
	"github.com/mudler/LocalAI/pkg/logsetup"
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

	It("keeps the connection rate defaults of the package and reads the flags", func() {
		cfg, err := parse().Config()
		Expect(err).ToNot(HaveOccurred())
		def := p2prelay.DefaultConfig(false)
		Expect(cfg.ConnRatePerIP).To(Equal(def.ConnRatePerIP))
		Expect(cfg.ConnBurstPerIP).To(Equal(def.ConnBurstPerIP))
		Expect(cfg.ConnRateTotal).To(Equal(def.ConnRateTotal))
		Expect(cfg.ConnBurstTotal).To(Equal(def.ConnBurstTotal))
		cfg, err = parse("--conn-rate-per-ip=0", "--conn-burst-per-ip=7", "--conn-rate-total=3.5", "--conn-burst-total=9").Config()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.ConnRatePerIP).To(BeZero())
		Expect(cfg.ConnBurstPerIP).To(Equal(7))
		Expect(cfg.ConnRateTotal).To(Equal(3.5))
		Expect(cfg.ConnBurstTotal).To(Equal(9))
	})

	It("limits an open relay unless --rate-limit=0", func() {
		cfg, err := parse("--open").Config()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RateLimit).To(Equal(p2prelay.DefaultOpenRateLimit))
		cfg, err = parse("--open", "--rate-limit=0").Config()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RateLimit).To(BeZero())
	})

	It("sets the ceiling of the limits an admin may set from the flags", func() {
		cfg, err := parse().Config()
		Expect(err).ToNot(HaveOccurred())
		def := p2prelay.DefaultConfig(false)
		Expect(cfg.Ceiling).To(Equal(def.Ceiling))
		cfg, err = parse("--max-circuit-bytes=4194304", "--max-circuit-duration=1h", "--max-rate-limit=1048576").Config()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Ceiling).To(Equal(p2prelay.Ceiling{CircuitMaxBytes: 4 << 20, CircuitMaxDuration: time.Hour, RateLimit: 1 << 20}))
	})

	It("refuses a limit flag above the ceiling flag when the relay starts", func() {
		cfg, err := parse("--max-circuit-bytes=1048576").Config()
		Expect(err).ToNot(HaveOccurred())
		cfg.DataDir = GinkgoT().TempDir()
		_, err = p2prelay.New(cfg.Config)
		Expect(err).To(MatchError(ContainSubstring("ceiling")))
	})

	It("health check succeeds only while something listens on the port", func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		c := parse("--healthcheck", "--port", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
		Expect(c.Execute(context.Background())).To(Succeed())
		Expect(ln.Close()).To(Succeed())
		Expect(c.Execute(context.Background())).ToNot(Succeed())
	})

	It("prints only the JSON banner on standard output with --json", func() {
		c := parse("--json", "--listen", "/ip4/127.0.0.1/tcp/0")

		stdout, stdoutDone := capture(&os.Stdout)
		stderr, stderrDone := capture(&os.Stderr)
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		// The entry point sends the logs to standard error when --json is set.
		logsetup.SetLogger(logsetup.LogSettings{}, true)
		go func() { errc <- c.Execute(ctx) }()
		// The banner is written once the relay runs; give it time to log too.
		time.Sleep(2 * time.Second)
		cancel()
		var err error
		Eventually(errc).WithTimeout(15 * time.Second).Should(Receive(&err))
		out, errOut := stdout(), stderr()
		stdoutDone()
		stderrDone()
		logsetup.SetLogger(logsetup.LogSettings{}, false)

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
