package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mudler/LocalAI/pkg/credentials"
	"github.com/mudler/xlog"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

const (
	// DefaultMaxArtifactLayers and DefaultMaxArtifactBytes bound what an
	// unattended pull from a registry can cost on disk. A gallery tree is a
	// handful of small YAML files, so the defaults are generous enough that a
	// legitimate publisher never meets them.
	DefaultMaxArtifactLayers = 1024
	DefaultMaxArtifactBytes  = int64(512 << 20)
)

// ArtifactPullOption configures PullArtifact.
type ArtifactPullOption func(*artifactPullOptions)

type artifactPullOptions struct {
	artifactType string
	maxLayers    int
	maxBytes     int64
}

// WithArtifactType pins the artifactType the manifest must declare. It is
// mandatory: without it a plain container image could be unpacked as if it
// were the caller's own artifact kind.
func WithArtifactType(artifactType string) ArtifactPullOption {
	return func(o *artifactPullOptions) { o.artifactType = artifactType }
}

// WithMaxArtifactLayers caps how many layers the artifact may carry.
func WithMaxArtifactLayers(layers int) ArtifactPullOption {
	return func(o *artifactPullOptions) { o.maxLayers = layers }
}

// WithMaxArtifactBytes caps the total declared size of the artifact.
func WithMaxArtifactBytes(bytes int64) ArtifactPullOption {
	return func(o *artifactPullOptions) { o.maxBytes = bytes }
}

// PullArtifact pulls an ORAS artifact and lays its layers out under dest,
// each at the relative path in its org.opencontainers.image.title annotation,
// so a published tree keeps its subdirectories. It returns the resolved
// manifest digest, which callers pin on and verify signatures against.
//
// Everything in the manifest is remote input and this writes files, so the
// whole manifest is validated before the first byte is fetched: a layer that
// would land outside dest, an unexpected artifact type or a tree over the
// caps aborts the pull with nothing written.
func PullArtifact(ctx context.Context, ref, dest string, opts ...ArtifactPullOption) (string, error) {
	options := artifactPullOptions{
		maxLayers: DefaultMaxArtifactLayers,
		maxBytes:  DefaultMaxArtifactBytes,
	}
	for _, o := range opts {
		o(&options)
	}
	if options.artifactType == "" {
		return "", fmt.Errorf("no expected artifact type given for %q", ref)
	}

	root, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}

	repo, err := remote.NewRepository(ref)
	if err != nil {
		return "", fmt.Errorf("failed to create repository: %w", err)
	}
	repo.SkipReferrersGC = true
	// Loopback and private-network registries are reached over plain HTTP,
	// matching how the go-containerregistry paths in this package resolve the
	// scheme, so a local registry behaves the same whichever puller is used.
	repo.PlainHTTP = plainHTTP(repo.Reference.Registry)

	client := &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.NewCache(),
	}
	client.SetUserAgent(UserAgent())
	client.Credential = credentials.OrasCredential(repo.Reference.Registry + "/" + repo.Reference.Repository)
	repo.Client = client

	manifestDesc, rc, err := repo.FetchReference(ctx, repo.Reference.ReferenceOrDefault())
	if err != nil {
		return "", fmt.Errorf("failed to resolve %q: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()

	if manifestDesc.Size > options.maxBytes {
		return "", fmt.Errorf("manifest of %q is %d bytes, over the %d byte limit", ref, manifestDesc.Size, options.maxBytes)
	}
	raw, err := content.ReadAll(rc, manifestDesc)
	if err != nil {
		return "", fmt.Errorf("failed to read the manifest of %q: %w", ref, err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("failed to parse the manifest of %q: %w", ref, err)
	}

	// An OCI 1.0 packer records the type in the config media type instead, so
	// artifacts pushed by older tooling still identify themselves.
	artifactType := manifest.ArtifactType
	if artifactType == "" {
		artifactType = manifest.Config.MediaType
	}
	if artifactType != options.artifactType {
		return "", fmt.Errorf("%q is a %q artifact, expected %q", ref, artifactType, options.artifactType)
	}

	if len(manifest.Layers) > options.maxLayers {
		return "", fmt.Errorf("%q has %d layers, over the %d layer limit", ref, len(manifest.Layers), options.maxLayers)
	}

	targets := make([]string, len(manifest.Layers))
	var total int64
	for i, layer := range manifest.Layers {
		target, err := artifactLayerPath(root, layer.Annotations[ocispec.AnnotationTitle])
		if err != nil {
			return "", fmt.Errorf("refusing layer %d of %q: %w", i, ref, err)
		}
		targets[i] = target
		if layer.Size < 0 {
			return "", fmt.Errorf("layer %d of %q declares a negative size", i, ref)
		}
		total += layer.Size
		if total > options.maxBytes {
			return "", fmt.Errorf("%q is at least %d bytes, over the %d byte limit", ref, total, options.maxBytes)
		}
	}

	for i, layer := range manifest.Layers {
		if err := writeArtifactLayer(ctx, repo, layer, targets[i]); err != nil {
			return "", fmt.Errorf("failed to write layer %d of %q: %w", i, ref, err)
		}
	}

	return manifestDesc.Digest.String(), nil
}

// plainHTTP mirrors go-containerregistry's scheme detection, which the image
// paths of this package already rely on, so both agree on which registries are
// not expected to serve TLS.
func plainHTTP(registry string) bool {
	r, err := name.NewRegistry(registry)
	if err != nil {
		return false
	}
	return r.Scheme() == "http"
}

// artifactLayerPath resolves a layer title against root. The title comes from
// the registry, so it is treated as hostile: anything that is not a plain
// relative path inside root is refused rather than sanitized, since a title
// that needed rewriting is not a title a publisher meant.
func artifactLayerPath(root, title string) (string, error) {
	if title == "" {
		return "", fmt.Errorf("the layer has no %s title", ocispec.AnnotationTitle)
	}
	if strings.ContainsAny(title, "\\\x00") {
		return "", fmt.Errorf("title %q contains a path separator or NUL byte that is not valid in an artifact title", title)
	}
	if path.IsAbs(title) || filepath.IsAbs(title) || filepath.VolumeName(title) != "" {
		return "", fmt.Errorf("title %q is an absolute path", title)
	}
	clean := path.Clean(title)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("title %q escapes the destination directory", title)
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("title %q escapes the destination directory", title)
	}
	return target, nil
}

func writeArtifactLayer(ctx context.Context, repo *remote.Repository, layer ocispec.Descriptor, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	blob, err := repo.Fetch(ctx, layer)
	if err != nil {
		return err
	}
	defer func() { _ = blob.Close() }()

	// O_EXCL keeps the write from following a symlink already sitting at the
	// target, and makes two layers claiming the same title an error instead of
	// a silent overwrite.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}

	// A registry that serves more bytes than the descriptor declares would
	// otherwise blow past the size cap already accounted for.
	verifier := content.NewVerifyReader(io.LimitReader(blob, layer.Size+1), layer)
	_, err = io.Copy(f, verifier)
	if err == nil {
		err = verifier.Verify()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		// Content that failed to verify must not be left behind for a caller
		// to read as if it were the published layer.
		if rmErr := os.Remove(target); rmErr != nil {
			xlog.Debug("Could not remove a partially written artifact layer", "path", target, "error", rmErr)
		}
		return err
	}
	return nil
}
