package ext4ro

import (
	"fmt"
)

const (
	xattrMagic      = 0xEA020000
	xattrBlockHdr   = 32
	xattrEntryFixed = 16
)

// Xattr is one extended attribute.
type Xattr struct {
	Name  string // with the namespace, for example "trusted.overlay.opaque"
	Value []byte
	// Err is set when the reader cannot give the value: the value lives in
	// an inode (ea_inode), which the reader does not support. The other
	// attributes of the inode stay readable.
	Err error
}

// xattrPrefix maps the name index of an entry to the namespace.
var xattrPrefix = map[uint8]string{
	0: "",
	1: "user.",
	2: "system.posix_acl_access",
	3: "system.posix_acl_default",
	4: "trusted.",
	6: "security.",
	7: "system.",
	8: "system.richacl",
}

// Xattrs returns the attributes of the inode, first the ones in the inode
// body, then the ones in the attribute block. A damaged area is an error:
// the area has bytes but no magic, an entry or a value is outside the area,
// or the terminator is missing. The total size of the values is at most
// maxXattrBytes.
func (f *FS) Xattrs(ino uint64) ([]Xattr, error) {
	in, err := f.load(ino)
	if err != nil {
		return nil, err
	}
	out, err := f.ibodyXattrs(in)
	if err != nil {
		return nil, err
	}
	if in.fileACL == 0 {
		return out, nil
	}
	blk, err := f.readBlock(in.fileACL)
	if err != nil {
		return nil, fmt.Errorf("inode %d: attribute block: %w", ino, err)
	}
	if le.Uint32(blk) != xattrMagic || le.Uint32(blk[8:]) != 1 {
		return nil, fmt.Errorf("%w: inode %d: bad attribute block header", ErrCorrupt, ino)
	}
	more, err := parseXattrs(blk, xattrBlockHdr)
	if err != nil {
		return nil, fmt.Errorf("inode %d: %w", ino, err)
	}
	out = append(out, more...)
	total := 0
	for _, x := range out {
		total += len(x.Value)
	}
	if total > maxXattrBytes {
		return nil, fmt.Errorf("%w: attributes of inode %d hold %d bytes", ErrLimit, ino, total)
	}
	return out, nil
}

// Xattr returns the value of the attribute name. The bool is false when the
// inode has no such attribute. If the attribute exists but the reader cannot
// read its value, the error is Xattr.Err.
func (f *FS) Xattr(ino uint64, name string) ([]byte, bool, error) {
	xs, err := f.Xattrs(ino)
	if err != nil {
		return nil, false, err
	}
	for _, x := range xs {
		if x.Name == name {
			return x.Value, true, x.Err
		}
	}
	return nil, false, nil
}

// parseXattrs reads entries from region, from offset start up to the
// terminator (four zero bytes). A value offset counts from the start of
// region, as the kernel does for the block, and from the first entry for the
// inode body (the caller passes the region that starts there).
func parseXattrs(region []byte, start int) ([]Xattr, error) {
	var out []Xattr
	total := 0
	for off := start; ; {
		if off+4 > len(region) {
			return nil, fmt.Errorf("%w: attribute entries have no terminator", ErrCorrupt)
		}
		if le.Uint32(region[off:]) == 0 {
			return out, nil
		}
		if off+xattrEntryFixed > len(region) {
			return nil, fmt.Errorf("%w: attribute entry at %d is cut off", ErrCorrupt, off)
		}
		nl := int(region[off])
		idx := region[off+1]
		voff := uint64(le.Uint16(region[off+2:]))
		inum := le.Uint32(region[off+4:])
		vsize := uint64(le.Uint32(region[off+8:]))
		next := off + (xattrEntryFixed+nl+3)&^3
		if next+4 > len(region) {
			return nil, fmt.Errorf("%w: attribute entry at %d is cut off", ErrCorrupt, off)
		}
		prefix, ok := xattrPrefix[idx]
		if !ok {
			return nil, fmt.Errorf("%w: attribute name index %d", ErrUnsupported, idx)
		}
		name := prefix + string(region[off+xattrEntryFixed:off+xattrEntryFixed+nl])
		if name == "" {
			return nil, fmt.Errorf("%w: attribute entry at %d has no name", ErrCorrupt, off)
		}
		x := Xattr{Name: name}
		if inum != 0 {
			x.Err = fmt.Errorf("%w: the value of %q is in inode %d (ea_inode)", ErrUnsupported, name, inum)
		} else {
			if voff+vsize > uint64(len(region)) {
				return nil, fmt.Errorf("%w: value of %q is outside the attribute area", ErrCorrupt, name)
			}
			if total += int(vsize); total > maxXattrBytes { // #nosec G115 -- vsize is inside the attribute area, which is a slice
				return nil, fmt.Errorf("%w: attribute values hold more than %d bytes", ErrLimit, maxXattrBytes)
			}
			x.Value = append(make([]byte, 0, vsize), region[voff:voff+vsize]...)
		}
		out = append(out, x)
		off = next
	}
}
