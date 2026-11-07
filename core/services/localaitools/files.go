package localaitools

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// Size limits for the files that the client tools read. A tool refuses a
// bigger file with ErrFileTooLarge before it sends anything to a model.
const (
	MaxImageBytes = 20 << 20
	MaxAudioBytes = 100 << 20
)

// maxResultBytes bounds one result file that a tool stores. It is the same as
// the answer a model may send back, so a result that fitted in memory is
// saved, and nothing larger is.
var maxResultBytes int64 = maxAnswerBytes

// Errors of a FileStore.
var (
	// ErrFileNotFound means a file reference names nothing the caller may read.
	ErrFileNotFound = errors.New("file not found")
	// ErrFileTooLarge means a file is over the limit of the tool.
	ErrFileTooLarge = errors.New("file too large")
)

type filesCtxKey struct{}

// WithFiles returns a context whose client tool calls read their input files
// from fs instead of the file store of the client. The caller is code in this
// process that already knows what the user may read, for example the chats,
// whose files are fenced to a folder. A nil fs returns ctx unchanged.
func WithFiles(ctx context.Context, fs FileStore) context.Context {
	if fs == nil {
		return ctx
	}
	return context.WithValue(ctx, filesCtxKey{}, fs)
}

func filesFromContext(ctx context.Context) FileStore {
	fs, _ := ctx.Value(filesCtxKey{}).(FileStore)
	return fs
}

// WrongKindError means a file is not of the kind the tool needs.
type WrongKindError struct {
	// What names the file; a caller that knows the file's real name may set it.
	What string
	// Kind is the text after "is not", for example "an image".
	Kind string
}

func (e *WrongKindError) Error() string { return e.What + " is not " + e.Kind }

// File is a file that a FileStore read.
type File struct {
	Name string
	MIME string
	Data []byte
}

// FileStore is where the client tools read their input files from and write
// their results to. A tool never carries file bytes in its arguments or its
// result: it passes references. The in-process client resolves a reference
// in the workspace of the acting user, the standalone MCP server in a local
// folder. A reference that the store does not know is ErrFileNotFound.
type FileStore interface {
	// Read returns the file the reference names. A file of more than max
	// bytes is ErrFileTooLarge.
	Read(ctx context.Context, ref string, max int64) (File, error)
	// Write stores data under a new name that starts with name and returns
	// the reference of the file.
	Write(ctx context.Context, name string, data []byte, mimeType string) (ref string, err error)
}

// DirFileStore is a FileStore on a local folder. It reads a data URL, or a
// file inside Dir given as a path relative to Dir or as an absolute path;
// a path that leaves Dir, by ".." or by a link, is ErrFileNotFound. It writes
// new files into Dir. It serves the standalone MCP server, which has no
// workspace.
type DirFileStore struct {
	Dir string
}

// Read implements FileStore.
func (d DirFileStore) Read(_ context.Context, ref string, max int64) (File, error) {
	if strings.HasPrefix(ref, "data:") {
		return ReadDataURL(ref, max)
	}
	root, err := os.OpenRoot(d.dir())
	if err != nil {
		return File{}, fmt.Errorf("open the files folder: %w", err)
	}
	defer func() { _ = root.Close() }()

	rel := ref
	if filepath.IsAbs(ref) {
		abs, err := filepath.Abs(d.dir())
		if err != nil {
			return File{}, err
		}
		if rel, err = filepath.Rel(abs, filepath.Clean(ref)); err != nil {
			return File{}, fmt.Errorf("%w: %s", ErrFileNotFound, ref)
		}
	}
	// O_NONBLOCK: opening a FIFO for reading would otherwise wait for a
	// writer that may never come. The file is checked on the open handle, not
	// by name, so it cannot change between the check and the read.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return File{}, fmt.Errorf("%w: %s", ErrFileNotFound, ref)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		// A directory, a FIFO, a device or a socket is not a file to read.
		return File{}, fmt.Errorf("%w: %s", ErrFileNotFound, ref)
	}
	if st.Size() > max {
		return File{}, fmt.Errorf("%w: %s is %d bytes, the limit is %d", ErrFileTooLarge, path.Base(ref), st.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return File{}, err
	}
	return File{Name: filepath.Base(rel), MIME: mime.TypeByExtension(filepath.Ext(rel)), Data: data}, nil
}

// Write implements FileStore.
func (d DirFileStore) Write(_ context.Context, name string, data []byte, _ string) (string, error) {
	if int64(len(data)) > maxResultBytes {
		return "", fmt.Errorf("%w: the result is %d bytes, the limit is %d", ErrFileTooLarge, len(data), maxResultBytes)
	}
	dir, err := filepath.Abs(d.dir())
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	full := filepath.Join(dir, UniqueName(name))
	if err := os.WriteFile(full, data, 0o640); err != nil {
		return "", err
	}
	return full, nil
}

func (d DirFileStore) dir() string {
	if d.Dir == "" {
		return "."
	}
	return d.Dir
}

// UniqueName returns name with a short random part before the extension, so
// that a store never overwrites an earlier result.
func UniqueName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	ext := filepath.Ext(name)
	var b [4]byte
	_, _ = rand.Read(b[:])
	return strings.TrimSuffix(name, ext) + "-" + hex.EncodeToString(b[:]) + ext
}

// ReadDataURL decodes a "data:<mime>;base64,<data>" URL.
func ReadDataURL(ref string, max int64) (File, error) {
	head, body, ok := strings.Cut(strings.TrimPrefix(ref, "data:"), ",")
	if !ok {
		return File{}, fmt.Errorf("%w: not a data URL", ErrFileNotFound)
	}
	mt, isB64 := strings.CutSuffix(head, ";base64")
	if !isB64 {
		return File{}, fmt.Errorf("%w: only base64 data URLs are supported", ErrFileNotFound)
	}
	if int64(base64.StdEncoding.DecodedLen(len(body))) > max+3 {
		return File{}, fmt.Errorf("%w: the data URL is over the limit of %d bytes", ErrFileTooLarge, max)
	}
	data, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return File{}, fmt.Errorf("%w: the data URL is not valid base64", ErrFileNotFound)
	}
	if int64(len(data)) > max {
		return File{}, fmt.Errorf("%w: the data URL is over the limit of %d bytes", ErrFileTooLarge, max)
	}
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	return File{Name: "data", MIME: mt, Data: data}, nil
}

// extForMIME returns a file extension for a media type, or "" when unknown.
func extForMIME(mt string) string {
	mt, _, _ = strings.Cut(mt, ";")
	switch strings.TrimSpace(strings.ToLower(mt)) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "application/json":
		return ".json"
	}
	return ""
}

// dataURL encodes a file as a data URL, the form the JSON routes accept for
// an image or audio argument.
func dataURL(f File) string {
	mt := f.MIME
	if mt == "" {
		mt = http.DetectContentType(f.Data)
	}
	return "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(f.Data)
}
