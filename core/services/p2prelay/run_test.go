package p2prelay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
)

// syncBuffer lets the test read the banner while Run still holds the writer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func runConfig(open bool) p2prelay.RunConfig {
	cfg := p2prelay.DefaultConfig(open)
	cfg.DataDir = filepath.Join(GinkgoT().TempDir(), "relay")
	return p2prelay.RunConfig{Config: cfg, Listen: []string{"/ip4/127.0.0.1/tcp/0"}}
}

// startRun runs the relay until the returned stop function is called.
func startRun(cfg p2prelay.RunConfig) (p2prelay.Banner, *syncBuffer, func() error) {
	out := &syncBuffer{}
	cfg.Out = out
	ready := make(chan p2prelay.Banner, 1)
	cfg.OnReady = func(b p2prelay.Banner) { ready <- b }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p2prelay.Run(ctx, cfg) }()
	var b p2prelay.Banner
	Eventually(ready).WithTimeout(20 * time.Second).Should(Receive(&b))
	stop := func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			Fail("Run did not return after its context ended")
			return nil
		}
	}
	return b, out, stop
}

var _ = Describe("Run", func() {
	It("starts, prints the banner with the claim code and stops cleanly", func() {
		cfg := runConfig(false)
		b, out, stop := startRun(cfg)

		Expect(b.Mode).To(Equal("unclaimed"))
		Expect(b.ClaimCode).ToNot(BeEmpty())
		Expect(b.Addrs).ToNot(BeEmpty())
		Expect(b.AdminCommand).To(ContainSubstring("local-ai p2p relays add /ip4/127.0.0.1/tcp/"))
		Expect(b.AdminCommand).To(ContainSubstring("--claim " + b.ClaimCode))
		text := out.String()
		Expect(text).To(ContainSubstring("PEERID " + b.PeerID))
		Expect(text).To(ContainSubstring("CLAIM_CODE " + b.ClaimCode))
		Expect(text).To(ContainSubstring("RELAY_ADDR /ip4/127.0.0.1/tcp/"))
		Expect(text).To(ContainSubstring("No public address found"))

		Expect(stop()).To(Succeed())
	})

	It("keeps its key in a 0600 file, reuses it and never prints it", func() {
		cfg := runConfig(false)
		b1, out, stop := startRun(cfg)
		Expect(stop()).To(Succeed())

		keyPath := filepath.Join(cfg.DataDir, p2prelay.KeyFile)
		st, err := os.Stat(keyPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		key, err := os.ReadFile(keyPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(out.String()).ToNot(ContainSubstring(string(key)))

		b2, _, stop2 := startRun(cfg)
		defer func() { Expect(stop2()).To(Succeed()) }()
		Expect(b2.PeerID).To(Equal(b1.PeerID))
	})

	It("prints loud warnings for an open relay and the default rate limit", func() {
		b, out, stop := startRun(runConfig(true))
		defer func() { Expect(stop()).To(Succeed()) }()

		Expect(b.Mode).To(Equal("open"))
		Expect(b.ClaimCode).To(BeEmpty())
		Expect(b.Limits).To(ContainSubstring("64 KiB/s per circuit"))
		Expect(strings.Split(out.String(), "\n")[0]).To(HavePrefix("WARNING: OPEN RELAY"))
	})

	It("warns about an open relay without a bandwidth limit", func() {
		cfg := runConfig(true)
		cfg.RateLimit = 0
		b, _, stop := startRun(cfg)
		defer func() { Expect(stop()).To(Succeed()) }()
		Expect(strings.Join(b.Warnings, "\n")).To(ContainSubstring("WITHOUT A BANDWIDTH LIMIT"))
	})

	It("prints one JSON line with --json", func() {
		cfg := runConfig(false)
		cfg.JSON = true
		b, out, stop := startRun(cfg)
		defer func() { Expect(stop()).To(Succeed()) }()

		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		Expect(lines).To(HaveLen(1))
		var got p2prelay.Banner
		Expect(json.Unmarshal([]byte(lines[0]), &got)).To(Succeed())
		Expect(got.PeerID).To(Equal(b.PeerID))
		Expect(got.ClaimCode).To(Equal(b.ClaimCode))
	})

	It("starts unclaimed again after ResetAdmins", func() {
		cfg := runConfig(false)
		cfg.Admins = nil
		b, _, stop := startRun(cfg)
		Expect(b.Mode).To(Equal("unclaimed"))
		Expect(stop()).To(Succeed())

		cfg.ResetAdmins = true
		b2, _, stop2 := startRun(cfg)
		defer func() { Expect(stop2()).To(Succeed()) }()
		Expect(b2.Mode).To(Equal("unclaimed"))
	})

	It("refuses to run without a data directory", func() {
		cfg := p2prelay.RunConfig{Config: p2prelay.DefaultConfig(false)}
		Expect(p2prelay.Run(context.Background(), cfg)).To(MatchError(ContainSubstring("DataDir")))
	})
})

var _ = Describe("Banner", func() {
	It("lists public addresses before loopback and gives the admin command", func() {
		b := p2prelay.Banner{
			Version: "1", Mode: "gated", PeerID: "12D3KooWx", DataDir: "/data", Limits: "x",
			Addrs:        []string{"/ip4/203.0.113.7/tcp/4001/p2p/12D3KooWx", "/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWx"},
			PublicAddr:   true,
			AdminCommand: "local-ai p2p relays add /ip4/203.0.113.7/tcp/4001/p2p/12D3KooWx",
		}
		text := b.Text()
		Expect(strings.Index(text, "203.0.113.7")).To(BeNumerically("<", strings.Index(text, "127.0.0.1")))
		Expect(text).To(ContainSubstring("On your LocalAI server run:"))
		Expect(text).ToNot(ContainSubstring("CLAIM_CODE"))
	})
})
