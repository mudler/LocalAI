package chv

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func TestReadUpperEntryCap(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	_, r := testUpper(t)
	r.lim = readLimits{entries: 3}
	_, _, err := r.read(readCtx(t), nil)
	if err == nil || !strings.Contains(err.Error(), "more than 3 entries") {
		t.Fatalf("an entry cap of 3: %v", err)
	}
}

func TestReadUpperDepthCap(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	_, r := testUpper(t)
	r.lim = readLimits{depth: 1}
	_, _, err := r.read(readCtx(t), nil)
	if err == nil || !strings.Contains(err.Error(), "deeper than 1") {
		t.Fatalf("a depth cap of 1 with data/w/d: %v", err)
	}
}

func TestReadUpperOutputCap(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	_, r := testUpper(t)
	r.lim = readLimits{output: 16}
	_, _, err := r.read(readCtx(t), nil)
	if err == nil || !strings.Contains(err.Error(), "over 16 bytes") {
		t.Fatalf("an output cap of 16 bytes: %v", err)
	}
}

func TestReadLimitsDefaults(t *testing.T) {
	l := readLimits{}.withDefaults()
	if l.depth != 512 || l.entries != 1000000 || l.output != 512<<20 {
		t.Fatalf("defaults: %+v", l)
	}
	if got := (readLimits{depth: 3}).withDefaults(); got.depth != 3 || got.entries != 1000000 {
		t.Fatalf("a set field must stay: %+v", got)
	}
}

func TestCheckStderr(t *testing.T) {
	ea := []command{{text: "ea_get -x <13> trusted.overlay.opaque"}}
	missing := eaNotFound + " while getting extended attribute\n"
	banner := "debugfs 1.47.2 (1-Jan-2025)\n"
	for _, tc := range []struct {
		name   string
		stderr string
		cmds   []command
		ok     bool
	}{
		{"banner only", banner, nil, true},
		{"a missing key", banner + missing, ea, true},
		{"a corrupt attribute", "ea_get: Corrupt extended attribute block\n", ea, false},
		{"more misses than ea_get", missing + missing, ea, false},
		{"a miss without ea_get", missing, []command{{text: "ls -p <13>"}}, false},
		{"a failed dump", "dump: Cannot write file\n", nil, false},
	} {
		err := checkStderr([]byte(tc.stderr), tc.cmds)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestDebugfsDeadline(t *testing.T) {
	ctx, cancel := debugfsContext(context.Background())
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > debugfsTimeout || time.Until(dl) < debugfsTimeout-time.Minute {
		t.Fatalf("no default deadline: %v %v", dl, ok)
	}
	parent, pc := context.WithTimeout(context.Background(), time.Hour)
	defer pc()
	ctx2, c2 := debugfsContext(parent)
	defer c2()
	dl2, _ := ctx2.Deadline()
	pd, _ := parent.Deadline()
	if !dl2.Equal(pd) {
		t.Fatalf("a caller deadline was replaced: %v, want %v", dl2, pd)
	}
}

func TestSuperStatsBounds(t *testing.T) {
	body := "Block count:              8192\nBlock size:               1024\nInode count:              2048\nFirst inode:              11\nInode size: 256\n"
	b, rest, err := parseSuperStats([]byte(body))
	if err != nil || rest != nil || b != (fsBounds{firstIno: 11, inodes: 2048}) {
		t.Fatalf("bounds %+v, rest %q, err %v", b, rest, err)
	}
	for _, bad := range []string{
		"Inode count: 2048\n",
		"First inode: 11\n",
		"Inode count: 2048\nFirst inode: 1\n",
		"Inode count: 5\nFirst inode: 11\n",
		"Inode count: x\nFirst inode: 11\n",
	} {
		if _, _, err := parseSuperStats([]byte(bad)); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	for ino, want := range map[uint64]bool{1: false, 2: true, 10: false, 11: true, 2048: true, 2049: false} {
		if got := b.inodeInRange(ino); got != want {
			t.Errorf("inode %d in range = %v, want %v", ino, got, want)
		}
	}
}

// synthInode is a 256-byte inode with the fields that the parser reads.
func synthInode(extra uint16) []byte {
	raw := make([]byte, 256)
	le := binary.LittleEndian
	le.PutUint16(raw[0x00:], 0o100644)
	le.PutUint16(raw[0x80:], extra)
	le.PutUint32(raw[0x84:], 0x11111111) // ctime_extra
	le.PutUint32(raw[0x88:], 0x22222222) // mtime_extra
	le.PutUint32(raw[0x98:], 0x33333333) // i_version_hi
	return raw
}

func TestInodeExtraFieldsFollowExtraISize(t *testing.T) {
	for _, tc := range []struct {
		extra                   uint16
		ctime, mtime, versionHi bool
	}{
		{0x00, false, false, false},
		{0x08, true, false, false},
		{0x0C, true, true, false},
		{0x1C, true, true, true},
	} {
		in, _, err := parseInodeDump(dumpText(synthInode(tc.extra)))
		if err != nil {
			t.Fatal(err)
		}
		if got := in.CtimeExtra == 0x11111111; got != tc.ctime {
			t.Errorf("extra %#x: ctime_extra read %v, want %v", tc.extra, got, tc.ctime)
		}
		if got := in.MtimeExtra == 0x22222222; got != tc.mtime {
			t.Errorf("extra %#x: mtime_extra read %v, want %v", tc.extra, got, tc.mtime)
		}
		if got := in.Version>>32 == 0x33333333; got != tc.versionHi {
			t.Errorf("extra %#x: i_version_hi read %v, want %v", tc.extra, got, tc.versionHi)
		}
	}
}

func TestInlineXattrBad(t *testing.T) {
	le := binary.LittleEndian
	empty := synthInode(32) // the area after 128+32 is zero
	ok := synthInode(32)
	le.PutUint32(ok[0xA0:], xattrMagic)
	ok[0xA4] = 0x0e
	bad := synthInode(32)
	bad[0xA4] = 0x0e // entries without the magic
	for _, tc := range []struct {
		name string
		raw  []byte
		want bool
	}{
		{"an empty area", empty, false},
		{"entries with the magic", ok, false},
		{"entries without the magic", bad, true},
	} {
		if got := inlineXattrBad(tc.raw); got != tc.want {
			t.Errorf("%s: inlineXattrBad = %v, want %v", tc.name, got, tc.want)
		}
	}
}
