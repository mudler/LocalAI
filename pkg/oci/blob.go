package oci

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/pkg/credentials"
	"github.com/mudler/LocalAI/pkg/xio"
	"github.com/mudler/xlog"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// plainHTTPRegistries lists registry hosts that are reached over plain HTTP.
// It is empty unless UseOllamaRegistry registers one, for a local mirror or a
// test registry.
var (
	plainHTTPMu         sync.RWMutex
	plainHTTPRegistries = map[string]bool{}
)

func isPlainHTTPRegistry(host string) bool {
	plainHTTPMu.RLock()
	defer plainHTTPMu.RUnlock()
	return plainHTTPRegistries[host]
}

// openBlob connects to the repository r and opens the blob named by the digest
// reference. The caller must close the reader and must verify the bytes it
// reads against the returned descriptor: oras does not do it while reading.
func openBlob(ctx context.Context, r, reference string) (ocispec.Descriptor, io.ReadCloser, error) {
	repo, err := remote.NewRepository(r)
	if err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("failed to create repository: %v", err)
	}
	repo.SkipReferrersGC = true
	repo.PlainHTTP = isPlainHTTPRegistry(repo.Reference.Registry)

	// Identify LocalAI to the registry. This mirrors oras' auth.DefaultClient
	// (same retry policy) but advertises a LocalAI User-Agent instead of the
	// library default, and keeps the credential off cross-origin redirects.
	client := &auth.Client{
		Client: registryHTTPClient(),
		Cache:  auth.NewCache(),
	}
	client.SetUserAgent(UserAgent())
	client.Credential = credentials.OrasCredential(r)
	repo.Client = client

	// https://github.com/oras-project/oras/blob/main/cmd/oras/internal/option/remote.go#L364
	// https://github.com/oras-project/oras/blob/main/cmd/oras/root/blob/fetch.go#L136
	desc, reader, err := oras.Fetch(ctx, repo.Blobs(), reference, oras.DefaultFetchOptions)
	if err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("failed to fetch image: %v", err)
	}
	return desc, reader, nil
}

// copyVerified copies the blob described by desc from reader to w and fails
// unless the bytes match the size and the sha256 digest of desc. The registry
// may send more bytes than it declared, so the read is capped one byte past
// the declared size to let the verifier notice.
func copyVerified(ctx context.Context, w io.Writer, reader io.Reader, desc ocispec.Descriptor) error {
	verifier := content.NewVerifyReader(io.LimitReader(reader, desc.Size+1), desc)
	if _, err := xio.Copy(ctx, w, verifier); err != nil {
		return err
	}
	if err := verifier.Verify(); err != nil {
		return fmt.Errorf("blob %s failed verification: %w", desc.Digest, err)
	}
	return nil
}

// FetchImageBlob downloads the blob named by the digest reference from the
// repository r into the file dst. The size and the sha256 digest are verified
// while reading. When anything fails, including a digest mismatch, dst is
// removed so no partial or unverified file is left behind.
func FetchImageBlob(ctx context.Context, r, reference, dst string, statusReader func(ocispec.Descriptor) io.Writer) (err error) {
	desc, reader, err := openBlob(ctx, r, reference)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	fs, err := os.Create(dst) // #nosec G304 -- dst is the download path the caller builds under its models folder
	if err != nil {
		return err
	}
	defer func() {
		if cerr := fs.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			if rmErr := os.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
				xlog.Debug("Could not remove a partially downloaded blob", "path", dst, "error", rmErr)
			}
		}
	}()

	var w io.Writer = fs
	if statusReader != nil {
		w = io.MultiWriter(fs, statusReader(desc))
	}
	return copyVerified(ctx, w, reader, desc)
}

// FetchImageBlobBytes reads a small blob into memory, verified like
// FetchImageBlob. A blob larger than limit bytes is refused before it is read.
func FetchImageBlobBytes(ctx context.Context, r, reference string, limit int64) ([]byte, error) {
	desc, reader, err := openBlob(ctx, r, reference)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	if desc.Size > limit {
		return nil, fmt.Errorf("blob %s is %d bytes, more than the %d bytes allowed", desc.Digest, desc.Size, limit)
	}
	var buf bytes.Buffer
	if err := copyVerified(ctx, &buf, reader, desc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PeekImageBlob returns the first n bytes of a blob without reading the rest.
// The prefix cannot be checked against the digest, so use it only to decide
// whether to download the blob, never as trusted content.
func PeekImageBlob(ctx context.Context, r, reference string, n int) ([]byte, error) {
	_, reader, err := openBlob(ctx, r, reference)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	head := make([]byte, n)
	read, err := io.ReadFull(reader, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return head[:read], nil
}

// ErrNotGGUF is returned when a file that must be a GGUF model does not start
// with the GGUF magic bytes.
var ErrNotGGUF = fmt.Errorf("this model is not a GGUF file")

// CheckGGUF verifies that the file at path starts with the 4 bytes "GGUF". A
// file that does not is removed, so a wrong download is never mistaken for a
// model.
func CheckGGUF(path string) error {
	f, err := os.Open(path) // #nosec G304 -- path is the model file the importer just downloaded
	if err != nil {
		return err
	}
	head := make([]byte, 4)
	n, readErr := io.ReadFull(f, head)
	_ = f.Close()
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return readErr
	}
	if string(head[:n]) == "GGUF" {
		return nil
	}
	if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
		xlog.Debug("Could not remove a file that is not a GGUF model", "path", path, "error", rmErr)
	}
	return fmt.Errorf("%s: %w", filepath.Base(path), ErrNotGGUF)
}
