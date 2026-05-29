package prefixcache

// LenForTest exposes the internal per-model slice length so black-box tests can
// assert that Record bounds its backing slice. Test-only.
func (p *Pressure) LenForTest(model string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events[model])
}
