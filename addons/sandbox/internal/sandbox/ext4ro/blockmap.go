package ext4ro

import (
	"fmt"
)

// run describes a range of logical blocks that map the same way. A hole has
// no physical block. An unwritten run has physical blocks, but they read as
// zeros.
type run struct {
	phys, n   uint64
	hole      bool
	unwritten bool
}

func holeRun(n uint64) run { return run{n: min(n, maxRun), hole: true} }

// lookup maps logical block lb of an inode that has no inline data. The
// budget counts the extent nodes that the caller may still visit.
func (f *FS) lookup(in *inode, lb uint64, budget *int) (run, error) {
	if in.Flags&flagInlineData != 0 {
		return run{}, fmt.Errorf("%w: inode %d: block lookup on inline data", ErrCorrupt, in.Ino)
	}
	if in.Flags&flagExtents != 0 {
		return f.extentLookup(in, lb, budget)
	}
	return f.blockMapLookup(in, lb)
}

// blockMapLookup maps lb through the classic block map: 12 direct blocks,
// then one indirect, one double indirect and one triple indirect block. The
// map has a fixed depth, so a pointer loop cannot make the lookup loop.
func (f *FS) blockMapLookup(in *inode, lb uint64) (run, error) {
	per := f.bs / 4
	word := func(i uint64) uint64 { return uint64(le.Uint32(in.raw[0x28+4*i:])) }
	if lb < 12 {
		return f.dataBlock(word(lb))
	}
	lb -= 12
	if lb < per {
		return f.indirect(word(12), 1, lb)
	}
	lb -= per
	if lb < per*per {
		return f.indirect(word(13), 2, lb)
	}
	lb -= per * per
	if lb < per*per*per {
		return f.indirect(word(14), 3, lb)
	}
	return holeRun(maxRun), nil
}

func (f *FS) dataBlock(p uint64) (run, error) {
	if p == 0 {
		return holeRun(1), nil
	}
	if p >= f.blocks {
		return run{}, fmt.Errorf("%w: data block %d is outside the file system", ErrCorrupt, p)
	}
	return run{phys: p, n: 1}, nil
}

// indirect follows pointers from the block cur, which is an indirect block
// of the given level (1 holds data pointers). rel is the block index inside
// the range that cur covers.
func (f *FS) indirect(cur uint64, level int, rel uint64) (run, error) {
	per := f.bs / 4
	span := func(l int) uint64 { // blocks that one pointer of a level l block covers
		s := uint64(1)
		for ; l > 1; l-- {
			s *= per
		}
		return s
	}
	for ; level >= 1; level-- {
		if cur == 0 {
			return holeRun(span(level+1) - rel), nil
		}
		if cur >= f.blocks {
			return run{}, fmt.Errorf("%w: indirect block %d is outside the file system", ErrCorrupt, cur)
		}
		s := span(level)
		idx := rel / s
		rel %= s
		var p [4]byte
		if err := f.readAt(p[:], cur*f.bs+idx*4); err != nil {
			return run{}, err
		}
		cur = uint64(le.Uint32(p[:]))
	}
	return f.dataBlock(cur)
}

// extentLookup maps lb through the extent tree in i_block. It checks each
// node that it reads: the magic, the counts, the depth (the root has the
// largest depth, and each level has one less), the order of the entries and
// the blocks. The depth rule and the cap of five levels stop any cycle.
func (f *FS) extentLookup(in *inode, lb uint64, budget *int) (run, error) {
	if lb >= 1<<32 {
		// Logical blocks of an extent tree are 32 bits wide.
		return holeRun(maxRun), nil
	}
	node := in.iblock()
	depth := -1
	limit := uint64(maxRun) // exclusive end of the logical range of this node
	for {
		if *budget <= 0 {
			return run{}, fmt.Errorf("%w: more than %d extent nodes visited", ErrLimit, maxNodeVisits)
		}
		*budget--
		if len(node) < 12 || le.Uint16(node[0:]) != 0xF30A {
			return run{}, fmt.Errorf("%w: inode %d: bad extent node magic", ErrCorrupt, in.Ino)
		}
		entries := int(le.Uint16(node[2:]))
		maxEntries := int(le.Uint16(node[4:]))
		nodeDepth := int(le.Uint16(node[6:]))
		if entries > maxEntries || maxEntries > (len(node)-12)/12 {
			return run{}, fmt.Errorf("%w: inode %d: extent node with %d of %d entries", ErrCorrupt, in.Ino, entries, maxEntries)
		}
		if depth < 0 && nodeDepth > maxExtentDepth || depth >= 0 && nodeDepth != depth-1 {
			return run{}, fmt.Errorf("%w: inode %d: extent node of depth %d below depth %d", ErrCorrupt, in.Ino, nodeDepth, depth)
		}
		depth = nodeDepth
		body := node[12:]
		if depth == 0 {
			return f.leafLookup(in, body[:entries*12], lb, limit)
		}
		if entries == 0 {
			return run{}, fmt.Errorf("%w: inode %d: empty extent index node", ErrCorrupt, in.Ino)
		}
		pick := -1
		next := limit
		var prev uint64
		for i := range entries {
			e := body[i*12:]
			blk := uint64(le.Uint32(e))
			if i > 0 && blk <= prev {
				return run{}, fmt.Errorf("%w: inode %d: extent index entries out of order", ErrCorrupt, in.Ino)
			}
			prev = blk
			if blk > lb {
				next = min(limit, blk)
				break
			}
			pick = i
		}
		if pick < 0 {
			return holeRun(next - lb), nil
		}
		e := body[pick*12:]
		leaf := uint64(le.Uint32(e[4:])) | uint64(le.Uint16(e[8:]))<<32
		var err error
		if node, err = f.readBlock(leaf); err != nil {
			return run{}, err
		}
		limit = next
	}
}

// leafLookup searches the extents of a leaf. limit is where the logical range
// of the leaf ends.
func (f *FS) leafLookup(in *inode, ext []byte, lb, limit uint64) (run, error) {
	var prevEnd uint64
	for i := 0; i < len(ext)/12; i++ {
		e := ext[i*12:]
		start := uint64(le.Uint32(e))
		n := uint64(le.Uint16(e[4:]))
		unwritten := false
		if n > 32768 {
			n -= 32768
			unwritten = true
		}
		phys := uint64(le.Uint16(e[6:]))<<32 | uint64(le.Uint32(e[8:]))
		switch {
		case n == 0:
			return run{}, fmt.Errorf("%w: inode %d: extent of length 0", ErrCorrupt, in.Ino)
		case i > 0 && start < prevEnd:
			return run{}, fmt.Errorf("%w: inode %d: extents overlap or are out of order", ErrCorrupt, in.Ino)
		case phys >= f.blocks || n > f.blocks-phys:
			return run{}, fmt.Errorf("%w: inode %d: extent at block %d is outside the file system", ErrCorrupt, in.Ino, phys)
		}
		if lb < start {
			return holeRun(min(start, limit) - lb), nil
		}
		end := start + n
		if lb < end {
			return run{phys: phys + (lb - start), n: min(end-lb, maxRun), unwritten: unwritten}, nil
		}
		prevEnd = end
	}
	return holeRun(limit - lb), nil
}
