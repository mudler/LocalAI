// SPDX-License-Identifier: MIT

package worker

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

const ephemeralCapacityWriteChunk int64 = 64 << 10

const (
	defaultEphemeralByteLimitCeiling = int64(10 << 30)
	defaultEphemeralMinFreeFloor     = int64(1 << 30)
	ephemeralReleaseTombstoneTTL     = time.Hour
	maxEphemeralReleaseTombstones    = 16384
)

func effectiveEphemeralCapacity(roots []string, byteLimit, minFreeBytes int64) (int64, int64, error) {
	if byteLimit > 0 && minFreeBytes > 0 {
		return byteLimit, minFreeBytes, nil
	}
	var smallestTotal int64
	var largestTotal int64
	for _, root := range roots {
		diskInfo, err := xsysinfo.GetDiskInfo(root)
		if err != nil {
			return 0, 0, fmt.Errorf("reading ephemeral filesystem capacity for %q: %w", root, err)
		}
		total := int64(min(diskInfo.Total, uint64(math.MaxInt64)))
		if smallestTotal == 0 || total < smallestTotal {
			smallestTotal = total
		}
		largestTotal = max(largestTotal, total)
	}
	if smallestTotal == 0 {
		return 0, 0, fmt.Errorf("at least one ephemeral root is required")
	}
	if byteLimit <= 0 {
		byteLimit = min(defaultEphemeralByteLimitCeiling, smallestTotal/10)
	}
	if minFreeBytes <= 0 {
		minFreeBytes = max(defaultEphemeralMinFreeFloor, largestTotal/20)
	}
	return byteLimit, minFreeBytes, nil
}

// EphemeralCapacityError reports the values used to reject a reservation.
type EphemeralCapacityError struct {
	RequestedBytes int64
	UsageBytes     int64
	LimitBytes     int64
	AvailableBytes int64
	HeadroomBytes  int64
}

func (e *EphemeralCapacityError) Error() string {
	return fmt.Sprintf(
		"ephemeral capacity exceeded: requested=%d usage=%d limit=%d available=%d headroom=%d",
		e.RequestedBytes,
		e.UsageBytes,
		e.LimitBytes,
		e.AvailableBytes,
		e.HeadroomBytes,
	)
}

// EphemeralReservationConflictError reports an attempt to change an active
// reservation without first committing or releasing it.
type EphemeralReservationConflictError struct {
	Path           string
	ActiveBytes    int64
	RequestedBytes int64
}

// EphemeralRequestReleasedError reports an attempt to stage another file for
// a request whose cleanup has already begun.
type EphemeralRequestReleasedError struct {
	RequestID string
}

func (e *EphemeralRequestReleasedError) Error() string {
	return fmt.Sprintf("ephemeral request %q has already been released", e.RequestID)
}

type ephemeralReleaseTombstone struct {
	requestID string
	expires   time.Time
}

func (e *EphemeralReservationConflictError) Error() string {
	return fmt.Sprintf(
		"ephemeral path %q already has an active reservation of %d bytes; requested %d bytes",
		e.Path,
		e.ActiveBytes,
		e.RequestedBytes,
	)
}

type ephemeralCapacityState uint8

const (
	ephemeralCapacityExisting ephemeralCapacityState = iota
	ephemeralCapacityActive
	ephemeralCapacityWriting
	ephemeralCapacityCommitted
)

type ephemeralCapacityEntry struct {
	state            ephemeralCapacityState
	owned            bool
	releaseRequested bool
	baseline         int64
	reserved         int64
	pending          int64
	inflight         int64
	openWriters      int
}

// EphemeralCapacityGuard accounts files and in-flight writes below a fixed set
// of ephemeral roots.
type EphemeralCapacityGuard struct {
	mu            sync.Mutex
	changed       *sync.Cond
	roots         []string
	byteLimit     int64
	minFreeBytes  int64
	usage         int64
	entries       map[string]ephemeralCapacityEntry
	commitWaiters map[string]int
	released      map[string]time.Time
	releaseOrder  []ephemeralReleaseTombstone
	requestOps    map[string]int
	closingOps    map[string]bool
	releasePins   map[string]int
}

// NewEphemeralCapacityGuard creates a guard and accounts regular files already
// present below roots. Directory walks do not follow symbolic links.
func NewEphemeralCapacityGuard(roots []string, byteLimit, minFreeBytes int64) (*EphemeralCapacityGuard, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one ephemeral root is required")
	}
	if byteLimit < 0 {
		return nil, fmt.Errorf("ephemeral byte limit must not be negative")
	}
	if minFreeBytes < 0 {
		return nil, fmt.Errorf("ephemeral free-space headroom must not be negative")
	}

	guard := &EphemeralCapacityGuard{
		roots:         make([]string, 0, len(roots)),
		byteLimit:     byteLimit,
		minFreeBytes:  minFreeBytes,
		entries:       make(map[string]ephemeralCapacityEntry),
		commitWaiters: make(map[string]int),
		released:      make(map[string]time.Time),
		requestOps:    make(map[string]int),
		closingOps:    make(map[string]bool),
		releasePins:   make(map[string]int),
	}
	guard.changed = sync.NewCond(&guard.mu)
	seenRoots := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		cleanRoot, err := cleanEphemeralAbsolutePath(root)
		if err != nil {
			return nil, fmt.Errorf("resolving ephemeral root: %w", err)
		}
		if _, found := seenRoots[cleanRoot]; found {
			continue
		}
		if err := rejectEphemeralSymlinkComponents(cleanRoot); err != nil {
			return nil, fmt.Errorf("validating ephemeral root %q: %w", cleanRoot, err)
		}
		seenRoots[cleanRoot] = struct{}{}
		guard.roots = append(guard.roots, cleanRoot)
	}

	for _, root := range guard.roots {
		if err := guard.accountExistingFiles(root); err != nil {
			return nil, err
		}
	}
	return guard, nil
}

// BeginRequestOperation registers staging work before it performs filesystem
// or object-store operations. A release waits for registered work and prevents
// new work for the same request from entering.
func (g *EphemeralCapacityGuard) BeginRequestOperation(requestID string) error {
	if err := validateEphemeralCacheRequestID(requestID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneReleaseTombstonesLocked(time.Now())
	if _, found := g.released[requestID]; found || g.closingOps[requestID] {
		return &EphemeralRequestReleasedError{RequestID: requestID}
	}
	if _, found := g.requestOps[requestID]; !found && len(g.requestOps) >= maxEphemeralReleaseTombstones {
		return fmt.Errorf("too many concurrent ephemeral request operations")
	}
	g.requestOps[requestID]++
	return nil
}

// EndRequestOperation completes work registered by BeginRequestOperation.
func (g *EphemeralCapacityGuard) EndRequestOperation(requestID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.requestOps[requestID] <= 1 {
		delete(g.requestOps, requestID)
		delete(g.closingOps, requestID)
	} else {
		g.requestOps[requestID]--
	}
	g.pruneReleaseTombstonesLocked(time.Now())
	g.changed.Broadcast()
}

// Reserve atomically reserves size additional bytes for path. An existing or
// committed file remains charged until the new write is committed.
func (g *EphemeralCapacityGuard) Reserve(path string, size int64) error {
	if size < 0 {
		return fmt.Errorf("reservation size must not be negative")
	}
	cleanPath, root, err := g.registeredPath(path)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.rejectReleasedRequestLocked(cleanPath, root); err != nil {
		return err
	}
	entry, found := g.entries[cleanPath]
	if found && entry.isActive() {
		if entry.reserved == size {
			return nil
		}
		return &EphemeralReservationConflictError{
			Path: cleanPath, ActiveBytes: entry.reserved, RequestedBytes: size,
		}
	}
	if err := g.checkCapacityLocked(root, size); err != nil {
		return err
	}
	entry.state = ephemeralCapacityActive
	entry.owned = true
	entry.reserved = size
	entry.pending = size
	entry.inflight = 0
	g.entries[cleanPath] = entry
	g.usage += size
	return nil
}

// Commit replaces the path's baseline and reservation with the regular file's
// actual size. It waits for every bounded writer for the path to close and
// preserves request ownership until Release.
func (g *EphemeralCapacityGuard) Commit(path string) error {
	cleanPath, root, err := g.registeredPath(path)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		entry, found := g.entries[cleanPath]
		if !found {
			return fmt.Errorf("ephemeral path %q has no reservation", cleanPath)
		}
		if entry.state == ephemeralCapacityExisting || entry.state == ephemeralCapacityCommitted {
			return nil
		}
		if entry.openWriters == 0 {
			break
		}
		g.commitWaiters[cleanPath]++
		g.changed.Wait()
		g.commitWaiters[cleanPath]--
		if g.commitWaiters[cleanPath] == 0 {
			delete(g.commitWaiters, cleanPath)
		}
	}

	info, err := os.Lstat(cleanPath)
	if err != nil {
		return fmt.Errorf("stating committed ephemeral file %q: %w", cleanPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("committed ephemeral path %q is not a regular file", cleanPath)
	}
	entry := g.entries[cleanPath]
	charged := entry.baseline + entry.reserved
	if info.Size() > charged {
		additional := info.Size() - charged
		if err := g.checkCapacityLocked(root, additional); err != nil {
			return err
		}
		charged += additional
		g.usage += additional
	}
	g.usage -= charged - info.Size()
	entry.state = ephemeralCapacityCommitted
	entry.owned = !entry.releaseRequested && !g.requestReleasedLocked(cleanPath, root)
	entry.baseline = info.Size()
	entry.reserved = 0
	entry.pending = 0
	entry.inflight = 0
	g.entries[cleanPath] = entry
	g.changed.Broadcast()
	return nil
}

// Claim makes an existing regular file request-owned until Release. It
// serializes with recovery deletion, preserves bytes already discovered by a
// startup scan, and only admits growth that fits the configured capacity.
// Repeated claims of the same committed file are idempotent.
func (g *EphemeralCapacityGuard) Claim(path string) error {
	cleanPath, root, err := g.registeredPath(path)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.rejectReleasedRequestLocked(cleanPath, root); err != nil {
		return err
	}

	info, err := os.Lstat(cleanPath)
	if err != nil {
		return fmt.Errorf("stating claimed ephemeral file %q: %w", cleanPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("claimed ephemeral path %q is not a regular file", cleanPath)
	}

	entry, found := g.entries[cleanPath]
	if found && entry.isOwned() && entry.state != ephemeralCapacityCommitted {
		return &EphemeralReservationConflictError{
			Path: cleanPath, ActiveBytes: entry.reserved, RequestedBytes: info.Size(),
		}
	}

	accounted := int64(0)
	if found {
		accounted = entry.baseline + entry.reserved
	}
	delta := info.Size() - accounted
	if delta > 0 {
		if err := g.checkCapacityLocked(root, delta); err != nil {
			// The file already occupies the filesystem, so keep accounting
			// truthful even though a new request cannot claim it. Preserve
			// ownership if an earlier claim is still awaiting Release.
			state := ephemeralCapacityExisting
			owned := false
			if found && entry.state == ephemeralCapacityCommitted && entry.isOwned() {
				state = ephemeralCapacityCommitted
				owned = true
			}
			entry = ephemeralCapacityEntry{
				state: state, owned: owned, baseline: info.Size(),
			}
			g.entries[cleanPath] = entry
			g.usage += delta
			return err
		}
	}
	g.usage += delta
	entry = ephemeralCapacityEntry{
		state: ephemeralCapacityCommitted, owned: true, baseline: info.Size(),
	}
	g.entries[cleanPath] = entry
	return nil
}

// Release forgets all accounting for path. It is safe to call repeatedly.
func (g *EphemeralCapacityGuard) Release(path string) error {
	cleanPath, _, err := g.registeredPath(path)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		entry, found := g.entries[cleanPath]
		if !found {
			return nil
		}
		if entry.openWriters == 0 {
			g.releaseLocked(cleanPath)
			g.changed.Broadcast()
			return nil
		}
		g.changed.Wait()
	}
}

// BeginRequestRelease prevents new staging for requestID and waits until every
// reservation that started before cleanup has either committed or rolled back.
// The caller can then enumerate the request directories without missing a file
// created after the enumeration.
func (g *EphemeralCapacityGuard) BeginRequestRelease(ctx context.Context, requestID string) error {
	if ctx == nil {
		return fmt.Errorf("release context is nil")
	}
	if err := validateEphemeralCacheRequestID(requestID); err != nil {
		return err
	}

	g.mu.Lock()
	stop := context.AfterFunc(ctx, func() {
		g.mu.Lock()
		g.changed.Broadcast()
		g.mu.Unlock()
	})
	defer stop()
	pinned := false
	keepPin := false
	defer func() {
		if pinned && !keepPin {
			g.endRequestReleaseLocked(requestID)
		}
		g.mu.Unlock()
	}()
	for {
		_, alreadyPinned := g.releasePins[requestID]
		if alreadyPinned || len(g.releasePins) < maxEphemeralReleaseTombstones {
			break
		}
		if err := ctx.Err(); err != nil {
			g.makeRequestRecoverableLocked(requestID)
			return err
		}
		g.changed.Wait()
	}
	g.releasePins[requestID]++
	pinned = true
	now := time.Now()
	g.pruneReleaseTombstonesLocked(now)
	if _, found := g.released[requestID]; !found {
		expires := now.Add(ephemeralReleaseTombstoneTTL)
		g.released[requestID] = expires
		g.releaseOrder = append(g.releaseOrder, ephemeralReleaseTombstone{requestID: requestID, expires: expires})
		g.pruneReleaseTombstonesLocked(now)
	}
	for path, entry := range g.entries {
		_, root, err := g.registeredPathLocked(path)
		if err == nil && ephemeralRequestID(path, root) == requestID && !entry.isActive() {
			entry.owned = false
			g.entries[path] = entry
		}
	}
	for g.requestOps[requestID] > 0 || g.hasActiveRequestLocked(requestID) {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.changed.Wait()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	keepPin = true
	return nil
}

// EndRequestRelease allows an inactive release marker to expire or be evicted
// after the caller has completed its request-directory scan.
func (g *EphemeralCapacityGuard) EndRequestRelease(requestID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.endRequestReleaseLocked(requestID)
}

func (g *EphemeralCapacityGuard) endRequestReleaseLocked(requestID string) {
	if g.releasePins[requestID] <= 1 {
		delete(g.releasePins, requestID)
	} else {
		g.releasePins[requestID]--
	}
	g.pruneReleaseTombstonesLocked(time.Now())
	g.changed.Broadcast()
}

// Account records unowned bytes found by recovery after startup. Existing
// request-owned entries are left unchanged so recovery cannot erase live
// charges or ownership.
func (g *EphemeralCapacityGuard) Account(path string, size int64) error {
	if size < 0 {
		return fmt.Errorf("accounted size must not be negative")
	}
	cleanPath, _, err := g.registeredPath(path)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	entry, found := g.entries[cleanPath]
	if found && entry.isOwned() {
		return &EphemeralReservationConflictError{
			Path: cleanPath, ActiveBytes: entry.reserved, RequestedBytes: size,
		}
	}
	newUsage := g.usage
	if found {
		newUsage -= entry.baseline
	}
	if size > math.MaxInt64-newUsage {
		return fmt.Errorf("ephemeral usage exceeds supported size")
	}
	entry = ephemeralCapacityEntry{state: ephemeralCapacityExisting, baseline: size}
	g.entries[cleanPath] = entry
	g.usage = newUsage + size
	return nil
}

// ReleaseTree forgets accounting for files at or below path. Recovery cleanup
// can call this after it successfully removes a stale request tree.
func (g *EphemeralCapacityGuard) ReleaseTree(path string) error {
	cleanPath, _, err := g.registeredPath(path)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for g.hasOpenWriterLocked(cleanPath) {
		g.changed.Wait()
	}
	for entryPath := range g.entries {
		if ephemeralPathAtOrBelow(entryPath, cleanPath) {
			g.releaseLocked(entryPath)
		}
	}
	return nil
}

// RemoveTreeIfInactive serializes recovery deletion with new reservations so
// cleanup cannot remove a request tree between an ownership check and Reserve.
func (g *EphemeralCapacityGuard) RemoveTreeIfInactive(path string, remove func() error) (bool, error) {
	if remove == nil {
		return false, fmt.Errorf("ephemeral tree remover is nil")
	}
	cleanPath, _, err := g.registeredPath(path)
	if err != nil {
		return false, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for entryPath, entry := range g.entries {
		if entry.isOwned() && ephemeralPathAtOrBelow(entryPath, cleanPath) {
			return false, nil
		}
	}
	if err := remove(); err != nil {
		return false, err
	}
	for entryPath := range g.entries {
		if ephemeralPathAtOrBelow(entryPath, cleanPath) {
			g.releaseLocked(entryPath)
		}
	}
	return true, nil
}

// HasActiveReservation reports whether path itself or a descendant is owned
// by a request. Recovery cleanup uses it to avoid live request trees, including
// inputs whose upload has committed while inference is still running.
func (g *EphemeralCapacityGuard) HasActiveReservation(path string) bool {
	cleanPath, _, err := g.registeredPath(path)
	if err != nil {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for entryPath, entry := range g.entries {
		if entry.isOwned() && ephemeralPathAtOrBelow(entryPath, cleanPath) {
			return true
		}
	}
	return false
}

func (g *EphemeralCapacityGuard) commitWaiterCount(path string) int {
	cleanPath, _, err := g.registeredPath(path)
	if err != nil {
		return 0
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	return g.commitWaiters[cleanPath]
}

func (g *EphemeralCapacityGuard) releaseTombstoneCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneReleaseTombstonesLocked(time.Now())
	return len(g.released)
}

// NewWriter returns a writer that admits unknown-length input in bounded
// chunks. Callers must close it before committing or releasing the path.
func (g *EphemeralCapacityGuard) NewWriter(path string, destination io.Writer) (*EphemeralCapacityWriter, error) {
	if destination == nil {
		return nil, fmt.Errorf("ephemeral writer destination is nil")
	}
	cleanPath, root, err := g.registeredPath(path)
	if err != nil {
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.rejectReleasedRequestLocked(cleanPath, root); err != nil {
		return nil, err
	}
	entry, found := g.entries[cleanPath]
	if !found || !entry.isActive() {
		if err := g.checkCapacityLocked(root, 0); err != nil {
			return nil, err
		}
		entry.state = ephemeralCapacityActive
		entry.reserved = 0
		entry.pending = 0
		entry.inflight = 0
	}
	entry.owned = true
	entry.state = ephemeralCapacityWriting
	entry.openWriters++
	g.entries[cleanPath] = entry
	return &EphemeralCapacityWriter{guard: g, path: cleanPath, destination: destination}, nil
}

// CapacityWriter exposes NewWriter through the transport-facing interface
// without leaking the concrete writer type across packages.
func (g *EphemeralCapacityGuard) CapacityWriter(path string, destination io.Writer) (io.WriteCloser, error) {
	return g.NewWriter(path, destination)
}

// EphemeralCapacityWriter bounds writes through an EphemeralCapacityGuard.
// Close finalizes its accounting lifecycle without closing the destination.
type EphemeralCapacityWriter struct {
	mu          sync.Mutex
	guard       *EphemeralCapacityGuard
	path        string
	destination io.Writer
	closed      bool
}

var _ io.WriteCloser = (*EphemeralCapacityWriter)(nil)

func (w *EphemeralCapacityWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, fmt.Errorf("ephemeral capacity writer is closed")
	}

	total := 0
	for len(payload) > 0 {
		chunkLength := min(len(payload), int(ephemeralCapacityWriteChunk))
		grown, err := w.guard.beginWrite(w.path, int64(chunkLength))
		if err != nil {
			return total, err
		}
		written, writeErr := w.destination.Write(payload[:chunkLength])
		if written < 0 || written > chunkLength {
			w.guard.settleWrite(w.path, int64(chunkLength), 0, grown)
			return total, fmt.Errorf("ephemeral destination returned invalid write count %d", written)
		}
		w.guard.settleWrite(w.path, int64(chunkLength), int64(written), grown)
		total += written
		payload = payload[written:]
		if writeErr != nil {
			return total, writeErr
		}
		if written != chunkLength {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (w *EphemeralCapacityWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	w.guard.closeWriter(w.path)
	return nil
}

func (e ephemeralCapacityEntry) isActive() bool {
	return e.state == ephemeralCapacityActive || e.state == ephemeralCapacityWriting
}

func (e ephemeralCapacityEntry) isOwned() bool {
	return e.owned
}

func (g *EphemeralCapacityGuard) accountExistingFiles(root string) error {
	err := filepath.WalkDir(root, func(path string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if dirEntry.Type()&os.ModeSymlink != 0 {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		cleanPath := filepath.Clean(path)
		if _, found := g.entries[cleanPath]; found {
			return nil
		}
		if info.Size() > math.MaxInt64-g.usage {
			return fmt.Errorf("ephemeral usage exceeds supported size")
		}
		g.entries[cleanPath] = ephemeralCapacityEntry{
			state: ephemeralCapacityExisting, baseline: info.Size(),
		}
		g.usage += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("accounting ephemeral root %q: %w", root, err)
	}
	return nil
}

func (g *EphemeralCapacityGuard) beginWrite(path string, size int64) (int64, error) {
	_, root, err := g.registeredPath(path)
	if err != nil {
		return 0, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	entry, found := g.entries[path]
	if !found || entry.state != ephemeralCapacityWriting || entry.openWriters == 0 {
		return 0, fmt.Errorf("ephemeral path %q has no active writer", path)
	}
	availableReservation := entry.pending - entry.inflight
	grow := max(int64(0), size-availableReservation)
	if err := g.checkCapacityLocked(root, grow); err != nil {
		return 0, err
	}
	entry.reserved += grow
	entry.pending += grow
	entry.inflight += size
	g.entries[path] = entry
	g.usage += grow
	return grow, nil
}

func (g *EphemeralCapacityGuard) settleWrite(path string, attempted, written, grown int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, found := g.entries[path]
	if !found {
		return
	}
	entry.inflight -= attempted
	entry.pending -= written
	unusedGrowth := min(grown, attempted-written)
	entry.reserved -= unusedGrowth
	entry.pending -= unusedGrowth
	g.usage -= unusedGrowth
	g.entries[path] = entry
}

func (g *EphemeralCapacityGuard) closeWriter(path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, found := g.entries[path]
	if !found || entry.openWriters == 0 {
		return
	}
	entry.openWriters--
	if entry.openWriters == 0 {
		entry.state = ephemeralCapacityActive
	}
	g.entries[path] = entry
	g.changed.Broadcast()
}

func (g *EphemeralCapacityGuard) checkCapacityLocked(root string, requested int64) error {
	available, err := ephemeralAvailableBytes(root)
	if err != nil {
		return err
	}
	exceedsLimit := requested > g.byteLimit-g.usage
	pending := g.pendingLocked()
	freeAfterHeadroom := available - min(available, g.minFreeBytes)
	exceedsAvailable := pending > freeAfterHeadroom || requested > freeAfterHeadroom-pending
	if exceedsLimit || exceedsAvailable {
		return &EphemeralCapacityError{
			RequestedBytes: requested,
			UsageBytes:     g.usage,
			LimitBytes:     g.byteLimit,
			AvailableBytes: available,
			HeadroomBytes:  g.minFreeBytes,
		}
	}
	return nil
}

func (g *EphemeralCapacityGuard) pendingLocked() int64 {
	var pending int64
	for _, entry := range g.entries {
		if entry.pending > math.MaxInt64-pending {
			return math.MaxInt64
		}
		pending += entry.pending
	}
	return pending
}

func (g *EphemeralCapacityGuard) hasOpenWriterLocked(path string) bool {
	for entryPath, entry := range g.entries {
		if entry.openWriters > 0 && ephemeralPathAtOrBelow(entryPath, path) {
			return true
		}
	}
	return false
}

func (g *EphemeralCapacityGuard) releaseLocked(path string) {
	entry, found := g.entries[path]
	if !found {
		return
	}
	g.usage -= entry.baseline + entry.reserved
	delete(g.entries, path)
}

func (g *EphemeralCapacityGuard) hasActiveRequestLocked(requestID string) bool {
	for path, entry := range g.entries {
		if !entry.isActive() {
			continue
		}
		_, root, err := g.registeredPathLocked(path)
		if err == nil && ephemeralRequestID(path, root) == requestID {
			return true
		}
	}
	return false
}

func (g *EphemeralCapacityGuard) rejectReleasedRequestLocked(path, root string) error {
	requestID := ephemeralRequestID(path, root)
	if requestID == "" {
		return nil
	}
	g.pruneReleaseTombstonesLocked(time.Now())
	if _, found := g.released[requestID]; found || g.closingOps[requestID] {
		return &EphemeralRequestReleasedError{RequestID: requestID}
	}
	return nil
}

func (g *EphemeralCapacityGuard) makeRequestRecoverableLocked(requestID string) {
	if g.requestOps[requestID] > 0 {
		g.closingOps[requestID] = true
	}
	for path, entry := range g.entries {
		_, root, err := g.registeredPathLocked(path)
		if err != nil || ephemeralRequestID(path, root) != requestID {
			continue
		}
		entry.owned = false
		entry.releaseRequested = true
		g.entries[path] = entry
	}
}

func (g *EphemeralCapacityGuard) requestReleasedLocked(path, root string) bool {
	requestID := ephemeralRequestID(path, root)
	if requestID == "" {
		return false
	}
	g.pruneReleaseTombstonesLocked(time.Now())
	_, found := g.released[requestID]
	return found
}

func (g *EphemeralCapacityGuard) pruneReleaseTombstonesLocked(now time.Time) {
	kept := g.releaseOrder[:0]
	for _, marker := range g.releaseOrder {
		expires, found := g.released[marker.requestID]
		if !found || !expires.Equal(marker.expires) {
			continue
		}
		removable := g.requestOps[marker.requestID] == 0 && g.releasePins[marker.requestID] == 0 &&
			(!marker.expires.After(now) || len(g.released) > maxEphemeralReleaseTombstones)
		if removable {
			delete(g.released, marker.requestID)
			continue
		}
		kept = append(kept, marker)
	}
	g.releaseOrder = kept
}

func ephemeralRequestID(path, root string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func (g *EphemeralCapacityGuard) registeredPath(path string) (string, string, error) {
	cleanPath, err := cleanEphemeralAbsolutePath(path)
	if err != nil {
		return "", "", err
	}
	root := ""
	for _, candidate := range g.roots {
		if ephemeralPathAtOrBelow(cleanPath, candidate) && len(candidate) > len(root) {
			root = candidate
		}
	}
	if root == "" {
		return "", "", fmt.Errorf("path %q is outside registered ephemeral roots", cleanPath)
	}
	if err := rejectEphemeralSymlinkComponents(cleanPath); err != nil {
		return "", "", fmt.Errorf("validating ephemeral path %q: %w", cleanPath, err)
	}
	return cleanPath, root, nil
}

// registeredPathLocked validates a path already stored by the guard. Stored
// paths were validated on admission, so this avoids filesystem work while the
// guard mutex is held during request scans.
func (g *EphemeralCapacityGuard) registeredPathLocked(cleanPath string) (string, string, error) {
	root := ""
	for _, candidate := range g.roots {
		if ephemeralPathAtOrBelow(cleanPath, candidate) && len(candidate) > len(root) {
			root = candidate
		}
	}
	if root == "" {
		return "", "", fmt.Errorf("path %q is outside registered ephemeral roots", cleanPath)
	}
	return cleanPath, root, nil
}

func cleanEphemeralAbsolutePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("ephemeral path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving ephemeral path %q: %w", path, err)
	}
	return filepath.Clean(absPath), nil
}

func rejectEphemeralSymlinkComponents(path string) error {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	current := volume + string(filepath.Separator)
	remainder = strings.TrimPrefix(remainder, string(filepath.Separator))
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("checking path component %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink", current)
		}
	}
	return nil
}

func ephemeralPathAtOrBelow(path, parent string) bool {
	return path == parent || strings.HasPrefix(path, parent+string(filepath.Separator))
}

func ephemeralAvailableBytes(path string) (int64, error) {
	diskInfo, err := xsysinfo.GetDiskInfo(path)
	if err != nil {
		return 0, fmt.Errorf("reading ephemeral filesystem availability: %w", err)
	}
	if diskInfo.Available > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(diskInfo.Available), nil
}
