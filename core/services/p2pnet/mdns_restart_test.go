package p2pnet

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeMDNS struct {
	startErr error
	closed   atomic.Bool
}

func (f *fakeMDNS) Start() error { return f.startErr }
func (f *fakeMDNS) Close() error { f.closed.Store(true); return nil }

// fakeNet is the interface address set of a host that a test changes.
type fakeNet struct {
	mu      sync.Mutex
	addrs   []string
	err     error
	started []*fakeMDNS
	failing bool
}

func (f *fakeNet) set(a ...string) { f.mu.Lock(); f.addrs = a; f.mu.Unlock() }

func (f *fakeNet) get() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.addrs...), f.err
}

func (f *fakeNet) runner() *mdnsRunner {
	return &mdnsRunner{
		addrs: func() ([]string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]string(nil), f.addrs...), f.err
		},
		service: func() mdns.Service {
			f.mu.Lock()
			defer f.mu.Unlock()
			s := &fakeMDNS{}
			if f.failing {
				s.startErr = errors.New("no address")
			}
			f.started = append(f.started, s)
			return s
		},
	}
}

func (f *fakeNet) services() []*fakeMDNS {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeMDNS(nil), f.started...)
}

func mustAddr(s string) ma.Multiaddr {
	GinkgoHelper()
	a, err := ma.NewMultiaddr(s)
	Expect(err).ToNot(HaveOccurred())
	return a
}

var _ = Describe("Local network discovery restarts", func() {
	It("compares announced addresses as a set, ignoring order and addresses mDNS does not announce", func() {
		g := NewWithT(GinkgoT())
		a := []ma.Multiaddr{
			mustAddr("/ip4/192.0.2.9/tcp/4001"),
			mustAddr("/ip4/192.0.2.9/udp/4001/quic-v1"),
			mustAddr("/ip6/2001:db8::1/tcp/4001"),
		}
		b := []ma.Multiaddr{a[2], a[0], a[1], a[1]}
		g.Expect(mdnsAddrSet(a)).To(Equal(mdnsAddrSet(b)))

		// A relay address, a DNS name that is not local and an address of another
		// kind are not announced, so a change in them is not a change of the set.
		noise := append([]ma.Multiaddr{
			mustAddr("/ip4/198.51.100.1/tcp/4001/p2p/12D3KooWD3eckifWpRn9wQpMG9R9hX3sD158z7EqHWmweQAJU5SA/p2p-circuit"),
			mustAddr("/dns4/example.com/tcp/4001"),
		}, a...)
		g.Expect(mdnsAddrSet(noise)).To(Equal(mdnsAddrSet(a)))

		g.Expect(mdnsAddrSet([]ma.Multiaddr{mustAddr("/ip4/192.0.2.10/tcp/4001")})).ToNot(Equal(mdnsAddrSet(a)))
		g.Expect(mdnsAddrSet(nil)).To(BeEmpty())
	})

	It("restarts only when the announced addresses change, and not after close", func() {
		g := NewWithT(GinkgoT())
		f := &fakeNet{}
		f.set("/ip4/192.0.2.1/tcp/1")
		r := f.runner()

		r.refresh(true)
		g.Expect(f.services()).To(HaveLen(1))

		r.refresh(false) // the same set: no restart
		g.Expect(f.services()).To(HaveLen(1))
		g.Expect(f.services()[0].closed.Load()).To(BeFalse())

		f.set("/ip4/192.0.2.2/tcp/1")
		r.refresh(false)
		svcs := f.services()
		g.Expect(svcs).To(HaveLen(2))
		g.Expect(svcs[0].closed.Load()).To(BeTrue())
		g.Expect(svcs[1].closed.Load()).To(BeFalse())

		g.Expect(r.close()).To(Succeed())
		g.Expect(svcs[1].closed.Load()).To(BeTrue())
		f.set("/ip4/192.0.2.3/tcp/1")
		r.refresh(false) // after close: nothing starts
		g.Expect(f.services()).To(HaveLen(2))
	})

	It("starts when an address appears and stops when all are gone", func() {
		g := NewWithT(GinkgoT())
		f := &fakeNet{}
		r := f.runner()
		r.refresh(true)
		g.Expect(f.services()).To(BeEmpty())

		f.set("/ip4/192.0.2.1/tcp/1")
		r.refresh(false)
		g.Expect(f.services()).To(HaveLen(1))

		// All addresses gone: the service stops and nothing new starts.
		f.set()
		r.refresh(false)
		g.Expect(f.services()).To(HaveLen(1))
		g.Expect(f.services()[0].closed.Load()).To(BeTrue())
	})

	It("tries again after a failed start", func() {
		g := NewWithT(GinkgoT())
		f := &fakeNet{failing: true}
		f.set("/ip4/192.0.2.1/tcp/1")
		r := f.runner()
		r.refresh(true)
		g.Expect(f.services()).To(HaveLen(1))

		f.mu.Lock()
		f.failing = false
		f.mu.Unlock()
		r.refresh(false) // same addresses, but the service did not start: try again
		g.Expect(f.services()).To(HaveLen(2))
		g.Expect(f.services()[1].closed.Load()).To(BeFalse())
	})

	It("reads the interface addresses of a wildcard listener without libp2p's cache", func() {
		g := NewWithT(GinkgoT())
		h, err := New(context.Background(), Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
			ListenAddrs:  []string{"/ip4/0.0.0.0/tcp/0"},
		})
		g.Expect(err).ToNot(HaveOccurred())
		defer func() { _ = h.Close() }()
		got, err := liveHost{h.h}.Network().InterfaceListenAddresses()
		g.Expect(err).ToNot(HaveOccurred())
		set := mdnsAddrSet(got)
		g.Expect(set).ToNot(BeEmpty())
		g.Expect(set).To(HaveEach(HavePrefix("/ip4/")))
		g.Expect(set).ToNot(ContainElement(HavePrefix("/ip4/0.0.0.0")))
		g.Expect(liveHost{h.h}.ID()).To(Equal(h.ID()))
	})

	It("stops the watcher and the runner when the host closes", func() {
		g := NewWithT(GinkgoT())
		h, err := New(context.Background(), Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			MDNS:         true,
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(h.disco).ToNot(BeNil())
		g.Expect(h.addrs).ToNot(BeNil())
		g.Expect(h.Close()).To(Succeed())
		g.Expect(h.addrs.done).To(BeClosed())
		g.Expect(h.Close()).To(Succeed())
	})
})
