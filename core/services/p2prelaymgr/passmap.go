package p2prelaymgr

import "sync"

type passMap struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *passMap) store(invite, code string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]string{}
	}
	p.m[invite] = code
}

func (p *passMap) load(invite string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.m[invite]
	return c, ok
}

func (p *passMap) delete(invite string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, invite)
}
