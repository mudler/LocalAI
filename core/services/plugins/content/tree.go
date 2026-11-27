// Package content checks the files of a plugin, computes its content digest,
// writes it once into the workspace store and removes the packages that no
// install uses.
//
// Mode (see .agents/distributed-state.md): the package files are shared and
// immutable, in the workspace store by digest. Garbage collection is a single
// runner under advisorylock.KeyPluginsRefresh.
package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The two modes a package keeps.
const (
	ModeFile = "0644"
	ModeExec = "0755"
)

// Limits caps a plugin tree.
type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxDepth      int // segments of a file path
	MaxPathBytes  int
	// MaxTextBytes caps SKILL.md, command and agent files.
	MaxTextBytes int64
	// MaxConfigBytes caps plugin.json, hooks.json and .mcp.json.
	MaxConfigBytes int64
}

// DefaultLimits returns the limits of the design. MaxTotalBytes is the
// default of the plugins_max_bytes setting; the caller sets the configured
// value.
func DefaultLimits() Limits {
	return Limits{
		MaxFiles: 2000, MaxFileBytes: 5 << 20, MaxTotalBytes: 20 << 20,
		MaxDepth: 16, MaxPathBytes: 255, MaxTextBytes: 64 << 10, MaxConfigBytes: 256 << 10,
	}
}

// File is one file of a checked tree.
type File struct {
	Path   string // relative, with forward slashes
	Size   int64
	Mode   string // ModeFile or ModeExec
	SHA256 string // hex
	// Script is set when the file starts with "#!".
	Script bool
}

// Tree is the checked list of the files of a plugin, sorted by path.
type Tree struct {
	Files []File
	Size  int64
	// Manifest is the SHA-256 (hex) of the effective manifest: what the
	// plugin contributes once the marketplace entry is merged in. Two
	// entries can point at the same files and add different components, so
	// the digest must name both. Empty for a bare tree.
	Manifest string
}

// ErrRefused is the error of a tree or a path that breaks a rule. A
// *CheckError carries the rule.
var ErrRefused = errors.New("plugin content refused")

// Codes of CheckError.
const (
	CodeTraversal         = "traversal"
	CodeAbsolute          = "absolute-path"
	CodeBackslash         = "backslash"
	CodeNUL               = "nul"
	CodeDrive             = "drive-letter"
	CodeBadName           = "bad-name"
	CodeSpecialFile       = "special-file"
	CodeSymlink           = "symlink"
	CodeCaseCollision     = "case-collision"
	CodeTooManyFiles      = "too-many-files"
	CodeFileTooLarge      = "file-too-large"
	CodeTooLarge          = "too-large"
	CodeTooDeep           = "too-deep"
	CodePathTooLong       = "path-too-long"
	CodeComponentTooLarge = "component-too-large"
	// CodeFormatChar is a path with an invisible format character (Unicode
	// category Cf: bidi overrides and isolates, zero-width characters, the
	// byte order mark), which can make a name in the review look like
	// another one.
	CodeFormatChar = "format-character"
)

// CheckError names the path and the rule it breaks. errors.Is(err,
// ErrRefused) holds for it.
type CheckError struct {
	Path string
	Code string
}

func (e *CheckError) Error() string {
	return fmt.Sprintf("plugin content refused: %s: %q", e.Code, e.Path)
}

// Is makes the error match ErrRefused.
func (e *CheckError) Is(target error) bool { return target == ErrRefused }

func refuse(p, code string) error { return &CheckError{Path: p, Code: code} }

// CheckRelPath checks that p is a clean relative path with forward slashes:
// no "..", no leading "/", no backslash, no NUL, no drive letter, no empty
// or "." segment, valid UTF-8, no control character and no invisible format
// character.
func CheckRelPath(p string) error {
	switch {
	case p == "":
		return refuse(p, CodeBadName)
	case strings.ContainsRune(p, 0):
		return refuse(p, CodeNUL)
	case strings.HasPrefix(p, "/"):
		return refuse(p, CodeAbsolute)
	case strings.Contains(p, `\`):
		return refuse(p, CodeBackslash)
	case len(p) >= 2 && p[1] == ':' && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z'):
		return refuse(p, CodeDrive)
	case !utf8.ValidString(p):
		return refuse(p, CodeBadName)
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return refuse(p, CodeBadName)
		}
		if unicode.Is(unicode.Cf, r) {
			return refuse(p, CodeFormatChar)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "..":
			return refuse(p, CodeTraversal)
		case "", ".":
			return refuse(p, CodeBadName)
		}
	}
	return nil
}

// componentCap returns the cap of a file that the format reads as a
// component, or 0.
func componentCap(p string, lim Limits) int64 {
	base := path.Base(p)
	first, _, _ := strings.Cut(p, "/")
	switch {
	case p == ".claude-plugin/plugin.json", p == "hooks/hooks.json", p == ".mcp.json":
		return lim.MaxConfigBytes
	case base == "SKILL.md":
		return lim.MaxTextBytes
	case (first == "commands" || first == "agents") && strings.HasSuffix(base, ".md"):
		return lim.MaxTextBytes
	}
	return 0
}

// Check walks the plugin folder dir and checks every file against the rules
// of the design. A symlink to a file inside dir is read as that file; any
// other symlink, and a device, socket or pipe, is refused. .git folders are
// left out: git never stores one, so they come from the checkout.
func Check(dir string, lim Limits) (Tree, error) {
	rootAbs, err := filepath.Abs(dir)
	if err != nil {
		return Tree{}, err
	}
	if rootAbs, err = filepath.EvalSymlinks(rootAbs); err != nil {
		return Tree{}, err
	}
	var t Tree
	folded := map[string]string{}
	err = filepath.WalkDir(rootAbs, func(full string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if full == rootAbs {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, full)
		if err != nil {
			return err
		}
		if filepath.Separator != '/' && strings.ContainsRune(d.Name(), '/') {
			return refuse(rel, CodeBadName)
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if err := CheckRelPath(rel); err != nil {
			return err
		}
		if len(rel) > lim.MaxPathBytes {
			return refuse(rel, CodePathTooLong)
		}
		if strings.Count(rel, "/")+1 > lim.MaxDepth {
			return refuse(rel, CodeTooDeep)
		}
		// The workspace store compares names folded, so two paths that fold
		// to the same text cannot both be stored.
		key := foldPath(rel)
		if other, ok := folded[key]; ok {
			return refuse(rel+" and "+other, CodeCaseCollision)
		}
		folded[key] = rel
		if d.IsDir() {
			return nil
		}

		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		src := full
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := filepath.EvalSymlinks(full)
			if err != nil || !within(rootAbs, target) {
				return refuse(rel, CodeSymlink)
			}
			if info, err = os.Stat(target); err != nil || !info.Mode().IsRegular() {
				return refuse(rel, CodeSymlink)
			}
			src = target
		case !info.Mode().IsRegular():
			return refuse(rel, CodeSpecialFile)
		}

		if len(t.Files)+1 > lim.MaxFiles {
			return refuse(rel, CodeTooManyFiles)
		}
		if info.Size() > lim.MaxFileBytes {
			return refuse(rel, CodeFileTooLarge)
		}
		if c := componentCap(rel, lim); c > 0 && info.Size() > c {
			return refuse(rel, CodeComponentTooLarge)
		}
		f, err := hashFile(src, lim.MaxFileBytes)
		if err != nil {
			return err
		}
		if f.Size > lim.MaxFileBytes {
			return refuse(rel, CodeFileTooLarge)
		}
		if c := componentCap(rel, lim); c > 0 && f.Size > c {
			return refuse(rel, CodeComponentTooLarge)
		}
		t.Size += f.Size
		if t.Size > lim.MaxTotalBytes {
			return refuse(rel, CodeTooLarge)
		}
		f.Path = rel
		f.Mode = ModeFile
		if info.Mode().Perm()&0o111 != 0 {
			f.Mode = ModeExec
		}
		t.Files = append(t.Files, f)
		return nil
	})
	if err != nil {
		return Tree{}, err
	}
	sort.Slice(t.Files, func(i, j int) bool { return t.Files[i].Path < t.Files[j].Path })
	return t, nil
}

func foldPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = workspace.FoldName(s)
	}
	return strings.Join(segs, "/")
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && filepath.IsLocal(rel)
}

// hashFile reads at most limit+1 bytes, so a file that grew after the
// Lstat is still caught.
func hashFile(p string, limit int64) (File, error) {
	fh, err := os.Open(p)
	if err != nil {
		return File{}, err
	}
	defer fh.Close()
	h := sha256.New()
	var head [2]byte
	n, err := io.ReadFull(fh, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return File{}, err
	}
	h.Write(head[:n])
	rest, err := io.Copy(h, io.LimitReader(fh, limit+1-int64(n)))
	if err != nil {
		return File{}, err
	}
	return File{Size: int64(n) + rest, SHA256: hex.EncodeToString(h.Sum(nil)), Script: bytes.Equal(head[:n], []byte("#!"))}, nil
}
