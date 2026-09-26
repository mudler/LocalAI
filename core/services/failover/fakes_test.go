package failover

import (
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fakeSource struct {
	mu    sync.Mutex
	cfgs  map[string]config.ModelConfig
	scans int // GetAllModelsConfigs calls
}

func newFakeSource(cfgs ...config.ModelConfig) *fakeSource {
	s := &fakeSource{cfgs: map[string]config.ModelConfig{}}
	for _, c := range cfgs {
		s.cfgs[c.Name] = c
	}
	return s
}
func (s *fakeSource) Put(c config.ModelConfig) { s.mu.Lock(); s.cfgs[c.Name] = c; s.mu.Unlock() }
func (s *fakeSource) Delete(name string)       { s.mu.Lock(); delete(s.cfgs, name); s.mu.Unlock() }
func (s *fakeSource) Scans() int               { s.mu.Lock(); defer s.mu.Unlock(); return s.scans }
func (s *fakeSource) GetModelConfig(n string) (config.ModelConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cfgs[n]
	return c, ok
}
func (s *fakeSource) GetAllModelsConfigs() []config.ModelConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scans++
	out := make([]config.ModelConfig, 0, len(s.cfgs))
	for _, c := range s.cfgs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func local(name string) config.ModelConfig {
	return config.ModelConfig{Name: name, Backend: "llama-cpp"}
}
func remote(name string) config.ModelConfig {
	return config.ModelConfig{Name: name, Backend: "cloud-proxy"}
}

// chainCfg builds a chain; fc may be nil for defaults.
func chainCfg(name string, fc *config.FailoverConfig, targets ...config.FailoverTarget) config.ModelConfig {
	f := config.FailoverConfig{}
	if fc != nil {
		f = *fc
	}
	f.Targets = targets
	return config.ModelConfig{Name: name, Failover: &f}
}

func t(model string) config.FailoverTarget { return config.FailoverTarget{Model: model} }
func warmT(model string) config.FailoverTarget {
	return config.FailoverTarget{Model: model, Warm: true}
}

// drain returns the events buffered so far without blocking.
func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}
