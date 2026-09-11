package oci

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/archive"
	registrytypes "github.com/docker/docker/api/types/registry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/pkg/xio"
)

// ref: https://github.com/mudler/luet/blob/master/pkg/helpers/docker/docker.go#L117
type staticAuth struct {
	auth *registrytypes.AuthConfig
}

func (s staticAuth) Authorization() (*authn.AuthConfig, error) {
	if s.auth == nil {
		return nil, nil
	}
	return &authn.AuthConfig{
		Username:      s.auth.Username,
		Password:      s.auth.Password,
		Auth:          s.auth.Auth,
		IdentityToken: s.auth.IdentityToken,
		RegistryToken: s.auth.RegistryToken,
	}, nil
}

var defaultRetryBackoff = remote.Backoff{
	Duration: 1.0 * time.Second,
	Factor:   3.0,
	Jitter:   0.1,
	Steps:    3,
}

var defaultRetryPredicate = func(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || strings.Contains(err.Error(), "connection refused") {
		logs.Warn.Printf("retrying %v", err)
		return true
	}
	return false
}

// layerDownloadRetries is the number of additional attempts made when a layer
// download fails with a transient/retryable network error.
var layerDownloadRetries = 3

// layerRetryBackoff returns the wait before retry attempt n (1-indexed). It is a
// variable so tests can eliminate the wait.
var layerRetryBackoff = func(attempt int) time.Duration {
	d := defaultRetryBackoff.Duration
	for i := 1; i < attempt; i++ {
		d = time.Duration(float64(d) * defaultRetryBackoff.Factor)
	}
	return d
}

// blobRangeOpener re-opens a layer blob at a byte offset. It returns the
// stream and the offset it actually starts at: the requested offset when the
// server honoured the Range request, or 0 when it ignored it and is sending
// the blob from the first byte again.
type blobRangeOpener func(ctx context.Context, offset int64) (io.ReadCloser, int64, error)

// newBlobRangeOpener returns a blobRangeOpener that re-fetches the layer's
// blob from its registry with an HTTP Range request. Registries like quay.io
// redirect blob downloads to pre-signed S3/CDN URLs that expire after ~10
// minutes; on a slow connection a multi-GiB layer cannot finish inside that
// window, so restarting from byte zero can never succeed while resuming from
// the current offset can (docker pull survives the same expiry this way).
// Each call goes back to the registry, so it obtains a fresh redirect URL and
// a fresh auth token. Returns nil when imageRef does not name a registry blob
// (e.g. local tarballs), which disables resuming. See issue #10577.
func newBlobRangeOpener(imageRef string, layer v1.Layer, auth *registrytypes.AuthConfig, base http.RoundTripper) blobRangeOpener {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil
	}
	digest, err := layer.Digest()
	if err != nil || digest.Hex == "" {
		return nil
	}
	repo := ref.Context()
	if base == nil {
		base = http.DefaultTransport
	}
	var authenticator authn.Authenticator
	if auth != nil {
		authenticator = staticAuth{auth}
	} else if authenticator, err = authn.DefaultKeychain.Resolve(repo.Registry); err != nil {
		authenticator = authn.Anonymous
	}
	blobURL := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", repo.Registry.Scheme(), repo.RegistryStr(), repo.RepositoryStr(), digest.String())

	return func(ctx context.Context, offset int64) (io.ReadCloser, int64, error) {
		tr, err := transport.NewWithContext(ctx, repo.Registry, authenticator, base, []string{repo.Scope(transport.PullScope)})
		if err != nil {
			return nil, 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
		if err != nil {
			return nil, 0, err
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		req.Header.Set("User-Agent", UserAgent())
		resp, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			return nil, 0, err
		}
		switch resp.StatusCode {
		case http.StatusPartialContent:
			return resp.Body, offset, nil
		case http.StatusOK:
			return resp.Body, 0, nil
		default:
			_ = resp.Body.Close()
			return nil, 0, fmt.Errorf("unexpected status %d resuming blob %s", resp.StatusCode, digest.String())
		}
	}
}

// verifyLayerFile proves the assembled layer file matches the digest the
// registry advertised. A resumed download splices bytes from independent HTTP
// responses and bypasses the verified reader layer.Compressed() provides, so
// the whole file must be re-checked before it is trusted.
func verifyLayerFile(layer v1.Layer, f *os.File) error {
	digest, err := layer.Digest()
	if err != nil || digest.Hex == "" || digest.Algorithm != "sha256" {
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	got, _, err := v1.SHA256(f)
	if err != nil {
		return err
	}
	if got.Hex != digest.Hex {
		return fmt.Errorf("resumed layer digest mismatch: got %s, want %s", got, digest)
	}
	return nil
}

// downloadLayerToFile streams a single compressed layer into dst, retrying on
// transient network errors (unexpected EOF, connection reset, ...). Large
// backend images (e.g. vLLM) are several GiB and a single dropped connection
// mid-stream previously failed the whole install with "unexpected EOF" and no
// recovery. When resume is non-nil, a retry keeps the bytes already on disk
// and continues from that offset instead of starting over: registries that
// serve blobs through expiring pre-signed URLs (quay.io + S3/Akamai) cut off
// every full-length transfer on slow connections, so restarting can never
// finish while resuming makes progress each round. The retry budget only
// counts attempts that made no forward progress, so a download that keeps
// advancing keeps going. See issue #10577.
func downloadLayerToFile(ctx context.Context, layer v1.Layer, dst *os.File, progress *progressWriter, resume blobRangeOpener) error {
	var lastErr error
	// written tracks the valid bytes currently in dst across attempts, and
	// bestWritten the furthest offset any attempt has reached: only beating
	// it counts as forward progress for the retry budget, so a server that
	// ignores Range requests and keeps dropping mid-stream still runs out
	// of attempts instead of looping forever.
	var written, bestWritten int64
	// resumed records whether any byte in dst came from a resumed raw blob
	// fetch, which requires re-verifying the assembled file at the end.
	resumed := false

	truncate := func() error {
		if _, err := dst.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := dst.Truncate(0); err != nil {
			return err
		}
		written = 0
		resumed = false
		if progress != nil {
			progress.written = 0
		}
		return nil
	}

	for attempt := 0; attempt <= layerDownloadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(layerRetryBackoff(attempt)):
			}
		}

		var reader io.ReadCloser
		if attempt > 0 && resume != nil && written > 0 {
			r, offset, rerr := resume(ctx, written)
			switch {
			case rerr != nil:
				// Keep the partial bytes: opening the resume stream can
				// fail transiently (token refresh, connection refused)
				// and the next attempt can still continue from here.
				lastErr = rerr
			case offset != written:
				// The server ignored the Range request and is sending
				// the blob from the first byte: drop the partial data.
				if err := truncate(); err != nil {
					_ = r.Close()
					return err
				}
				reader = r
				resumed = true
			default:
				reader = r
				resumed = true
			}
		} else {
			// First attempt, or no way to resume: restart from scratch
			// through the digest-verifying layer reader.
			if err := truncate(); err != nil {
				return err
			}
			reader, lastErr = layer.Compressed()
		}

		if reader != nil {
			var w io.Writer = dst
			if progress != nil {
				w = io.MultiWriter(dst, progress)
			}
			var n int64
			n, lastErr = xio.Copy(ctx, w, reader)
			written += n
			_ = reader.Close()
			if written > bestWritten {
				// Forward progress: don't charge this round against the
				// retry budget, or slow links would still exhaust it.
				bestWritten = written
				attempt = 0
			}
		}

		if lastErr == nil {
			if !resumed {
				return nil
			}
			verr := verifyLayerFile(layer, dst)
			if verr == nil {
				return nil
			}
			// The spliced file is corrupt: discard it and retry cleanly.
			logs.Warn.Printf("discarding resumed layer download: %v", verr)
			lastErr = verr
			if err := truncate(); err != nil {
				return err
			}
			continue
		}

		// Stop early on context cancellation or non-retryable errors.
		if ctx.Err() != nil || !defaultRetryPredicate(lastErr) {
			return lastErr
		}
		logs.Warn.Printf("layer download failed (attempt %d/%d), retrying: %v", attempt+1, layerDownloadRetries+1, lastErr)
	}
	return lastErr
}

type progressWriter struct {
	written        int64
	total          int64
	fileName       string
	downloadStatus func(string, string, string, float64)
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.written += int64(n)
	if pw.total > 0 {
		percentage := float64(pw.written) / float64(pw.total) * 100
		//log.Debug().Msgf("Downloading %s: %s/%s (%.2f%%)", pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
	} else {
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), "", 0)
	}

	return n, nil
}

// ExtractOCIImage will extract a given targetImage into a given targetDestination
func ExtractOCIImage(ctx context.Context, img v1.Image, imageRef string, targetDestination string, downloadStatus func(string, string, string, float64)) error {
	// Create a temporary tar file
	tmpTarFile, err := os.CreateTemp("", "localai-oci-*.tar")
	if err != nil {
		return fmt.Errorf("failed to create temporary tar file: %v", err)
	}
	defer os.Remove(tmpTarFile.Name())
	defer tmpTarFile.Close()

	// Download the image as tar with progress tracking
	err = DownloadOCIImageTar(ctx, img, imageRef, tmpTarFile.Name(), downloadStatus)
	if err != nil {
		return fmt.Errorf("failed to download image tar: %v", err)
	}

	// Extract the tar file to the target destination
	err = ExtractOCIImageFromTar(ctx, tmpTarFile.Name(), imageRef, targetDestination, downloadStatus)
	if err != nil {
		return fmt.Errorf("failed to extract image tar: %v", err)
	}

	return nil
}

func ParseImageParts(image string) (tag, repository, dstimage string) {
	tag = "latest"
	repository = "library"
	if strings.Contains(image, ":") {
		parts := strings.Split(image, ":")
		image = parts[0]
		tag = parts[1]
	}
	if strings.Contains("/", image) {
		parts := strings.Split(image, "/")
		repository = parts[0]
		image = parts[1]
	}
	dstimage = image
	return tag, repository, image
}

// GetImage if returns the proper image to pull with transport and auth
// tries local daemon first and then fallbacks into remote
// if auth is nil, it will try to use the default keychain https://github.com/google/go-containerregistry/tree/main/pkg/authn#tldr-for-consumers-of-this-package
func GetImage(targetImage, targetPlatform string, auth *registrytypes.AuthConfig, t http.RoundTripper) (v1.Image, error) {
	var platform *v1.Platform
	var image v1.Image
	var err error

	if targetPlatform != "" {
		platform, err = v1.ParsePlatform(targetPlatform)
		if err != nil {
			return image, err
		}
	} else {
		platform, err = v1.ParsePlatform(fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))
		if err != nil {
			return image, err
		}
	}

	ref, err := name.ParseReference(targetImage)
	if err != nil {
		return image, err
	}

	if t == nil {
		t = http.DefaultTransport
	}

	tr := transport.NewRetry(t,
		transport.WithRetryBackoff(defaultRetryBackoff),
		transport.WithRetryPredicate(defaultRetryPredicate),
	)

	opts := []remote.Option{
		remote.WithTransport(tr),
		remote.WithPlatform(*platform),
		remote.WithUserAgent(UserAgent()),
	}
	if auth != nil {
		opts = append(opts, remote.WithAuth(staticAuth{auth}))
	} else {
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	image, err = remote.Image(ref, opts...)

	return image, err
}

// GetImageDigest returns the OCI image digest for the given image reference without downloading it.
// It uses remote.Head to fetch only the descriptor, which is much cheaper than pulling the full image.
func GetImageDigest(targetImage, targetPlatform string, auth *registrytypes.AuthConfig, t http.RoundTripper) (string, error) {
	var platform *v1.Platform
	var err error

	if targetPlatform != "" {
		platform, err = v1.ParsePlatform(targetPlatform)
		if err != nil {
			return "", err
		}
	} else {
		platform, err = v1.ParsePlatform(fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))
		if err != nil {
			return "", err
		}
	}

	ref, err := name.ParseReference(targetImage)
	if err != nil {
		return "", err
	}

	if t == nil {
		t = http.DefaultTransport
	}

	tr := transport.NewRetry(t,
		transport.WithRetryBackoff(defaultRetryBackoff),
		transport.WithRetryPredicate(defaultRetryPredicate),
	)

	opts := []remote.Option{
		remote.WithTransport(tr),
		remote.WithPlatform(*platform),
		remote.WithUserAgent(UserAgent()),
	}
	if auth != nil {
		opts = append(opts, remote.WithAuth(staticAuth{auth}))
	} else {
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	desc, err := remote.Head(ref, opts...)
	if err != nil {
		return "", err
	}

	return desc.Digest.String(), nil
}

func GetOCIImageSize(targetImage, targetPlatform string, auth *registrytypes.AuthConfig, t http.RoundTripper) (int64, error) {
	var size int64
	var img v1.Image
	var err error

	img, err = GetImage(targetImage, targetPlatform, auth, t)
	if err != nil {
		return size, err
	}
	layers, _ := img.Layers()
	for _, layer := range layers {
		s, _ := layer.Size()
		size += s
	}

	return size, nil
}

// DownloadOCIImageTar downloads the compressed layers of an image and then creates an uncompressed tar
// This provides accurate size estimation and allows for later extraction
func DownloadOCIImageTar(ctx context.Context, img v1.Image, imageRef string, tarFilePath string, downloadStatus func(string, string, string, float64)) error {
	// Get layers to calculate total compressed size for estimation
	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("failed to get layers: %v", err)
	}

	// Calculate total compressed size for progress tracking
	var totalCompressedSize int64
	for _, layer := range layers {
		size, err := layer.Size()
		if err != nil {
			return fmt.Errorf("failed to get layer size: %v", err)
		}
		totalCompressedSize += size
	}

	// Create a temporary directory to store the compressed layers
	tmpDir, err := os.MkdirTemp("", "localai-oci-layers-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Download all compressed layers with progress tracking
	var downloadedLayers []v1.Layer
	var downloadedSize int64

	// Extract image name from the reference for display
	imageName := imageRef
	for i, layer := range layers {
		layerSize, err := layer.Size()
		if err != nil {
			return fmt.Errorf("failed to get layer size: %v", err)
		}

		// Create a temporary file for this layer
		layerFile := fmt.Sprintf("%s/layer-%d.tar.gz", tmpDir, i)
		file, err := os.Create(layerFile)
		if err != nil {
			return fmt.Errorf("failed to create layer file: %v", err)
		}

		// Create progress writer for this layer
		var progress *progressWriter
		if downloadStatus != nil {
			progress = &progressWriter{
				total:          totalCompressedSize,
				fileName:       fmt.Sprintf("Downloading %d/%d %s", i+1, len(layers), imageName),
				downloadStatus: downloadStatus,
			}
		}

		// Download the compressed layer, retrying on transient network
		// errors and resuming from the last byte received where possible.
		// Anonymous/default-keychain credentials match what GetImage uses
		// for every in-tree caller (they all pass a nil auth).
		err = downloadLayerToFile(ctx, layer, file, progress, newBlobRangeOpener(imageRef, layer, nil, nil))
		file.Close()
		if err != nil {
			return fmt.Errorf("failed to download layer %d: %v", i, err)
		}

		// Load the downloaded layer
		downloadedLayer, err := tarball.LayerFromFile(layerFile)
		if err != nil {
			return fmt.Errorf("failed to load downloaded layer: %v", err)
		}

		downloadedLayers = append(downloadedLayers, downloadedLayer)
		downloadedSize += layerSize
	}

	// Build the local image only from the downloaded layers. Appending them to
	// img duplicates the layer stack and makes extraction reopen the source.
	localImg, err := mutate.AppendLayers(empty.Image, downloadedLayers...)
	if err != nil {
		return fmt.Errorf("failed to create local image: %v", err)
	}

	// Now extract the uncompressed tar from the local image
	tarFile, err := os.Create(tarFilePath)
	if err != nil {
		return fmt.Errorf("failed to create tar file: %v", err)
	}
	defer tarFile.Close()

	// Extract uncompressed tar from local image
	extractReader := mutate.Extract(localImg)
	_, err = xio.Copy(ctx, tarFile, extractReader)
	if err != nil {
		return fmt.Errorf("failed to extract uncompressed tar: %v", err)
	}

	return nil
}

// ExtractOCIImageFromTar extracts an image from a previously downloaded tar file
func ExtractOCIImageFromTar(ctx context.Context, tarFilePath, imageRef, targetDestination string, downloadStatus func(string, string, string, float64)) error {
	// Open the tar file
	tarFile, err := os.Open(tarFilePath)
	if err != nil {
		return fmt.Errorf("failed to open tar file: %v", err)
	}
	defer tarFile.Close()

	// Get file size for progress tracking
	fileInfo, err := tarFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %v", err)
	}

	var reader io.Reader = tarFile
	if downloadStatus != nil {
		reader = io.TeeReader(tarFile, &progressWriter{
			total:          fileInfo.Size(),
			fileName:       fmt.Sprintf("Extracting %s", imageRef),
			downloadStatus: downloadStatus,
		})
	}

	// Extract the tar file
	_, err = archive.Apply(ctx,
		targetDestination, reader,
		archive.WithNoSameOwner())
	if err == nil {
		return nil
	}

	// Some filesystems (notably CIFS/SMB mounts, which users commonly bind as the
	// /backends volume) reject symlink/hardlink creation with "operation not
	// supported"/"operation not permitted". containerd's archive.Apply hard-fails
	// there, so no backend can be installed. Fall back to a pure-Go extractor that
	// degrades unsupported links into plain file copies. mutate.Extract already
	// flattened the layers, so this tar carries no whiteouts to interpret.
	if !isLinkUnsupportedError(err) {
		return err
	}
	logs.Warn.Printf("symlink/hardlink creation is not supported on filesystem at %q (%v), retrying extraction with links copied in place", targetDestination, err)

	// archive.Apply may have written some entries before failing; start from a
	// clean destination so the manual pass is deterministic. The caller stages
	// into an ephemeral, per-install temp directory, so wiping its contents is safe.
	if err := cleanDirContents(targetDestination); err != nil {
		return fmt.Errorf("failed to reset destination before fallback extraction: %w", err)
	}

	// Re-read the tar from the beginning for the second pass.
	if _, err := tarFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind tar for fallback extraction: %w", err)
	}
	return extractTarCopyingLinks(tarFile, targetDestination)
}

// symlink and hardlink are indirected so tests can simulate a filesystem that
// rejects link creation (e.g. CIFS/SMB).
var (
	symlink  = os.Symlink
	hardlink = os.Link
)

// isLinkUnsupportedError reports whether err indicates the destination
// filesystem cannot create symlinks or hardlinks (e.g. CIFS/SMB, some FUSE
// mounts). Such filesystems surface ENOTSUP/EOPNOTSUPP, or EPERM in some
// configurations; the error text is also matched because containerd wraps the
// syscall error into a formatted string.
func isLinkUnsupportedError(err error) bool {
	if errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.EPERM) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "operation not supported") || strings.Contains(msg, "operation not permitted")
}

// cleanDirContents removes the entries inside dir without removing dir itself,
// preserving the directory (and its permissions) the caller created.
func cleanDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// extractTarCopyingLinks extracts a flattened image tar into targetDestination,
// copying the target contents of any symlink/hardlink that the filesystem cannot
// represent. Regular symlinks are still attempted first, so link semantics are
// preserved wherever the filesystem allows it. Link copies are deferred to a
// second pass so that forward references (a link appearing before its target in
// the tar) resolve correctly.
func extractTarCopyingLinks(r io.Reader, targetDestination string) error {
	root, err := filepath.Abs(targetDestination)
	if err != nil {
		return err
	}

	type pendingLink struct {
		path       string // absolute destination path of the link
		targetPath string // absolute path of the file to copy from
	}
	var pending []pendingLink

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		cleaned, err := safeJoin(root, hdr.Name)
		if err != nil {
			return err
		}
		// Skip aufs/overlay whiteout markers defensively; a flattened tar
		// should not contain any, but ignoring them is always correct here.
		if strings.HasPrefix(filepath.Base(hdr.Name), ".wh.") {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleaned, hdr.FileInfo().Mode().Perm()|0700); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", cleaned, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(cleaned), 0700); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", cleaned, err)
			}
			if err := writeRegularFile(cleaned, tr, hdr.FileInfo().Mode().Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(cleaned), 0700); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", cleaned, err)
			}
			// Remove any pre-existing entry so os.Symlink does not fail with EEXIST.
			_ = os.Remove(cleaned)
			if err := symlink(hdr.Linkname, cleaned); err == nil {
				break
			} else if !isLinkUnsupportedError(err) {
				return fmt.Errorf("failed to create symlink %s -> %s: %w", cleaned, hdr.Linkname, err)
			}
			// Resolve the link target: absolute targets are image-root relative,
			// relative ones are resolved against the link's own directory.
			var src string
			if filepath.IsAbs(hdr.Linkname) {
				src, err = safeJoin(root, hdr.Linkname)
			} else {
				// #nosec G305 -- safeJoin rejects any result that resolves outside the extraction root
				src, err = safeJoin(root, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname))
			}
			if err != nil {
				return err
			}
			pending = append(pending, pendingLink{path: cleaned, targetPath: src})
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(cleaned), 0700); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", cleaned, err)
			}
			// Hardlink targets are always relative to the image root.
			src, err := safeJoin(root, hdr.Linkname)
			if err != nil {
				return err
			}
			_ = os.Remove(cleaned)
			if err := hardlink(src, cleaned); err == nil {
				break
			} else if !isLinkUnsupportedError(err) {
				return fmt.Errorf("failed to create hardlink %s -> %s: %w", cleaned, src, err)
			}
			pending = append(pending, pendingLink{path: cleaned, targetPath: src})
		default:
			// Ignore device nodes, fifos, etc: backend artifacts do not use them.
			logs.Debug.Printf("skipping unsupported tar entry type during fallback extraction: name=%q type=%d", hdr.Name, hdr.Typeflag)
		}
	}

	// Materialise links in dependency order. Soname links are often chained
	// (libfoo.so -> libfoo.so.1 -> libfoo.so.1.2), and archives do not guarantee
	// that the nearest-to-file link appears first.
	for len(pending) > 0 {
		var unresolved []pendingLink
		for _, link := range pending {
			if err := copyFilePreservingMode(link.targetPath, link.path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					unresolved = append(unresolved, link)
					continue
				}
				return fmt.Errorf("failed to copy link target %s -> %s: %w", link.targetPath, link.path, err)
			}
		}
		if len(unresolved) == len(pending) {
			link := unresolved[0]
			return fmt.Errorf("failed to resolve copied link target %s -> %s", link.targetPath, link.path)
		}
		pending = unresolved
	}
	return nil
}

// safeJoin joins name onto root and guarantees the result stays within root,
// rejecting path-traversal entries in a malicious tar. An absolute name (e.g. an
// absolute symlink target) is treated as image-root relative, so it is mapped
// under root rather than escaping it.
func safeJoin(root, name string) (string, error) {
	cleaned := filepath.Join(root, name)
	rel, err := filepath.Rel(root, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("tar entry escapes extraction root: %s", name)
	}
	return cleaned, nil
}

func writeRegularFile(path string, r io.Reader, mode os.FileMode) error {
	// Remove any pre-existing symlink so we do not write through it.
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(path)
	}
	// #nosec G304 -- path is validated by safeJoin to stay within the extraction root
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0600)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", path, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close file %s: %w", path, err)
	}
	return nil
}

func copyFilePreservingMode(src, dst string) error {
	// #nosec G304 -- src is a safeJoin-validated link target within the extraction root
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	_ = os.Remove(dst)
	// #nosec G304 -- dst is a safeJoin-validated path within the extraction root
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm()|0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// GetOCIImageUncompressedSize returns the total uncompressed size of an image
func GetOCIImageUncompressedSize(targetImage, targetPlatform string, auth *registrytypes.AuthConfig, t http.RoundTripper) (int64, error) {
	var totalSize int64
	var img v1.Image
	var err error

	img, err = GetImage(targetImage, targetPlatform, auth, t)
	if err != nil {
		return totalSize, err
	}

	layers, err := img.Layers()
	if err != nil {
		return totalSize, err
	}

	for _, layer := range layers {
		// Use compressed size as an approximation since uncompressed size is not directly available
		size, err := layer.Size()
		if err != nil {
			return totalSize, err
		}
		totalSize += size
	}

	return totalSize, nil
}
