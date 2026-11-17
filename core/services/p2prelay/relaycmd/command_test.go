package relaycmd_test

import (
	"net"
	"strconv"
	"testing"

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
		Expect(c.Execute()).To(Succeed())
		Expect(ln.Close()).To(Succeed())
		Expect(c.Execute()).ToNot(Succeed())
	})
})
