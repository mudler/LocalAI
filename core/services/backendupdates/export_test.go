package backendupdates

// Source exposes the state mapping of a SourceStatus to the external tests.
func (s SourceStatus) SourceForTest() Source { return s.source() }
