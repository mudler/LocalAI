// SPDX-License-Identifier: MIT
package wsl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWSL(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WSL launcher suite")
}

// utf16Out is text as most versions of wsl.exe write it.
func utf16Out(s string, bom bool) []byte {
	var b []byte
	if bom {
		b = append(b, 0xff, 0xfe)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

const list = "  NAME            STATE           VERSION\r\n* Ubuntu-24.04    Running         2\r\n  Legacy          Stopped         1\r\n  docker-desktop  Stopped         2\r\n"

// fakeWSL answers the calls of the launcher. Files of the distribution live
// under root, and "chmod" runs on them for real.
type fakeWSL struct {
	status, listErr error
	list            []byte
	home            string
	root            string
	mu              sync.Mutex
	calls           [][]string
}

// since returns the calls after the first n.
func (f *fakeWSL) since(n int) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls[n:]...)
}

func (f *fakeWSL) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeWSL) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	f.mu.Unlock()
	switch {
	case args[0] == "--status":
		return nil, f.status
	case args[0] == "--list":
		return f.list, f.listErr
	case len(args) > 3 && args[2] == "--exec" && args[3] == "/bin/sh":
		return []byte(f.home), nil
	case len(args) > 3 && args[2] == "--exec" && args[3] == "/bin/chmod":
		return nil, filepath.Walk(filepath.Join(f.root, args[len(args)-1]), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return os.Chmod(p, info.Mode()|0o700)
		})
	}
	return nil, errors.New("unexpected call " + strings.Join(args, " "))
}

func newFake() (*fakeWSL, *Launcher) {
	f := &fakeWSL{list: utf16Out(list, true), home: "/home/user", root: GinkgoT().TempDir()}
	exe := filepath.Join(GinkgoT().TempDir(), "wsl.exe")
	Expect(os.WriteFile(exe, nil, 0o600)).To(Succeed())
	l := New(Options{Exe: exe, Run: f.run, Env: []string{"USERPROFILE=x"}, HostPath: func(_, p string) string { return filepath.Join(f.root, filepath.FromSlash(p)) }})
	return f, l
}

func pkgOf(digest string) packages.PackageReceipt {
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "sandbox"), []byte("\x7fELF program "+digest), 0o600)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(dir, "gvisor-bin"), 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "gvisor-bin", "runsc"), []byte("runtime"), 0o600)).To(Succeed())
	m := contract.Manifest{ID: "sandbox", Executable: "sandbox", OS: "windows", Arch: "amd64", OSSettings: &contract.OSSettings{Windows: &contract.WindowsSettings{Execution: contract.ExecutionWSL2}}}
	return packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:" + digest}
}

var _ = Describe("Reading wsl.exe", func() {
	It("reads UTF-16 with or without a byte order mark, and UTF-8", func() {
		for _, b := range [][]byte{utf16Out(list, true), utf16Out(list, false), []byte(list)} {
			ds := ParseList(decode(b))
			Expect(ds).To(Equal([]Distro{{Name: "Ubuntu-24.04", Default: true, Version: 2}, {Name: "Legacy", Version: 1}, {Name: "docker-desktop", Version: 2}}))
		}
	})
	It("does not depend on the words of the header, which Windows translates", func() {
		ds := ParseList("  NOM   ÉTAT   VERSION\n* Debian   En cours d'exécution   2\n")
		Expect(ds).To(Equal([]Distro{{Name: "Debian", Default: true, Version: 2}}))
	})
	It("finds no distribution in an empty list", func() {
		Expect(ParseList("  NAME STATE VERSION\n")).To(BeEmpty())
	})
	It("maps a Linux path to the UNC path of the distribution", func() {
		Expect(UNCPath("Ubuntu", "/home/user/x")).To(Equal(`\\wsl.localhost\Ubuntu\home\user\x`))
	})
})

var _ = Describe("Checking WSL", func() {
	It("uses the default WSL 2 distribution", func() {
		_, l := newFake()
		d, err := l.Check(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Name).To(Equal("Ubuntu-24.04"))
	})
	It("says how to install WSL when wsl.exe is missing or does not work", func() {
		l := New(Options{Exe: filepath.Join(GinkgoT().TempDir(), "missing", "wsl.exe")})
		_, err := l.Check(context.Background())
		Expect(err).To(MatchError(ErrNotInstalled))
		Expect(err.Error()).To(ContainSubstring("wsl --install"))
		f, l := newFake()
		f.status = errors.New("exit status 1")
		_, err = l.Check(context.Background())
		Expect(err).To(MatchError(ErrNotInstalled))
	})
	It("says to update a WSL that cannot list its distributions", func() {
		f, l := newFake()
		f.listErr = errors.New("invalid command line option: --verbose")
		_, err := l.Check(context.Background())
		Expect(err).To(MatchError(ErrTooOld))
		Expect(err.Error()).To(ContainSubstring("wsl --update"))
	})
	It("says how to install a distribution when there is none", func() {
		f, l := newFake()
		f.list = utf16Out("  NAME STATE VERSION\r\n", true)
		_, err := l.Check(context.Background())
		Expect(err).To(MatchError(ErrNoDistro))
	})
	It("refuses a WSL 1 distribution and says how to convert it", func() {
		f, l := newFake()
		l.o.Distro = "legacy"
		_, err := l.Check(context.Background())
		Expect(err).To(MatchError(ErrDistro))
		Expect(err.Error()).To(ContainSubstring(`wsl --set-version Legacy 2`))
		_ = f
	})
	It("refuses a distribution that does not exist, and a list without a default", func() {
		f, l := newFake()
		l.o.Distro = "Fedora"
		_, err := l.Check(context.Background())
		Expect(err).To(MatchError(ContainSubstring(`no distribution "Fedora"`)))
		l.o.Distro = ""
		f.list = []byte("  NAME STATE VERSION\n  Ubuntu Running 2\n")
		_, err = l.Check(context.Background())
		Expect(err).To(MatchError(ContainSubstring("no default distribution")))
	})
})

var _ = Describe("Preparing a launch", func() {
	It("copies the package into the distribution once, makes it executable and starts it there through wsl.exe", func() {
		f, l := newFake()
		p := pkgOf("aaaa")
		launch, err := l.Prepare(context.Background(), p)
		Expect(err).NotTo(HaveOccurred())
		pkgDir := "/home/user/.local/share/localai-addons/packages/sandbox/aaaa"
		Expect(launch.Path).To(Equal(l.o.Exe))
		Expect(launch.Args).To(Equal([]string{"-d", "Ubuntu-24.04", "--cd", pkgDir, "--exec", pkgDir + "/sandbox", "--localai-addon-v1"}))
		Expect(launch.Env).To(Equal([]string{"WSLENV=LANG:TZ:" + contract.ChannelEnv, "USERPROFILE=x"}))
		Expect(launch.DataDir).To(Equal("/home/user/.local/share/localai-addons/data/sandbox"))
		Expect(launch.HostDataDir).To(Equal(filepath.Join(f.root, "home", "user", ".local", "share", "localai-addons", "data", "sandbox")))
		copied := filepath.Join(f.root, filepath.FromSlash(pkgDir))
		info, err := os.Stat(filepath.Join(copied, "sandbox"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm() & 0o100).NotTo(BeZero())
		Expect(filepath.Join(copied, "gvisor-bin", "runsc")).To(BeARegularFile())
		// A second launch of the same digest copies nothing.
		n := f.count()
		_, err = l.Prepare(context.Background(), p)
		Expect(err).NotTo(HaveOccurred())
		for _, c := range f.since(n) {
			Expect(c).NotTo(ContainElement("/bin/chmod"))
		}
		// A new version replaces the copy of the old one.
		_, err = l.Prepare(context.Background(), pkgOf("bbbb"))
		Expect(err).NotTo(HaveOccurred())
		Expect(copied).NotTo(BeADirectory())
		Expect(filepath.Join(f.root, "home", "user", ".local", "share", "localai-addons", "packages", "sandbox", "bbbb", "sandbox")).To(BeARegularFile())
	})
	It("refuses a package with a link, and leaves no half copy", func() {
		f, l := newFake()
		p := pkgOf("cccc")
		Expect(os.Symlink("/etc/passwd", filepath.Join(p.Directory, "link"))).To(Succeed())
		_, err := l.Prepare(context.Background(), p)
		Expect(err).To(MatchError(ErrDistro))
		entries, _ := os.ReadDir(filepath.Join(f.root, "home", "user", ".local", "share", "localai-addons", "packages", "sandbox"))
		Expect(entries).To(BeEmpty())
	})
	It("goes on with a copy that takes longer than the start may wait, and the next start uses it", func() {
		f, l := newFake()
		release := make(chan struct{})
		run := l.o.Run
		l.o.Run = func(ctx context.Context, exe string, args ...string) ([]byte, error) {
			if len(args) > 3 && args[3] == "/bin/chmod" {
				<-release
			}
			return run(ctx, exe, args...)
		}
		p := pkgOf("ffff")
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(50 * time.Millisecond); cancel() }()
		_, err := l.Prepare(ctx, p)
		Expect(err).To(MatchError(ContainSubstring("the next start uses it")))
		close(release)
		copied := filepath.Join(f.root, "home", "user", ".local", "share", "localai-addons", "packages", "sandbox", "ffff", complete)
		Eventually(copied, 5*time.Second).Should(BeARegularFile())
		n := f.count()
		_, err = l.Prepare(context.Background(), p)
		Expect(err).NotTo(HaveOccurred())
		for _, c := range f.since(n) {
			Expect(c).NotTo(ContainElement("/bin/chmod"))
		}
	})
	It("refuses a home folder it cannot use", func() {
		f, l := newFake()
		f.home = "relative"
		_, err := l.Prepare(context.Background(), pkgOf("dddd"))
		Expect(err).To(MatchError(ContainSubstring("home folder")))
	})
	It("starts nothing when WSL cannot run the add-on", func() {
		f, l := newFake()
		f.status = errors.New("not installed")
		_, err := l.Prepare(context.Background(), pkgOf("eeee"))
		Expect(err).To(MatchError(ErrNotInstalled))
		for _, c := range f.since(0) {
			Expect(c).NotTo(ContainElement("--exec"))
		}
	})
})
