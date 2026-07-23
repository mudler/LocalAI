package modelartifacts

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

type SnapshotResolver interface {
	ResolveSnapshot(context.Context, hfapi.SnapshotRequest) (hfapi.Snapshot, error)
}

// Locker is the cross-process exclusion primitive that keeps two replicas from
// materializing the same snapshot at once. It is an interface rather than a
// concrete *flock.Flock because the contention path has to be exercised without
// a network filesystem: no test environment can make flock(2) return the
// CIFS-specific errno this code must tolerate, and it is also the seam a
// database-backed lock would plug into for multi-node deployments.
type Locker interface {
	TryLock() (bool, error)
	Unlock() error
}

// ErrLockContended reports that a peer held the artifact lock for the whole
// wait window and never published a usable snapshot. It is deliberately
// distinct from an acquisition failure: the work is in progress elsewhere, so
// callers can say so rather than blaming the model.
var ErrLockContended = errors.New("artifact lock is held by another process")

const (
	// DefaultLockWait bounds how long Ensure waits for a peer replica. It is
	// generous because the peer may legitimately be downloading tens of
	// gigabytes, and waiting is strictly cheaper than the fallback, which makes
	// the backend download the same repo in-band.
	DefaultLockWait = 30 * time.Minute

	initialLockRetryInterval = 100 * time.Millisecond
	maxLockRetryInterval     = 5 * time.Second
)

type Manager struct {
	resolver         SnapshotResolver
	huggingFaceToken string
	newLocker        func(string) Locker
	lockWait         time.Duration
	// writerID names this manager's staging trees. It is drawn once, at
	// construction, and deliberately never persisted: a partial tree belongs to
	// the process run that created it, and outliving that run is precisely what
	// it must not do.
	writerID string
}

type ManagerOption func(*Manager)

type Result struct {
	Spec         Spec
	RelativePath string
	Manifest     Manifest
	CacheHit     bool
}

func WithHuggingFaceToken(token string) ManagerOption {
	return func(manager *Manager) { manager.huggingFaceToken = token }
}

// WithLocker overrides how the artifact lock is created. The default is an
// flock(2) lock on the shared models directory.
func WithLocker(factory func(path string) Locker) ManagerOption {
	return func(manager *Manager) {
		if factory != nil {
			manager.newLocker = factory
		}
	}
}

// WithLockWait bounds how long Ensure waits for a peer replica holding the
// artifact lock before giving up with ErrLockContended.
func WithLockWait(wait time.Duration) ManagerOption {
	return func(manager *Manager) {
		if wait > 0 {
			manager.lockWait = wait
		}
	}
}

func NewManager(resolver SnapshotResolver, options ...ManagerOption) *Manager {
	manager := &Manager{
		resolver:  resolver,
		newLocker: func(path string) Locker { return flock.New(path) },
		lockWait:  DefaultLockWait,
		writerID:  newWriterID(),
	}
	for _, option := range options {
		option(manager)
	}
	return manager
}

func NewDefaultManager(options ...ManagerOption) *Manager {
	client := hfapi.NewClient()
	client.SetBaseURL(strings.TrimRight(downloader.HF_ENDPOINT, "/") + "/api/models")
	return NewManager(client, options...)
}

func committedResult(modelsPath string, spec Spec) (Result, bool) {
	if spec.Resolved == nil || spec.Resolved.CacheKey == "" {
		return Result{}, false
	}
	layout, err := LayoutFor(modelsPath, spec)
	if err != nil {
		return Result{}, false
	}
	manifest, err := ReadManifest(layout.Manifest)
	if err != nil || manifest.Artifact.Resolved == nil || manifest.Artifact.Resolved.CacheKey != spec.Resolved.CacheKey {
		return Result{}, false
	}
	specKey, err := CacheKey(spec)
	if err != nil || specKey != spec.Resolved.CacheKey {
		return Result{}, false
	}
	manifestKey, err := CacheKey(manifest.Artifact)
	if err != nil || manifestKey != spec.Resolved.CacheKey || len(manifest.Files) == 0 {
		return Result{}, false
	}
	for _, file := range manifest.Files {
		info, err := os.Stat(filepath.Join(layout.Snapshot, filepath.FromSlash(file.Path)))
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return Result{}, false
		}
	}
	relative, err := RelativeSnapshotPath(spec.Resolved.CacheKey)
	if err != nil {
		return Result{}, false
	}
	return Result{Spec: spec, RelativePath: relative, Manifest: manifest, CacheHit: true}, true
}

func (m *Manager) Ensure(ctx context.Context, modelsPath string, spec Spec) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	normalized, err := spec.Normalize()
	if err != nil {
		return Result{}, err
	}
	if cached, ok := committedResult(modelsPath, normalized); ok {
		return cached, nil
	}
	if m == nil || m.resolver == nil {
		return Result{}, fmt.Errorf("artifact materializer has no snapshot resolver")
	}
	token := ""
	if normalized.Source.TokenEnv == HuggingFaceTokenEnv {
		token = m.huggingFaceToken
		if token == "" {
			return Result{}, fmt.Errorf("artifact requires non-empty %s", HuggingFaceTokenEnv)
		}
	}
	revision := normalized.Source.Revision
	if normalized.Resolved != nil {
		revision = normalized.Resolved.Revision
	}
	ReportProgress(ctx, ProgressEvent{Phase: PhaseResolving, Artifact: normalized.Name})
	snapshot, err := m.resolver.ResolveSnapshot(ctx, hfapi.SnapshotRequest{
		Repo: normalized.Source.Repo, Revision: revision, Token: token,
		AllowPatterns: normalized.Source.AllowPatterns, IgnorePatterns: normalized.Source.IgnorePatterns,
	})
	if err != nil {
		return Result{}, err
	}
	if normalized.Resolved != nil && (snapshot.Endpoint != normalized.Resolved.Endpoint || snapshot.ResolvedRevision != normalized.Resolved.Revision) {
		return Result{}, fmt.Errorf("resolved artifact identity changed; reinstall the model")
	}
	normalized.Resolved = &Resolved{Endpoint: snapshot.Endpoint, Revision: snapshot.ResolvedRevision}
	// A snapshot with exactly one file is a single-file model (e.g. a GGUF for
	// llama.cpp/whisper). Record it so the load target resolves to the file
	// itself rather than the snapshot directory. PrimaryFile is deliberately not
	// part of the cache key: it is derived from the resolved contents, not the
	// request identity.
	if len(snapshot.Files) == 1 {
		normalized.Resolved.PrimaryFile = snapshot.Files[0].Path
	}
	cacheKey, err := CacheKey(normalized)
	if err != nil {
		return Result{}, err
	}
	normalized.Resolved.CacheKey = cacheKey
	layout, err := LayoutFor(modelsPath, normalized)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(layout.Lock), 0o750); err != nil {
		return Result{}, err
	}
	artifactLock := m.newLocker(layout.Lock)
	if err := m.acquireLock(ctx, artifactLock, layout.Lock); err != nil {
		// A peer that held the lock for the whole window was very likely doing
		// exactly this work. Its committed snapshot is the answer we wanted, so
		// prefer it over reporting contention to a caller that would degrade to
		// an in-band download.
		if errors.Is(err, ErrLockContended) {
			if cached, ok := committedResult(modelsPath, normalized); ok {
				return cached, nil
			}
		}
		return Result{}, err
	}
	defer func() {
		if err := artifactLock.Unlock(); err != nil {
			xlog.Warn("failed to unlock model artifact", "lock", layout.Lock, "error", err)
		}
	}()
	if cached, ok := committedResult(modelsPath, normalized); ok {
		return cached, nil
	}
	if err := removeInvalidFinal(layout); err != nil {
		return Result{}, err
	}
	return m.materializeLocked(ctx, modelsPath, normalized, snapshot, token, layout)
}

// isLockContention reports whether a failed lock attempt means "somebody else
// holds it" rather than "this will never work".
//
// Only EWOULDBLOCK is portable, and it is the only errno gofrs/flock treats as
// contention. Network filesystems translate their own protocol status codes
// instead: CIFS/SMB maps STATUS_LOCK_NOT_GRANTED and STATUS_FILE_LOCK_CONFLICT
// to EACCES, and a busy share can surface EBUSY. Both look like hard errors and
// aborted materialization outright on a shared /models (#10981).
//
// EACCES is ambiguous at the syscall boundary, where it also spells "permission
// denied", but it is not ambiguous at this call site. The lock file was already opened
// O_CREATE|O_RDWR before we get here, so a genuine permission problem would
// have failed the open with an *fs.PathError naming the path. flock(2) itself
// documents no EACCES on Linux (EBADF, EINTR, EINVAL, ENOLCK, EWOULDBLOCK), so
// a bare EACCES from the lock call can only have come from a network
// filesystem's lock-conflict translation. The wait is bounded regardless, so
// even a misclassification degrades to a delay, not a hang.
func isLockContention(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) ||
		errors.Is(err, syscall.EAGAIN) ||
		errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EBUSY)
}

// acquireLock blocks until the artifact lock is held, the context is done, or
// the wait window expires with ErrLockContended.
func (m *Manager) acquireLock(ctx context.Context, locker Locker, lockPath string) error {
	wait := m.lockWait
	if wait <= 0 {
		wait = DefaultLockWait
	}
	deadline := time.Now().Add(wait)
	interval := initialLockRetryInterval
	waited := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		locked, err := locker.TryLock()
		if err != nil && !isLockContention(err) {
			return err
		}
		if locked {
			if waited {
				xlog.Info("acquired the model artifact lock after waiting for another replica", "lock", lockPath)
			}
			return nil
		}
		if !waited {
			waited = true
			xlog.Info("another replica is materializing this model artifact; waiting for it", "lock", lockPath)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s", ErrLockContended, lockPath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		if interval < maxLockRetryInterval {
			interval = min(interval*2, maxLockRetryInterval)
		}
	}
}

func removeInvalidFinal(layout Layout) error {
	root, err := os.OpenRoot(layout.Root)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	relative, err := filepath.Rel(layout.Root, layout.Final)
	if err != nil || filepath.Dir(relative) != "huggingface" || !cacheKeyPattern.MatchString(filepath.Base(relative)) {
		return fmt.Errorf("refusing to remove invalid artifact path %q", layout.Final)
	}
	return root.RemoveAll(relative)
}

func (m *Manager) materializeLocked(ctx context.Context, modelsPath string, spec Spec, snapshot hfapi.Snapshot, token string, layout Layout) (Result, error) {
	layout, err := layout.WithWriter(m.writerID)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(layout.PartialRoot, 0o750); err != nil {
		return Result{}, err
	}
	// Reclaim before staging, while the lock is held, so the two operations
	// that touch foreign trees only ever run when a peer that respects the lock
	// cannot be writing.
	if removed, err := SweepStalePartialTrees(modelsPath, PartialOrphanTTL, layout.Partial); err != nil {
		xlog.Warn("failed to sweep abandoned artifact partials", "error", err)
	} else if removed > 0 {
		xlog.Info("reclaimed abandoned artifact partials", "count", removed)
	}
	adoptOrphanPartial(layout)
	if err := os.MkdirAll(layout.Partial, 0o750); err != nil {
		return Result{}, err
	}
	root, err := os.OpenRoot(layout.Partial)
	if err != nil {
		return Result{}, err
	}
	rootClosed := false
	defer func() {
		if !rootClosed {
			_ = root.Close()
		}
	}()
	if err := root.MkdirAll(".downloads", 0o750); err != nil {
		return Result{}, err
	}
	if err := root.MkdirAll("snapshot", 0o750); err != nil {
		return Result{}, err
	}
	totalBytes := int64(0)
	if len(snapshot.Files) == 0 {
		return Result{}, fmt.Errorf("resolved snapshot contains no selected files")
	}
	seenPaths := make(map[string]struct{}, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if err := ValidateRelativeHubPath(file.Path); err != nil {
			return Result{}, err
		}
		if file.Size < 0 || totalBytes > int64(^uint64(0)>>1)-file.Size {
			return Result{}, fmt.Errorf("invalid aggregate snapshot size")
		}
		if _, exists := seenPaths[file.Path]; exists {
			return Result{}, fmt.Errorf("duplicate Hub path %q", file.Path)
		}
		seenPaths[file.Path] = struct{}{}
		totalBytes += file.Size
	}

	// Files land in manifest.Files at their snapshot index, not in completion
	// order, so a mix of skipped and freshly downloaded files still records the
	// manifest in the resolved snapshot's order. committedResult and staging both
	// read this manifest, and getting its order or contents wrong would make a
	// corrupt tree look valid.
	manifest := Manifest{Version: ManifestVersion, Artifact: spec, Files: make([]ManifestFile, len(snapshot.Files))}
	completedBytes := int64(0)
	skippedFiles := 0
	skippedBytes := int64(0)
	tasks := make([]downloader.FileTask, 0, len(snapshot.Files))
	for index, file := range snapshot.Files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		file := file
		taskIndex := index
		// A file already present and verified in this staging tree survives a
		// restart: an interrupted pass promotes each completed file into
		// snapshot/ before it moves on, so on re-entry (a resubmit, a controller
		// roll, an adopted orphan) we must resume past it rather than re-fetch
		// tens of gigabytes from scratch. Verification reuses the exact happy-path
		// check so the recorded manifest entry is byte-for-byte identical to the
		// one a fresh download would have produced; a file that fails it falls
		// through to a normal re-download.
		snapshotRel := path.Join("snapshot", file.Path)
		snapshotAbs := filepath.Join(layout.Partial, filepath.FromSlash(snapshotRel))
		if entry, ok := reuseMaterializedFile(snapshotAbs, file); ok {
			manifest.Files[taskIndex] = entry
			completedBytes += file.Size
			skippedFiles++
			skippedBytes += file.Size
			continue
		}
		nameSum := sha256.Sum256([]byte(file.Path))
		blobRel := path.Join(".downloads", hex.EncodeToString(nameSum[:]))
		blobAbs := filepath.Join(layout.Partial, filepath.FromSlash(blobRel))
		task := downloader.FileTask{
			URI:         downloader.URI(file.URL),
			Destination: blobAbs,
			SHA256:      file.LFSOID,
			FileIndex:   taskIndex,
			TotalFiles:  len(snapshot.Files),
			Options: []downloader.DownloadOption{
				downloader.WithBearerToken(token),
				downloader.WithTransferProgress(func(event downloader.TransferProgress) {
					ReportProgress(ctx, ProgressEvent{
						Phase:          PhaseDownloading,
						Artifact:       spec.Name,
						File:           file.Path,
						CurrentBytes:   completedBytes + event.Written,
						TotalBytes:     totalBytes,
						CompletedFiles: taskIndex,
						TotalFiles:     len(snapshot.Files),
					})
				}),
			},
			AfterDownload: func(string) error {
				ReportProgress(ctx, ProgressEvent{
					Phase:          PhaseVerifying,
					Artifact:       spec.Name,
					File:           file.Path,
					CurrentBytes:   completedBytes + file.Size,
					TotalBytes:     totalBytes,
					CompletedFiles: taskIndex,
					TotalFiles:     len(snapshot.Files),
				})
				entry, err := verifyDownloadedFile(blobAbs, file)
				if err != nil {
					_ = root.Remove(blobRel)
					return err
				}
				destination := path.Join("snapshot", file.Path)
				if err := root.MkdirAll(path.Dir(destination), 0o750); err != nil {
					return err
				}
				// A freshly downloaded file replaces whatever sits at the
				// destination (a stale or unverifiable leftover); a file we chose
				// to keep never reaches this path, so the removal only ever
				// discards bytes we are about to overwrite.
				_ = root.Remove(destination)
				if err := root.Rename(blobRel, destination); err != nil {
					return err
				}
				manifest.Files[taskIndex] = entry
				completedBytes += file.Size
				return nil
			},
		}
		tasks = append(tasks, task)
	}
	// Surface resume at INFO: the absence of this signal is part of what made a
	// never-converging download invisible in production, where each restart
	// silently re-fetched every completed file.
	if skippedFiles > 0 {
		xlog.Info("resuming artifact materialization; keeping already-completed files",
			"artifact", spec.Name,
			"skipped_files", skippedFiles,
			"skipped_bytes", skippedBytes,
			"remaining_files", len(tasks),
			"total_files", len(snapshot.Files))
	}
	if err := downloader.DownloadFilesWithContext(ctx, tasks, nil); err != nil {
		return Result{}, err
	}
	if err := root.RemoveAll(".downloads"); err != nil {
		return Result{}, err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := root.WriteFile("manifest.json.tmp", append(encoded, '\n'), 0o644); err != nil {
		return Result{}, err
	}
	if err := root.Rename("manifest.json.tmp", "manifest.json"); err != nil {
		return Result{}, err
	}
	if err := root.Close(); err != nil {
		return Result{}, err
	}
	rootClosed = true
	if err := os.MkdirAll(filepath.Dir(layout.Final), 0o750); err != nil {
		return Result{}, err
	}
	ReportProgress(ctx, ProgressEvent{Phase: PhaseCommitting, Artifact: spec.Name, CurrentBytes: totalBytes, TotalBytes: totalBytes, CompletedFiles: len(snapshot.Files), TotalFiles: len(snapshot.Files)})
	return m.commit(modelsPath, spec, layout, manifest)
}

// commit publishes this writer's staging tree under the artifact's final path.
//
// The rename is atomic and refuses to land on a populated destination, so a
// peer either published before us or has not published at all. Losing that race
// is not an error worth surfacing: the artifact is content-addressed, so the
// peer's tree holds the same bytes we just downloaded and verified. Adopting it
// and dropping ours is what keeps a broken lock cheap - without this, two
// writers racing to commit would hand one caller a bare ENOTEMPTY for work that
// actually succeeded.
func (m *Manager) commit(modelsPath string, spec Spec, layout Layout, manifest Manifest) (Result, error) {
	if err := os.Rename(layout.Partial, layout.Final); err != nil {
		cached, ok := committedResult(modelsPath, spec)
		if !ok {
			return Result{}, err
		}
		xlog.Info("another writer published this artifact first; discarding the duplicate", "partial", layout.Partial)
		if rmErr := removePartialTree(layout.PartialRoot, layout.Partial); rmErr != nil {
			xlog.Warn("failed to discard a duplicate artifact partial", "partial", layout.Partial, "error", rmErr)
		}
		return cached, nil
	}
	relative, err := RelativeSnapshotPath(spec.Resolved.CacheKey)
	if err != nil {
		return Result{}, err
	}
	return Result{Spec: spec, RelativePath: relative, Manifest: manifest}, nil
}

// reuseMaterializedFile reports whether a file already staged in this tree's
// snapshot/ can be kept as-is, returning the manifest entry it should
// contribute. It is the resume counterpart to the download path: a completed
// file is promoted into snapshot/ before the pass moves on, so on re-entry we
// verify what is there and skip the fetch instead of restarting from the first
// shard.
//
// Verification is a full re-hash via the same verifyDownloadedFile the happy
// path uses, not a size-only check. The manifest requires a SHA-256 for every
// file, and a non-LFS file carries no precomputed SHA-256 to borrow, so a hash
// is unavoidable for the manifest's sake; doing it through the shared verifier
// also guarantees the kept entry is byte-for-byte identical to a freshly
// downloaded one and re-checks integrity for free. Reading a large file from
// local disk is still orders of magnitude cheaper than re-downloading it. A
// file that is missing, the wrong size, or fails verification is not reused; the
// caller re-downloads it.
func reuseMaterializedFile(fileName string, source hfapi.SnapshotFile) (ManifestFile, bool) {
	info, err := os.Stat(fileName)
	if err != nil || !info.Mode().IsRegular() || info.Size() != source.Size {
		return ManifestFile{}, false
	}
	entry, err := verifyDownloadedFile(fileName, source)
	if err != nil {
		return ManifestFile{}, false
	}
	return entry, true
}

func verifyDownloadedFile(fileName string, source hfapi.SnapshotFile) (ManifestFile, error) {
	file, err := os.Open(fileName)
	if err != nil {
		return ManifestFile{}, err
	}
	defer func() { _ = file.Close() }()
	sha256Hash := sha256.New()
	gitHash := sha1.New()
	if _, err := fmt.Fprintf(gitHash, "blob %d%c", source.Size, byte(0)); err != nil {
		return ManifestFile{}, err
	}
	if _, err := io.Copy(io.MultiWriter(sha256Hash, gitHash), file); err != nil {
		return ManifestFile{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return ManifestFile{}, err
	}
	if info.Size() != source.Size {
		return ManifestFile{}, fmt.Errorf("size mismatch for %q", source.Path)
	}
	rawSHA256 := hex.EncodeToString(sha256Hash.Sum(nil))
	if source.LFSOID != "" {
		if decoded, err := hex.DecodeString(source.LFSOID); err != nil || len(decoded) != sha256.Size {
			return ManifestFile{}, fmt.Errorf("invalid LFS SHA-256 for %q", source.Path)
		}
		if !strings.EqualFold(rawSHA256, source.LFSOID) {
			return ManifestFile{}, fmt.Errorf("LFS SHA-256 mismatch for %q", source.Path)
		}
	} else if source.BlobOID != "" {
		if decoded, err := hex.DecodeString(source.BlobOID); err != nil || len(decoded) != sha1.Size {
			return ManifestFile{}, fmt.Errorf("invalid Git blob OID for %q", source.Path)
		}
		if !strings.EqualFold(hex.EncodeToString(gitHash.Sum(nil)), source.BlobOID) {
			return ManifestFile{}, fmt.Errorf("Git blob OID mismatch for %q", source.Path)
		}
	}
	return ManifestFile{
		Path: source.Path, Size: source.Size, SHA256: rawSHA256,
		BlobOID: source.BlobOID, LFSOID: source.LFSOID, XetHash: source.XetHash,
	}, nil
}
