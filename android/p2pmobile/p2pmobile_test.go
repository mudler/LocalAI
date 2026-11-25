// SPDX-License-Identifier: MIT

package p2pmobile

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

func TestP2PMobile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "p2pmobile suite")
}

type rec struct {
	mu    sync.Mutex
	lines []string
}

func (r *rec) OnState(s string) {
	r.mu.Lock()
	r.lines = append(r.lines, s)
	r.mu.Unlock()
}

func (r *rec) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func states(lines []string) []string {
	var out []string
	for _, l := range lines {
		var m map[string]any
		ExpectWithOffset(1, json.Unmarshal([]byte(l), &m)).To(Succeed(), "bad JSON %q", l)
		s, _ := m["state"].(string)
		out = append(out, s)
	}
	return out
}

var _ = Describe("Version", func() {
	It("names the library and its API revision", func() {
		Expect(Version()).To(Equal("p2pmobile/1"))
	})
})

var _ = Describe("Pair", func() {
	It("rejects a link without a peer and writes no file but the identity", func() {
		dir := GinkgoT().TempDir()
		_, err := Pair(filepath.Join(dir, "id.key"), "localai://pair?v=1", "x", false)
		Expect(err).To(HaveOccurred())
		entries, err := os.ReadDir(dir)
		Expect(err).ToNot(HaveOccurred())
		for _, e := range entries {
			Expect(e.Name()).To(Equal("id.key"))
		}
	})
})

var _ = Describe("PeerID", func() {
	It("fails on a missing identity file and creates none", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "id.key")
		_, err := PeerID(path)
		Expect(err).To(HaveOccurred())
		_, err = os.Stat(path)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
})

var _ = Describe("StartForward", func() {
	var id, peer string

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		id = filepath.Join(dir, "identity.key")
		_, err := p2pnet.LoadOrCreateIdentity(id)
		Expect(err).ToNot(HaveOccurred())
		// A peer that does not exist: a fresh identity that never starts.
		other := filepath.Join(dir, "other.key")
		_, err = p2pnet.LoadOrCreateIdentity(other)
		Expect(err).ToNot(HaveOccurred())
		peer, err = PeerID(other)
		Expect(err).ToNot(HaveOccurred())
	})

	It("refuses to start without an identity file", func() {
		_, err := StartForward(filepath.Join(GinkgoT().TempDir(), "none.key"), peer, "127.0.0.1:0", "", "", nil)
		Expect(err).To(MatchError(ContainSubstring("pair first")))
	})

	It("rejects relays that are not a JSON array", func() {
		_, err := StartForward(id, peer, "127.0.0.1:0", "not json", "", nil)
		Expect(err).To(HaveOccurred())
	})

	It("reports ready, down and closed for a peer that does not answer", func() {
		r := &rec{}
		f, err := StartForward(id, peer, "127.0.0.1:0", `[]`, `["/ip4/127.0.0.1/tcp/1"]`, r)
		Expect(err).ToNot(HaveOccurred())
		addr := f.Addr()
		Expect(addr).To(HavePrefix("127.0.0.1:"))

		// A client connection makes the forwarder dial the peer, which fails
		// and resets the client.
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.SetReadDeadline(time.Now().Add(20 * time.Second))).To(Succeed())
		_, rerr := c.Read(make([]byte, 1))
		Expect(rerr).To(HaveOccurred())
		_ = c.Close()

		f.Stop()
		f.Stop() // idempotent

		Eventually(func() []string { return states(r.snapshot()) }, 2*time.Second).
			Should(ContainElements("ready", "down", "closed"))
		Expect(states(r.snapshot())[0]).To(Equal("ready"))

		_, err = net.DialTimeout("tcp", addr, time.Second)
		Expect(err).To(HaveOccurred(), "listener still open after Stop")
	})
})
