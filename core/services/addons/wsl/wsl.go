// SPDX-License-Identifier: MIT

// Package wsl runs the add-ons whose manifest has osSettings.windows.execution
// "wsl2" in a WSL 2 distribution of this Windows host. It finds a usable distribution, copies
// the package into the file system of the distribution and says how to start
// it with wsl.exe. Launch has the fields of Launch, so a Launcher
// serves as runtime.WSL through a conversion; this package does not import
// the runtime, whose specs use it. It never runs an add-on on Windows: when
// WSL 2 cannot run it, Prepare returns the reason and nothing starts.
//
// Every file goes through the file system of the distribution, which Windows
// reaches as \\wsl.localhost\<distribution>\... The add-on never needs the
// Windows drives that WSL mounts under /mnt, and its data stays on the Linux
// file system, where the guest images and sandboxes are fast.
package wsl

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The refusals. Their text is for people; each says what to do.
var (
	ErrNotInstalled = errors.New("WSL is not installed. Install it with \"wsl --install\" in a terminal that runs as administrator, restart Windows, then restart LocalAI")
	ErrTooOld       = errors.New("this WSL is too old to list its distributions. Update it with \"wsl --update\", then restart LocalAI")
	ErrNoDistro     = errors.New("WSL has no Linux distribution. Install one with \"wsl --install -d Ubuntu\", then restart LocalAI")
	ErrDistro       = errors.New("the WSL distribution cannot run add-ons")
)

// Options configures a Launcher.
type Options struct {
	// Distro is the distribution that runs the add-ons. Empty uses the
	// default distribution of WSL.
	Distro string
	// Exe is wsl.exe. Empty is wsl.exe in the System32 folder of Windows.
	Exe string
	// Run runs Exe with args and returns its standard output. Nil runs it.
	Run func(ctx context.Context, exe string, args ...string) ([]byte, error)
	// HostPath maps a path of the distribution to the path this host reaches
	// it by. Nil is \\wsl.localhost\<distro>\<path>.
	HostPath func(distro, linuxPath string) string
	// Env is added to the environment of wsl.exe for every launch, besides
	// WSLENV. Nil takes USERPROFILE, LOCALAPPDATA and SystemDrive from this
	// process, which wsl.exe may need to find the distributions of the user.
	Env []string
}

// Launch is how to start one add-on in WSL 2. It has the fields of
// Launch, in the same order.
type Launch struct {
	Path                 string
	Args                 []string
	Env                  []string
	DataDir, HostDataDir string
}

// Launcher prepares the launches.
type Launcher struct {
	o    Options
	mu   sync.Mutex
	home map[string]string
	// install holds the one copy of a package that may run at a time.
	install chan struct{}
}

// copyBudget bounds one copy of a package into the distribution. The copy
// goes on when the start that asked for it gives up, so a slow first copy
// serves the next start.
const copyBudget = 15 * time.Minute

// New returns a launcher.
func New(o Options) *Launcher {
	if o.Exe == "" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		o.Exe = filepath.Join(root, "System32", "wsl.exe")
	}
	if o.Run == nil {
		o.Run = run
	}
	if o.HostPath == nil {
		o.HostPath = UNCPath
	}
	if o.Env == nil {
		for _, k := range []string{"USERPROFILE", "LOCALAPPDATA", "SystemDrive"} {
			if v := os.Getenv(k); v != "" {
				o.Env = append(o.Env, k+"="+v)
			}
		}
	}
	return &Launcher{o: o, home: map[string]string{}, install: make(chan struct{}, 1)}
}

// UNCPath is the path by which Windows reaches linuxPath of a distribution.
func UNCPath(distro, linuxPath string) string {
	return `\\wsl.localhost\` + distro + strings.ReplaceAll(linuxPath, "/", `\`)
}

// run starts wsl.exe with no console input. WSL_UTF8 asks a recent wsl.exe to
// write its own messages as UTF-8; an older one writes UTF-16, which decode
// reads too.
func run(ctx context.Context, exe string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, exe, args...) // #nosec G204 -- wsl.exe with arguments this package builds
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(decode(errb.Bytes()) + " " + decode(out.Bytes()))
		return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
	}
	return out.Bytes(), nil
}

// Distro is one row of "wsl --list --verbose".
type Distro struct {
	Name    string
	Default bool
	Version int
}

// decode reads the output of wsl.exe: UTF-16LE (with or without a byte order
// mark) from most versions, UTF-8 when WSL_UTF8 is honoured.
func decode(b []byte) string {
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		b = b[2:]
		return utf16le(b)
	}
	zeros := 0
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			zeros++
		}
	}
	if len(b) >= 2 && zeros*2 >= len(b)/2 {
		return utf16le(b)
	}
	return string(b)
}

func utf16le(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// ParseList reads the output of "wsl --list --verbose". The first line is the
// header, whose words Windows translates, so only the shape is read: an
// optional "*" for the default, the name, the state and the version.
func ParseList(out string) []Distro {
	var ds []Distro
	header := true
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" {
			continue
		}
		if header {
			header = false
			continue
		}
		d := Distro{}
		if strings.HasPrefix(line, "*") {
			d.Default = true
			line = strings.TrimSpace(line[1:])
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		d.Name = f[0]
		switch f[len(f)-1] {
		case "1":
			d.Version = 1
		case "2":
			d.Version = 2
		default:
			continue
		}
		ds = append(ds, d)
	}
	return ds
}

// distroName is what WSL accepts as the name of a distribution.
var distroName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Check finds the distribution that runs the add-ons and checks that it is a
// WSL 2 distribution. It is what the status of the Sandbox add-on shows when
// the add-on cannot start.
func (l *Launcher) Check(ctx context.Context) (Distro, error) {
	if _, err := os.Stat(l.o.Exe); err != nil && filepath.IsAbs(l.o.Exe) {
		return Distro{}, ErrNotInstalled
	}
	if _, err := l.o.Run(ctx, l.o.Exe, "--status"); err != nil {
		return Distro{}, fmt.Errorf("%w (%v)", ErrNotInstalled, err)
	}
	out, err := l.o.Run(ctx, l.o.Exe, "--list", "--verbose")
	if err != nil {
		return Distro{}, fmt.Errorf("%w (%v)", ErrTooOld, err)
	}
	ds := ParseList(decode(out))
	if len(ds) == 0 {
		return Distro{}, ErrNoDistro
	}
	var d *Distro
	for i := range ds {
		if (l.o.Distro == "" && ds[i].Default) || (l.o.Distro != "" && strings.EqualFold(ds[i].Name, l.o.Distro)) {
			d = &ds[i]
			break
		}
	}
	switch {
	case d == nil && l.o.Distro != "":
		return Distro{}, fmt.Errorf("%w: there is no distribution %q (set --addons-wsl-distro to one that \"wsl --list --verbose\" shows)", ErrDistro, l.o.Distro)
	case d == nil:
		return Distro{}, fmt.Errorf("%w: WSL has no default distribution; set one with \"wsl --set-default <name>\" or set --addons-wsl-distro", ErrDistro)
	case !distroName.MatchString(d.Name):
		return Distro{}, fmt.Errorf("%w: the name %q is not supported", ErrDistro, d.Name)
	case d.Version != 2:
		return Distro{}, fmt.Errorf("%w: %q uses WSL 1; convert it with \"wsl --set-version %s 2\"", ErrDistro, d.Name, d.Name)
	}
	return *d, nil
}

// homeOf returns the home folder of the default user of the distribution.
func (l *Launcher) homeOf(ctx context.Context, distro string) (string, error) {
	l.mu.Lock()
	h, ok := l.home[distro]
	l.mu.Unlock()
	if ok {
		return h, nil
	}
	out, err := l.o.Run(ctx, l.o.Exe, "-d", distro, "--exec", "/bin/sh", "-c", `printf '%s' "$HOME"`)
	if err != nil {
		return "", fmt.Errorf("%w: cannot start a program in %q: %v", ErrDistro, distro, err)
	}
	h = strings.TrimSpace(string(out))
	if !path.IsAbs(h) || path.Clean(h) != h || h == "/" || strings.ContainsAny(h, "\\:\x00\n") {
		return "", fmt.Errorf("%w: the home folder of the default user of %q is not usable (%q)", ErrDistro, distro, h)
	}
	l.mu.Lock()
	l.home[distro] = h
	l.mu.Unlock()
	return h, nil
}

// Base is the folder of the add-ons in the home folder of the distribution.
const Base = ".local/share/localai-addons"

// Prepare readies one launch of the package. The package is copied, once per digest, to
// <home>/.local/share/localai-addons/packages/<id>/<digest> in the
// distribution, and the data directory is <home>/.local/share/localai-addons/data/<id>.
func (l *Launcher) Prepare(ctx context.Context, pkg packages.PackageReceipt) (Launch, error) {
	d, err := l.Check(ctx)
	if err != nil {
		return Launch{}, err
	}
	home, err := l.homeOf(ctx, d.Name)
	if err != nil {
		return Launch{}, err
	}
	id := pkg.Manifest.ID
	digest := strings.TrimPrefix(pkg.Digest, "sha256:")
	if !safeElement(id) || !safeElement(digest) {
		return Launch{}, errors.New("the package has no usable ID or digest")
	}
	base := path.Join(home, Base)
	pkgDir := path.Join(base, "packages", id, digest)
	if err := l.installPackage(ctx, d.Name, pkg, pkgDir); err != nil {
		return Launch{}, err
	}
	dataDir := path.Join(base, "data", id)
	exe := path.Join(pkgDir, pkg.Manifest.Executable)
	env := append([]string{"WSLENV=LANG:TZ:" + contract.ChannelEnv}, l.o.Env...)
	return Launch{
		Path:        l.o.Exe,
		Args:        []string{"-d", d.Name, "--cd", pkgDir, "--exec", exe, "--localai-addon-v1"},
		Env:         env,
		DataDir:     dataDir,
		HostDataDir: l.o.HostPath(d.Name, dataDir),
	}, nil
}

func safeElement(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\:\x00") && len(s) <= 128
}

// complete marks a package folder whose copy finished and was checked.
const complete = ".localai-complete"

// installPackage copies the package to pkgDir in the distribution, unless a
// finished copy of the same digest is there. The copy goes to a new folder
// first and is renamed when it is whole, so a crash leaves no half package.
// The files get the owner's execute bit, which a copy from Windows does not
// carry, and the executable is read back and compared.
func (l *Launcher) installPackage(ctx context.Context, distro string, pkg packages.PackageReceipt, pkgDir string) error {
	select {
	case l.install <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("%w: a copy of the add-on into the distribution is still running; the add-on starts after it", ErrDistro)
	}
	done := make(chan error, 1)
	go func() {
		defer func() { <-l.install }()
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), copyBudget)
		defer cancel()
		done <- l.copyPackage(bounded, distro, pkg, pkgDir)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("%w: the copy of the add-on into the distribution takes longer than a start may wait; it goes on, and the next start uses it", ErrDistro)
	}
}

// copyPackage does the copy of installPackage.
func (l *Launcher) copyPackage(ctx context.Context, distro string, pkg packages.PackageReceipt, pkgDir string) error {
	hostDir := l.o.HostPath(distro, pkgDir)
	if b, err := os.ReadFile(filepath.Join(hostDir, complete)); err == nil && string(b) == pkg.Digest { // #nosec G304 -- a path in the add-on folder of the distribution
		return nil
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	stageLinux := pkgDir + ".tmp-" + hex.EncodeToString(suffix)
	stage := l.o.HostPath(distro, stageLinux)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return fmt.Errorf("%w: cannot write to %s: %v", ErrDistro, stage, err)
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := copyTree(pkg.Directory, stage); err != nil {
		return fmt.Errorf("%w: cannot copy the add-on into the distribution: %v", ErrDistro, err)
	}
	if _, err := l.o.Run(ctx, l.o.Exe, "-d", distro, "--exec", "/bin/chmod", "-R", "u+rwx,go-rwx", stageLinux); err != nil {
		return fmt.Errorf("%w: cannot make the add-on executable in the distribution: %v", ErrDistro, err)
	}
	want, err := fileHash(filepath.Join(pkg.Directory, filepath.FromSlash(pkg.Manifest.Executable)))
	if err != nil {
		return err
	}
	got, err := fileHash(filepath.Join(stage, filepath.FromSlash(pkg.Manifest.Executable)))
	if err != nil || got != want {
		return fmt.Errorf("%w: the copy of the add-on in the distribution does not match the package", ErrDistro)
	}
	if err := os.WriteFile(filepath.Join(stage, complete), []byte(pkg.Digest), 0o600); err != nil {
		return err
	}
	_ = os.RemoveAll(hostDir)
	if err := os.MkdirAll(filepath.Dir(hostDir), 0o700); err != nil {
		return err
	}
	if err := os.Rename(stage, hostDir); err != nil {
		return fmt.Errorf("%w: cannot place the add-on in the distribution: %v", ErrDistro, err)
	}
	done = true
	// Copies of other versions of this add-on are not used any more: the
	// supervisor runs one version of an add-on at a time.
	parent := filepath.Dir(hostDir)
	if others, err := os.ReadDir(parent); err == nil {
		for _, o := range others {
			if o.Name() != filepath.Base(hostDir) {
				_ = os.RemoveAll(filepath.Join(parent, o.Name()))
			}
		}
	}
	return nil
}

// copyTree copies the regular files and folders of src into dst. A link or
// any other kind of file in a package is refused.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case e.IsDir():
			return os.MkdirAll(target, 0o700)
		case e.Type().IsRegular():
			return copyFile(p, target)
		default:
			return fmt.Errorf("%s is not a regular file", rel)
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- a file of the installed package
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700) // #nosec G304 -- a path in the staging folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileHash(p string) (string, error) {
	f, err := os.Open(p) // #nosec G304 -- a file of the package or of its copy
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
