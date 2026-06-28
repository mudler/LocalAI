package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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

	return err
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
