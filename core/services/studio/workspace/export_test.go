package workspace

// SetArchiveMaxEntries lowers the entry limit of ArchiveSource for a test and
// returns the function that puts it back.
func SetArchiveMaxEntries(n int) func() {
	old := archiveMaxEntries
	archiveMaxEntries = n
	return func() { archiveMaxEntries = old }
}
