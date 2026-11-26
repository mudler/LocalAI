package plugins

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
)

// lockPoll is how often a change waits for the lock of its plugin ID.
const lockPoll = 200 * time.Millisecond

// localLocks serializes the changes of one plugin ID on a server without a
// database, which has one frontend. One mutex per plugin ID that was ever
// changed stays in memory; it holds no state.
var localLocks sync.Map

// lockPlugin holds the change lock of a plugin ID until the returned release
// runs. The key is the plugin ID alone, not the scope: an instance install
// and a user install of one plugin ID must not run at once, since each
// checks the other in its transaction. With a database it is a HeldLock on
// KeyFromString("plugin-change:" + pluginID), so it holds across frontends.
func (s *Service) lockPlugin(ctx context.Context, pluginID string) (func(), error) {
	if s.db == nil {
		v, _ := localLocks.LoadOrStore(pluginID, &sync.Mutex{})
		mu := v.(*sync.Mutex)
		done := make(chan struct{})
		go func() { mu.Lock(); close(done) }()
		select {
		case <-done:
			return mu.Unlock, nil
		case <-ctx.Done():
			// Release the mutex when the waiter finally gets it.
			go func() { <-done; mu.Unlock() }()
			return nil, ctx.Err()
		}
	}
	l := advisorylock.NewHeldLock(s.db, advisorylock.KeyFromString("plugin-change:"+pluginID))
	for {
		ok, err := l.TryAcquire(ctx)
		if err != nil {
			l.Close()
			return nil, err
		}
		if ok {
			return l.Close, nil
		}
		select {
		case <-ctx.Done():
			l.Close()
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}
