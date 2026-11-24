// SPDX-License-Identifier: MIT
package connector

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Host is the host operation channel of the add-on. *hostclient.Client
// implements it.
type Host interface {
	Do(ctx context.Context, operationID, op string, args, out any) error
}

// transferTries is how often one chunk is sent before the transfer fails. A
// repeat is safe: the host answers where it is, and a chunk it already has is
// not written twice.
const transferTries = 3

// ErrNoHost means the add-on got a handle but has no host operation channel.
var ErrNoHost = errors.New("the host gave this add-on no channel for file transfer")

func hostError(err error) error {
	var he *contract.HostError
	if errors.As(err, &he) && he.Code == contract.HostTooLarge {
		return fmt.Errorf("%w: %v", ErrTooLarge, err)
	}
	return err
}

// Put streams r to the host through the handle of an inbound file and ends
// the file. It holds one chunk in memory, whatever the size of the file, and
// returns the bytes sent. The host refuses the file when it passes the bound
// of the handle, and Put then returns ErrTooLarge.
func Put(ctx context.Context, h Host, t *contract.ConnectorTransfer, r io.Reader) (int64, error) {
	if h == nil {
		return 0, ErrNoHost
	}
	buf := make([]byte, contract.TransferChunkBytes)
	var off int64
	for {
		n, rerr := io.ReadFull(r, buf)
		last := rerr != nil // a short read or the end
		if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
			return off, rerr
		}
		if n == 0 && !last {
			continue
		}
		var res contract.ConnectorFilePutResult
		var err error
		for try := 0; try < transferTries; try++ {
			err = h.Do(ctx, t.Handle, contract.ConnectorFilePutOp, contract.ConnectorFilePut{Handle: t.Handle, Offset: off, Data: buf[:n], Final: last}, &res)
			if err == nil || ctx.Err() != nil || errors.Is(hostError(err), ErrTooLarge) {
				break
			}
		}
		if err != nil {
			return off, hostError(err)
		}
		if res.Received != off+int64(n) {
			// The host is not where the add-on is, and a stream cannot be
			// rewound; the platform download starts again.
			return off, fmt.Errorf("the host has %d bytes and the add-on sent %d", res.Received, off+int64(n))
		}
		off += int64(n)
		if last {
			return off, nil
		}
	}
}

// Get returns a reader of the outbound file of the handle. Each Read pulls at
// most one chunk, so a platform upload that reads from it streams the file
// and holds one chunk. A repeated read of a chunk is safe.
func Get(ctx context.Context, h Host, t *contract.ConnectorTransfer) io.Reader {
	return &pull{ctx: ctx, h: h, t: t}
}

type pull struct {
	ctx context.Context
	h   Host
	t   *contract.ConnectorTransfer
	off int64
	buf []byte
	eof bool
}

func (p *pull) Read(b []byte) (int, error) {
	if p.h == nil {
		return 0, ErrNoHost
	}
	for len(p.buf) == 0 {
		if p.eof {
			return 0, io.EOF
		}
		var res contract.ConnectorFileGetResult
		var err error
		for try := 0; try < transferTries; try++ {
			err = p.h.Do(p.ctx, p.t.Handle, contract.ConnectorFileGetOp, contract.ConnectorFileGet{Handle: p.t.Handle, Offset: p.off, Length: contract.TransferChunkBytes}, &res)
			if err == nil || p.ctx.Err() != nil {
				break
			}
		}
		if err != nil {
			return 0, err
		}
		p.buf, p.eof = res.Data, res.EOF
		p.off += int64(len(res.Data))
		if len(res.Data) == 0 && !res.EOF {
			return 0, errors.New("the host sent an empty chunk before the end of the file")
		}
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}

// Sniff returns a reader that gives the same bytes as r and the first bytes
// of r, at most n, for a platform that decides the type of a file from them.
func Sniff(r io.Reader, n int) (io.Reader, []byte) {
	br := bufio.NewReaderSize(r, max(n, 16))
	head, _ := br.Peek(n)
	return br, head
}

// MediaStream is a file that a platform streams to the add-on.
type MediaStream struct {
	Name, MIME string
	// Size is the size the platform states, or zero when it states none.
	Size int64
	// Body is the stream of bytes; Serve closes it.
	Body io.ReadCloser
}

// StreamReader is a Platform that streams the files of messages for the op
// media when the host gives a handle. max is the bound of the handle: the
// platform should refuse a larger file as soon as it knows the size.
type StreamReader interface {
	MediaStream(ctx context.Context, token, ref string, max int64) (MediaStream, error)
}

// StreamSender is a Platform that sends a file for the op send_file from a
// stream. body is the file of req.Transfer: t.Size bytes. Serve builds a new
// body for every attempt.
type StreamSender interface {
	SendStream(ctx context.Context, req contract.ConnectorRequest, body io.Reader) (string, error)
}
