package p2prelaymgr

import (
	"sync"
	"time"
)

// passMap keeps the code of the relay pass of each invite, until the pass
// expires or is cancelled. It is per instance, and not shared between the
// frontends of a cluster; see NewPassRegistrar.
type passMap struct {
	mu sync.Mutex
	m  map[string]passEntry
}

type passEntry struct {
	code    string
	expires time.Time
}

// store keeps code for invite until expires, and drops the entries that
// expired.
func (p *passMap) store(invite, code string, expires time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]passEntry{}
	}
	p.pruneLocked(time.Now())
	p.m[invite] = passEntry{code: code, expires: expires}
}

// load returns the code of invite if its pass did not expire at now.
func (p *passMap) load(invite string, now time.Time) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	e, ok := p.m[invite]
	return e.code, ok
}

func (p *passMap) delete(invite string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, invite)
}

func (p *passMap) pruneLocked(now time.Time) {
	for k, e := range p.m {
		if !now.Before(e.expires) {
			delete(p.m, k)
		}
	}
}
