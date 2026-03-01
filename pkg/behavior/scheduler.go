package behavior

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Scheduler manages time-driven behavior tree execution
type Scheduler struct {
	trees       map[string]*Tree
	interval    time.Duration
	stopCh      chan struct{}
	runCh       chan struct{}
	mu          sync.RWMutex
	ticker      *time.Ticker
	running     bool
	lastRun     time.Time
	runCount    int
}

// NewScheduler creates a new scheduler with the specified interval
func NewScheduler(interval time.Duration) *Scheduler {
	return &Scheduler{
		trees:    make(map[string]*Tree),
		interval: interval,
		stopCh:   make(chan struct{}),
		runCh:    make(chan struct{}, 1),
	}
}

// RegisterTree adds a behavior tree to the scheduler
func (s *Scheduler) RegisterTree(tree *Tree) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trees[tree.name] = tree
}

// UnregisterTree removes a behavior tree from the scheduler
func (s *Scheduler) UnregisterTree(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.trees, name)
}

// Start begins the scheduler's tick loop
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.ticker = time.NewTicker(s.interval)
	s.mu.Unlock()

	go s.runLoop(ctx)
}

// Stop halts the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if !s.running {
		return
	}
	
	s.ticker.Stop()
	s.running = false
	close(s.stopCh)
	s.stopCh = make(chan struct{})
}

// Trigger forces an immediate tick
func (s *Scheduler) Trigger(ctx context.Context) {
	select {
	case s.runCh <- struct{}{}:
	default:
	}
}

// runLoop is the main scheduler loop
func (s *Scheduler) runLoop(ctx context.Context) {
	for {
		select {
		case <-s.ticker.C:
			s.tick(ctx)
		case <-s.runCh:
			s.tick(ctx)
		case <-s.stopCh:
			return
		case <-ctx.Done():
			s.Stop()
			return
		}
	}
}

// tick executes all registered trees
func (s *Scheduler) tick(ctx context.Context) {
	s.mu.RLock()
	trees := make([]*Tree, 0, len(s.trees))
	for _, tree := range s.trees {
		trees = append(trees, tree)
	}
	s.mu.RUnlock()

	for _, tree := range trees {
		status := tree.Tick(ctx)
		fmt.Printf("[Scheduler] Ticked tree %s: %s\n", tree.name, status)
	}

	s.mu.Lock()
	s.lastRun = time.Now()
	s.runCount++
	s.mu.Unlock()
}

// LastRun returns the time of the last tick
func (s *Scheduler) LastRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRun
}

// RunCount returns the number of ticks performed
func (s *Scheduler) RunCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runCount
}

// IsRunning returns whether the scheduler is active
func (s *Scheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Interval returns the tick interval
func (s *Scheduler) Interval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.interval
}

// SetInterval changes the tick interval (requires restart)
func (s *Scheduler) SetInterval(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval = interval
}
