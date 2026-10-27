package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

// Limits of the tools.
const (
	// MaxOutput is the most output bytes a tool returns.
	MaxOutput = 64 << 10
	// MaxFile is the largest file that write and edit handle.
	MaxFile = 8 << 20
	// DefaultTimeout and MaxTimeout bound a bash call.
	DefaultTimeout = 2 * time.Minute
	MaxTimeout     = 30 * time.Minute
)

// tempPrefix starts the name of a temporary file of writeFile.
const tempPrefix = ".sbx-w."

// maxLineArg bounds the offset and limit of read, so that their sum cannot
// overflow an int32.
const maxLineArg = 1 << 29

// fileToolTimeout bounds a call of read, glob and grep. It is a variable so
// that the tests can shorten it.
var fileToolTimeout = 2 * time.Minute

// NoSandboxText is the result of a tool on a host without sandboxes.
const NoSandboxText = "This host cannot run commands."

// Acquire returns the chat's sandbox, creating or resuming it when needed,
// and a function that the tool calls when it is done. It returns ErrNoSandbox
// when the host cannot run sandboxes.
type Acquire func(ctx context.Context) (sandbox.Sandbox, func(), error)

// NewTools returns the six tools of a chat. Each runs in the sandbox that
// acquire returns.
//
// LocalAI does not call it: it sets no Options.Sandbox, and the code of a chat
// runs through the sandbox add-on, which keeps its own copy of these tools in
// addons/sandbox/internal/codetools. This one stays for a host that gives the
// manager a sandbox. A change to the behaviour of a tool belongs in both.
func NewTools(acquire Acquire) []Tool {
	r := &runner{acquire: acquire}
	return []Tool{
		r.tool("bash", "Run a shell script with bash in "+GuestRoot+". Output is cut at 64 KiB. A call stops after timeout_seconds (default 120, at most 1800).",
			`{"type":"object","properties":{"command":{"type":"string","description":"The script to run."},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800}},"required":["command"]}`, r.bash),
		r.tool("read", "Read lines of a text file. offset is the first line (default 1), limit the number of lines (default 2000).",
			`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1}},"required":["path"]}`, r.read),
		r.tool("write", "Write a file. It replaces the file when it exists and creates the folders above it.",
			`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`, r.write),
		r.tool("edit", "Replace text in a file. old_string must match exactly once, unless replace_all is true.",
			`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"]}`, r.edit),
		r.tool("glob", "List files whose path matches a pattern. In the pattern, * matches any characters including /. At most 500 results.",
			`{"type":"object","properties":{"pattern":{"type":"string","description":"For example *.md or reports/*.xlsx"},"path":{"type":"string","description":"The folder to search. Default: the working directory."}},"required":["pattern"]}`, r.glob),
		r.tool("grep", "Search file contents with an extended regular expression. At most 200 lines.",
			`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string","description":"A file or folder. Default: the working directory."},"ignore_case":{"type":"boolean"}},"required":["pattern"]}`, r.grep),
	}
}

type runner struct{ acquire Acquire }

func (r *runner) tool(name, desc, schema string, fn func(ctx context.Context, sb sandbox.Sandbox, args json.RawMessage) (string, error)) Tool {
	return Tool{
		Name: name, Description: desc, Schema: json.RawMessage(schema),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			sb, done, err := r.acquire(ctx)
			if errors.Is(err, ErrNoSandbox) {
				return NoSandboxText, nil
			}
			if err != nil {
				return "", err
			}
			defer done()
			return fn(ctx, sb, args)
		},
	}
}

// GuestPath turns a path from the model into a path below the working
// directory: "/workspace/home/a.txt" and "home/a.txt" both give "home/a.txt".
// It refuses a path outside /workspace. A name that starts with a dash gets
// a "./" prefix, so that no command reads it as an option.
//
// The check is lexical: it looks only at the text of p. The guest overlay is
// the boundary: the tools run in the guest, and a symbolic link or a mount
// there cannot reach past the view that the sandbox was created with.
func GuestPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: empty path", ErrInvalid)
	}
	if strings.HasPrefix(p, "/") {
		rest, ok := strings.CutPrefix(path.Clean(p), GuestRoot)
		if !ok || (rest != "" && rest[0] != '/') {
			return "", fmt.Errorf("%w: %q is outside %s", ErrInvalid, p, GuestRoot)
		}
		p = strings.TrimPrefix(rest, "/")
		if p == "" {
			p = "."
		}
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%w: %q is outside %s", ErrInvalid, p, GuestRoot)
	}
	if strings.HasPrefix(c, "-") {
		c = "./" + c
	}
	return c, nil
}

// noNUL refuses a text argument that carries a NUL byte.
func noNUL(s, what string) error {
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("%w: the %s contains a NUL byte", ErrInvalid, what)
	}
	return nil
}

// capWriter keeps the first max bytes and counts the rest. It is safe for
// the two writers (stdout and stderr) of one process. When the cap is
// reached, full is called once, so that the process can be stopped.
type capWriter struct {
	mu    sync.Mutex
	max   int
	buf   bytes.Buffer
	extra int64
	full  func()
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.max - w.buf.Len()
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		w.buf.Write(p[:room])
	}
	if w.extra == 0 && room < len(p) && w.full != nil {
		w.full()
	}
	w.extra += int64(len(p) - max(room, 0))
	return len(p), nil
}

// cut reports whether output was dropped at the cap.
func (w *capWriter) cut() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.extra > 0
}

func (w *capWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := strings.ToValidUTF8(w.buf.String(), "�")
	if w.extra > 0 {
		s += fmt.Sprintf("\n[output cut: %d more bytes]", w.extra)
	}
	return s
}

// rawWriter keeps the first max bytes exactly as they arrive and records
// whether more came. Unlike capWriter it never changes a byte.
type rawWriter struct {
	max  int
	buf  bytes.Buffer
	over bool
}

func (w *rawWriter) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if len(p) <= room {
		w.buf.Write(p)
		return len(p), nil
	}
	if room > 0 {
		w.buf.Write(p[:room])
	}
	w.over = true
	return len(p), nil
}

// run runs args in the guest and returns the output of stdout and stderr in
// one string, and the exit code. When the output reaches limit, run stops the
// process at once and returns the output with its cut marker, and no error.
func run(ctx context.Context, sb sandbox.Sandbox, args []string, stdin io.Reader, limit int) (string, int, error) {
	rctx, stop := context.WithCancel(ctx)
	defer stop()
	out := &capWriter{max: limit, full: stop}
	p, err := sb.Exec(rctx, sandbox.Cmd{Args: args, Stdin: stdin, Stdout: out, Stderr: out})
	if err != nil {
		return "", 0, err
	}
	code, err := p.Wait()
	if ctx.Err() == nil && rctx.Err() != nil && out.cut() {
		return out.String(), 0, nil // stopped by us at the cap
	}
	return out.String(), code, err
}

// fileToolTimedOut is the result of a read, glob or grep that ran out of time.
func fileToolTimedOut() string {
	return "timed out after " + fileToolTimeout.String()
}

// readText returns the raw bytes of the guest file p. It refuses a file that
// is larger than MaxFile or is not valid UTF-8, so that a caller never writes
// back a changed copy of a file it could not read exactly.
func readText(ctx context.Context, sb sandbox.Sandbox, p string) (string, error) {
	out := &rawWriter{max: MaxFile}
	errOut := &capWriter{max: 4096}
	script := `[ -f "$1" ] || { echo "not a file: $1" >&2; exit 2; }; cat -- "$1"`
	proc, err := sb.Exec(ctx, sandbox.Cmd{Args: sh(script, p), Stdout: out, Stderr: errOut})
	if err != nil {
		return "", err
	}
	code, err := proc.Wait()
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errors.New(strings.TrimSpace(errOut.String()))
	}
	if out.over {
		return "", fmt.Errorf("the file is larger than %d MiB", MaxFile>>20)
	}
	if !utf8.Valid(out.buf.Bytes()) {
		return "", errors.New("the file is not valid UTF-8 text, so edit did not change it")
	}
	return out.buf.String(), nil
}

func sh(script string, args ...string) []string {
	return append([]string{"sh", "-c", script, "sh"}, args...)
}

func (r *runner) bash(ctx context.Context, sb sandbox.Sandbox, raw json.RawMessage) (string, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || strings.TrimSpace(a.Command) == "" {
		return "", errors.New("bash needs a command")
	}
	if err := noNUL(a.Command, "command"); err != nil {
		return "", err
	}
	timeout := DefaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = MaxTimeout
		if a.TimeoutSeconds < int(MaxTimeout/time.Second) {
			timeout = time.Duration(a.TimeoutSeconds) * time.Second
		}
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, code, err := run(tctx, sb, []string{"bash", "-lc", a.Command}, nil, MaxOutput)
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err() // the turn was stopped
	case errors.Is(err, context.DeadlineExceeded):
		return out + fmt.Sprintf("\n[stopped after %d seconds]", int(timeout.Seconds())), nil
	case err != nil:
		return "", err
	case code != 0:
		return out + fmt.Sprintf("\n[exit code %d]", code), nil
	}
	return out, nil
}

func (r *runner) read(ctx context.Context, sb sandbox.Sandbox, raw json.RawMessage) (string, error) {
	var a struct {
		Path          string `json:"path"`
		Offset, Limit int
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", errors.New("read needs a path")
	}
	p, err := GuestPath(a.Path)
	if err != nil {
		return "", err
	}
	if a.Offset < 1 {
		a.Offset = 1
	}
	if a.Limit < 1 {
		a.Limit = 2000
	}
	a.Offset = min(a.Offset, maxLineArg)
	a.Limit = min(a.Limit, maxLineArg)
	script := `[ -f "$1" ] || { echo "not a file: $1"; exit 2; }; sed -n "$2,$3p" -- "$1"`
	tctx, cancel := context.WithTimeout(ctx, fileToolTimeout)
	defer cancel()
	out, code, err := run(tctx, sb, sh(script, p, fmt.Sprint(a.Offset), fmt.Sprint(a.Offset+a.Limit-1)), nil, MaxOutput)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fileToolTimedOut(), nil
	}
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errors.New(strings.TrimSpace(out))
	}
	return out, nil
}

// writeFile stores content at the guest path p. It writes a temporary file in
// the same folder and renames it over p, so that a write which stops part way
// leaves p as it was. The temporary file takes the mode of p when p exists.
// A symbolic link at p is replaced by a regular file; it is not followed.
func writeFile(ctx context.Context, sb sandbox.Sandbox, p, content string) error {
	if len(content) > MaxFile {
		return fmt.Errorf("the file is larger than %d MiB", MaxFile>>20)
	}
	tmp, err := tempName(p)
	if err != nil {
		return err
	}
	script := `[ ! -d "$1" ] || { echo "is a folder: $1" >&2; exit 2; }; ` +
		`mkdir -p -- "$(dirname -- "$1")" && trap 'rm -f -- "$2"' EXIT && cat > "$2" && ` +
		`{ [ ! -e "$1" ] || chmod --reference="$1" "$2" 2>/dev/null; true; } && mv -f -- "$2" "$1"`
	out, code, err := run(ctx, sb, sh(script, p, tmp), strings.NewReader(content), 4096)
	if err == nil && code == 0 {
		return nil
	}
	// A killed process runs no trap, so the temporary file is removed here,
	// with a context of its own.
	removeTemp(sb, tmp)
	if err != nil {
		return err
	}
	return errors.New(strings.TrimSpace(out))
}

// tempName returns a name for a temporary file in the folder of p. The name
// does not depend on the name of p, so a long file name does not get longer,
// and the random part keeps two writes apart.
func tempName(p string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return path.Join(path.Dir(p), tempPrefix+hex.EncodeToString(b[:])), nil
}

// removeTemp deletes a temporary file of writeFile. It runs even when the
// call that wrote it has ended, and it gives up after a few seconds.
func removeTemp(sb sandbox.Sandbox, tmp string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, _ = run(ctx, sb, sh(`rm -f -- "$1"`, tmp), nil, 4096)
}

func (r *runner) write(ctx context.Context, sb sandbox.Sandbox, raw json.RawMessage) (string, error) {
	var a struct{ Path, Content string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", errors.New("write needs a path and content")
	}
	p, err := GuestPath(a.Path)
	if err != nil {
		return "", err
	}
	if p == "." {
		return "", fmt.Errorf("%w: %q is a folder", ErrInvalid, a.Path)
	}
	if err := writeFile(ctx, sb, p, a.Content); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), p), nil
}

func (r *runner) edit(ctx context.Context, sb sandbox.Sandbox, raw json.RawMessage) (string, error) {
	var a struct {
		Path       string `json:"path"`
		Old        string `json:"old_string"`
		New        string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.Old == "" {
		return "", errors.New("edit needs a path and a non-empty old_string")
	}
	p, err := GuestPath(a.Path)
	if err != nil {
		return "", err
	}
	cur, err := readText(ctx, sb, p)
	if err != nil {
		return "", err
	}
	n := strings.Count(cur, a.Old)
	switch {
	case n == 0:
		return "", errors.New("old_string was not found in the file")
	case n > 1 && !a.ReplaceAll:
		return "", fmt.Errorf("old_string matches %d times; add context or set replace_all", n)
	}
	if err := writeFile(ctx, sb, p, strings.ReplaceAll(cur, a.Old, a.New)); err != nil {
		return "", err
	}
	return fmt.Sprintf("replaced %d occurrence(s) in %s", n, p), nil
}

func (r *runner) glob(ctx context.Context, sb sandbox.Sandbox, raw json.RawMessage) (string, error) {
	var a struct{ Pattern, Path string }
	if err := json.Unmarshal(raw, &a); err != nil || strings.TrimSpace(a.Pattern) == "" {
		return "", errors.New("glob needs a pattern")
	}
	if err := noNUL(a.Pattern, "pattern"); err != nil {
		return "", err
	}
	dir := "."
	if strings.TrimSpace(a.Path) != "" {
		var err error
		if dir, err = GuestPath(a.Path); err != nil {
			return "", err
		}
	}
	pat := strings.TrimPrefix(strings.TrimPrefix(a.Pattern, "./"), "/")
	tctx, cancel := context.WithTimeout(ctx, fileToolTimeout)
	defer cancel()
	out, code, err := run(tctx, sb, sh(`cd -- "$1" 2>/dev/null || { echo "not a folder: $1"; exit 2; }; find . -path "./$2" 2>/dev/null | sed 's|^\./||' | sort | head -n 500`, dir, pat), nil, MaxOutput)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fileToolTimedOut(), nil
	}
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errors.New(strings.TrimSpace(out))
	}
	if strings.TrimSpace(out) == "" {
		return "no files match", nil
	}
	return out, nil
}

func (r *runner) grep(ctx context.Context, sb sandbox.Sandbox, raw json.RawMessage) (string, error) {
	var a struct {
		Pattern    string
		Path       string
		IgnoreCase bool `json:"ignore_case"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.Pattern == "" {
		return "", errors.New("grep needs a pattern")
	}
	if err := noNUL(a.Pattern, "pattern"); err != nil {
		return "", err
	}
	target := "."
	if strings.TrimSpace(a.Path) != "" {
		var err error
		if target, err = GuestPath(a.Path); err != nil {
			return "", err
		}
	}
	flags := "-E"
	if a.IgnoreCase {
		flags = "-Ei"
	}
	tctx, cancel := context.WithTimeout(ctx, fileToolTimeout)
	defer cancel()
	out, _, err := run(tctx, sb, sh(`grep -rnI --exclude-dir=.git "$1" -e "$2" -- "$3" 2>&1 | head -n 200`, flags, a.Pattern, target), nil, MaxOutput)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fileToolTimedOut(), nil
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "no matches", nil
	}
	return out, nil
}
