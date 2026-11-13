// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

// The folder operations of a paired client. An add-on that serves a client can
// list, read and write text files in the workspace of the owner, inside the
// folders the owner granted to that client key, and nowhere else.
//
//	client.workspace.list   names and sizes inside a granted folder
//	client.workspace.read   one text file of a granted folder
//	client.workspace.write  one text file inside the write folder
//
// The grant of the key decides, never the home of the owner and never the
// add-on: the folders come from the key as the host reads it at each call (a
// revoked, paused or expired key, or a key without the grant, gets the generic
// refusal), the client from the host's record of the live call, and each path
// is checked against the folders before the store sees it. The store then
// holds the actor to the one granted folder as well (workspace.Actor.Fenced),
// so a mistake in the path check cannot reach another folder.
//
// The workspace is a versioned store of nodes, not a directory tree on disk,
// so it has no link to follow and no os.Root to open: a name is a name, and
// the fence is the folder node the actor is bound to.

const (
	// DefaultClientReadBytes is the largest file client.workspace.read returns.
	// The answer is JSON, and the encoder writes "<" as six bytes, so even
	// this size can grow past one payload; read checks the encoded answer too.
	DefaultClientReadBytes = 512 << 10
	// DefaultClientWriteFileBytes is the largest file client.workspace.write
	// stores, and DefaultClientWriteFolderBytes the most the write folder may
	// hold in all.
	DefaultClientWriteFileBytes   = 256 << 10
	DefaultClientWriteFolderBytes = 50 << 20

	maxClientListEntries = 200
	// DefaultClientFolderNodes bounds the nodes counted to size the write
	// folder. A folder with more takes no write.
	DefaultClientFolderNodes = 4096
	// clientSizeTTL is how long the size of a write folder, counted once and
	// then kept up to date by the writes of this host, is trusted before it is
	// counted again. The owner can change the folder by other ways.
	clientSizeTTL = 30 * time.Second
	// maxClientPathSegments bounds a path below a folder.
	maxClientPathSegments = 16
	maxClientPathBytes    = 512
)

// ClientTextExts are the only file types a client may list, read or write.
var ClientTextExts = []string{".md", ".txt", ".json"}

// clientFileSegment is the rule for a name below the write folder: a letter,
// digit or underscore first, then letters, digits and . _ ~ -. It has no space
// and no leading dot, so no hidden file and no look-alike of a parent.
var clientFileSegment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._~-]{0,127}$`)

// ClientGrantSource is the host side of the grant. Grant returns the folders
// the client key may read and the one it may write, relative to the workspace
// of the owner, for a call that the add-on makes while it serves the client. It
// returns an error that wraps ErrForbidden for a client that is not paired
// (revoked, expired, paused, its owner no longer an administrator), may not
// reach the add-on or holds no folder grant. The error text goes to the log of
// the host only.
type ClientGrantSource interface {
	Grant(ctx context.Context, c ClientCall, addonID string) (read []string, write string, err error)
}

// ClientAudit is one record of the audit of the folder operations: a write
// that went through (Reason empty) or any operation that was refused (Reason
// set). It holds the id of the client key, never its credential, and for a
// write the digest of the content, never the content.
type ClientAudit struct {
	Op     string
	Addon  string
	Client string
	Path   string
	Bytes  int
	SHA256 string
	Reason string
}

// ClientFolderHost serves the client.workspace.* operations.
type ClientFolderHost struct {
	Files  *files.Service
	Grants ClientGrantSource
	// ReadBytes, WriteFileBytes and WriteFolderBytes replace the defaults when
	// they are above zero.
	ReadBytes, WriteFileBytes, WriteFolderBytes int64
	// Audit receives each write and each refusal. Nil logs them with the
	// logger of the host (xlog), which is the only audit a host operation has.
	Audit func(ClientAudit)

	// MaxFolderNodes and PayloadBytes replace the defaults when above zero.
	// PayloadBytes is the largest answer a host operation may return.
	MaxFolderNodes int
	PayloadBytes   int64

	// smu guards the two maps. Each write folder has one lock, so writes to it
	// run one at a time and the check of its size is not raced, while a write
	// that waits ends with its context.
	smu   sync.Mutex
	locks map[string]chan struct{}
	sizes map[string]folderSize
}

// folderSize is the counted content of a write folder.
type folderSize struct {
	bytes int64
	nodes int
	at    time.Time
}

// errFolderTooBig means the folder has more nodes than MaxFolderNodes.
var errFolderTooBig = errors.New("the folder has too many files")

func (h *ClientFolderHost) maxNodes() int {
	if h.MaxFolderNodes > 0 {
		return h.MaxFolderNodes
	}
	return DefaultClientFolderNodes
}

func (h *ClientFolderHost) payloadBytes() int64 {
	if h.PayloadBytes > 0 {
		return h.PayloadBytes
	}
	return contract.DefaultLimits().PayloadBytes
}

// folderKey names the lock and the cached size of a write folder.
func folderKey(owner string, folder []string) string {
	return owner + "\x00" + strings.Join(folder, "/")
}

// lock takes the lock of a write folder, or gives up when ctx ends.
func (h *ClientFolderHost) lock(ctx context.Context, key string) (unlock func(), err error) {
	h.smu.Lock()
	if h.locks == nil {
		h.locks = map[string]chan struct{}{}
	}
	ch := h.locks[key]
	if ch == nil {
		ch = make(chan struct{}, 1)
		h.locks[key] = ch
	}
	h.smu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *ClientFolderHost) readBytes() int64 {
	if h.ReadBytes > 0 {
		return h.ReadBytes
	}
	return DefaultClientReadBytes
}

func (h *ClientFolderHost) writeFileBytes() int64 {
	if h.WriteFileBytes > 0 {
		return h.WriteFileBytes
	}
	return DefaultClientWriteFileBytes
}

func (h *ClientFolderHost) writeFolderBytes() int64 {
	if h.WriteFolderBytes > 0 {
		return h.WriteFolderBytes
	}
	return DefaultClientWriteFolderBytes
}

func (h *ClientFolderHost) audit(a ClientAudit) {
	if h.Audit != nil {
		h.Audit(a)
		return
	}
	if a.Reason != "" {
		xlog.Info("client folder operation refused", "op", a.Op, "addon", a.Addon, "client", a.Client, "path", a.Path, "reason", a.Reason)
		return
	}
	xlog.Info("client folder write", "op", a.Op, "addon", a.Addon, "client", a.Client, "path", a.Path, "bytes", a.Bytes, "sha256", a.SHA256)
}

// RegisterClientFolders registers client.workspace.list, .read and .write. A
// host without the files service or the grant source registers nothing. The
// write operation needs the apply scope, which the host gives only to a client
// call on a route that is not a GET (see runtime.Client.clientEnvelope); the
// two others run under any scope.
func RegisterClientFolders(b *Broker, h *ClientFolderHost) error {
	if h == nil || h.Files == nil || h.Grants == nil {
		return nil
	}
	for _, s := range []Spec{
		{Op: "client.workspace.list", Scope: "read", Feature: featureChats, Handler: h.list},
		{Op: "client.workspace.read", Scope: "read", Feature: featureChats, Handler: h.read},
		{Op: "client.workspace.write", Scope: "apply", Feature: featureChats, Handler: h.write},
	} {
		if err := b.Register(s); err != nil {
			return err
		}
	}
	return nil
}

// grant is what one call may reach.
type clientGrant struct {
	read  [][]string
	write []string
}

func (g clientGrant) readable() [][]string {
	out := slices.Clone(g.read)
	if g.write != nil {
		out = append(out, g.write)
	}
	return out
}

// refuse audits a refusal and returns the error. The reason is for the log; the
// error carries only fixed text.
func (h *ClientFolderHost) refuse(op string, c Call, cl ClientCall, p, reason string, err error) error {
	h.audit(ClientAudit{Op: op, Addon: c.AddonID, Client: cl.ClientID, Path: logPath(p), Reason: reason})
	return err
}

// logPath keeps what the log shows of a path short and printable.
func logPath(p string) string {
	if len(p) > 200 {
		p = p[:200]
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return '?'
		}
		return r
	}, p)
}

// begin resolves who is calling and what they may reach. Every failure is the
// same generic refusal; the reason goes to the audit.
func (h *ClientFolderHost) begin(ctx context.Context, op string, c Call) (ClientCall, clientGrant, error) {
	cl, err := clientFrom(c)
	if err != nil {
		return ClientCall{}, clientGrant{}, h.refuse(op, c, cl, "", "not a client call", err)
	}
	read, write, err := h.Grants.Grant(ctx, cl, c.AddonID)
	if err != nil {
		reason := err.Error()
		if !errors.Is(err, ErrForbidden) {
			// A store that fails is an outage, not a refusal, and says nothing
			// about the grant.
			h.audit(ClientAudit{Op: op, Addon: c.AddonID, Client: cl.ClientID, Reason: "grant unavailable: " + reason})
			return cl, clientGrant{}, err
		}
		return cl, clientGrant{}, h.refuse(op, c, cl, "", reason, err)
	}
	var g clientGrant
	for _, f := range read {
		segs, err := grantFolder(f)
		if err != nil {
			return cl, clientGrant{}, h.refuse(op, c, cl, "", "the grant names a bad folder", fmt.Errorf("%w: no access", ErrForbidden))
		}
		g.read = append(g.read, segs)
	}
	if write != "" {
		segs, err := grantFolder(write)
		if err != nil {
			return cl, clientGrant{}, h.refuse(op, c, cl, "", "the grant names a bad folder", fmt.Errorf("%w: no access", ErrForbidden))
		}
		g.write = segs
	}
	if len(g.read) == 0 && g.write == nil {
		return cl, clientGrant{}, h.refuse(op, c, cl, "", "the client has no folder", fmt.Errorf("%w: no access", ErrForbidden))
	}
	return cl, g, nil
}

// grantFolder splits a folder of a grant. The grant was checked when it was
// stored; it is checked again here, since the store of keys is outside this
// package.
func grantFolder(f string) ([]string, error) {
	return splitClientPath(f, false)
}

// splitClientPath splits a path relative to the workspace of the owner. It
// refuses an empty path unless allowEmpty is set, an absolute path, a backslash,
// NUL and any control character, bytes that are not UTF-8, an empty, dot or
// parent segment, a segment with a space at either end, and a path that is too
// long or too deep.
func splitClientPath(p string, allowEmpty bool) ([]string, error) {
	bad := fmt.Errorf("%w: invalid path", ErrBadRequest)
	if p == "" {
		if allowEmpty {
			return nil, nil
		}
		return nil, bad
	}
	if len(p) > maxClientPathBytes || strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') || !utf8.ValidString(p) || strings.ContainsFunc(p, unicode.IsControl) {
		return nil, bad
	}
	segs := strings.Split(p, "/")
	if len(segs) > maxClientPathSegments {
		return nil, bad
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || strings.TrimSpace(s) != s {
			return nil, bad
		}
	}
	if _, err := workspace.SplitPath("/" + p); err != nil {
		return nil, bad
	}
	return segs, nil
}

// covering returns the granted folder that holds the path, or nil. The path is
// inside the folder when it starts with every segment of it.
func covering(folders [][]string, segs []string) []string {
	for _, f := range folders {
		if len(segs) >= len(f) && slices.Equal(segs[:len(f)], f) {
			return f
		}
	}
	return nil
}

func clientTextExt(name string) bool {
	return slices.Contains(ClientTextExts, strings.ToLower(path.Ext(name)))
}

// fence returns the actor of the owner bound to the one folder, at the mode.
// The folder must exist: a read of a folder that is not there finds nothing.
func (h *ClientFolderHost) fence(ctx context.Context, owner string, folder []string, mode workspace.Mode) (workspace.Actor, string, error) {
	abs, err := h.Files.Abs(owner, "/"+strings.Join(folder, "/"))
	if err != nil {
		return workspace.Actor{}, "", fmt.Errorf("%w: no access", ErrForbidden)
	}
	n, err := h.Files.Store().Stat(ctx, h.Files.Actor(owner), abs)
	if err != nil {
		return workspace.Actor{}, "", clientStoreErr(err)
	}
	if n.Kind != workspace.KindDir {
		return workspace.Actor{}, "", ErrNotFound
	}
	return h.Files.Actor(owner).Fenced(workspace.FenceRoot{Node: n.ID, Mode: mode}), abs, nil
}

// clientStoreErr maps an error of the store to what the add-on may see: fixed
// text and no path.
func clientStoreErr(err error) error {
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, workspace.ErrReadOnly):
		return fmt.Errorf("%w: no access", ErrForbidden)
	case errors.Is(err, workspace.ErrBadName), errors.Is(err, workspace.ErrInvalid), errors.Is(err, workspace.ErrExists):
		return fmt.Errorf("%w: the path is not available", ErrBadRequest)
	case errors.Is(err, workspace.ErrTooLarge):
		return fmt.Errorf("%w: the file is too large", ErrBadRequest)
	}
	return err
}

type clientPathArgs struct {
	Path string `json:"path"`
	// After is the cursor of list: the last name of the page before.
	After string `json:"after,omitempty"`
}

// list names the entries of a granted folder. The path "" lists the granted
// folders themselves, so an add-on can learn what the key reaches.
func (h *ClientFolderHost) list(ctx context.Context, c Call) (json.RawMessage, error) {
	const op = "client.workspace.list"
	cl, g, err := h.begin(ctx, op, c)
	if err != nil {
		return nil, err
	}
	var a clientPathArgs
	if err := clientArgs(c.Arguments, &a); err != nil {
		return nil, h.refuse(op, c, cl, "", "bad arguments", err)
	}
	type entry struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir,omitempty"`
		Size int64  `json:"size,omitempty"`
	}
	if a.Path == "" {
		out := []entry{}
		for _, f := range g.readable() {
			out = append(out, entry{Name: strings.Join(f, "/"), Dir: true})
		}
		slices.SortFunc(out, func(x, y entry) int { return strings.Compare(x.Name, y.Name) })
		out = slices.CompactFunc(out, func(x, y entry) bool { return x.Name == y.Name })
		return json.Marshal(map[string]any{"path": "", "entries": out})
	}
	segs, err := splitClientPath(a.Path, false)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "bad path", err)
	}
	folder := covering(g.readable(), segs)
	if folder == nil {
		return nil, h.refuse(op, c, cl, a.Path, "outside the granted folders", fmt.Errorf("%w: no access", ErrForbidden))
	}
	actor, _, err := h.fence(ctx, c.Actor.UserID, folder, workspace.ModeRead)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "the granted folder is not readable", err)
	}
	abs, err := h.Files.Abs(c.Actor.UserID, "/"+strings.Join(segs, "/"))
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "bad path", fmt.Errorf("%w: no access", ErrForbidden))
	}
	nodes, err := h.Files.Store().List(ctx, actor, abs)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "the store refused the folder", clientStoreErr(err))
	}
	// The store sorts by name. A page holds at most maxClientListEntries of the
	// entries that this operation shows (folders and text files), after the
	// cursor; next is set when more follow.
	out := []entry{}
	next := ""
	for _, n := range nodes {
		var e entry
		switch {
		case n.Kind == workspace.KindDir:
			e = entry{Name: n.Name, Dir: true}
		case n.Kind == workspace.KindFile && clientTextExt(n.Name):
			e = entry{Name: n.Name, Size: n.Size}
		default:
			continue
		}
		if a.After != "" && e.Name <= a.After {
			continue
		}
		if len(out) == maxClientListEntries {
			next = out[len(out)-1].Name
			break
		}
		out = append(out, e)
	}
	res := map[string]any{"path": strings.Join(segs, "/"), "entries": out, "truncated": next != ""}
	if next != "" {
		res["next"] = next
	}
	return json.Marshal(res)
}

// read returns one text file of a granted folder.
func (h *ClientFolderHost) read(ctx context.Context, c Call) (json.RawMessage, error) {
	const op = "client.workspace.read"
	cl, g, err := h.begin(ctx, op, c)
	if err != nil {
		return nil, err
	}
	var a clientPathArgs
	if err := clientArgs(c.Arguments, &a); err != nil {
		return nil, h.refuse(op, c, cl, "", "bad arguments", err)
	}
	segs, err := splitClientPath(a.Path, false)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "bad path", err)
	}
	folder := covering(g.readable(), segs)
	if folder == nil || len(segs) == len(folder) {
		return nil, h.refuse(op, c, cl, a.Path, "outside the granted folders", fmt.Errorf("%w: no access", ErrForbidden))
	}
	if !clientTextExt(segs[len(segs)-1]) {
		return nil, h.refuse(op, c, cl, a.Path, "not a text format", fmt.Errorf("%w: only .md, .txt and .json files", ErrBadRequest))
	}
	actor, _, err := h.fence(ctx, c.Actor.UserID, folder, workspace.ModeRead)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "the granted folder is not readable", err)
	}
	abs, err := h.Files.Abs(c.Actor.UserID, "/"+strings.Join(segs, "/"))
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "bad path", fmt.Errorf("%w: no access", ErrForbidden))
	}
	rc, n, err := h.Files.Store().Open(ctx, actor, abs)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "the store refused the file", clientStoreErr(err))
	}
	defer func() { _ = rc.Close() }()
	limit := h.readBytes()
	if n.Kind != workspace.KindFile || n.Size > limit {
		return nil, h.refuse(op, c, cl, a.Path, "not a file, or over the read limit", fmt.Errorf("%w: not a file, or too large", ErrBadRequest))
	}
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || !utf8.Valid(data) || slices.Contains(data, 0) {
		return nil, h.refuse(op, c, cl, a.Path, "not text, or over the read limit", fmt.Errorf("%w: not a text file, or too large", ErrBadRequest))
	}
	out, err := json.Marshal(map[string]any{"path": strings.Join(segs, "/"), "size": len(data), "content": string(data)})
	if err != nil {
		return nil, err
	}
	// The encoder escapes some characters to six bytes, so the answer can be
	// larger than the file. One that would not fit the payload of a host
	// operation is refused here, not cut by the channel.
	if int64(len(out)) > h.payloadBytes()-1024 {
		return nil, h.refuse(op, c, cl, a.Path, "the encoded answer is over the payload limit", fmt.Errorf("%w: the file is too large to return", ErrBadRequest))
	}
	return out, nil
}

// write stores one text file inside the write folder.
func (h *ClientFolderHost) write(ctx context.Context, c Call) (json.RawMessage, error) {
	const op = "client.workspace.write"
	cl, g, err := h.begin(ctx, op, c)
	if err != nil {
		return nil, err
	}
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := clientArgs(c.Arguments, &a); err != nil {
		return nil, h.refuse(op, c, cl, "", "bad arguments", err)
	}
	segs, err := splitClientPath(a.Path, false)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "bad path", err)
	}
	// Only the write folder takes a write. A folder the key may only read, and
	// a path outside the grant, are the same refusal.
	if g.write == nil || len(segs) <= len(g.write) || !slices.Equal(segs[:len(g.write)], g.write) {
		return nil, h.refuse(op, c, cl, a.Path, "outside the write folder", fmt.Errorf("%w: no access", ErrForbidden))
	}
	for _, s := range segs[len(g.write):] {
		if !clientFileSegment.MatchString(s) {
			return nil, h.refuse(op, c, cl, a.Path, "bad file name", fmt.Errorf("%w: invalid file name", ErrBadRequest))
		}
	}
	name := segs[len(segs)-1]
	if !clientTextExt(name) {
		return nil, h.refuse(op, c, cl, a.Path, "not a text format", fmt.Errorf("%w: only .md, .txt and .json files", ErrBadRequest))
	}
	if int64(len(a.Content)) > h.writeFileBytes() {
		return nil, h.refuse(op, c, cl, a.Path, "over the file limit", fmt.Errorf("%w: the file is too large", ErrBadRequest))
	}
	if !utf8.ValidString(a.Content) || strings.ContainsRune(a.Content, 0) {
		return nil, h.refuse(op, c, cl, a.Path, "not text", fmt.Errorf("%w: the content must be text", ErrBadRequest))
	}
	if strings.EqualFold(path.Ext(name), ".json") && !json.Valid([]byte(a.Content)) {
		return nil, h.refuse(op, c, cl, a.Path, "invalid JSON", fmt.Errorf("%w: the content is not valid JSON", ErrBadRequest))
	}

	owner := c.Actor.UserID
	key := folderKey(owner, g.write)
	unlock, err := h.lock(ctx, key)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "gave up waiting for the folder: "+err.Error(), fmt.Errorf("%w: busy", ErrConflict))
	}
	defer unlock()
	// The grant is read again once the lock is ours: a write that waited must
	// not complete for a key that was revoked, paused or narrowed meanwhile.
	cl, g2, err := h.begin(ctx, op, c)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(g2.write, g.write) {
		return nil, h.refuse(op, c, cl, a.Path, "the write folder changed while the call waited", fmt.Errorf("%w: no access", ErrForbidden))
	}
	if err := h.ensureFolder(ctx, owner, g.write); err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "the write folder is not available", clientStoreErr(err))
	}
	actor, abs, err := h.fence(ctx, owner, g.write, workspace.ModeWrite)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "the write folder is not writable", err)
	}
	target, err := h.Files.Abs(owner, "/"+strings.Join(segs, "/"))
	if err != nil || !strings.HasPrefix(target, abs+"/") {
		return nil, h.refuse(op, c, cl, a.Path, "outside the write folder", fmt.Errorf("%w: no access", ErrForbidden))
	}
	size, err := h.sizeOf(ctx, key, actor, abs)
	switch {
	case errors.Is(err, errFolderTooBig):
		return nil, h.refuse(op, c, cl, a.Path, "the write folder has too many files", fmt.Errorf("%w: the folder has too many files", ErrBadRequest))
	case err != nil:
		// The size could not be counted: an outage, not a full folder.
		h.audit(ClientAudit{Op: op, Addon: c.AddonID, Client: cl.ClientID, Path: logPath(a.Path), Reason: "the size of the write folder could not be counted: " + err.Error()})
		return nil, err
	}
	old, existed := int64(0), false
	if n, err := h.Files.Store().Stat(ctx, actor, target); err == nil {
		if n.Kind != workspace.KindFile {
			return nil, h.refuse(op, c, cl, a.Path, "the path is a folder", fmt.Errorf("%w: the path is not a file", ErrBadRequest))
		}
		old, existed = n.Size, true
	}
	if size.bytes-old+int64(len(a.Content)) > h.writeFolderBytes() {
		return nil, h.refuse(op, c, cl, a.Path, "over the folder limit", fmt.Errorf("%w: the folder is full", ErrBadRequest))
	}
	if !existed && size.nodes+1 > h.maxNodes() {
		return nil, h.refuse(op, c, cl, a.Path, "the write folder has too many files", fmt.Errorf("%w: the folder has too many files", ErrBadRequest))
	}
	// The owner reads this title in the history of the files. It names the
	// client by its id, so the owner can tell which client wrote the file.
	title := "Paired client " + cl.ClientID + " wrote " + name
	tx, err := h.Files.Store().Begin(ctx, actor, title)
	if err != nil {
		return nil, h.refuse(op, c, cl, a.Path, "the store refused the change", clientStoreErr(err))
	}
	node, err := tx.Put(ctx, target, strings.NewReader(a.Content), workspace.PutOptions{Overwrite: true, MaxSize: h.writeFileBytes(), Title: title})
	if err != nil {
		_ = tx.Rollback()
		return nil, h.refuse(op, c, cl, a.Path, "the store refused the file", clientStoreErr(err))
	}
	cs, err := tx.Commit()
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return nil, h.refuse(op, c, cl, a.Path, "the store could not commit", clientStoreErr(err))
	}
	node.Rev = cs.Rev
	h.Files.Written(ctx, node, owner)
	// Keep the counted size up to date, so the next write does not walk again.
	size.bytes += int64(len(a.Content)) - old
	if !existed {
		size.nodes++
	}
	h.smu.Lock()
	h.sizes[key] = size
	h.smu.Unlock()

	sum := sha256.Sum256([]byte(a.Content))
	digest := hex.EncodeToString(sum[:])
	h.audit(ClientAudit{Op: op, Addon: c.AddonID, Client: cl.ClientID, Path: logPath(strings.Join(segs, "/")), Bytes: len(a.Content), SHA256: digest})
	return json.Marshal(map[string]any{"path": strings.Join(segs, "/"), "bytes": len(a.Content), "sha256": digest, "revision": cs.Rev})
}

// ensureFolder makes the write folder when it is not there yet. It acts as the
// owner, for the one path the owner named in the grant.
func (h *ClientFolderHost) ensureFolder(ctx context.Context, owner string, folder []string) error {
	if err := h.Files.EnsureHome(ctx, owner); err != nil {
		return err
	}
	abs, err := h.Files.Abs(owner, "/"+strings.Join(folder, "/"))
	if err != nil {
		return err
	}
	if _, err := h.Files.Store().Mkdir(ctx, h.Files.Actor(owner), abs); err != nil && !errors.Is(err, workspace.ErrExists) {
		return err
	}
	return nil
}

// sizeOf returns the counted content of the write folder. It walks the folder
// when nothing is counted yet or the count is older than clientSizeTTL, and
// otherwise trusts the count that the writes of this host kept up to date. The
// caller holds the lock of the folder.
func (h *ClientFolderHost) sizeOf(ctx context.Context, key string, actor workspace.Actor, abs string) (folderSize, error) {
	h.smu.Lock()
	if h.sizes == nil {
		h.sizes = map[string]folderSize{}
	}
	cur, ok := h.sizes[key]
	h.smu.Unlock()
	if ok && time.Since(cur.at) < clientSizeTTL {
		return cur, nil
	}
	cur, err := h.walk(ctx, actor, abs)
	if err != nil {
		return folderSize{}, err
	}
	h.smu.Lock()
	h.sizes[key] = cur
	h.smu.Unlock()
	return cur, nil
}

// walk adds up the files below a folder. It fails with errFolderTooBig past
// MaxFolderNodes nodes, and with the error of the store when a listing fails.
func (h *ClientFolderHost) walk(ctx context.Context, actor workspace.Actor, abs string) (folderSize, error) {
	out := folderSize{at: time.Now()}
	var rec func(dir string) error
	rec = func(dir string) error {
		nodes, err := h.Files.Store().List(ctx, actor, dir)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if out.nodes++; out.nodes > h.maxNodes() {
				return errFolderTooBig
			}
			if n.Kind == workspace.KindDir {
				if err := rec(n.Path); err != nil {
					return err
				}
				continue
			}
			out.bytes += n.Size
		}
		return nil
	}
	if err := rec(abs); err != nil {
		return folderSize{}, err
	}
	return out, nil
}
