package prefixcache

// LenForTest exposes the internal per-model slice length so black-box tests can
// assert that Record bounds its backing slice. Test-only.
func (p *Pressure) LenForTest(model string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events[model])
}

// TreeCountForTest exposes the number of per-model radix trees the Index
// currently retains, so black-box tests can assert that Invalidate does not
// intern empty trees for models that never used the prefix cache. Test-only.
func (ix *Index) TreeCountForTest() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.trees)
}
