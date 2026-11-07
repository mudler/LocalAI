package skills

import "io/fs"

const modeSymlinkBits = uint32(fs.ModeSymlink)

func fsMode(m uint32) fs.FileMode { return fs.FileMode(m) }
