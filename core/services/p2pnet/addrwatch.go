package p2pnet

import (
	"context"
	"slices"
	"sync"
	"time"
)

const (
	// addrDebounce is how long the address set must stay quiet before the
	// watcher acts. An interface that changes its address usually emits a
	// burst of events (remove, add, relay and observed addresses).
	addrDebounce = 2 * time.Second
	// addrPoll is how often the watcher reads the interface addresses. libp2p
	// keeps its own list of them for up to a minute before it emits an event
	// for a change, which is too slow for a host that got a new address.
	// Reading the list is one cheap system call, and an event still starts
	// the check at once when it comes first.
	addrPoll = 5 * time.Second
)

// addrWatcher finds out that the addresses of the host's interfaces changed
// and calls its subscribers: the mDNS service restarts, and the paired peers
// are dialed again. It is the one place that compares the address set.
type addrWatcher struct {
	// addrs returns the sorted set of addresses the host has now.
	addrs    func() ([]string, error)
	debounce time.Duration
	// poll is how often run compares the addresses; zero means never.
	poll time.Duration

	mu   sync.Mutex
	seen []string
	subs []func()

	done chan struct{}
}

func newAddrWatcher(addrs func() ([]string, error)) *addrWatcher {
	w := &addrWatcher{addrs: addrs, debounce: addrDebounce, poll: addrPoll, done: make(chan struct{})}
	w.seen, _ = addrs()
	return w
}

// subscribe adds fn, which run calls on its goroutine after each change.
// Call it before run starts.
func (w *addrWatcher) subscribe(fn func()) { w.subs = append(w.subs, fn) }

// changed reports whether the addresses differ from the last set that the
// subscribers were told about.
func (w *addrWatcher) changed() ([]string, bool) {
	cur, err := w.addrs()
	if err != nil {
		return nil, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return cur, !slices.Equal(cur, w.seen)
}

// run checks the addresses after an event, or when a poll finds a change,
// once they have been quiet for the debounce time, and calls the subscribers
// when the set differs from the last one. Events for other changes (relay and
// observed addresses) leave the set as it is and call nobody. run returns,
// and closes done, when ctx ends or events closes.
func (w *addrWatcher) run(ctx context.Context, events <-chan any) {
	defer close(w.done)
	var timer *time.Timer
	var fire <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(w.debounce)
		} else {
			timer.Reset(w.debounce)
		}
		fire = timer.C
	}
	var tick <-chan time.Time
	if w.poll > 0 {
		t := time.NewTicker(w.poll)
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
			// A change that polling finds again must not push the check
			// back: only a new event does.
			if _, ch := w.changed(); fire == nil && ch {
				arm()
			}
		case <-fire:
			fire = nil
			cur, ch := w.changed()
			if !ch {
				continue
			}
			w.mu.Lock()
			w.seen = cur
			w.mu.Unlock()
			for _, fn := range w.subs {
				fn()
			}
		}
	}
}
