package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A connector add-on moves the bytes of a file by a handle and not in the
// connector envelope. This file is the frontend side of it.
//
//   - Inbound (op media). The frontend makes a random handle and sends it
//     with the bound. The add-on streams the file to the host in chunks; the
//     process that runs the add-on stores them in the blob store of the
//     workspace, and answers with the hash and the size from its own record.
//     The chat upload reads the blob from that store, so in a cluster the
//     frontend and the add-on node share objects, not a disk.
//   - Outbound (op send_file). The frontend checks the chat's access to the
//     file, and gives the process that runs the add-on the hash and the size
//     of the content, next to the handle. The add-on pulls chunks.

// fileTurnFactor bounds the bytes of one message: this many times the limit.
const fileTurnFactor = 3

// sniffBytes is how much of a file decides its type.
const sniffBytes = 512

// newHandle makes the random handle of one transfer.
func newHandle() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// inboundFile is a file of a message the add-on handed over by handle: SHA
// names its bytes in the blob store of the workspace.
type inboundFile struct {
	Name, MIME string
	Size       int64
	SHA        string
}

// body is what the chat reads the file from.
func (f inboundFile) body(c *Connectors) io.Reader {
	return &blobBody{files: c.svc.d.Files.OpenBlob, sha: f.SHA}
}

// blobBody reads a stored blob when the first byte is asked for, and closes
// it at the end, so a file the chat never reads holds no descriptor.
type blobBody struct {
	files func(context.Context, string) (io.ReadSeekCloser, error)
	sha   string
	r     io.ReadSeekCloser
	done  bool
}

func (b *blobBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	if b.r == nil {
		r, err := b.files(context.Background(), b.sha)
		if err != nil {
			b.done = true
			return 0, err
		}
		b.r = r
	}
	n, err := b.r.Read(p)
	if err != nil {
		b.done = true
		_ = b.r.Close()
	}
	return n, err
}

// inboundOf turns the answer of the op media into a file. A file that is
// larger than the limit comes back with its size and errFileTooLarge.
func (c *Connectors) inboundOf(b connectors.Binding, l FileLimits, resp contract.ConnectorResponse, err error) (inboundFile, error) {
	if err != nil {
		return inboundFile{}, err
	}
	r := resp.Received
	switch {
	case r == nil || r.SHA256 == "" || r.Size <= 0:
		return inboundFile{}, errors.New("the add-on did not finish the file")
	case r.Size > l.Receive.Bytes:
		return inboundFile{Size: r.Size}, errFileTooLarge
	}
	return inboundFile{Name: contract.SafeFileName(r.Name), MIME: r.MIME, Size: r.Size, SHA: r.SHA256}, nil
}

// typeAllowed reports whether a media type is among the types a kind
// declares. An empty list allows every type, and so does a file with no type.
func typeAllowed(types []string, mime string) bool {
	if len(types) == 0 || mime == "" {
		return true
	}
	mime, _, _ = strings.Cut(mime, ";")
	mime = strings.ToLower(strings.TrimSpace(mime))
	for _, t := range types {
		if t == mime {
			return true
		}
		if pre, ok := strings.CutSuffix(t, "/*"); ok && strings.HasPrefix(mime, pre+"/") {
			return true
		}
	}
	return false
}

// auditFile records one transfer: direction, binding, size, result, the layer
// that set the limit and the time. It holds no name and no content.
func (c *Connectors) auditFile(b connectors.Binding, dir string, size int64, result string, l FileLimit, took time.Duration) {
	c.log.Info("studio: connector file audit", "binding", b.ID, "kind", b.Kind, "direction", dir, "bytes", size, "result", result, "layer", string(l.Layer), "duration", took.Round(time.Millisecond))
}

// sendHandled sends one file of a chat by handle.
// It returns the sentence for the sender when the file cannot go, and ""
// when it went or is not named (gone, empty). sent counts the bytes of the
// turn so far.
func (c *Connectors) sendHandled(ctx context.Context, v hostmsg.Voice, b connectors.Binding, token string, m contract.ConnectorMessage, x session.Access, f outFile, mimeType string, l FileLimits, sent *int64) string {
	start := time.Now()
	n, err := c.svc.d.Files.Store().Stat(ctx, x.Actor(), f.path)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return "" // gone since, or outside the chat's access: not named
	case err != nil:
		c.log.Warn("studio: could not read a file for a connector", "binding", b.ID, "error", err)
		return v.T("file.read_fail", "name", f.name)
	case n.Kind == workspace.KindDir || n.Blob == "" || n.Size == 0:
		return "" // an empty file says nothing, and platforms refuse it
	case n.Size > l.Send.Bytes:
		c.auditFile(b, "out", n.Size, "too_large", l.Send, time.Since(start))
		return noteSendTooBig(v, f.name, l)
	case !typeAllowed(l.SendTypes, mimeType):
		return v.T("file.type_not_sent", "name", f.name)
	case *sent+n.Size > l.Send.Bytes*fileTurnFactor:
		return v.T("file.stays", "name", f.name)
	}
	if head, err := c.svc.d.Files.OpenBlob(ctx, n.Blob); err == nil {
		buf := make([]byte, sniffBytes)
		k, _ := io.ReadFull(head, buf)
		_ = head.Close()
		// A page that a client could run goes under no name.
		if strings.HasPrefix(http.DetectContentType(buf[:k]), "text/html") {
			return v.T("file.type_not_sent", "name", f.name)
		}
	}
	scope := BindingConnectorScope(b)
	scope.Content = &connectors.TransferContent{SHA: n.Blob, Size: n.Size}
	req := contract.ConnectorRequest{Op: contract.ConnectorOpSendFile, Kind: b.Kind, Token: token, Chat: m.Chat, Thread: m.Thread,
		Transfer: &contract.ConnectorTransfer{Handle: newHandle(), Name: f.name, MIME: mimeType, Size: n.Size}}
	cctx, cancel := context.WithTimeout(ctx, contract.ConnectorTransferTimeout)
	defer cancel()
	resp, err := c.o.Transport.Call(cctx, scope, req)
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	if err != nil {
		c.auditFile(b, "out", n.Size, "failed", l.Send, time.Since(start))
		c.log.Warn("studio: could not send a file to a connector", "binding", b.ID, "kind", b.Kind, "error", err)
		return v.T("file.send_fail", "name", f.name, "reason", sendReason(err))
	}
	*sent += n.Size
	c.auditFile(b, "out", n.Size, "done", l.Send, time.Since(start))
	return ""
}
