package chv

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The host reads the upper image with debugfs in catastrophic mode (-c:
// read-only, no bitmaps), one process per batch of commands. The output is
// parsed with strict grammars, because names in it come from the guest:
//
//   - "ls -p <ino>" prints one record per entry, "/ino/mode/uid/gid/name/size/"
//     and a newline, then an empty line. debugfs does not escape names. On
//     Linux a name never holds "/", but a guest that writes the image can put
//     "/" in a name. The record then ends early, and the rest of the name
//     reads as more records. The parser cannot tell a forged record from a
//     real one, so the reader (upper.go) checks what the parser returns: each
//     inode against the superblock range, names, duplicate paths and loops.
//   - "inode_dump <ino>" prints the raw inode as hex lines; the text column
//     never starts a line.
//   - "ea_get -x <ino> <key>" prints "key (n) = hex bytes" or nothing. A
//     missing key is a message on stderr, not an error exit.
//   - "dump <ino> <file>" prints nothing.
//   - "show_super_stats -h" prints the superblock as "Name: value" lines.
//
// Every command is echoed as "debugfs: <command>" before its output.
// Messages go to stderr, and debugfs exits 0 even when a command fails. The
// caller therefore checks the messages (checkStderr) and the output.

const (
	// debugfsTimeout bounds one debugfs process when the caller's context has
	// no deadline.
	debugfsTimeout = 10 * time.Minute
	// maxDebugfsStderr bounds the messages of one process. A batch prints a
	// few lines; more than this is a hostile image.
	maxDebugfsStderr = 1 << 20
	// eaNotFound is the message of ea_get for a key that is not there.
	eaNotFound = "ea_get: Extended attribute key not found"
	// xattrMagic starts an in-inode attribute area (EXT4_XATTR_MAGIC).
	xattrMagic = 0xEA020000
)

var debugfsBanner = regexp.MustCompile(`^debugfs 1\.\d+`)

// lsEntry is one record of "ls -p".
type lsEntry struct {
	Ino  uint64
	Mode uint32 // st_mode: type and permission bits
	Name string
	Size int64
}

// inode holds the fields of a raw ext4 inode that the reader uses.
type inode struct {
	Mode       uint16
	Size       int64
	Ctime      uint32
	Mtime      uint32
	CtimeExtra uint32
	MtimeExtra uint32
	Generation uint32
	Version    uint64
	Block0     uint32 // i_block[0]: old device number
	Block1     uint32 // i_block[1]: new device number
	// xattrBad is set when the in-inode attribute area holds entries but not
	// the magic. debugfs then reports the attributes as missing.
	xattrBad bool
}

// key changes whenever the content of the file may have changed.
func (n inode) key(ino uint64) string {
	return fmt.Sprintf("%d:%d:%d:%x.%x:%x.%x:%x", ino, n.Generation, n.Size, n.Ctime, n.CtimeExtra, n.Mtime, n.MtimeExtra, n.Version)
}

// isWhiteout reports a character device 0/0.
func (n inode) isWhiteout() bool {
	return n.Mode&0o170000 == 0o020000 && n.Block0 == 0 && n.Block1 == 0
}

// command is one debugfs command and the parser of its output.
type command struct {
	text  string
	parse func(body []byte) ([]byte, error) // returns the rest of the output
}

// fsBounds holds the inode numbers that the superblock allows.
type fsBounds struct {
	firstIno uint64 // s_first_ino
	inodes   uint64 // s_inodes_count
}

// inodeInRange reports whether ino can be an inode of this file system: the
// root (2), or an inode from first_ino up to the inode count.
func (b fsBounds) inodeInRange(ino uint64) bool {
	return ino == 2 || (ino >= b.firstIno && ino <= b.inodes)
}

// debugfsContext returns ctx with debugfsTimeout as deadline, unless ctx has
// its own deadline.
func debugfsContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, debugfsTimeout)
}

// capWriter keeps at most max bytes. When a write goes past the cap it stops
// the process through cancel, so a hostile image cannot grow the buffer.
type capWriter struct {
	max    int64
	n      int64
	buf    bytes.Buffer
	over   bool
	cancel context.CancelFunc
}

var errOverCap = errors.New("chv: debugfs output over the cap")

func (w *capWriter) Write(p []byte) (int, error) {
	if w.over {
		return 0, errOverCap
	}
	if w.n+int64(len(p)) > w.max {
		w.over = true
		w.cancel()
		return 0, errOverCap
	}
	w.n += int64(len(p))
	return w.buf.Write(p)
}

// runBatch runs the commands in one debugfs process and parses the output.
// The process runs under ctx (with debugfsTimeout when ctx has no deadline),
// and its output is capped at maxOut bytes.
func runBatch(ctx context.Context, argv []string, cmds []command, maxOut int64) error {
	if len(cmds) == 0 {
		return nil
	}
	var in bytes.Buffer
	for _, c := range cmds {
		if strings.ContainsAny(c.text, "\n") {
			return fmt.Errorf("chv: bad debugfs command %q", c.text)
		}
		in.WriteString(c.text + "\n")
	}
	ctx, cancel := debugfsContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = &in
	out := &capWriter{max: maxOut, cancel: cancel}
	errw := &capWriter{max: maxDebugfsStderr, cancel: cancel}
	cmd.Stdout, cmd.Stderr = out, errw
	err := cmd.Run()
	switch {
	case out.over:
		return fmt.Errorf("chv: debugfs output is over %d bytes", maxOut)
	case errw.over:
		return fmt.Errorf("chv: debugfs printed more than %d bytes of messages", maxDebugfsStderr)
	case ctx.Err() != nil:
		return fmt.Errorf("chv: debugfs: %w", ctx.Err())
	case err != nil:
		return fmt.Errorf("chv: debugfs: %v: %s", err, lastLines(errw.buf.Bytes(), 3))
	}
	if err := checkStderr(errw.buf.Bytes(), cmds); err != nil {
		return err
	}
	if err := parseBatch(out.buf.Bytes(), cmds); err != nil {
		return fmt.Errorf("%w (debugfs said: %s)", err, lastLines(errw.buf.Bytes(), 3))
	}
	return nil
}

// checkStderr accepts the banner, and the not-found message of ea_get, which
// is the only message that a missing attribute gives. Any other message fails
// the batch: debugfs reports a corrupt attribute or block that way. The
// messages do not name their command, so the number of not-found messages
// may not exceed the number of ea_get commands in the batch.
func checkStderr(stderr []byte, cmds []command) error {
	eaCmds := 0
	for _, c := range cmds {
		if strings.HasPrefix(c.text, "ea_get ") {
			eaCmds++
		}
	}
	missing := 0
	for _, line := range strings.Split(string(stderr), "\n") {
		switch {
		case line == "" || debugfsBanner.MatchString(line):
		case strings.HasPrefix(line, eaNotFound) && missing < eaCmds:
			missing++
		default:
			return fmt.Errorf("chv: debugfs said: %s", line)
		}
	}
	return nil
}

// parseBatch checks the echo of each command and parses its output.
func parseBatch(out []byte, cmds []command) error {
	var err error
	rest := out
	for _, c := range cmds {
		hdr := "debugfs: " + c.text + "\n"
		if !bytes.HasPrefix(rest, []byte(hdr)) {
			return fmt.Errorf("chv: debugfs output out of step at %q", c.text)
		}
		if rest, err = c.parse(rest[len(hdr):]); err != nil {
			return fmt.Errorf("chv: debugfs %q: %w", c.text, err)
		}
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("chv: debugfs printed more than expected")
	}
	return nil
}

func lastLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "; ")
}

// parseSuperStats reads "show_super_stats -h" for the inode count and the
// first inode. It takes the rest of the output.
func parseSuperStats(body []byte) (fsBounds, []byte, error) {
	var b fsBounds
	var haveFirst, haveCount bool
	for _, line := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		if key != "First inode" && key != "Inode count" {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
		if err != nil {
			return fsBounds{}, nil, fmt.Errorf("superblock %s %q", key, v)
		}
		if key == "First inode" {
			b.firstIno, haveFirst = n, true
		} else {
			b.inodes, haveCount = n, true
		}
	}
	if !haveFirst || !haveCount {
		return fsBounds{}, nil, errors.New("superblock has no inode count or first inode")
	}
	if b.firstIno < 3 || b.inodes < b.firstIno {
		return fsBounds{}, nil, fmt.Errorf("superblock: first inode %d, %d inodes", b.firstIno, b.inodes)
	}
	return b, nil, nil
}

// parseLS reads the records of "ls -p" and the empty line after them.
func parseLS(b []byte) ([]lsEntry, []byte, error) {
	var out []lsEntry
	for len(b) > 0 && b[0] == '/' {
		var f [6][]byte
		b = b[1:]
		for i := range f {
			j := bytes.IndexByte(b, '/')
			if j < 0 {
				return nil, nil, errors.New("truncated ls record")
			}
			f[i], b = b[:j], b[j+1:]
		}
		if len(b) == 0 || b[0] != '\n' {
			return nil, nil, errors.New("ls record without an end")
		}
		b = b[1:]
		ino, err := strconv.ParseUint(string(f[0]), 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("ls inode %q", f[0])
		}
		mode, err := strconv.ParseUint(string(f[1]), 8, 32)
		if err != nil {
			return nil, nil, fmt.Errorf("ls mode %q", f[1])
		}
		var size int64
		if len(f[5]) > 0 {
			if size, err = strconv.ParseInt(string(f[5]), 10, 64); err != nil {
				return nil, nil, fmt.Errorf("ls size %q", f[5])
			}
		}
		out = append(out, lsEntry{Ino: ino, Mode: uint32(mode), Name: string(f[4]), Size: size})
	}
	if len(b) == 0 || b[0] != '\n' {
		return nil, nil, errors.New("ls output without the empty line")
	}
	return out, b[1:], nil
}

// parseInodeDump reads the hex lines of "inode_dump" and the empty line
// after them. A line is "%04o" offset, two spaces, 16 bytes as 8 groups of 4
// hex digits each followed by a space, a space and the text column. A "*"
// line stands for repeats of the line before it.
func parseInodeDump(b []byte) (inode, []byte, error) {
	// maxInodeDump bounds the bytes of one dump: an inode is at most 4 KiB.
	const maxInodeDump = 4096
	raw := make([]byte, 0, 256)
	var last []byte
	repeat := false
	for {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return inode{}, nil, errors.New("truncated inode dump")
		}
		line := b[:i]
		b = b[i+1:]
		if len(line) == 0 {
			break
		}
		if string(line) == "*" {
			// A repeat stands for the line before it. Without one the
			// fill loops below would never advance.
			if last == nil {
				return inode{}, nil, errors.New("inode dump repeat line before a data line")
			}
			repeat = true
			continue
		}
		if len(line) < 46 {
			return inode{}, nil, fmt.Errorf("short inode dump line %q", line)
		}
		off, err := strconv.ParseUint(string(line[:4]), 8, 32)
		if err != nil {
			return inode{}, nil, fmt.Errorf("inode dump offset %q", line[:4])
		}
		if off >= maxInodeDump {
			return inode{}, nil, fmt.Errorf("inode dump offset %d is over %d bytes", off, maxInodeDump)
		}
		if repeat {
			for uint64(len(raw)) < off {
				raw = append(raw, last...)
			}
			repeat = false
		}
		if uint64(len(raw)) != off {
			return inode{}, nil, fmt.Errorf("inode dump offset %d after %d bytes", off, len(raw))
		}
		last, err = hex.DecodeString(strings.ReplaceAll(string(line[6:46]), " ", ""))
		if err != nil || len(last) != 16 {
			return inode{}, nil, fmt.Errorf("inode dump bytes %q", line[6:46])
		}
		raw = append(raw, last...)
		if len(raw) > maxInodeDump {
			return inode{}, nil, fmt.Errorf("inode dump is over %d bytes", maxInodeDump)
		}
	}
	if repeat {
		for len(raw) < 256 {
			raw = append(raw, last...)
		}
	}
	if len(raw) < 128 {
		return inode{}, nil, fmt.Errorf("inode dump of %d bytes", len(raw))
	}
	le := binary.LittleEndian
	n := inode{
		Mode:       le.Uint16(raw[0x00:]),
		Size:       int64(le.Uint32(raw[0x04:])) | int64(le.Uint32(raw[0x6C:]))<<32,
		Ctime:      le.Uint32(raw[0x0C:]),
		Mtime:      le.Uint32(raw[0x10:]),
		Version:    uint64(le.Uint32(raw[0x24:])),
		Block0:     le.Uint32(raw[0x28:]),
		Block1:     le.Uint32(raw[0x2C:]),
		Generation: le.Uint32(raw[0x64:]),
		xattrBad:   inlineXattrBad(raw),
	}
	// i_extra_isize says how many bytes after the 128-byte base exist. Each
	// extra field is read only when that count reaches the end of the field:
	// ctime_extra ends at 0x88, mtime_extra at 0x8C, i_version_hi at 0x9C.
	extra := iExtraISize(raw)
	if len(raw) >= 0x88 && extra >= 0x08 {
		n.CtimeExtra = le.Uint32(raw[0x84:])
	}
	if len(raw) >= 0x8C && extra >= 0x0C {
		n.MtimeExtra = le.Uint32(raw[0x88:])
	}
	if len(raw) >= 0x9C && extra >= 0x1C {
		n.Version |= uint64(le.Uint32(raw[0x98:])) << 32
	}
	return n, b, nil
}

// iExtraISize returns i_extra_isize, or 0 when the dump is too short to hold
// it. A 128-byte inode (mke2fs -I 128) has no field at 0x80, so the dump of
// such an inode has no extra fields and no in-inode attribute area.
func iExtraISize(raw []byte) int {
	if len(raw) < 0x82 {
		return 0
	}
	return int(binary.LittleEndian.Uint16(raw[0x80:]))
}

// inlineXattrBad reports an in-inode attribute area that holds bytes but not
// the ext4 magic. The area starts after i_extra_isize. A zeroed magic is how
// a damaged image hides an attribute from debugfs, while the kernel reads the
// entries that follow it.
func inlineXattrBad(raw []byte) bool {
	start := 128 + iExtraISize(raw)
	if start >= len(raw) {
		return false
	}
	area := raw[start:]
	if bytes.Count(area, []byte{0}) == len(area) {
		return false
	}
	if len(area) < 4 {
		return true
	}
	return binary.LittleEndian.Uint32(area) != xattrMagic
}

// parseEAGet reads the output of "ea_get -x <ino> <key>": nothing when the
// attribute is missing, or "key (n) = hex " and an empty line. A missing key
// is reported on stderr, which checkStderr accepts.
func parseEAGet(key string) func([]byte) ([]byte, []byte, error) {
	return func(b []byte) ([]byte, []byte, error) {
		prefix := key + " ("
		if !bytes.HasPrefix(b, []byte(prefix)) {
			return nil, b, nil
		}
		i := bytes.IndexByte(b, '\n')
		if i < 0 || len(b) < i+2 || b[i+1] != '\n' {
			return nil, nil, errors.New("truncated ea_get output")
		}
		line := string(b[:i])
		_, hexPart, ok := strings.Cut(line, " = ")
		if !ok {
			return nil, nil, fmt.Errorf("ea_get line %q", line)
		}
		v, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(hexPart), " ", ""))
		if err != nil {
			return nil, nil, fmt.Errorf("ea_get value %q", hexPart)
		}
		return v, b[i+2:], nil
	}
}
