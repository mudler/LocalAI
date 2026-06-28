package oci

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// failingReader yields prefix bytes then returns err, simulating a connection
// dropped mid-stream while downloading a layer.
type failingReader struct {
	prefix []byte
	off    int
	err    error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.off < len(r.prefix) {
		n := copy(p, r.prefix[r.off:])
		r.off += n
		return n, nil
	}
	return 0, r.err
}

// fakeLayer is a minimal v1.Layer whose Compressed() fails failUntil times with
// err (after emitting a partial prefix) before finally returning data in full.
type fakeLayer struct {
	data      []byte
	failUntil int
	err       error
	calls     int
}

func (f *fakeLayer) Digest() (v1.Hash, error)            { return v1.Hash{}, nil }
func (f *fakeLayer) DiffID() (v1.Hash, error)            { return v1.Hash{}, nil }
func (f *fakeLayer) Size() (int64, error)                { return int64(len(f.data)), nil }
func (f *fakeLayer) MediaType() (types.MediaType, error) { return types.DockerLayer, nil }
func (f *fakeLayer) Uncompressed() (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeLayer) Compressed() (io.ReadCloser, error) {
	f.calls++
	if f.calls <= f.failUntil {
		return io.NopCloser(&failingReader{prefix: []byte("partial-garbage"), err: f.err}), nil
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

var _ = Describe("downloadLayerToFile", func() {
	var (
		dst         *os.File
		restoreWait func()
	)

	BeforeEach(func() {
		var err error
		dst, err = os.CreateTemp("", "layer-retry-*.tar.gz")
		Expect(err).NotTo(HaveOccurred())

		// Eliminate the real backoff sleep so the test is fast.
		prev := layerRetryBackoff
		layerRetryBackoff = func(int) time.Duration { return 0 }
		restoreWait = func() { layerRetryBackoff = prev }
	})

	AfterEach(func() {
		restoreWait()
		_ = dst.Close()
		_ = os.Remove(dst.Name())
	})

	It("retries on unexpected EOF and writes the complete layer", func() {
		layer := &fakeLayer{
			data:      []byte("the-real-layer-contents"),
			failUntil: 2,
			err:       io.ErrUnexpectedEOF,
		}

		err := downloadLayerToFile(context.Background(), layer, dst, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(layer.calls).To(Equal(3))

		got, err := os.ReadFile(dst.Name())
		Expect(err).NotTo(HaveOccurred())
		// The partial data from the two failed attempts must have been
		// discarded, leaving exactly the real contents.
		Expect(string(got)).To(Equal("the-real-layer-contents"))
	})

	It("does not retry on a non-retryable error", func() {
		layer := &fakeLayer{
			data:      []byte("never-reached"),
			failUntil: 1,
			err:       errors.New("permission denied"),
		}

		err := downloadLayerToFile(context.Background(), layer, dst, nil)
		Expect(err).To(HaveOccurred())
		Expect(layer.calls).To(Equal(1))
	})

	It("gives up after exhausting retries on a persistent transient error", func() {
		layer := &fakeLayer{
			data:      []byte("unreachable"),
			failUntil: 1000,
			err:       io.ErrUnexpectedEOF,
		}

		err := downloadLayerToFile(context.Background(), layer, dst, nil)
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		Expect(layer.calls).To(Equal(layerDownloadRetries + 1))
	})
})
