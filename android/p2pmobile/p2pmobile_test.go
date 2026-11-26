// SPDX-License-Identifier: MIT

package p2pmobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

// pairAnswer is the envelope of PairJSON.
type pairAnswer struct {
	OK     bool           `json:"ok"`
	Code   string         `json:"code"`
	Detail string         `json:"detail"`
	Result map[string]any `json:"result"`
}

func pairJSON(link string, allowOpenRelay bool) pairAnswer {
	GinkgoHelper()
	var a pairAnswer
	Expect(json.Unmarshal([]byte(PairJSON(filepath.Join(GinkgoT().TempDir(), "id.key"), link, "phone", allowOpenRelay)), &a)).To(Succeed())
	return a
}

// newPeerID returns the peer ID of a fresh identity that never starts.
func newPeerID() string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "peer.key")
	_, err := p2pnet.LoadOrCreateIdentity(path)
	Expect(err).ToNot(HaveOccurred())
	id, err := PeerID(path)
	Expect(err).ToNot(HaveOccurred())
	return id
}

func testLink(expires time.Time) *p2ppair.Link {
	GinkgoHelper()
	pid, err := p2pnet.ParsePeerID(newPeerID())
	Expect(err).ToNot(HaveOccurred())
	return &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: pid, Code: "ABCDE23456", Expires: expires}
}

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
		Expect(Version()).To(Equal("p2pmobile/2"))
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

var _ = Describe("PairJSON", func() {
	It("answers invalid_link for a link that does not parse", func() {
		a := pairJSON("localai://pair?v=1", false)
		Expect(a.OK).To(BeFalse())
		Expect(a.Code).To(Equal(CodeInvalidLink))
		Expect(a.Detail).ToNot(BeEmpty())
	})

	It("answers expired for an expired link", func() {
		a := pairJSON(testLink(time.Now().Add(-time.Minute)).String(), false)
		Expect(a.OK).To(BeFalse())
		Expect(a.Code).To(Equal(CodeExpired))
	})

	It("answers open_relay for a link with an open relay that was not allowed", func() {
		l := testLink(time.Now().Add(time.Hour))
		relay, err := p2pnet.ParseAddrInfo("/ip4/192.0.2.1/tcp/4001/p2p/" + newPeerID())
		Expect(err).ToNot(HaveOccurred())
		l.Relays = append(l.Relays, relay)
		l.Open = []int{0}
		a := pairJSON(l.String(), false)
		Expect(a.OK).To(BeFalse())
		Expect(a.Code).To(Equal(CodeOpenRelay))
	})

	It("answers no_route for a link with no address, relay or hint", func() {
		a := pairJSON(testLink(time.Now().Add(time.Hour)).String(), false)
		Expect(a.OK).To(BeFalse())
		Expect(a.Code).To(Equal(CodeNoRoute), a.Detail)
	})

	It("maps each pairing error to a stable code", func() {
		needsRelay := fmt.Errorf("%w: %w: %w", pairclient.ErrUnreachable, pairclient.ErrNeedsRelay, &p2pnet.NoDirectPathError{})
		cases := map[error]string{
			needsRelay:                    CodeNeedsRelay,
			&p2pnet.NoDirectPathError{}:   CodeNeedsRelay,
			pairclient.ErrGone:            CodeGone,
			pairclient.ErrTooManyTries:    CodeTooManyTries,
			pairclient.ErrWrongServer:     CodeWrongServer,
			pairclient.ErrExpired:         CodeExpired,
			pairclient.ErrOpenRelay:       CodeOpenRelay,
			pairclient.ErrUnreachable:     CodeUnreachable,
			errors.New("something else"):  CodeFailed,
			&linkError{errors.New("bad")}: CodeInvalidLink,
			fmt.Errorf("%w: %w", pairclient.ErrUnreachable, pairclient.ErrNoRoute): CodeNoRoute,
		}
		for err, code := range cases {
			Expect(errorCode(err)).To(Equal(code), "%v", err)
		}
	})
})

var _ = Describe("Host", func() {
	It("listens, so that a hole punch can reach it", func() {
		h, err := newHost(context.Background(), filepath.Join(GinkgoT().TempDir(), "id.key"))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = h.Close() }()
		Expect(h.Libp2p().Network().ListenAddresses()).ToNot(BeEmpty())
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
		_, err := StartForward(filepath.Join(GinkgoT().TempDir(), "none.key"), peer, "127.0.0.1:0", "", "", "", nil)
		Expect(err).To(MatchError(ContainSubstring("pair first")))
	})

	It("rejects relays that are not a JSON array", func() {
		_, err := StartForward(id, peer, "127.0.0.1:0", "not json", "", "", nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects hints that are not a JSON array of relay addresses", func() {
		_, err := StartForward(id, peer, "127.0.0.1:0", "", "", "not json", nil)
		Expect(err).To(HaveOccurred())
		_, err = StartForward(id, peer, "127.0.0.1:0", "", "", `["/ip4/192.0.2.1/tcp/4001"]`, nil)
		Expect(err).To(HaveOccurred())
	})

	It("starts with hints and a host that listens", func() {
		f, err := StartForward(id, peer, "127.0.0.1:0", "", "", `["/ip4/192.0.2.1/tcp/4001/p2p/`+newPeerID()+`"]`, nil)
		Expect(err).ToNot(HaveOccurred())
		defer f.Stop()
		Expect(f.host.Libp2p().Network().ListenAddresses()).ToNot(BeEmpty())
	})

	It("gives the needs_relay code to a forwarder down for want of a direct path", func() {
		l := forwardLine(p2pnet.ForwardStatus{State: p2pnet.ForwardDown, Err: fmt.Errorf("dial: %w", &p2pnet.NoDirectPathError{})})
		Expect(l.State).To(Equal("down"))
		Expect(l.Code).To(Equal(CodeNeedsRelay))
		b, err := json.Marshal(forwardLine(p2pnet.ForwardStatus{State: p2pnet.ForwardDown, Err: errors.New("other")}))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).ToNot(ContainSubstring("code"))
	})

	It("reports ready, down and closed for a peer that does not answer", func() {
		r := &rec{}
		f, err := StartForward(id, peer, "127.0.0.1:0", `[]`, `["/ip4/127.0.0.1/tcp/1"]`, "", r)
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
