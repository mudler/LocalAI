package xio

import (
	"context"
	"io"
)

const defaultBufferSize = 1 << 20

type options struct {
	bufferSize int
}

type Option func(*options)

func WithBufferSize(size int) Option {
	return func(options *options) {
		if size > 0 {
			options.bufferSize = size
		}
	}
}

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

func Copy(ctx context.Context, dst io.Writer, src io.Reader, opts ...Option) (int64, error) {
	copyOptions := options{bufferSize: defaultBufferSize}
	for _, option := range opts {
		option(&copyOptions)
	}

	buffer := make([]byte, copyOptions.bufferSize)
	return io.CopyBuffer(dst, readerFunc(func(p []byte) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			return src.Read(p)
		}
	}), buffer)
}
