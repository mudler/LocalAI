package modelartifacts

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

type SnapshotResolver interface {
	ResolveSnapshot(context.Context, hfapi.SnapshotRequest) (hfapi.Snapshot, error)
}

type Manager struct {
	resolver         SnapshotResolver
	huggingFaceToken string
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

func NewManager(resolver SnapshotResolver, options ...ManagerOption) *Manager {
	manager := &Manager{resolver: resolver}
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
	artifactLock := flock.New(layout.Lock)
	locked, err := artifactLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return Result{}, err
	}
	if !locked {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("artifact lock was not acquired")
	}
	defer func() {
		if err := artifactLock.Unlock(); err != nil {
			xlog.Warn("failed to unlock model artifact", "lock", layout.Lock, "error", err)
		}
	}()
	if err := os.Chmod(layout.Lock, 0o600); err != nil {
		return Result{}, err
	}
	if cached, ok := committedResult(modelsPath, normalized); ok {
		return cached, nil
	}
	if err := removeInvalidFinal(layout); err != nil {
		return Result{}, err
	}
	return m.materializeLocked(ctx, modelsPath, normalized, snapshot, token, layout)
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

	manifest := Manifest{Version: ManifestVersion, Artifact: spec, Files: make([]ManifestFile, 0, len(snapshot.Files))}
	completedBytes := int64(0)
	for index, file := range snapshot.Files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		nameSum := sha256.Sum256([]byte(file.Path))
		blobRel := path.Join(".downloads", hex.EncodeToString(nameSum[:]))
		blobAbs := filepath.Join(layout.Partial, filepath.FromSlash(blobRel))
		ReportProgress(ctx, ProgressEvent{Phase: PhaseDownloading, Artifact: spec.Name, File: file.Path, CurrentBytes: completedBytes, TotalBytes: totalBytes, CompletedFiles: index, TotalFiles: len(snapshot.Files)})
		err := downloader.URI(file.URL).DownloadFileWithContext(ctx, blobAbs, file.LFSOID, index, len(snapshot.Files), nil,
			downloader.WithBearerToken(token),
			downloader.WithTransferProgress(func(event downloader.TransferProgress) {
				ReportProgress(ctx, ProgressEvent{Phase: PhaseDownloading, Artifact: spec.Name, File: file.Path, CurrentBytes: completedBytes + event.Written, TotalBytes: totalBytes, CompletedFiles: index, TotalFiles: len(snapshot.Files)})
			}),
		)
		if err != nil {
			return Result{}, err
		}
		ReportProgress(ctx, ProgressEvent{Phase: PhaseVerifying, Artifact: spec.Name, File: file.Path, CurrentBytes: completedBytes + file.Size, TotalBytes: totalBytes, CompletedFiles: index, TotalFiles: len(snapshot.Files)})
		entry, err := verifyDownloadedFile(blobAbs, file)
		if err != nil {
			_ = root.Remove(blobRel)
			return Result{}, err
		}
		destination := path.Join("snapshot", file.Path)
		if err := root.MkdirAll(path.Dir(destination), 0o750); err != nil {
			return Result{}, err
		}
		_ = root.Remove(destination)
		if err := root.Rename(blobRel, destination); err != nil {
			return Result{}, err
		}
		manifest.Files = append(manifest.Files, entry)
		completedBytes += file.Size
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
	if err := os.Rename(layout.Partial, layout.Final); err != nil {
		return Result{}, err
	}
	relative, err := RelativeSnapshotPath(spec.Resolved.CacheKey)
	if err != nil {
		return Result{}, err
	}
	return Result{Spec: spec, RelativePath: relative, Manifest: manifest}, nil
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
