//go:build !unix

package promptcache

import "errors"

// OSStats reads the filesystem. This platform has no reader: the janitor
// then runs without the free-space watermarks.
type OSStats struct{}

// Stat always fails here.
func (OSStats) Stat(string) (FSStats, error) {
	return FSStats{}, errors.New("promptcache: no filesystem stats on this platform")
}
