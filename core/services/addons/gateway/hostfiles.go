// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The calls that move an add-on zip file to the owner. They ride the host
// access carrier call, so a frontend forwards them like any other: signed,
// over the pinned channel, one chunk per request. The owner keeps the bytes;
// a frontend keeps none. They are not writes of the host access policy: the
// install that follows is, and it keeps its human surface rule.
const (
	HostGrantsFileBegin    = "file-begin"
	HostGrantsFileChunk    = "file-chunk"
	HostGrantsFileFinish   = "file-finish"
	HostGrantsFileAbort    = "file-abort"
	HostGrantsFileDrop     = "file-drop"
	HostGrantsFileDescribe = "file-describe"
)

// FileChunkBytes is the size of one chunk. Base64 makes it a third larger on
// the wire, which stays under the 2 MiB envelope.
const FileChunkBytes = 1 << 20

// FileService is what the owner needs from the add-on manager to take files.
type FileService interface {
	FileBegin(ctx context.Context, actor contract.Actor, name string) (id string, max int64, err error)
	FileChunk(ctx context.Context, actor contract.Actor, id string, offset int64, data []byte) error
	FileFinish(ctx context.Context, actor contract.Actor, id string) (addons.FileView, error)
	FileAbort(ctx context.Context, actor contract.Actor, id string)
	FileDrop(ctx context.Context, actor contract.Actor, id string) error
	FileDescribe(ctx context.Context, actor contract.Actor, id string) (addons.FileView, error)
}

func isFileOp(op string) bool {
	switch op {
	case HostGrantsFileBegin, HostGrantsFileChunk, HostGrantsFileFinish, HostGrantsFileAbort, HostGrantsFileDrop, HostGrantsFileDescribe:
		return true
	}
	return false
}

func init() {
	// A lost answer to a file call is a plain failure: the upload starts again.
	for _, op := range []string{HostGrantsFileBegin, HostGrantsFileChunk, HostGrantsFileFinish, HostGrantsFileAbort, HostGrantsFileDrop, HostGrantsFileDescribe} {
		hostGrantsRead["hostgrants-"+op] = true
	}
}

type fileBeginRequest struct {
	Name string `json:"name"`
}

// FileBeginView is the answer of file-begin.
type FileBeginView struct {
	ID       string `json:"id"`
	MaxBytes int64  `json:"maxBytes"`
}

type fileChunkRequest struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
}

type fileIDRequest struct {
	ID string `json:"id"`
}

// WithFiles sets the service that takes staged files. Set it on the owner.
func (o *HostGrantsOwner) WithFiles(f FileService) *HostGrantsOwner {
	o.files = f
	return o
}

// fileCall serves one file call. The administrator check has run already.
func (o *HostGrantsOwner) fileCall(ctx context.Context, a contract.Actor, call HostGrantsCall) (any, error) {
	if o.files == nil {
		return nil, ErrUnavailable
	}
	bad := func() (any, error) { return nil, o.invalid() }
	// An unsigned file installs only where UI host grants work: when the
	// deployment locks them (sign-in off, or a policy file that is the only
	// source), nothing can be staged either, and the answer says why.
	if call.Op == HostGrantsFileBegin && o.cfg.Locked {
		return nil, hgErr(403, HostGrantsLocked, o.lockedText())
	}
	switch call.Op {
	case HostGrantsFileBegin:
		var r fileBeginRequest
		if !strictBody(call.Body, &r) || r.Name == "" || len(r.Name) > 512 {
			return bad()
		}
		id, max, err := o.files.FileBegin(ctx, a, r.Name)
		return FileBeginView{ID: id, MaxBytes: max}, o.fileError(err)
	case HostGrantsFileChunk:
		var r fileChunkRequest
		if !strictBody(call.Body, &r) || !uploads.ValidID(r.ID) || r.Offset < 0 || len(r.Data) == 0 || len(r.Data) > FileChunkBytes {
			return bad()
		}
		return struct{}{}, o.fileError(o.files.FileChunk(ctx, a, r.ID, r.Offset, r.Data))
	case HostGrantsFileFinish, HostGrantsFileAbort, HostGrantsFileDrop, HostGrantsFileDescribe:
		var r fileIDRequest
		if !strictBody(call.Body, &r) || !uploads.ValidID(r.ID) {
			return bad()
		}
		switch call.Op {
		case HostGrantsFileFinish:
			v, err := o.files.FileFinish(ctx, a, r.ID)
			return v, o.fileError(err)
		case HostGrantsFileDescribe:
			v, err := o.files.FileDescribe(ctx, a, r.ID)
			return v, o.fileError(err)
		case HostGrantsFileDrop:
			return struct{}{}, o.fileError(o.files.FileDrop(ctx, a, r.ID))
		}
		o.files.FileAbort(ctx, a, r.ID)
		return struct{}{}, nil
	}
	return bad()
}

// fileError types the refusals of the file calls for the routes. A refusal of
// the content keeps the code of the rule it broke.
func (o *HostGrantsOwner) fileError(err error) error {
	if err == nil {
		return nil
	}
	if fe, ok := addons.IsFileRefusal(err); ok {
		return hgErr(400, fe.Code, fe.Msg)
	}
	switch {
	case errors.Is(err, uploads.ErrNotFound):
		return hgErr(404, HostGrantsNotFound, "No such file. It may have expired; upload it again")
	case errors.Is(err, uploads.ErrTooLarge):
		return hgErr(413, "too_large", "The file is over the size limit")
	case errors.Is(err, uploads.ErrBusy):
		return hgErr(429, "busy", "Too many files wait for an install. Remove one or wait")
	case errors.Is(err, uploads.ErrOffset):
		return hgErr(400, HostGrantsInvalidRequest, "The upload chunks arrived out of order")
	case errors.Is(err, addons.ErrNoFileInstall):
		return ErrUnavailable
	}
	return err
}

func (g *Gateway) fileDo(ctx context.Context, a contract.Actor, op string, req any, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return ErrForbidden
	}
	raw, err := g.HostGrants(ctx, a, HostGrantsCall{Op: op, Body: body})
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// UploadFile sends a zip file to the owner chunk by chunk and returns what the
// owner made of it. In a split deployment the chunks cross the carrier; on the
// owner they go straight to the service. Nothing is kept here. The owner
// enforces the byte limit while it receives, so a client that sends more is cut.
func (g *Gateway) UploadFile(ctx context.Context, a contract.Actor, name string, r io.Reader) (addons.FileView, error) {
	var begin FileBeginView
	if err := g.fileDo(ctx, a, HostGrantsFileBegin, fileBeginRequest{Name: name}, &begin); err != nil {
		return addons.FileView{}, err
	}
	abort := func() {
		// The caller's context may be the reason for the failure.
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), contract.DefaultLimits().ReadTimeout)
		defer cancel()
		_ = g.fileDo(c, a, HostGrantsFileAbort, fileIDRequest{ID: begin.ID}, nil)
	}
	buf := make([]byte, FileChunkBytes)
	var offset int64
	for {
		n, rerr := io.ReadFull(r, buf)
		if n > 0 {
			if err := g.fileDo(ctx, a, HostGrantsFileChunk, fileChunkRequest{ID: begin.ID, Offset: offset, Data: buf[:n]}, nil); err != nil {
				abort()
				return addons.FileView{}, err
			}
			offset += int64(n)
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			abort()
			return addons.FileView{}, rerr
		}
	}
	if offset == 0 {
		abort()
		return addons.FileView{}, hgErr(400, "bad_zip", "The file is empty")
	}
	var view addons.FileView
	if err := g.fileDo(ctx, a, HostGrantsFileFinish, fileIDRequest{ID: begin.ID}, &view); err != nil {
		abort()
		return addons.FileView{}, err
	}
	return view, nil
}

// DescribeFile reads a staged file again.
func (g *Gateway) DescribeFile(ctx context.Context, a contract.Actor, id string) (addons.FileView, error) {
	var view addons.FileView
	err := g.fileDo(ctx, a, HostGrantsFileDescribe, fileIDRequest{ID: id}, &view)
	return view, err
}

// DropFile removes a staged file.
func (g *Gateway) DropFile(ctx context.Context, a contract.Actor, id string) error {
	return g.fileDo(ctx, a, HostGrantsFileDrop, fileIDRequest{ID: id}, nil)
}
