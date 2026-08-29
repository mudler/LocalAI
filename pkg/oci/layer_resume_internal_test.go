package oci

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingOpener is a test blobRangeOpener that records the offsets it was
// asked to resume from and delegates the stream to open.
type recordingOpener struct {
	offsets []int64
	open    func(offset int64) (io.ReadCloser, int64, error)
}

func (o *recordingOpener) opener() blobRangeOpener {
	return func(_ context.Context, offset int64) (io.ReadCloser, int64, error) {
		o.offsets = append(o.offsets, offset)
		return o.open(offset)
	}
}

func sha256Of(data []byte) v1.Hash {
	h, _, err := v1.SHA256(bytes.NewReader(data))
	Expect(err).NotTo(HaveOccurred())
	return h
}

var _ = Describe("downloadLayerToFile resume", func() {
	var (
		dst         *os.File
		data        []byte
		restoreWait func()
	)

	readDst := func() string {
		got, err := os.ReadFile(dst.Name())
		Expect(err).NotTo(HaveOccurred())
		return string(got)
	}

	BeforeEach(func() {
		var err error
		dst, err = os.CreateTemp("", "layer-resume-*.tar.gz")
		Expect(err).NotTo(HaveOccurred())

		data = []byte("0123456789abcdefghijklmnopqrstuvwxyzABCD")

		prev := layerRetryBackoff
		layerRetryBackoff = func(int) time.Duration { return 0 }
		restoreWait = func() { layerRetryBackoff = prev }
	})

	AfterEach(func() {
		restoreWait()
		_ = dst.Close()
		_ = os.Remove(dst.Name())
	})

	It("continues from the interruption offset instead of restarting", func() {
		layer := &fakeLayer{
			data:      data,
			prefix:    data[:15],
			digest:    sha256Of(data),
			failUntil: 1,
			err:       io.ErrUnexpectedEOF,
		}
		rec := &recordingOpener{open: func(offset int64) (io.ReadCloser, int64, error) {
			return io.NopCloser(bytes.NewReader(data[offset:])), offset, nil
		}}

		err := downloadLayerToFile(context.Background(), layer, dst, nil, rec.opener())
		Expect(err).NotTo(HaveOccurred())
		Expect(readDst()).To(Equal(string(data)))
		// The interrupted first attempt left 15 bytes; the resume must ask
		// for exactly the rest, without a second full-stream attempt.
		Expect(rec.offsets).To(Equal([]int64{15}))
		Expect(layer.calls).To(Equal(1))
	})

	It("restarts cleanly when the server ignores the Range request", func() {
		layer := &fakeLayer{
			data:      data,
			prefix:    data[:15],
			digest:    sha256Of(data),
			failUntil: 1,
			err:       io.ErrUnexpectedEOF,
		}
		rec := &recordingOpener{open: func(int64) (io.ReadCloser, int64, error) {
			// A 200 response: the whole blob from the first byte.
			return io.NopCloser(bytes.NewReader(data)), 0, nil
		}}

		err := downloadLayerToFile(context.Background(), layer, dst, nil, rec.opener())
		Expect(err).NotTo(HaveOccurred())
		// The partial bytes must have been discarded, not prepended.
		Expect(readDst()).To(Equal(string(data)))
		Expect(rec.offsets).To(HaveLen(1))
		Expect(layer.calls).To(Equal(1))
	})

	It("discards a resumed download whose digest does not match", func() {
		layer := &fakeLayer{
			data:      data,
			prefix:    data[:15],
			digest:    sha256Of(data),
			failUntil: 1,
			err:       io.ErrUnexpectedEOF,
		}
		rec := &recordingOpener{open: func(offset int64) (io.ReadCloser, int64, error) {
			corrupt := bytes.Repeat([]byte("x"), len(data)-int(offset))
			return io.NopCloser(bytes.NewReader(corrupt)), offset, nil
		}}

		err := downloadLayerToFile(context.Background(), layer, dst, nil, rec.opener())
		Expect(err).NotTo(HaveOccurred())
		// The spliced file failed verification, so the download must have
		// started over through the verified layer reader and succeeded.
		Expect(readDst()).To(Equal(string(data)))
		Expect(rec.offsets).To(Equal([]int64{15}))
		Expect(layer.calls).To(Equal(2))
	})

	It("keeps retrying beyond the budget while each resume makes progress", func() {
		const step = 5
		layer := &fakeLayer{
			data:      data,
			prefix:    data[:step],
			digest:    sha256Of(data),
			failUntil: 1,
			err:       io.ErrUnexpectedEOF,
		}
		rec := &recordingOpener{open: func(offset int64) (io.ReadCloser, int64, error) {
			if offset+step >= int64(len(data)) {
				return io.NopCloser(bytes.NewReader(data[offset:])), offset, nil
			}
			return io.NopCloser(&failingReader{prefix: data[offset : offset+step], err: io.ErrUnexpectedEOF}), offset, nil
		}}

		err := downloadLayerToFile(context.Background(), layer, dst, nil, rec.opener())
		Expect(err).NotTo(HaveOccurred())
		Expect(readDst()).To(Equal(string(data)))
		// 40 bytes delivered 5 at a time: 7 resumes, far more rounds than
		// the retry budget allows for stalled attempts.
		Expect(len(rec.offsets)).To(BeNumerically(">", layerDownloadRetries))
	})

	It("gives up when resumes stop making progress", func(ctx SpecContext) {
		layer := &fakeLayer{
			data:      data,
			prefix:    data[:15],
			digest:    sha256Of(data),
			failUntil: 1000,
			err:       io.ErrUnexpectedEOF,
		}
		rec := &recordingOpener{open: func(offset int64) (io.ReadCloser, int64, error) {
			// Resume accepted but the connection dies before any byte.
			return io.NopCloser(&failingReader{err: io.ErrUnexpectedEOF}), offset, nil
		}}

		err := downloadLayerToFile(ctx, layer, dst, nil, rec.opener())
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		Expect(len(rec.offsets)).To(Equal(layerDownloadRetries))
	}, NodeTimeout(10*time.Second))

	It("terminates when the server ignores Range and keeps dropping mid-stream", func(ctx SpecContext) {
		// Each round delivers some bytes from the start and dies: the file
		// never gets further than before, so this must exhaust the budget
		// rather than count the repeated partial bytes as progress.
		layer := &fakeLayer{
			data:      data,
			prefix:    data[:15],
			digest:    sha256Of(data),
			failUntil: 1000,
			err:       io.ErrUnexpectedEOF,
		}
		rec := &recordingOpener{open: func(int64) (io.ReadCloser, int64, error) {
			return io.NopCloser(&failingReader{prefix: data[:15], err: io.ErrUnexpectedEOF}), 0, nil
		}}

		err := downloadLayerToFile(ctx, layer, dst, nil, rec.opener())
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		Expect(len(rec.offsets)).To(Equal(layerDownloadRetries))
	}, NodeTimeout(10*time.Second))
})
