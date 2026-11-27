//go:build unix

package promptcache

import "golang.org/x/sys/unix"

// OSStats reads the filesystem with statfs and stat.
type OSStats struct{}

// Stat returns the free and total bytes, the device and the type of the
// filesystem of path.
func (OSStats) Stat(path string) (FSStats, error) {
	var fs unix.Statfs_t
	if err := unix.Statfs(path, &fs); err != nil {
		return FSStats{}, err
	}
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return FSStats{}, err
	}
	bsize := uint64(fs.Bsize)
	fsType, network := fsTypeOf(path, &fs)
	return FSStats{
		Free:    uint64(fs.Bavail) * bsize,
		Total:   uint64(fs.Blocks) * bsize,
		Device:  uint64(st.Dev),
		FSType:  fsType,
		Network: network,
	}, nil
}
