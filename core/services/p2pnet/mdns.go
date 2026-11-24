package p2pnet

import (
	"slices"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// mdnsRunner keeps a libp2p mDNS service in step with the host's interface
// addresses. The libp2p service reads the addresses once, when it starts, and
// has no call to change what it announces. After the address of the host
// changes it keeps announcing the old one, so peers on the network cannot
// find the host again. The runner closes the service and starts a new one
// when the set of announced addresses changes.
type mdnsRunner struct {
	// addrs returns the addresses the service would announce now.
	addrs func() ([]string, error)
	// service makes a new, stopped mDNS service.
	service func() mdns.Service

	mu      sync.Mutex
	svc     mdns.Service
	last    []string
	started bool
	closed  bool
}

// liveHost is a host whose network reads the interface addresses at each
// call. The swarm of libp2p caches them for a minute, so for up to a minute
// after the address of an interface changed, the mDNS service would read the
// old list when it starts and announce an address that is gone.
type liveHost struct{ host.Host }

func (h liveHost) Network() network.Network { return liveNetwork{h.Host.Network()} }

type liveNetwork struct{ network.Network }

// InterfaceListenAddresses expands the wildcard listen addresses with the
// addresses that the interfaces have now.
func (n liveNetwork) InterfaceListenAddresses() ([]ma.Multiaddr, error) {
	return manet.ResolveUnspecifiedAddresses(n.ListenAddresses(), nil)
}

// liveAddrSet returns a function that reads the sorted set of addresses that
// the host's interfaces have now and that mDNS can announce.
func liveAddrSet(h host.Host) func() ([]string, error) {
	live := liveHost{h}
	return func() ([]string, error) {
		as, err := live.Network().InterfaceListenAddresses()
		if err != nil {
			return nil, err
		}
		return mdnsAddrSet(as), nil
	}
}

func newMDNSRunner(h host.Host, notifee mdns.Notifee) *mdnsRunner {
	return &mdnsRunner{
		addrs:   liveAddrSet(h),
		service: func() mdns.Service { return mdns.NewMdnsService(liveHost{h}, mdnsServiceName, notifee) },
	}
}

// mdnsAddrSet returns the sorted, unique text forms of the addresses that
// the mDNS service can announce: those that start with an IP address and do
// not go through a relay. Two calls with the same addresses in any order
// return equal slices.
func mdnsAddrSet(addrs []ma.Multiaddr) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if !mdnsSuitable(a) {
			continue
		}
		out = append(out, a.String())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func mdnsSuitable(a ma.Multiaddr) bool {
	if a == nil {
		return false
	}
	first, _ := ma.SplitFirst(a)
	if first == nil {
		return false
	}
	switch first.Protocol().Code {
	case ma.P_IP4, ma.P_IP6:
	default:
		return false
	}
	return !isRelayed(a)
}

// refresh restarts the service when the announced addresses differ from the
// ones it started with. It starts the service when none runs. It does
// nothing after close. The first call reports why the service is off.
func (r *mdnsRunner) refresh(first bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	cur, err := r.addrs()
	if err != nil {
		xlog.Warn("p2p: local network discovery (mDNS) cannot read the interface addresses", "error", err)
		return
	}
	if r.started && slices.Equal(cur, r.last) {
		return
	}
	if r.svc != nil {
		if err := r.svc.Close(); err != nil {
			xlog.Debug("p2p: close mDNS before restart", "error", err)
		}
		r.svc = nil
	}
	r.started = false
	if len(cur) == 0 {
		// Nothing to announce: libp2p's service fails to start without an
		// address. A later address change tries again.
		if first {
			xlog.Warn("p2p: local network discovery (mDNS) is off", "error", "the host has no address to announce")
		}
		return
	}
	svc := r.service()
	if err := svc.Start(); err != nil {
		if first {
			xlog.Warn("p2p: local network discovery (mDNS) is off", "error", err)
		} else {
			xlog.Warn("p2p: local network discovery (mDNS) did not restart", "error", err)
		}
		_ = svc.Close()
		return
	}
	r.svc, r.last, r.started = svc, cur, true
	if !first {
		xlog.Info("p2p: local network discovery (mDNS) restarted after an address change", "addresses", len(cur))
	}
}

// close stops the service for good. A restart in progress finishes first.
func (r *mdnsRunner) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.svc == nil {
		return nil
	}
	err := r.svc.Close()
	r.svc = nil
	return err
}

// startMDNS starts local network discovery and restarts it whenever w sees a
// change of the host's addresses. Discovery only finds peers on the local
// network, so a host that cannot start it still works.
func (n *Host) startMDNS(w *addrWatcher) {
	r := newMDNSRunner(n.h, mdnsNotifee{n: n})
	r.refresh(true)
	n.disco = r
	w.subscribe(func() { r.refresh(false) })
}
