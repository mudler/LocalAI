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

// downloadLayerToFile streams a single compressed layer into dst, retrying on
// transient network errors (unexpected EOF, connection reset, ...). Large
// backend images (e.g. vLLM) are several GiB and a single dropped connection
// mid-stream previously failed the whole install with "unexpected EOF" and no
// recovery. The registry transport already retries manifest fetches via
// defaultRetryPredicate (see GetImage/GetImageDigest); this extends the same
// behaviour to the layer data stream. See issue #10577.
func downloadLayerToFile(ctx context.Context, layer v1.Layer, dst *os.File, progress *progressWriter) error {
	var lastErr error
	for attempt := 0; attempt <= layerDownloadRetries; attempt++ {
		if attempt > 0 {
			// Discard any partial data from the previous failed attempt.
			if _, err := dst.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if err := dst.Truncate(0); err != nil {
				return err
			}
			if progress != nil {
				progress.written = 0
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(layerRetryBackoff(attempt)):
			}
		}

		var w io.Writer = dst
		if progress != nil {
			w = io.MultiWriter(dst, progress)
		}

		var reader io.ReadCloser
		reader, lastErr = layer.Compressed()
		if lastErr == nil {
			_, lastErr = xio.Copy(ctx, w, reader)
			_ = reader.Close()
		}
		if lastErr == nil {
			return nil
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

		// Download the compressed layer, retrying on transient network errors.
		err = downloadLayerToFile(ctx, layer, file, progress)
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

	// Create a local image from the downloaded layers
	localImg, err := mutate.AppendLayers(img, downloadedLayers...)
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

	// Second pass: materialise links that the filesystem could not represent.
	for _, link := range pending {
		if err := copyFilePreservingMode(link.targetPath, link.path); err != nil {
			return fmt.Errorf("failed to copy link target %s -> %s: %w", link.targetPath, link.path, err)
		}
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
