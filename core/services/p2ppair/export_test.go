package p2ppair

// WordList exposes the phrase word list to the tests.
func WordList() []string { return wordList[:] }

// LimiterSize returns how many peers and sources the default limiter holds.
func LimiterSize(l Limiter) int {
	m := l.(*memLimiter)
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.peers) + len(m.sources)
}
