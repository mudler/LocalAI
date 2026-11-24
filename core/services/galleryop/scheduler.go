package galleryop

import (
	"context"
	"strings"
	"sync"
	"time"
)

// opTask is one unit of work for the opScheduler.
type opTask struct {
	// key serializes tasks: two tasks with the same key never run together.
	key string
	// ctx is the operation context. When it ends while the task still waits,
	// the task is dropped and abandon runs instead of run.
	ctx     context.Context
	run     func()
	abandon func()

	stopWatch func() bool
}

// opScheduler runs tasks with a bounded number of workers.
//
// Tasks start in submit order, except that a task whose key is busy waits and
// lets later tasks with other keys go first. The limit is read each time the
// scheduler looks for work, so it follows a runtime setting change without a
// restart. With a limit of 1 the behaviour is the old single worker: one task
// at a time, in submit order.
type opScheduler struct {
	limit func() int

	mu      sync.Mutex
	pending []*opTask
	running int
	busy    map[string]struct{}
}

func newOpScheduler(limit func() int) *opScheduler {
	return &opScheduler{limit: limit, busy: map[string]struct{}{}}
}

func (s *opScheduler) maxRunning() int {
	if n := s.limit(); n > 1 {
		return n
	}
	return 1
}

// submit queues a task. It never blocks.
func (s *opScheduler) submit(t *opTask) {
	s.mu.Lock()
	s.pending = append(s.pending, t)
	if t.ctx != nil {
		// A cancel of a queued task must free it at once, not when its turn
		// comes, so a cancelled operation never holds a place in the queue.
		t.stopWatch = context.AfterFunc(t.ctx, func() { s.dropPending(t) })
	}
	s.dispatchLocked()
	s.mu.Unlock()
}

// kick starts waiting tasks if the limit has grown since the last look.
func (s *opScheduler) kick() {
	s.mu.Lock()
	s.dispatchLocked()
	s.mu.Unlock()
}

// hasPending reports whether any task waits for a slot.
func (s *opScheduler) hasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

// abandonAll drops every waiting task. Running tasks are not touched.
func (s *opScheduler) abandonAll() {
	s.mu.Lock()
	dropped := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, t := range dropped {
		if t.stopWatch != nil {
			t.stopWatch()
		}
		t.abandon()
	}
}

func (s *opScheduler) dropPending(t *opTask) {
	s.mu.Lock()
	found := false
	for i, p := range s.pending {
		if p == t {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			found = true
			break
		}
	}
	s.mu.Unlock()
	if found {
		t.abandon()
	}
}

func (s *opScheduler) dispatchLocked() {
	max := s.maxRunning()
	for i := 0; i < len(s.pending) && s.running < max; {
		t := s.pending[i]
		if _, held := s.busy[t.key]; held {
			i++
			continue
		}
		s.pending = append(s.pending[:i], s.pending[i+1:]...)
		if t.stopWatch != nil && !t.stopWatch() {
			// The context already ended and its callback is on the way; it
			// finds the task gone and does nothing, so run the abandon here.
			go t.abandon()
			continue
		}
		s.busy[t.key] = struct{}{}
		s.running++
		go func() {
			defer func() {
				s.mu.Lock()
				s.running--
				delete(s.busy, t.key)
				s.dispatchLocked()
				s.mu.Unlock()
			}()
			t.run()
		}()
	}
}

// backendOpKey names the backend an operation touches, so install, upgrade and
// delete of one backend share a key. A gallery prefix ("gallery@name") is
// dropped and the case is folded: the same backend can be addressed both ways.
func backendOpKey(elementName, externalName string) string {
	name := externalName
	if name == "" {
		name = elementName
	}
	if i := strings.LastIndex(name, "@"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// schedulerPollInterval is how often a waiting queue re-reads the limit.
const schedulerPollInterval = time.Second
