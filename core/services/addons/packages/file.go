// SPDX-License-Identifier: MIT
package packages

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
)

// Codes of a FileError. They tell the administrator which rule a file broke.
const (
	FileBadZip          = "bad_zip"
	FileUnsafePath      = "unsafe_path"
	FileUnsupportedType = "unsupported_entry"
	FileTooLarge        = "too_large"
	FileTooManyFiles    = "too_many_files"
	FileRatio           = "compression_ratio"
	FileBadManifest     = "bad_manifest"
	FileDigestMismatch  = "digest_mismatch"
	FileWrongPlatform   = "wrong_platform"
	FileBadMode         = "bad_mode"
)

// FileError is a refusal of a local file. Its text is fixed and safe to show.
// It matches ErrPackage, so every caller that treats an unverified package as
// a refusal treats this one the same way.
type FileError struct{ Code, Msg string }

func (e *FileError) Error() string { return e.Msg }
func (e *FileError) Unwrap() error { return ErrPackage }

func fileErr(code, format string, a ...any) *FileError {
	return &FileError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// maxRatio is the largest expanded to compressed ratio of a file above
// ratioFloor bytes. A real executable compresses far less than this.
const (
	maxRatio   = 200
	ratioFloor = 1 << 20
	// maxZipManifestBytes bounds the digest manifest itself.
	maxZipManifestBytes = 4 << 20
)

var fileNameRE = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Limits returns the byte and file caps of a package, the ones that apply to
// a library package and to a local file alike.
func (i *Installer) Limits() (bytes int64, files int) { return i.byteLimit(), i.fileLimit() }

// FileSummary is what a verified zip says about itself. It is computed without
// writing the package to disk.
type FileSummary struct {
	// SHA256 is the hex digest of the whole zip file.
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Files  int    `json:"files"`
	zipformat.Manifest
	// Package is manifest.json of the add-on, parsed. InspectFile fills it; it
	// is not checked against a host there.
	Package contract.Manifest `json:"package"`
}

// zipSink receives each file of the package after the path and type checks.
// It must read r to the end; readZip then compares the digest and the size.
type zipSink func(name string, exec bool, r io.Reader) error

// readZip applies every check that does not need the host: the shape of the
// zip, the paths, the entry types, the size and file caps, the digest list.
// maxBytes caps both the zip and the sum of the expanded files.
func readZip(ctx context.Context, p string, maxBytes int64, maxFiles int, accept func(zipformat.Manifest) error, sink zipSink) (FileSummary, error) {
	var out FileSummary
	info, e := os.Lstat(p)
	if e != nil || !info.Mode().IsRegular() {
		return out, fileErr(FileBadZip, "The file is not a regular file")
	}
	if info.Size() > maxBytes {
		return out, fileErr(FileTooLarge, "The file is larger than the %d MiB limit", maxBytes>>20)
	}
	f, e := os.Open(p) // #nosec G304 -- the path is in the private staging area or named by the local administrator
	if e != nil {
		return out, fileErr(FileBadZip, "The file cannot be read")
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(contextReader{ctx, f}, maxBytes+1)); e != nil {
		return out, e
	}
	out.SHA256, out.Size = hex.EncodeToString(h.Sum(nil)), info.Size()
	zr, e := zip.NewReader(f, info.Size())
	if e != nil {
		return out, fileErr(FileBadZip, "The file is not a zip archive")
	}
	if len(zr.File) > maxFiles+1 {
		return out, fileErr(FileTooManyFiles, "The zip holds too many files")
	}
	var manifestEntry *zip.File
	byName := map[string]*zip.File{}
	folded := map[string]bool{}
	for _, zf := range zr.File {
		n := strings.TrimSuffix(zf.Name, "/")
		if n == "" || n == "." || path.Clean(n) != n || strings.HasPrefix(n, "/") || n == ".." || strings.HasPrefix(n, "../") || strings.ContainsAny(n, "\\:\x00") || zf.NonUTF8 && !utf8Valid(zf.Name) {
			return out, fileErr(FileUnsafePath, "The zip holds an unsafe file path")
		}
		key := strings.ToLower(n)
		if folded[key] {
			return out, fileErr(FileUnsafePath, "The zip holds a path twice")
		}
		folded[key] = true
		mode := zf.Mode()
		switch {
		case mode.IsDir():
			continue
		case !mode.IsRegular() || strings.HasSuffix(zf.Name, "/"):
			return out, fileErr(FileUnsupportedType, "The zip holds a link or a special file")
		}
		if zf.Method != zip.Store && zf.Method != zip.Deflate {
			return out, fileErr(FileBadZip, "The zip uses an unsupported compression method")
		}
		if n == zipformat.ManifestName {
			manifestEntry = zf
			continue
		}
		byName[n] = zf
	}
	if manifestEntry == nil || manifestEntry.UncompressedSize64 > maxZipManifestBytes {
		return out, fileErr(FileBadManifest, "The zip has no usable %s", zipformat.ManifestName)
	}
	rc, e := manifestEntry.Open()
	if e != nil {
		return out, fileErr(FileBadZip, "The zip cannot be read")
	}
	raw, e := io.ReadAll(io.LimitReader(rc, maxZipManifestBytes+1))
	_ = rc.Close()
	if e != nil || len(raw) > maxZipManifestBytes {
		return out, fileErr(FileBadManifest, "The zip has no usable %s", zipformat.ManifestName)
	}
	zm, e := zipformat.ParseManifest(raw)
	if e != nil {
		return out, fileErr(FileBadManifest, "The digest manifest of the zip is not valid")
	}
	out.Manifest = zm
	if accept != nil {
		if e = accept(zm); e != nil {
			return out, e
		}
	}
	if len(zm.Files) != len(byName) || len(zm.Files) > maxFiles {
		return out, fileErr(FileDigestMismatch, "The files of the zip are not the files its digest manifest lists")
	}
	var total int64
	for _, want := range zm.Files {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		zf, ok := byName[want.Path]
		if !ok || int64(zf.UncompressedSize64) != want.Size || want.Size < 0 {
			return out, fileErr(FileDigestMismatch, "The files of the zip are not the files its digest manifest lists")
		}
		if zf.Mode().Perm()&0o111 != 0 && want.Path != zm.Executable {
			return out, fileErr(FileBadMode, "Only the executable of the add-on may be executable")
		}
		if zf.UncompressedSize64 > uint64(maxBytes-total) {
			return out, fileErr(FileTooLarge, "The files of the zip are larger than the %d MiB limit", maxBytes>>20)
		}
		if zf.UncompressedSize64 > ratioFloor && zf.UncompressedSize64 > maxRatio*zf.CompressedSize64 {
			return out, fileErr(FileRatio, "A file of the zip compresses far more than a real add-on file does")
		}
		rc, e := zf.Open()
		if e != nil {
			return out, fileErr(FileBadZip, "The zip cannot be read")
		}
		fh := sha256.New()
		counted := &countingReader{r: io.LimitReader(contextReader{ctx, rc}, want.Size+1)}
		e = sink(want.Path, want.Path == zm.Executable, io.TeeReader(counted, fh))
		if e == nil {
			// A sink that stopped early leaves bytes behind; the digest and
			// the count must cover the whole file.
			_, e = io.Copy(io.Discard, io.TeeReader(counted, fh))
		}
		_ = rc.Close()
		if e != nil {
			var fe *FileError
			if errors.As(e, &fe) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return out, e
			}
			// The reader of archive/zip reports a size or CRC that does not
			// match the header as an error.
			if errors.Is(e, zip.ErrChecksum) || errors.Is(e, zip.ErrFormat) || errors.Is(e, io.ErrUnexpectedEOF) {
				return out, fileErr(FileBadZip, "The zip is damaged")
			}
			return out, e
		}
		if counted.n != want.Size || hex.EncodeToString(fh.Sum(nil)) != want.SHA256 {
			return out, fileErr(FileDigestMismatch, "A file of the zip does not match its digest")
		}
		total += counted.n
		out.Files++
	}
	return out, nil
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "") == s }

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, e := c.r.Read(b)
	c.n += int64(n)
	return n, e
}

// InspectFile verifies a zip without writing it anywhere and says what it
// holds. It does not know the host, so it cannot say that the platform fits;
// the owner's StageFile does. maxBytes and maxFiles are the caps of the
// installer; zero takes the defaults.
func InspectFile(ctx context.Context, p string, maxBytes int64, maxFiles int) (FileSummary, error) {
	i := &Installer{MaxBytes: maxBytes, MaxFiles: maxFiles}
	var pkg contract.Manifest
	sum, e := readZip(ctx, p, i.byteLimit(), i.fileLimit(), nil, func(n string, _ bool, r io.Reader) error {
		if n != "manifest.json" {
			_, e := io.Copy(io.Discard, r)
			return e
		}
		b, e := io.ReadAll(io.LimitReader(r, contract.DefaultLimits().ManifestBytes+1))
		if e != nil {
			return e
		}
		if int64(len(b)) > contract.DefaultLimits().ManifestBytes {
			return fileErr(FileBadManifest, "manifest.json is too large")
		}
		m, e := contract.ParseManifest(b)
		if e != nil {
			return fileErr(FileBadManifest, "manifest.json is not a valid add-on manifest")
		}
		pkg = m
		return nil
	})
	if e != nil {
		return sum, e
	}
	if pkg.ID == "" || pkg.ID != sum.ID || pkg.Version != sum.Version || pkg.OS != sum.OS || pkg.Arch != sum.Arch {
		return sum, fileErr(FileBadManifest, "The manifest of the add-on does not match the digest manifest of the zip")
	}
	sum.Package = pkg
	return sum, nil
}

// SafeFileName reduces the name a client gave a file to characters that are
// safe in a receipt. It is a label only; nothing opens a path by it.
func SafeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = fileNameRE.ReplaceAllString(name, "_")
	if len(name) > 100 {
		name = name[len(name)-100:]
	}
	if name == "" || name == "." || name == ".." {
		return "package.zip"
	}
	return name
}

// StageFile verifies the local zip at p and stages its package exactly as
// Stage stages a library package: same stage directory, same checks of the
// manifest, executable, UI, icon and skills, same receipt and same Commit. No
// signature exists for a file, so the receipt says so. name is the file name
// the administrator chose, kept in the receipt as a label.
func (i *Installer) StageFile(ctx context.Context, p, name string, progress func(Progress)) (out StagedPackage, err error) {
	if e := ctx.Err(); e != nil {
		return out, e
	}
	root, e := filepath.Abs(i.Root)
	if e != nil || i.Root == "" {
		return out, ErrPackage
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return out, e
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return out, ErrPackage
	}
	dir, e := os.MkdirTemp(root, "stage-")
	if e != nil {
		return out, e
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	if progress != nil {
		progress(Progress{Phase: "download"})
	}
	var written int64
	accept := func(zm zipformat.Manifest) error {
		// Refuse another platform before the first byte is written.
		if zm.OS != i.Host.OS || zm.Arch != i.Host.Arch {
			return fileErr(FileWrongPlatform, "This file is for %s/%s; this host is %s/%s", zm.OS, zm.Arch, i.Host.OS, i.Host.Arch)
		}
		return nil
	}
	sum, e := readZip(ctx, p, i.byteLimit(), i.fileLimit(), accept, func(n string, exec bool, r io.Reader) error {
		target := filepath.Join(dir, filepath.FromSlash(n))
		if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		mode := os.FileMode(0600)
		if exec {
			mode = 0700
		}
		f, e := i.create(target, mode)
		if e != nil {
			return e
		}
		w, copyErr := io.Copy(f, r)
		written += w
		if copyErr == nil {
			copyErr = f.Sync()
		}
		ce := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return ce
	})
	if e != nil {
		return out, e
	}
	m, icon, skillFiles, e := i.inspectStage(dir)
	if e != nil {
		return out, e
	}
	if m.ID != sum.ID || m.Version != sum.Version || m.OS != sum.OS || m.Arch != sum.Arch || m.Executable != sum.Executable {
		return out, fileErr(FileBadManifest, "The manifest of the add-on does not match the digest manifest of the zip")
	}
	if _, e = os.Lstat(filepath.Join(dir, "receipt.json")); !os.IsNotExist(e) {
		return out, ErrPackage
	}
	if e = ctx.Err(); e != nil {
		return out, e
	}
	if progress != nil {
		progress(Progress{Phase: "verified", Bytes: written})
	}
	return StagedPackage{&stagedState{owner: i, dir: dir, receipt: PackageReceipt{
		Digest: "sha256:" + sum.SHA256, SourceID: zipformat.SourceID, Selection: m.Version, Reference: "file:" + SafeFileName(name),
		Verification: zipformat.Verification, Origin: zipformat.OriginLabel, Manifest: m, Icon: icon, Skills: skillFiles}}}, nil
}
