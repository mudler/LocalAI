package textindex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio/extract"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ExtractorVersion is the version of what the indexer makes from a file. A
// change of the text format raises it, so that entries of the old format are
// not copied to new files.
const ExtractorVersion = 1

// Defaults of Options.
const (
	DefaultMaxTextBytes = 2 << 20
	DefaultMaxFileBytes = 50 << 20
	DefaultMaxPages     = 200
	defaultInterval     = 2 * time.Second
	defaultConcurrency  = 2
	defaultTimeout      = 60 * time.Second
	defaultBatch        = 200
	defaultSetsPerPass  = 5000
	defaultFilesPerPass = 200
	defaultSweepEvery   = 30 * time.Second
	defaultSweepBatch   = 200
)

// Reasons of skipped and failed entries.
const (
	ReasonNoText   = "no text layer"
	ReasonTooLarge = "the file is too large to index"
	ReasonNotType  = "the content does not match the file type"
	ReasonInvalid  = "the file could not be read"
	ReasonTimeout  = "reading the file took too long"
	ReasonPanic    = "the reader stopped with an internal error"
)

// indexable are the extensions phase 1 reads.
var indexable = map[string]bool{".pdf": true, ".docx": true, ".xlsx": true, ".xlsm": true, ".pptx": true}

// Indexable reports whether the indexer reads files of this name.
func Indexable(name string) bool { return indexable[strings.ToLower(path.Ext(name))] }

// ExtractFunc reads the text of a document; extract.ExtractWith by default.
type ExtractFunc func(ctx context.Context, name string, data []byte, l extract.Limits) (string, extract.Structure, error)

// Options configure an Indexer.
type Options struct {
	Store     Store
	Workspace workspace.Store
	// Admin is the workspace's admin actor: the indexer reads the journal
	// and the files as it.
	Admin workspace.Actor
	// DB, when set, is the shared database: each pass runs under an
	// advisory lock, so one frontend of a cluster indexes. Nil: this
	// process is alone and runs every pass.
	DB *gorm.DB
	// Enabled is the setting chats_index_files. Nil: on.
	Enabled func() bool
	// MaxTextBytes is the setting chats_index_max_text_bytes. Nil or 0:
	// DefaultMaxTextBytes.
	MaxTextBytes func() int
	// Interval between two passes; 0: 2 s. Kick starts one sooner.
	Interval time.Duration
	// Concurrency is how many files are read at a time; 0: 2.
	Concurrency int
	// Timeout bounds the reading of one file; 0: 60 s.
	Timeout time.Duration
	// MaxFileBytes is the largest file that is read; 0: 50 MiB.
	MaxFileBytes int64
	// MaxPages is the most pages of a PDF that are read; 0: 200.
	MaxPages int
	// SetsPerPass bounds the journal change sets one pass applies, and
	// FilesPerPass the files one pass reads, so a backfill goes in steps.
	SetsPerPass, FilesPerPass int
	// SweepEvery is how often a pass checks a batch of SweepBatch entries
	// for nodes that are gone; 0: 30 s and 200.
	SweepEvery time.Duration
	SweepBatch int
	// Extract replaces the reader, for tests.
	Extract ExtractFunc
	// Now replaces the clock, for tests.
	Now func() time.Time
}

// Indexer keeps a Store in step with the workspace. The journal is its queue:
// each pass applies the change sets after its cursor (a write marks the file
// pending, a delete removes the entry, a move changes its path), saves the
// cursor, and then reads the pending files. The first pass starts at
// revision 0, so it indexes the files that were there before the indexer.
type Indexer struct {
	o    Options
	kick chan struct{}
	solo sync.Mutex // a pass at a time when there is no database lock

	// The sweep goes through the entries a batch at a time. Where it is and
	// when it runs next are in the memory of this process: another frontend
	// that wins the lock sweeps from its own place, which only repeats work.
	sweepMu    sync.Mutex
	sweepAfter int64
	sweepNext  time.Time
}

// New returns an Indexer. Start it with Run.
func New(o Options) *Indexer {
	if o.Interval <= 0 {
		o.Interval = defaultInterval
	}
	if o.Concurrency <= 0 {
		o.Concurrency = defaultConcurrency
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.MaxPages <= 0 {
		o.MaxPages = DefaultMaxPages
	}
	if o.SetsPerPass <= 0 {
		o.SetsPerPass = defaultSetsPerPass
	}
	if o.FilesPerPass <= 0 {
		o.FilesPerPass = defaultFilesPerPass
	}
	if o.SweepEvery <= 0 {
		o.SweepEvery = defaultSweepEvery
	}
	if o.SweepBatch <= 0 {
		o.SweepBatch = defaultSweepBatch
	}
	if o.Extract == nil {
		o.Extract = extract.ExtractWith
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Indexer{o: o, kick: make(chan struct{}, 1)}
}

// Store returns the store of the indexer.
func (ix *Indexer) Store() Store { return ix.o.Store }

// On reports whether indexing is switched on.
func (ix *Indexer) On() bool { return ix != nil && (ix.o.Enabled == nil || ix.o.Enabled()) }

func (ix *Indexer) maxText() int {
	if ix.o.MaxTextBytes != nil {
		if n := ix.o.MaxTextBytes(); n > 0 {
			return n
		}
	}
	return DefaultMaxTextBytes
}

// Kick asks for a pass soon. It never blocks.
func (ix *Indexer) Kick() {
	if ix == nil {
		return
	}
	select {
	case ix.kick <- struct{}{}:
	default:
	}
}

// Run makes passes until ctx ends: every Interval, and after a Kick.
func (ix *Indexer) Run(ctx context.Context) {
	t := time.NewTicker(ix.o.Interval)
	defer t.Stop()
	for {
		if err := ix.Pass(ctx); err != nil && ctx.Err() == nil {
			xlog.Warn("File text index: a pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-ix.kick:
		}
	}
}

// Pass applies the journal and reads the pending files once. In a cluster
// it does nothing when another frontend holds the lock.
func (ix *Indexer) Pass(ctx context.Context) error {
	if !ix.On() {
		return nil
	}
	run := func() error {
		if err := ix.catchUp(ctx); err != nil {
			return err
		}
		if err := ix.sweep(ctx); err != nil {
			return err
		}
		return ix.readPending(ctx)
	}
	if ix.o.DB != nil {
		_, err := advisorylock.TryWithLockCtx(ctx, ix.o.DB, advisorylock.KeyChatsFileIndex, run)
		return err
	}
	if !ix.solo.TryLock() {
		return nil
	}
	defer ix.solo.Unlock()
	return run()
}

// Enqueue marks a file that was just written as pending and asks for a
// pass, so a search right after an upload says the file is being indexed.
// The journal would find the file anyway; this only makes it sooner.
func (ix *Indexer) Enqueue(ctx context.Context, n workspace.Node, owner string) {
	if !ix.On() || n.Kind != workspace.KindFile || n.Blob == "" || !Indexable(n.Path) {
		return
	}
	if err := ix.ensure(ctx, n.ID, n.Path, n.Blob, owner); err != nil {
		xlog.Debug("File text index: could not queue a file", "error", err)
	}
	ix.Kick()
}

// Forget removes the entries at or below prefix whose node is gone, after a
// purge erased nodes for good (their journal rows go with them, so a pass
// would not see the delete), and the whole texts that no entry needs any
// more. When it cannot finish (the process stops, the store fails), the
// sweep of the passes removes what is left.
func (ix *Indexer) Forget(ctx context.Context, prefix string) error {
	var after int64
	for {
		list, err := ix.o.Store.List(ctx, prefix, after, 200)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return ix.o.Store.PruneDocuments(ctx)
		}
		if err := ix.dropGone(ctx, list); err != nil {
			return err
		}
		after = list[len(list)-1].NodeID
	}
}

// dropGone deletes the entries of list whose node is gone: no node at the
// path, or another node there.
func (ix *Indexer) dropGone(ctx context.Context, list []Entry) error {
	var gone []int64
	for _, e := range list {
		n, err := ix.o.Workspace.Stat(ctx, ix.o.Admin, e.Path)
		if errors.Is(err, workspace.ErrNotFound) || (err == nil && n.ID != e.NodeID) {
			gone = append(gone, e.NodeID)
		}
	}
	return ix.o.Store.Delete(ctx, gone...)
}

// sweep checks one batch of entries, at most every SweepEvery, and deletes
// those whose node is gone. A purge erases the journal rows of what it
// erases, so only this finds the entries a Forget left behind; it goes
// round all the entries, then prunes the whole texts and starts again. It
// runs after catchUp, so a move the journal holds is applied first.
func (ix *Indexer) sweep(ctx context.Context) error {
	ix.sweepMu.Lock()
	now := ix.o.Now()
	if now.Before(ix.sweepNext) {
		ix.sweepMu.Unlock()
		return nil
	}
	ix.sweepNext = now.Add(ix.o.SweepEvery)
	after := ix.sweepAfter
	ix.sweepMu.Unlock()

	list, err := ix.o.Store.List(ctx, "/", after, ix.o.SweepBatch)
	if err != nil {
		return err
	}
	if err := ix.dropGone(ctx, list); err != nil {
		return err
	}
	next := int64(0)
	if len(list) == ix.o.SweepBatch {
		next = list[len(list)-1].NodeID
	} else if err := ix.o.Store.PruneDocuments(ctx); err != nil {
		return err
	}
	ix.sweepMu.Lock()
	ix.sweepAfter = next
	ix.sweepMu.Unlock()
	return nil
}

// catchUp applies the journal after the cursor, a batch at a time, and
// saves the cursor after each batch.
func (ix *Indexer) catchUp(ctx context.Context) error {
	cur, err := ix.o.Store.Cursor(ctx)
	if err != nil {
		return err
	}
	start := cur
	for applied := 0; applied < ix.o.SetsPerPass; {
		sets, err := ix.o.Workspace.ChangesSince(ctx, ix.o.Admin, cur, defaultBatch)
		if err != nil {
			return err
		}
		if len(sets) == 0 {
			break
		}
		for _, cs := range sets {
			for _, op := range cs.Ops {
				if err := ix.apply(ctx, cs, op); err != nil {
					return err
				}
			}
		}
		cur = sets[len(sets)-1].Rev
		if err := ix.o.Store.SetCursor(ctx, cur); err != nil {
			return err
		}
		applied += len(sets)
		if len(sets) < defaultBatch {
			break
		}
	}
	if start == 0 && cur > 0 {
		xlog.Info("File text index: read the workspace history", "revision", cur)
	}
	return nil
}

// apply brings the entry of one node in step with one journal operation.
func (ix *Indexer) apply(ctx context.Context, cs workspace.ChangeSet, op workspace.Op) error {
	switch {
	case op.Kind == workspace.OpDelete:
		return ix.o.Store.Delete(ctx, op.NodeID)
	case op.NewBlob != "": // a live file, maybe at a new path
		if !Indexable(op.NewPath) {
			return ix.o.Store.Delete(ctx, op.NodeID)
		}
		return ix.ensure(ctx, op.NodeID, op.NewPath, op.NewBlob, cs.Actor)
	case op.Kind == workspace.OpMove && op.OldPath != "" && op.OldPath != op.NewPath: // a folder
		return ix.o.Store.Move(ctx, op.OldPath, op.NewPath)
	}
	return nil
}

// ensure records that node id holds hash at p: an entry made from the same
// hash keeps its text and only follows the path; anything else is pending.
func (ix *Indexer) ensure(ctx context.Context, id int64, p, hash, owner string) error {
	e, err := ix.o.Store.Get(ctx, id)
	switch {
	case err == nil && e.Hash == hash && e.Extractor == ExtractorVersion:
		if e.Path == p {
			return nil
		}
		return ix.o.Store.Move(ctx, e.Path, p)
	case err != nil && !errors.Is(err, ErrNotFound):
		return err
	}
	return ix.o.Store.Put(ctx, Entry{NodeID: id, Path: p, Owner: owner, Hash: hash, Extractor: ExtractorVersion,
		Status: StatusPending, Updated: ix.o.Now()})
}

// readPending reads up to FilesPerPass pending files, Concurrency at a time.
func (ix *Indexer) readPending(ctx context.Context) error {
	for done := 0; done < ix.o.FilesPerPass; {
		if !ix.On() {
			return nil
		}
		list, err := ix.o.Store.Pending(ctx, "/", min(ix.o.Concurrency*4, ix.o.FilesPerPass-done))
		if err != nil || len(list) == 0 {
			return err
		}
		sem := make(chan struct{}, ix.o.Concurrency)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error
		for _, e := range list {
			sem <- struct{}{}
			wg.Add(1)
			go func(e Entry) {
				defer func() { <-sem; wg.Done() }()
				if err := ix.read(ctx, e); err != nil {
					mu.Lock()
					firstErr = cmpErr(firstErr, err)
					mu.Unlock()
				}
			}(e)
		}
		wg.Wait()
		if firstErr != nil {
			return firstErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		done += len(list)
	}
	return nil
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// read reads one pending file and stores what came of it. An error is
// returned only when the store fails or ctx ends; a file that cannot be read
// becomes a failed or skipped entry.
func (ix *Indexer) read(ctx context.Context, e Entry) (err error) {
	finish := func(st Status, reason string) error {
		e.Status, e.Reason, e.Text, e.Sections, e.Updated = st, reason, "", nil, ix.o.Now()
		return ix.o.Store.Put(ctx, e)
	}
	defer func() {
		if r := recover(); r != nil {
			xlog.Warn("File text index: the reader panicked", "node", e.NodeID, "panic", r)
			err = finish(StatusFailed, ReasonPanic)
		}
	}()
	r, n, err := ix.o.Workspace.Open(ctx, ix.o.Admin, e.Path)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		// Gone, or moved by a change the next pass applies.
		return ix.o.Store.Delete(ctx, e.NodeID)
	case err != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return finish(StatusFailed, ReasonInvalid)
	}
	defer func() { _ = r.Close() }()
	if n.ID != e.NodeID {
		return ix.o.Store.Delete(ctx, e.NodeID)
	}
	e.Hash = n.Blob // the content now: a newer write is read already
	if n.Size > ix.o.MaxFileBytes {
		return finish(StatusSkipped, ReasonTooLarge)
	}
	if same, ok, err := ix.o.Store.ReadyByHash(ctx, e.Hash, ExtractorVersion); err != nil {
		return err
	} else if ok && same.NodeID != e.NodeID {
		e.Status, e.Reason, e.Kind, e.Text, e.Sections, e.Truncated, e.Updated = StatusReady, "", same.Kind, same.Text, same.Sections, same.Truncated, ix.o.Now()
		return ix.o.Store.Put(ctx, e)
	}
	data, err := io.ReadAll(io.LimitReader(r, ix.o.MaxFileBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return finish(StatusFailed, ReasonInvalid)
	}
	if !magicMatches(e.Path, data) {
		return finish(StatusSkipped, ReasonNotType)
	}
	text, st, err := ix.extract(ctx, e.Path, data)
	switch {
	case ctx.Err() != nil:
		return ctx.Err() // the server stops: the entry stays pending
	case errors.Is(err, extract.ErrNoText):
		return finish(StatusSkipped, ReasonNoText)
	case errors.Is(err, context.DeadlineExceeded):
		return finish(StatusFailed, ReasonTimeout)
	case errors.Is(err, errPanic):
		return finish(StatusFailed, ReasonPanic)
	case err != nil:
		return finish(StatusFailed, ReasonInvalid)
	}
	e.Text, e.Sections, e.Truncated = capText(text, st.Sections, ix.maxText())
	e.Truncated = e.Truncated || st.Truncated
	e.Status, e.Reason, e.Kind, e.Updated = StatusReady, "", st.Kind, ix.o.Now()
	return ix.o.Store.Put(ctx, e)
}

var errPanic = errors.New("textindex: the reader panicked")

// extract runs the reader with the limits and the timeout, and turns a panic
// into errPanic.
func (ix *Indexer) extract(ctx context.Context, name string, data []byte) (text string, st extract.Structure, err error) {
	ctx, cancel := context.WithTimeout(ctx, ix.o.Timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			xlog.Warn("File text index: the reader panicked", "file", path.Base(name), "panic", r)
			err = fmt.Errorf("%w: %v", errPanic, r)
		}
	}()
	l := extract.DefaultLimits
	l.MaxBytes, l.Pages = int(ix.o.MaxFileBytes), ix.o.MaxPages
	// The character limit of the reader stops a long PDF early; the byte cap
	// below is exact.
	l.Text = ix.maxText()
	text, st, err = ix.o.Extract(ctx, name, data, l)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return text, st, err
}

// capText keeps at most max bytes of text, cut at a line, with the sections
// that start in what is kept.
func capText(text string, sections []extract.Section, max int) (string, []extract.Section, bool) {
	if len(text) <= max {
		return text, sections, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	if i := strings.LastIndexByte(text[:cut], '\n'); i > 0 {
		cut = i
	}
	text = text[:cut]
	lines := strings.Count(text, "\n") + 1
	var kept []extract.Section
	for _, s := range sections {
		if s.Line <= lines {
			kept = append(kept, s)
		}
	}
	return text + fmt.Sprintf("\n[The index keeps the first %d bytes of the text.]", max), kept, true
}

// magicMatches checks that the content is what the name says: a PDF starts
// with %PDF- (some writers put bytes before it), an Office file is a zip.
func magicMatches(name string, data []byte) bool {
	if strings.EqualFold(path.Ext(name), ".pdf") {
		return bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-"))
	}
	return bytes.HasPrefix(data, []byte("PK\x03\x04"))
}
