package workspace

import (
	"context"
	"io"
	"io/fs"
	"strings"
)

// FixtureSource is an in-memory UpperSource for tests.
type FixtureSource []UpperEntry

// Walk calls fn for each entry in order.
func (f FixtureSource) Walk(ctx context.Context, fn func(UpperEntry) error) error {
	for _, e := range f {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// FixtureFile is a regular file with content.
func FixtureFile(p, content string) UpperEntry {
	return UpperEntry{Path: p, Kind: EntryFile, Mode: 0o644, Open: func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(content)), nil
	}}
}

// FixtureDir is a folder without attributes.
func FixtureDir(p string) UpperEntry {
	return UpperEntry{Path: p, Kind: EntryDir, Mode: fs.ModeDir | 0o755}
}

// FixtureOpaqueDir is a folder with the opaque attribute. prefix is
// "trusted." or "user.".
func FixtureOpaqueDir(p, prefix string) UpperEntry {
	e := FixtureDir(p)
	e.Xattrs = map[string][]byte{prefix + "overlay.opaque": []byte("y")}
	return e
}

// FixtureWhiteout is a whiteout (character device 0/0).
func FixtureWhiteout(p string) UpperEntry {
	return UpperEntry{Path: p, Kind: EntryWhiteout, Mode: fs.ModeDevice | fs.ModeCharDevice}
}

// FixtureSymlink is a symbolic link.
func FixtureSymlink(p string) UpperEntry {
	return UpperEntry{Path: p, Kind: EntrySymlink, Mode: fs.ModeSymlink | 0o777}
}
