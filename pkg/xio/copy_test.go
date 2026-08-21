package xio_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/xio"
)

type recordingReader struct {
	reader  io.Reader
	maxRead int
	reads   int
}

type writerFunc func(p []byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

var discardWriter = writerFunc(func(p []byte) (int, error) { return len(p), nil })

func (r *recordingReader) Read(p []byte) (int, error) {
	r.reads++
	if len(p) > r.maxRead {
		r.maxRead = len(p)
	}
	return r.reader.Read(p)
}

var _ = Describe("Copy", func() {
	It("copies the complete source", func() {
		contents := bytes.Repeat([]byte("complete copy"), 10_000)
		var destination bytes.Buffer

		written, err := xio.Copy(context.Background(), &destination, bytes.NewReader(contents))

		Expect(err).NotTo(HaveOccurred())
		Expect(written).To(Equal(int64(len(contents))))
		Expect(destination.Bytes()).To(Equal(contents))
	})

	It("uses a default read buffer larger than 32 KiB", func() {
		source := &recordingReader{reader: bytes.NewReader(make([]byte, 2<<20))}

		_, err := xio.Copy(context.Background(), discardWriter, source)

		Expect(err).NotTo(HaveOccurred())
		Expect(source.maxRead).To(Equal(1 << 20))
	})

	It("uses a custom buffer size", func() {
		const bufferSize = 64 << 10
		source := &recordingReader{reader: bytes.NewReader(make([]byte, 2*bufferSize))}

		_, err := xio.Copy(context.Background(), discardWriter, source, xio.WithBufferSize(bufferSize))

		Expect(err).NotTo(HaveOccurred())
		Expect(source.maxRead).To(Equal(bufferSize))
	})

	DescribeTable("falls back to the default buffer for invalid sizes",
		func(size int) {
			source := &recordingReader{reader: bytes.NewReader(make([]byte, 2<<20))}

			_, err := xio.Copy(context.Background(), discardWriter, source, xio.WithBufferSize(size))

			Expect(err).NotTo(HaveOccurred())
			Expect(source.maxRead).To(Equal(1 << 20))
		},
		Entry("zero", 0),
		Entry("negative", -1),
	)

	It("checks cancellation before reading the source", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		source := &recordingReader{reader: bytes.NewReader([]byte("unread"))}

		written, err := xio.Copy(ctx, io.Discard, source)

		Expect(err).To(MatchError(context.Canceled))
		Expect(written).To(BeZero())
		Expect(source.reads).To(BeZero())
	})
})

func BenchmarkCopy(b *testing.B) {
	contents := bytes.Repeat([]byte("benchmark payload"), 1<<16)
	tests := []struct {
		name    string
		options []xio.Option
	}{
		{name: "default", options: []xio.Option{}},
		{name: "32 KiB", options: []xio.Option{xio.WithBufferSize(32 << 10)}},
		{name: "1 MiB", options: []xio.Option{xio.WithBufferSize(1 << 20)}},
		{name: "4 MiB", options: []xio.Option{xio.WithBufferSize(4 << 20)}},
	}

	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := xio.Copy(context.Background(), io.Discard, bytes.NewReader(contents), test.options...)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
