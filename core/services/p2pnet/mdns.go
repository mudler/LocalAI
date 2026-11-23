package p2pnet

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

const (
	// mdnsDebounce is how long the address set must stay quiet before the
	// mDNS service restarts. An interface that changes its address usually
	// emits a burst of events (remove, add, relay and observed addresses).
	mdnsDebounce = 2 * time.Second
	// mdnsPoll is how often the runner reads the interface addresses. libp2p
	// keeps its own list of them for up to a minute before it emits an event
	// for a change, which is too slow for a host that got a new address.
	// Reading the list is one cheap system call, and the event still
	// restarts the service at once when it comes first.
	mdnsPoll = 5 * time.Second
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
	// debounce is the quiet time before a restart.
	debounce time.Duration
	// poll is how often run compares the addresses; zero means never.
	poll time.Duration

	mu      sync.Mutex
	svc     mdns.Service
	last    []string
	tried   []string // the addresses of the last start, whether it worked or not
	started bool
	closed  bool

	done chan struct{}
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

func newMDNSRunner(h host.Host, notifee mdns.Notifee) *mdnsRunner {
	live := liveHost{h}
	return &mdnsRunner{
		addrs: func() ([]string, error) {
			as, err := live.Network().InterfaceListenAddresses()
			if err != nil {
				return nil, err
			}
			return mdnsAddrSet(as), nil
		},
		service:  func() mdns.Service { return mdns.NewMdnsService(live, mdnsServiceName, notifee) },
		debounce: mdnsDebounce,
		poll:     mdnsPoll,
		done:     make(chan struct{}),
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
	r.tried = cur
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

// changed reports whether the addresses differ from the ones of the last
// start. A start that failed does not count as a change, so that a failing
// start is not retried at every poll.
func (r *mdnsRunner) changed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	cur, err := r.addrs()
	return err == nil && !slices.Equal(cur, r.tried)
}

// run restarts the service after the address events, or a change that a poll
// finds, have been quiet for the debounce time, until ctx ends or events
// closes. It closes done on return.
func (r *mdnsRunner) run(ctx context.Context, events <-chan any) {
	defer close(r.done)
	var timer *time.Timer
	var fire <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(r.debounce)
		} else {
			timer.Reset(r.debounce)
		}
		fire = timer.C
	}
	var tick <-chan time.Time
	if r.poll > 0 {
		t := time.NewTicker(r.poll)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			arm()
		case <-tick:
			// A change that polling finds again must not push the restart
			// back: only a new event does.
			if fire == nil && r.changed() {
				arm()
			}
		case <-fire:
			fire = nil
			r.refresh(false)
		}
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

// startMDNS starts local network discovery and keeps it current while the
// host's addresses change. Discovery only finds peers on the local network,
// so a host that cannot start it still works.
func (n *Host) startMDNS() error {
	// Subscribe before the first start, so that no change slips between them.
	sub, err := n.h.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		return fmt.Errorf("p2pnet: watch addresses: %w", err)
	}
	r := newMDNSRunner(n.h, mdnsNotifee{n: n})
	r.refresh(true)
	n.disco = r
	n.wg.Go(func() {
		defer func() { _ = sub.Close() }()
		r.run(n.ctx, sub.Out())
	})
	return nil
}
