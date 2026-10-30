//go:build linux

package chv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// vm is one Cloud Hypervisor process and its virtiofsd.
type vm struct {
	dir      string
	api      *api
	ch       *exec.Cmd
	done     chan struct{} // closed when cloud-hypervisor exited
	vfsd     *exec.Cmd
	vfsdDone chan struct{}
}

func (v *vm) exited() bool {
	select {
	case <-v.done:
		return true
	default:
		return false
	}
}

func (v *vm) vsock() string { return filepath.Join(v.dir, "vsock.sock") }

// stop ends cloud-hypervisor (API shutdown, then SIGKILL) and virtiofsd.
func (v *vm) stop() {
	if v.ch != nil && !v.exited() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = v.api.put(c, "vmm.shutdown", nil)
		cancel()
		select {
		case <-v.done:
		case <-time.After(3 * time.Second):
			_ = v.ch.Process.Kill()
			<-v.done
		}
	}
	if v.vfsd != nil {
		_ = v.vfsd.Process.Signal(syscall.SIGTERM)
		select {
		case <-v.vfsdDone:
		case <-time.After(2 * time.Second):
			_ = v.vfsd.Process.Kill()
			<-v.vfsdDone
		}
	}
}

// start runs a host process with its output in logPath. The process gets
// SIGKILL when this process dies.
func start(argv []string, logPath string) (*exec.Cmd, chan struct{}, error) {
	logf, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
	// Pdeathsig fires when the OS thread that forked the child exits, not
	// when the process does. Pin the thread while Start forks. This is a
	// best effort: the Go runtime does not promise to keep that thread alive
	// for the child's lifetime. The child also stops when its VM is stopped
	// (vm.stop), which is the normal path.
	runtime.LockOSThread()
	err = cmd.Start()
	runtime.UnlockOSThread()
	if err != nil {
		_ = logf.Close()
		return nil, nil, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		_ = logf.Close()
		close(done)
	}()
	return cmd, done, nil
}

// sysBinds gives a bubblewrap sandbox the host's programs and libraries,
// read-only.
func sysBinds(extra ...string) []string {
	args := []string{"--ro-bind", "/usr", "/usr", "--ro-bind-try", "/etc/ld.so.cache", "/etc/ld.so.cache"}
	for _, d := range []string{"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32"} {
		st, err := os.Lstat(d)
		switch {
		case err != nil:
		case st.Mode()&os.ModeSymlink != 0:
			if t, err := os.Readlink(d); err == nil {
				args = append(args, "--symlink", t, d)
			}
		case st.IsDir():
			args = append(args, "--ro-bind", d, d)
		}
	}
	for _, p := range extra {
		if !strings.HasPrefix(p, "/usr/") {
			args = append(args, "--ro-bind", p, p)
		}
	}
	return args
}

// bwrap returns the bubblewrap prefix for an isolated helper: no network, no
// capabilities, no view of the host except binds.
func (b *Backend) bwrap(bin string, binds ...string) []string {
	args := []string{b.bins.Bwrap, "--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL",
		"--dev", "/dev", "--proc", "/proc"}
	args = append(args, sysBinds(bin)...)
	return append(args, binds...)
}

// startVirtiofsd shares dir/lower read-only on the socket dir/vfs/virtiofs.sock.
func (b *Backend) startVirtiofsd(dir string) (*exec.Cmd, chan struct{}, error) {
	sockDir := filepath.Join(dir, "vfs")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		return nil, nil, err
	}
	sock := filepath.Join(sockDir, "virtiofs.sock")
	_ = os.Remove(sock)
	lower, sockArg := filepath.Join(dir, "lower"), sock
	var argv []string
	if !b.opts.NoBwrap {
		argv = b.bwrap(b.bins.Virtiofsd, "--ro-bind", lower, "/share", "--bind", sockDir, "/sock")
		lower, sockArg = "/share", "/sock/virtiofs.sock"
	}
	argv = append(argv, b.bins.Virtiofsd, "--socket-path="+sockArg, "--shared-dir="+lower, "--sandbox", "none", "--readonly", "--xattr")
	cmd, done, err := start(argv, filepath.Join(dir, "virtiofsd.log"))
	if err != nil {
		return nil, nil, err
	}
	for range 200 {
		if _, err := os.Stat(sock); err == nil {
			return cmd, done, nil
		}
		select {
		case <-done:
			return nil, nil, fmt.Errorf("chv: virtiofsd exited: %s", tail(filepath.Join(dir, "virtiofsd.log"), 1024))
		case <-time.After(10 * time.Millisecond):
		}
	}
	_ = cmd.Process.Kill()
	<-done
	return nil, nil, errors.New("chv: virtiofsd did not create its socket")
}

// debugfsArgv is the command line that reads dir/upper.ext4 and dumps files
// into dir/stage. It returns the stage folder as debugfs sees it.
func (b *Backend) debugfsArgv(dir string) ([]string, string) {
	img, stage := filepath.Join(dir, "upper.ext4"), filepath.Join(dir, "stage")
	if b.opts.NoBwrap {
		return []string{b.bins.Debugfs, "-c", "-f", "-", img}, stage
	}
	argv := b.bwrap(b.bins.Debugfs, "--ro-bind", img, "/upper.ext4", "--bind", stage, "/stage")
	return append(argv, b.bins.Debugfs, "-c", "-f", "-", "/upper.ext4"), "/stage"
}

// mkUpper makes a sparse ext4 image without a journal, so debugfs reads it
// without a journal replay.
func (b *Backend) mkUpper(ctx context.Context, path string, size int64) error {
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, b.bins.Mke2fs, "-q", "-F", "-t", "ext4", "-O", "^has_journal",
		"-E", "root_owner=1000:1000,lazy_itable_init=1", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("chv: mke2fs: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// chArgs is the command line of a fresh boot.
func chArgs(dir string, img guestImage, st state) []string {
	cmdline := "console=hvc0 root=/dev/vda ro init=/sbin/sandbox-agent"
	if len(st.Ports) > 0 {
		ps := make([]string, len(st.Ports))
		for i, p := range st.Ports {
			ps[i] = strconv.Itoa(p)
		}
		cmdline += " sandbox.ports=" + strings.Join(ps, ",")
	}
	return []string{
		"--api-socket", "path=" + filepath.Join(dir, "chv.sock"),
		"--kernel", img.Kernel,
		"--cmdline", cmdline,
		"--disk", "path=" + img.Root + ",readonly=on,image_type=raw", "path=" + filepath.Join(dir, "upper.ext4") + ",image_type=raw",
		"--fs", "tag=lower,socket=" + filepath.Join(dir, "vfs", "virtiofs.sock"),
		"--vsock", "cid=3,socket=" + filepath.Join(dir, "vsock.sock"),
		"--cpus", "boot=" + strconv.Itoa(st.CPUs),
		"--memory", "size=" + strconv.FormatInt(st.Memory>>20, 10) + "M,shared=on",
		"--console", "file=" + filepath.Join(dir, "console.log"),
		"--serial", "off",
	}
}

// tail returns the last n bytes of a file.
func tail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	return strings.TrimSpace(string(b))
}
