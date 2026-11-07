package localaitools

// SetMaxResultBytes lowers the cap on a stored result for a test and returns
// the function that puts it back.
func SetMaxResultBytes(n int64) func() {
	old := maxResultBytes
	maxResultBytes = n
	return func() { maxResultBytes = old }
}
