package files

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zip"
	"github.com/klauspost/compress/zstd"
	"github.com/mholt/archives"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

// Extract opens an archive of the user's home and writes what it holds into a
// folder of the same home, as one change set, so that one Undo takes it all
// back.
//
// Every limit is counted on the bytes that are really read, never on a size
// that an archive declares. Nothing is written when a limit stops the read: the
// files are staged first and the change set is made only after the whole
// archive has been read. Nested archives are written as plain files. A name
// that is not valid UTF-8 is skipped, not repaired, because two different bad
// names could repair to the same name.

// Errors of Extract. The text after the sentinel is written for the person.
var (
	// ErrArchiveLimit means the archive expands past a limit.
	ErrArchiveLimit = errors.New("archive too big")
	// ErrArchiveBad means the archive is damaged, or it holds a name that
	// would leave the destination folder.
	ErrArchiveBad = errors.New("archive not usable")
	// ErrArchiveUnsupported means LocalAI does not extract this kind of file.
	ErrArchiveUnsupported = errors.New("archive not supported")
	// ErrArchiveTimeout means the extraction took longer than the deadline.
	ErrArchiveTimeout = errors.New("archive took too long")
)

// Fixed limits of Extract. The total bytes and the entry count are settings.
const (
	// ExtractTimeout is the wall-clock deadline of one extraction.
	ExtractTimeout = 10 * time.Minute
	// ExtractMaxFileBytes is the largest single file, the same as the largest
	// file the workspace stores.
	ExtractMaxFileBytes int64 = 256 << 20

	extractMaxDepth = 64
	// A ratio check starts when an archive has expanded past the grace size.
	// Past it, an archive may not expand to more than ratioLimit bytes for
	// each byte it read.
	extractRatioGrace = 64 << 20
	extractRatioLimit = 1000
	// extractZstdWindow is the largest zstd window the reader may allocate.
	extractZstdWindow = 64 << 20
	// extractReportMax is how many skipped entries and conflicts a result lists.
	extractReportMax = 200
)

// ExtractLimits are the limits of one extraction. Zero values use the defaults
// of the settings.
type ExtractLimits struct {
	MaxBytes     int64         // total bytes of all files
	MaxEntries   int           // entries of the archive, files and folders
	MaxFileBytes int64         // bytes of one file
	Timeout      time.Duration // wall-clock deadline
	// SevenZipRar allows 7z and RAR. Their readers run in this process and
	// cannot limit their own memory, so they are off unless an administrator
	// turns them on.
	SevenZipRar bool
}

func (l ExtractLimits) withDefaults() ExtractLimits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = 1 << 30
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = 20000
	}
	if l.MaxFileBytes <= 0 || l.MaxFileBytes > ExtractMaxFileBytes {
		l.MaxFileBytes = ExtractMaxFileBytes
	}
	if l.Timeout <= 0 {
		l.Timeout = ExtractTimeout
	}
	return l
}

// Reasons for a skipped entry.
const (
	SkipSymlink    = "symlink"
	SkipHardLink   = "hard_link"
	SkipSpecial    = "special" // device, socket, pipe or another kind
	SkipBadName    = "bad_name"
	SkipNotUTF8    = "not_utf8"
	SkipTooDeep    = "too_deep"
	SkipTooLarge   = "too_large"
	SkipDuplicate  = "duplicate"
	SkipEncrypted  = "encrypted"
	SkipManaged    = "managed" // a folder that LocalAI manages
	SkipUnreadable = "unreadable"
)

// ExtractSkip is an entry that was not extracted.
type ExtractSkip struct {
	Path   string // the name in the archive
	Reason string
}

// ExtractConflict is a file whose name was taken. Kept is where the new
// content went, or empty when it was dropped.
type ExtractConflict struct {
	Path   string
	Reason string
	Kept   string
}

// ExtractResult says what Extract did.
type ExtractResult struct {
	// Dest is the folder that holds the files, inside the home.
	Dest string
	// Rev is the revision of the change set, which Undo takes.
	Rev int64
	// Files and Folders count what was offered to the store and kept; Bytes
	// are the bytes of those files.
	Files   int
	Folders int
	Bytes   int64
	// Skipped lists entries that were not extracted (at most 200), and
	// SkippedCount counts all of them.
	Skipped      []ExtractSkip
	SkippedCount int
	// Conflicts lists names that were taken (at most 200). The existing file
	// stays; the new content is kept next to it under a copy name.
	Conflicts []ExtractConflict
}

// ArchiveExtensions are the file extensions that Extract may open, longest
// first. The Files page offers the action for these names.
var ArchiveExtensions = []string{
	".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tar.lz4", ".tar.br", ".tar.sz", ".tar.z",
	".tgz", ".tbz2", ".tbz", ".txz", ".tzst",
	".zip", ".tar", ".7z", ".rar",
}

// ExtractFolderName is the name of the folder for an archive: the file name
// without its archive extension.
func ExtractFolderName(archive string) string {
	lower := strings.ToLower(archive)
	for _, e := range ArchiveExtensions {
		if strings.HasSuffix(lower, e) && len(archive) > len(e) {
			return archive[:len(archive)-len(e)]
		}
	}
	if dot := strings.LastIndexByte(archive, '.'); dot > 0 {
		return archive[:dot]
	}
	return archive
}

// Extract extracts the archive at archiveRel (a path inside the user's home)
// into dest, a folder inside the home. An empty dest means a new folder next
// to the archive, named like the archive without its extension; when that name
// is taken the folder is "name 2", "name 3" and so on.
func (s *Service) Extract(ctx context.Context, principal, archiveRel, destRel string, lim ExtractLimits) (res ExtractResult, err error) {
	if !s.Available() {
		return res, fmt.Errorf("%w: %s", ErrUnavailable, s.Unavailable())
	}
	lim = lim.withDefaults()
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()
	defer func() {
		// A deadline of our own is a message; a client that went away stays
		// a plain cancellation.
		if err != nil && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("%w: the extraction ran past %s and was stopped; nothing was written", ErrArchiveTimeout, lim.Timeout)
		}
	}()

	actor := s.Actor(principal)
	home := session.HomePath(principal)
	abs, err := s.Abs(principal, archiveRel)
	if err != nil {
		return res, err
	}
	r, node, err := s.ws.Open(ctx, actor, abs)
	if err != nil {
		return res, err
	}
	defer func() { _ = r.Close() }()
	if node.Kind != workspace.KindFile {
		return res, workspace.ErrInvalid
	}

	src := newMeteredSource(r)
	format, _, err := archives.Identify(ctx, node.Name, r)
	if err != nil {
		if errors.Is(err, archives.NoMatch) {
			return res, fmt.Errorf("%w: this file is not an archive that LocalAI knows", ErrArchiveUnsupported)
		}
		return res, fmt.Errorf("%w: the file could not be read as an archive", ErrArchiveBad)
	}
	plan, err := planFor(format, lim)
	if err != nil {
		return res, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return res, err
	}

	destAbs, err := s.extractDest(ctx, principal, actor, abs, node.Name, destRel)
	if err != nil {
		return res, err
	}
	destInHome := s.Rel(principal, destAbs)

	g := &extractGuard{lim: lim, src: src}
	if plan.zip {
		if err := zipEntryCheck(src, node.Size, lim.MaxEntries); err != nil {
			return res, err
		}
	}
	us := &extractSource{
		ctx: ctx, g: g, plan: plan, prefix: destPrefix(principal, destAbs, home), destRel: destInHome,
		src: src, size: node.Size,
	}

	view, err := s.ws.View(ctx, actor, []workspace.MountRequest{{Path: home, At: extractMount, Mode: workspace.ModeWrite}})
	if err != nil {
		return res, err
	}
	// Base 0: every node that exists has a change after revision 0, so a file
	// in the way is never replaced; the store keeps the new content as a copy
	// next to it. A base at the latest revision would overwrite it.
	cs, conflicts, err := s.ws.CommitUpper(ctx, view, actor, 0, us, workspace.UpperOptions{Title: "Extract " + node.Name})
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return res, mapExtractErr(err)
	}
	res = us.result()
	res.Dest = destInHome
	res.Rev = cs.Rev
	for _, c := range conflicts {
		kept := ""
		if c.Kept != "" {
			kept = s.Rel(principal, c.Kept)
		}
		if kept == "" {
			res.Files-- // dropped, not stored
		}
		if len(res.Conflicts) < extractReportMax {
			res.Conflicts = append(res.Conflicts, ExtractConflict{Path: s.Rel(principal, c.Path), Reason: c.Reason, Kept: kept})
		}
	}
	if res.Files < 0 {
		res.Files = 0
	}
	return res, nil
}

// extractMount is the name of the one mount of an extraction view.
const extractMount = "h"

// destPrefix is the path of the destination folder below the mount point.
func destPrefix(principal, destAbs, home string) string {
	rel := strings.TrimPrefix(destAbs, home)
	return extractMount + rel
}

// mapExtractErr turns the errors of the guards into the sentinels.
func mapExtractErr(err error) error {
	switch {
	case errors.Is(err, ErrArchiveLimit), errors.Is(err, ErrArchiveBad), errors.Is(err, ErrArchiveUnsupported), errors.Is(err, ErrArchiveTimeout):
		return err
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, workspace.ErrTooLarge), errors.Is(err, workspace.ErrInvalid), errors.Is(err, workspace.ErrBadName):
		return err
	}
	return fmt.Errorf("%w: the archive is damaged or uses a feature that is not supported (%v)", ErrArchiveBad, innermost(err))
}

// innermost returns the shortest message in the chain of err, which is the
// reader's own words without the wrapping of the walk.
func innermost(err error) error {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
}

// extractDest finds the folder to write into and checks it. A destination the
// user gave is used as it is; a folder that does not exist is created. The
// default is a new folder next to the archive.
func (s *Service) extractDest(ctx context.Context, principal string, actor workspace.Actor, archiveAbs, name, destRel string) (string, error) {
	if strings.TrimSpace(destRel) != "" {
		dest, err := s.Abs(principal, destRel)
		if err != nil {
			return "", err
		}
		rel := s.Rel(principal, dest)
		if rel != "/" && workfiles.Managed(rel) {
			return "", fmt.Errorf("%w: LocalAI manages that folder, so nothing can be extracted into it", ErrArchiveBad)
		}
		n, err := s.ws.Stat(ctx, actor, dest)
		switch {
		case err == nil && n.Kind != workspace.KindDir:
			return "", workspace.ErrExists
		case err != nil && !errors.Is(err, workspace.ErrNotFound):
			return "", err
		}
		return dest, nil
	}
	dir := archiveAbs[:strings.LastIndexByte(archiveAbs, '/')]
	kids, err := s.ws.List(ctx, actor, dir)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, k := range kids {
		taken[workspace.FoldName(k.Name)] = true
	}
	base := ExtractFolderName(name)
	if _, err := workspace.CleanName(base); err != nil {
		base = "extracted"
	}
	cand := base
	for i := 2; taken[workspace.FoldName(cand)]; i++ {
		cand = fmt.Sprintf("%s %d", base, i)
	}
	if _, err := workspace.CleanName(cand); err != nil {
		return "", workspace.ErrBadName
	}
	dest := workspace.JoinChild(dir, cand)
	if rel := s.Rel(principal, dest); workfiles.Managed(rel) {
		return "", fmt.Errorf("%w: LocalAI manages that folder, so nothing can be extracted into it", ErrArchiveBad)
	}
	return dest, nil
}

// plan says how to read one format.
type plan struct {
	zip         bool
	compression archives.Decompressor // nil: the archive is not compressed on top
	extractor   archives.Extractor
	stream      bool // tar family: the bytes of the decompressed stream are metered
}

// planFor checks that LocalAI extracts the format and builds the reader. Only
// the compressions that were reviewed for memory are accepted: the zstd window
// is capped, and xz has its own cap of 64 MiB.
func planFor(f archives.Format, lim ExtractLimits) (plan, error) {
	var ca archives.CompressedArchive
	switch v := f.(type) {
	case archives.CompressedArchive:
		ca = v
	case archives.Zip, archives.Tar, archives.SevenZip, archives.Rar:
		ca.Extraction = v.(archives.Extraction)
	default:
		return plan{}, fmt.Errorf("%w: this is a single compressed file, not an archive of files", ErrArchiveUnsupported)
	}
	if ca.Extraction == nil {
		return plan{}, fmt.Errorf("%w: this is a single compressed file, not an archive of files", ErrArchiveUnsupported)
	}
	p := plan{extractor: ca.Extraction}
	switch ca.Extraction.(type) {
	case archives.Zip:
		p.zip = true
	case archives.SevenZip:
		if !lim.SevenZipRar {
			return plan{}, fmt.Errorf("%w: 7z archives are off on this server. An administrator can turn them on in Settings, or you can open the file in a chat sandbox", ErrArchiveUnsupported)
		}
	case archives.Rar:
		if !lim.SevenZipRar {
			return plan{}, fmt.Errorf("%w: RAR archives are off on this server. An administrator can turn them on in Settings, or you can open the file in a chat sandbox", ErrArchiveUnsupported)
		}
	case archives.Tar:
		p.stream = true
	}
	switch c := ca.Compression.(type) {
	case nil:
	case archives.Zstd:
		c.DecoderOptions = []zstd.DOption{
			zstd.WithDecoderMaxWindow(extractZstdWindow),
			zstd.WithDecoderMaxMemory(2 * extractZstdWindow),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
		}
		p.compression = c
	case archives.Gz, archives.Bz2, archives.Xz, archives.Brotli, archives.Lz4, archives.Sz, archives.Zlib:
		p.compression = ca.Compression
	default:
		return plan{}, fmt.Errorf("%w: this compression is not supported", ErrArchiveUnsupported)
	}
	if p.compression != nil && !p.stream {
		return plan{}, fmt.Errorf("%w: this combination of compression and archive is not supported", ErrArchiveUnsupported)
	}
	return p, nil
}

// meteredSource counts the bytes read from the stored archive and serves the
// random reads that ZIP and 7z need. It is the compressed side of the ratio.
type meteredSource struct {
	r  io.ReadSeeker
	at io.ReaderAt // the reader itself when it can read at an offset
	mu sync.Mutex
	n  atomic.Int64
}

func newMeteredSource(r io.ReadSeeker) *meteredSource {
	m := &meteredSource{r: r}
	if at, ok := r.(io.ReaderAt); ok {
		m.at = at
	}
	return m
}

func (m *meteredSource) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, err := m.r.Read(p)
	m.n.Add(int64(n))
	return n, err
}

func (m *meteredSource) Seek(off int64, whence int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.r.Seek(off, whence)
}

func (m *meteredSource) ReadAt(p []byte, off int64) (int, error) {
	if m.at != nil {
		n, err := m.at.ReadAt(p, off)
		m.n.Add(int64(n))
		return n, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, err := m.r.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	defer func() { _, _ = m.r.Seek(cur, io.SeekStart) }()
	if _, err := m.r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(m.r, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	m.n.Add(int64(n))
	return n, err
}

// zipEntryCheck reads the entry count from the end of a ZIP file and refuses
// the archive when it is over the limit. The ZIP reader loads the whole central
// directory into memory before it returns, so the count must be known before.
func zipEntryCheck(at io.ReaderAt, size int64, max int) error {
	n, err := zipEntryCount(at, size)
	if err != nil {
		return fmt.Errorf("%w: this is not a valid ZIP file", ErrArchiveBad)
	}
	if n > uint64(max) {
		return fmt.Errorf("%w: the archive has %d entries and the limit is %d", ErrArchiveLimit, n, max)
	}
	return nil
}

// zipEntryCount reads the "total number of entries" field of the end record,
// or of the ZIP64 end record when the file has one.
func zipEntryCount(at io.ReaderAt, size int64) (uint64, error) {
	const (
		eocdLen  = 22
		eocdSig  = 0x06054b50
		loc64Len = 20
		loc64Sig = 0x07064b50
		rec64Sig = 0x06064b50
	)
	tail := int64(eocdLen + 0xFFFF)
	if size < eocdLen {
		return 0, io.ErrUnexpectedEOF
	}
	if tail > size {
		tail = size
	}
	buf := make([]byte, tail)
	if _, err := at.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	le16 := func(b []byte) uint64 { return uint64(b[0]) | uint64(b[1])<<8 }
	le32 := func(b []byte) uint64 { return le16(b) | le16(b[2:])<<16 }
	le64 := func(b []byte) uint64 { return le32(b) | le32(b[4:])<<32 }
	for i := len(buf) - eocdLen; i >= 0; i-- {
		if le32(buf[i:]) != eocdSig {
			continue
		}
		total := le16(buf[i+10:])
		if total != 0xFFFF {
			return total, nil
		}
		// ZIP64: the locator stands just before the end record.
		if i >= loc64Len && le32(buf[i-loc64Len:]) == loc64Sig {
			off := int64(le64(buf[i-loc64Len+8:]))
			rec := make([]byte, 56)
			if off < 0 || off > size-int64(len(rec)) {
				return 0, io.ErrUnexpectedEOF
			}
			if _, err := at.ReadAt(rec, off); err != nil && !errors.Is(err, io.EOF) {
				return 0, err
			}
			if le32(rec) != rec64Sig {
				return 0, io.ErrUnexpectedEOF
			}
			return le64(rec[32:]), nil
		}
		return total, nil
	}
	return 0, io.ErrUnexpectedEOF
}

// extractGuard holds the counters of one extraction and checks them.
type extractGuard struct {
	lim    ExtractLimits
	src    *meteredSource
	files  atomic.Int64 // bytes of file content read
	stream atomic.Int64 // bytes of the decompressed tar stream
}

// check is called on every read. It stops the read when the context ends, the
// bytes pass the limit or the archive expands too fast.
func (g *extractGuard) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	out := max(g.files.Load(), g.stream.Load())
	// The tar stream holds headers and padding besides the files, so its
	// limit has room for them.
	if g.files.Load() > g.lim.MaxBytes || g.stream.Load() > g.lim.MaxBytes+int64(g.lim.MaxEntries)*2048 {
		return fmt.Errorf("%w: the archive expands to more than %s", ErrArchiveLimit, humanBytes(g.lim.MaxBytes))
	}
	if out > extractRatioGrace {
		if in := max(g.src.n.Load(), 1); out/in > extractRatioLimit {
			return fmt.Errorf("%w: the archive expands more than %d times its size, which is how a decompression bomb looks", ErrArchiveLimit, extractRatioLimit)
		}
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// guardedReader counts and checks what is read through it.
type guardedReader struct {
	r   io.Reader
	ctx context.Context
	g   *extractGuard
	ctr *atomic.Int64
}

func (r guardedReader) Read(p []byte) (int, error) {
	if err := r.g.check(r.ctx); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	r.ctr.Add(int64(n))
	if cerr := r.g.check(r.ctx); cerr != nil {
		return n, cerr
	}
	return n, err
}

// onceCloser closes an entry once, whoever closes it first.
type onceCloser struct {
	io.Reader
	closer io.Closer
	once   sync.Once
}

func (o *onceCloser) Close() (err error) {
	o.once.Do(func() { err = o.closer.Close() })
	return err
}

// extractSource is the upper layer of an extraction: the entries of the
// archive below the destination folder.
type extractSource struct {
	ctx     context.Context
	g       *extractGuard
	plan    plan
	prefix  string // destination below the mount point
	destRel string
	src     *meteredSource
	size    int64

	used atomic.Bool

	mu      sync.Mutex
	files   int
	folders int
	bytes   int64
	skipped []ExtractSkip
	nSkip   int
	seen    map[string]bool
}

func (e *extractSource) result() ExtractResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return ExtractResult{Files: e.files, Folders: e.folders, Bytes: e.bytes, Skipped: e.skipped, SkippedCount: e.nSkip}
}

func (e *extractSource) skip(name, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nSkip++
	if len(e.skipped) < extractReportMax {
		e.skipped = append(e.skipped, ExtractSkip{Path: strings.ToValidUTF8(name, "�"), Reason: reason})
	}
}

// Walk reads the archive once and calls fn for each entry.
func (e *extractSource) Walk(ctx context.Context, fn func(workspace.UpperEntry) error) error {
	if !e.used.CompareAndSwap(false, true) {
		return errors.New("files: an archive can be read once")
	}
	e.seen = map[string]bool{}
	if e.destRel != "/" {
		if err := fn(workspace.UpperEntry{Path: e.prefix, Kind: workspace.EntryDir, Mode: fs.ModeDir | 0o755}); err != nil {
			return err
		}
	}
	var in io.Reader = e.src
	if e.plan.compression != nil {
		rc, err := e.plan.compression.OpenReader(e.src)
		if err != nil {
			return fmt.Errorf("%w: the compressed data could not be opened (%v)", ErrArchiveBad, innermost(err))
		}
		defer func() { _ = rc.Close() }()
		in = rc
	}
	if e.plan.stream {
		in = guardedReader{r: in, ctx: ctx, g: e.g, ctr: &e.g.stream}
	}
	count := 0
	return e.plan.extractor.Extract(ctx, in, func(ctx context.Context, fi archives.FileInfo) error {
		if err := e.g.check(ctx); err != nil {
			return err
		}
		count++
		if count > e.g.lim.MaxEntries {
			return fmt.Errorf("%w: the archive has more than %d entries", ErrArchiveLimit, e.g.lim.MaxEntries)
		}
		return e.entry(ctx, fi, fn)
	})
}

// entry maps one archive entry to an upper entry, or skips it.
func (e *extractSource) entry(ctx context.Context, fi archives.FileInfo, fn func(workspace.UpperEntry) error) error {
	name := fi.NameInArchive
	segs, bad, err := cleanEntryName(name)
	if err != nil {
		return err
	}
	if bad != "" {
		e.skip(name, bad)
		return nil
	}
	if len(segs) == 0 {
		return nil // the archive's own root, "./"
	}
	if kind, reason := entryKind(fi); reason != "" {
		e.skip(name, reason)
		return nil
	} else if kind == workspace.EntryDir {
		return e.dir(segs, fn)
	}
	return e.file(ctx, fi, name, segs, fn)
}

func (e *extractSource) dir(segs []string, fn func(workspace.UpperEntry) error) error {
	key := strings.Join(segs, "/")
	if e.seen[key+"/"] {
		return nil // a folder listed twice is the same folder
	}
	e.seen[key+"/"] = true
	if workfiles.Managed(e.joinRel(segs)) {
		e.skip(key, SkipManaged)
		return nil
	}
	e.mu.Lock()
	e.folders++
	e.mu.Unlock()
	return fn(workspace.UpperEntry{Path: e.prefix + "/" + key, Kind: workspace.EntryDir, Mode: fs.ModeDir | 0o755})
}

func (e *extractSource) file(ctx context.Context, fi archives.FileInfo, name string, segs []string, fn func(workspace.UpperEntry) error) error {
	key := strings.Join(segs, "/")
	if e.seen[key] {
		e.skip(name, SkipDuplicate)
		return nil
	}
	if workfiles.Managed(e.joinRel(segs)) {
		e.skip(name, SkipManaged)
		return nil
	}
	if fi.Size() > e.g.lim.MaxFileBytes {
		e.skip(name, SkipTooLarge)
		return nil
	}
	if h, ok := fi.Header.(zip.FileHeader); ok && h.Flags&0x1 != 0 {
		e.skip(name, SkipEncrypted)
		return nil
	}
	f, err := fi.Open()
	if err != nil {
		e.skip(name, SkipUnreadable)
		return nil
	}
	e.seen[key] = true
	var counted atomic.Int64
	var rd io.Reader = guardedReader{r: io.LimitReader(f, e.g.lim.MaxFileBytes+1), ctx: ctx, g: e.g, ctr: &e.g.files}
	rd = countingReader{r: rd, n: &counted}
	rc := &onceCloser{Reader: rd, closer: f}
	defer func() { _ = rc.Close() }()
	err = fn(workspace.UpperEntry{
		Path: e.prefix + "/" + key, Kind: workspace.EntryFile, Mode: 0o644,
		Open: func() (io.ReadCloser, error) { return rc, nil },
	})
	if err != nil {
		return err
	}
	if n := counted.Load(); n > e.g.lim.MaxFileBytes {
		e.skip(name, SkipTooLarge) // the store kept no blob of it
	} else {
		e.mu.Lock()
		e.files++
		e.bytes += n
		e.mu.Unlock()
	}
	return nil
}

// joinRel is the path of an entry inside the user's home.
func (e *extractSource) joinRel(segs []string) string {
	base := e.destRel
	if base == "/" {
		base = ""
	}
	return base + "/" + strings.Join(segs, "/")
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// cleanEntryName turns a name in an archive into clean segments. It returns an
// error for a name that tries to leave the destination (absolute, a ".."
// segment or a NUL), which refuses the whole archive, and a skip reason for
// a name that only the store would refuse.
func cleanEntryName(name string) (segs []string, skip string, err error) {
	if strings.ContainsRune(name, 0) {
		return nil, "", fmt.Errorf("%w: an entry has a NUL byte in its name", ErrArchiveBad)
	}
	// Some tools write a backslash as the separator.
	n := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(n, "/") {
		return nil, "", fmt.Errorf("%w: an entry has an absolute path (%q)", ErrArchiveBad, trimName(name))
	}
	for _, s := range strings.Split(n, "/") {
		switch s {
		case "", ".":
			continue
		case "..":
			return nil, "", fmt.Errorf("%w: an entry would leave the destination folder (%q)", ErrArchiveBad, trimName(name))
		}
		segs = append(segs, s)
	}
	if !utf8.ValidString(n) {
		return nil, SkipNotUTF8, nil
	}
	if len(segs) > extractMaxDepth {
		return nil, SkipTooDeep, nil
	}
	for i, s := range segs {
		c, cerr := workspace.CleanName(s)
		if cerr != nil {
			return nil, SkipBadName, nil
		}
		segs[i] = c
	}
	return segs, "", nil
}

func trimName(s string) string {
	if len(s) > 80 {
		s = s[:80]
	}
	return strings.ToValidUTF8(s, "�")
}

// entryKind says what an entry is. A reason is set for anything that is not a
// regular file or a folder.
func entryKind(fi archives.FileInfo) (workspace.EntryKind, string) {
	if h, ok := fi.Header.(*tar.Header); ok {
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeGNUSparse:
			return workspace.EntryFile, ""
		case tar.TypeDir:
			return workspace.EntryDir, ""
		case tar.TypeSymlink:
			return 0, SkipSymlink
		case tar.TypeLink:
			return 0, SkipHardLink
		}
		return 0, SkipSpecial
	}
	mode := fi.Mode()
	switch {
	case mode&fs.ModeSymlink != 0 || (fi.LinkTarget != "" && !fi.IsDir()):
		return 0, SkipSymlink
	case mode.IsDir() || strings.HasSuffix(fi.NameInArchive, "/"):
		return workspace.EntryDir, ""
	case !mode.IsRegular():
		return 0, SkipSpecial
	}
	return workspace.EntryFile, ""
}
