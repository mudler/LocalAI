package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultAttemptTimeout bounds one provider of a Fallback, so a provider that
// hangs leaves time for the next one.
const DefaultAttemptTimeout = 8 * time.Second

// DefaultCooldown is how long a Fallback with a cool-down skips a provider
// that challenged or blocked it.
const DefaultCooldown = 5 * time.Minute

// Answerer is a Provider that can say which provider answered a search. A
// Searcher over an Answerer labels the results with the host that answered.
type Answerer interface {
	Provider
	SearchFrom(ctx context.Context, q Query) ([]Result, Provider, error)
}

// Attempt is what one provider of a Fallback did with one search.
type Attempt struct {
	Provider string  `json:"provider"`
	Host     string  `json:"host"`
	Outcome  Outcome `json:"outcome"`
	// Results counts the results of an answer.
	Results int `json:"results"`
	// Reason says why the provider gave no answer. Empty on an answer.
	Reason string `json:"reason,omitempty"`
}

// Fallback tries its providers in order and returns the first answer. A
// provider that fails (it is down, refuses its key, limits this server, does
// not answer in time) is skipped and the next one is tried. An answer with no
// results is an answer: the next provider is not asked.
//
// With a cool-down, a provider that answered with a bot check or refused this
// server is not asked again for that time, so each search does not pay for it
// first. The cool-down is in the memory of the Fallback: in a cluster each
// frontend keeps its own, and building a new Fallback (a change of the
// settings) forgets it.
type Fallback struct {
	providers []Provider
	attempt   time.Duration
	cooldown  time.Duration
	now       func() time.Time

	mu    sync.Mutex
	until map[string]cool
}

// cool is the cool-down of one provider: until when, and what put it there.
type cool struct {
	until time.Time
	why   Outcome
}

// NewFallback returns a Fallback over providers, in that order. attempt bounds
// each provider; zero means DefaultAttemptTimeout. It has no cool-down; see
// WithCooldown.
func NewFallback(attempt time.Duration, providers ...Provider) *Fallback {
	if attempt <= 0 {
		attempt = DefaultAttemptTimeout
	}
	return &Fallback{providers: append([]Provider(nil), providers...), attempt: attempt, now: time.Now, until: map[string]cool{}}
}

// WithCooldown sets the cool-down of f and returns f. Zero turns it off.
func (f *Fallback) WithCooldown(d time.Duration) *Fallback {
	f.cooldown = d
	return f
}

// SetClock replaces the clock, for tests.
func (f *Fallback) SetClock(now func() time.Time) { f.now = now }

// Providers lists the providers in order.
func (f *Fallback) Providers() []Provider { return append([]Provider(nil), f.providers...) }

// Name joins the names of the providers, so that a cache keyed by the name
// does not mix the answers of two orders.
func (f *Fallback) Name() string {
	names := make([]string, len(f.providers))
	for i, p := range f.providers {
		names[i] = p.Name()
	}
	return strings.Join(names, "+")
}

// Host is the host of the first provider. The host that answered a search is
// what SearchFrom returns.
func (f *Fallback) Host() string {
	if len(f.providers) == 0 {
		return ""
	}
	return f.providers[0].Host()
}

// Search implements Provider.
func (f *Fallback) Search(ctx context.Context, q Query) ([]Result, error) {
	r, _, err := f.SearchFrom(ctx, q)
	return r, err
}

// SearchFrom returns the first answer and the provider that gave it. When no
// provider answers, the error wraps ErrProvider and names each provider with
// its reason, in order.
func (f *Fallback) SearchFrom(ctx context.Context, q Query) ([]Result, Provider, error) {
	r, p, _, err := f.SearchAttempts(ctx, q)
	return r, p, err
}

// SearchAttempts is SearchFrom that also returns what each provider did, in
// order. A provider in its cool-down is skipped, unless every provider is:
// then they are all asked, so a provider that works again is found.
func (f *Fallback) SearchAttempts(ctx context.Context, q Query) ([]Result, Provider, []Attempt, error) {
	if len(f.providers) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: no provider is set", ErrProvider)
	}
	cooling := f.cooling()
	allCooling := len(cooling) == len(f.providers)
	var attempts []Attempt
	var reasons []string
	for _, p := range f.providers {
		if err := ctx.Err(); err != nil {
			return nil, nil, attempts, err
		}
		if why, ok := cooling[p.Name()]; ok && !allCooling {
			reason := "skipped, " + why + " a short while ago"
			attempts = append(attempts, Attempt{Provider: p.Name(), Host: p.Host(), Outcome: OutcomeSkipped, Reason: reason})
			reasons = append(reasons, p.Name()+": "+reason)
			continue
		}
		res, from, err := f.ask(ctx, p, q)
		a := Attempt{Provider: p.Name(), Host: from.Host(), Outcome: OutcomeOf(res, err), Results: len(res)}
		if err == nil {
			f.clear(p.Name())
			attempts = append(attempts, a)
			return res, from, attempts, nil
		}
		if ctx.Err() != nil {
			// The caller stopped, or its own time ran out: no next provider.
			return nil, nil, attempts, ctx.Err()
		}
		f.mark(p.Name(), a.Outcome)
		a.Reason = reasonOf(err)
		attempts = append(attempts, a)
		reasons = append(reasons, p.Name()+": "+a.Reason)
	}
	return nil, nil, attempts, fmt.Errorf("%w: %s", ErrProvider, strings.Join(reasons, "; "))
}

// TestEach asks every provider, in order and outside the cool-down, and
// returns what each did, with the first answer and the provider that gave it
// (what a search would return), or the error a search would return. A
// provider that answers leaves its cool-down; one that challenges or blocks
// enters it. It is the administrator's check of the chain, so it does not
// stop at the first answer.
func (f *Fallback) TestEach(ctx context.Context, q Query) ([]Result, Provider, []Attempt, error) {
	if len(f.providers) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: no provider is set", ErrProvider)
	}
	var (
		first    []Result
		answered Provider
		attempts = make([]Attempt, 0, len(f.providers))
		reasons  []string
	)
	for _, p := range f.providers {
		if err := ctx.Err(); err != nil {
			return nil, nil, attempts, err
		}
		res, from, err := f.ask(ctx, p, q)
		a := Attempt{Provider: p.Name(), Host: from.Host(), Outcome: OutcomeOf(res, err), Results: len(res)}
		if err != nil {
			a.Reason = reasonOf(err)
			reasons = append(reasons, p.Name()+": "+a.Reason)
			f.mark(p.Name(), a.Outcome)
		} else {
			f.clear(p.Name())
			if answered == nil {
				first, answered = res, from
			}
		}
		attempts = append(attempts, a)
	}
	if answered == nil {
		return nil, nil, attempts, fmt.Errorf("%w: %s", ErrProvider, strings.Join(reasons, "; "))
	}
	return first, answered, attempts, nil
}

// ask runs one provider within the attempt time. A provider that is itself
// an Answerer says which of its own providers answered.
func (f *Fallback) ask(ctx context.Context, p Provider, q Query) ([]Result, Provider, error) {
	actx, cancel := context.WithTimeout(ctx, f.attempt)
	defer cancel()
	if a, ok := p.(Answerer); ok {
		res, from, err := a.SearchFrom(actx, q)
		if from == nil {
			from = p
		}
		return res, from, err
	}
	res, err := p.Search(actx, q)
	return res, p, err
}

// Cooling returns the providers in their cool-down, by name, with what put
// them there, for a settings page that shows the state of the chain.
func (f *Fallback) Cooling() map[string]string { return f.cooling() }

// cooling returns the providers in their cool-down, with what put them there.
func (f *Fallback) cooling() map[string]string {
	out := map[string]string{}
	if f.cooldown <= 0 {
		return out
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for name, c := range f.until {
		if !now.Before(c.until) {
			delete(f.until, name)
			continue
		}
		if c.why == OutcomeBlocked {
			out[name] = "it blocked this server"
		} else {
			out[name] = "it answered with a bot check"
		}
	}
	return out
}

// mark puts name in its cool-down after a challenge or a block.
func (f *Fallback) mark(name string, o Outcome) {
	if f.cooldown <= 0 || (o != OutcomeChallenged && o != OutcomeBlocked) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.until[name] = cool{until: f.now().Add(f.cooldown), why: o}
}

func (f *Fallback) clear(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.until, name)
}

// reasonOf is the sentence of a provider's error without the class prefix.
func reasonOf(err error) string {
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return "it did not answer in time"
	}
	s := err.Error()
	s = strings.TrimPrefix(s, ErrProvider.Error()+": ")
	if s == ErrProvider.Error() {
		return "it did not answer with results"
	}
	return s
}
