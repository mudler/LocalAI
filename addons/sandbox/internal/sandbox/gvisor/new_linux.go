//go:build linux

package gvisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// geteuid is os.Geteuid. It is a variable so that a test can check the
// refusal of root.
var geteuid = os.Geteuid

// probeID is the sandbox id of the user-namespace probe.
const probeID = "00000000c4ec"

// socketTail is the longest part of a socket path below StateDir:
// <id>/ctl/port-65535.sock.
const socketTail = "/0123456789ab/ctl/port-65535.sock"

// New checks the host and returns a Backend. It returns an error that wraps
// sandbox.ErrUnsupported, with a one-line reason, for the first check that
// fails:
//
//  1. the process does not run as root;
//  2. runsc exists, is at least MinRelease, and its gvisor-bin folder is next
//     to it (and taskset exists);
//  3. runsc can start a minimal sandbox in a user namespace;
//  4. StateDir is short enough for the longest socket path (107 bytes).
//
// StateDir and CacheDir are created only after every check has passed. New
// then takes an exclusive lock on StateDir (the file ".lock" in it), which it
// holds until Close. A second New on the same StateDir, in this process or in
// another, fails with a plain error (not ErrUnsupported).
//
// Sandboxes outlive the process that started them. New therefore stops the
// ones that an earlier process left in StateDir: every session folder whose
// state.json is missing, unreadable or not suspended is deleted with "runsc
// delete --force", then removed. Suspended folders are removed too, unless
// Options.KeepSuspended is set: the caller that held their references is gone.
func New(o Options) (*Backend, error) {
	if geteuid() == 0 {
		return nil, fmt.Errorf("%w: the backend must not run as root on the host: run it as an unprivileged user", sandbox.ErrUnsupported)
	}
	if o.StateDir == "" || o.CacheDir == "" {
		return nil, errors.New("gvisor: StateDir and CacheDir are required")
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 10 * time.Second
	}
	if o.StageLimit < 0 {
		return nil, errors.New("gvisor: StageLimit is negative")
	}
	// Every check that can return ErrUnsupported runs before New creates a
	// folder, so a refused host is left untouched.
	for _, d := range []*string{&o.StateDir, &o.CacheDir} {
		abs, err := filepath.Abs(*d)
		if err != nil {
			return nil, err
		}
		*d = abs
	}
	b := &Backend{opts: o, memEvery: time.Second, diskEvery: 10 * time.Second, aliveEvery: 500 * time.Millisecond}
	name := o.Runsc
	if name == "" {
		name = "runsc"
	}
	p, err := sandbox.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%w: runsc not found: %v", sandbox.ErrUnsupported, err)
	}
	if b.runsc, err = filepath.Abs(p); err != nil {
		return nil, err
	}
	// AppArmor attaches a profile by the real path of the executed file.
	if b.runsc, err = filepath.EvalSymlinks(b.runsc); err != nil {
		return nil, fmt.Errorf("%w: runsc path: %v", sandbox.ErrUnsupported, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := output(exec.CommandContext(ctx, b.runsc, "--version"))
	if err != nil {
		return nil, fmt.Errorf("%w: %s --version: %v", sandbox.ErrUnsupported, b.runsc, err)
	}
	date, minor, ok := parseRelease(out)
	if !ok {
		return nil, fmt.Errorf("%w: %s: no release in %q", sandbox.ErrUnsupported, b.runsc, firstLine(out))
	}
	if !releaseAtLeast(date, minor) {
		return nil, fmt.Errorf("%w: runsc release %d.%d is older than %s", sandbox.ErrUnsupported, date, minor, MinRelease)
	}
	if err := ValidateProfilePath(b.runsc); err != nil {
		return nil, fmt.Errorf("%w: %v", sandbox.ErrUnsupported, err)
	}
	bin := filepath.Join(filepath.Dir(b.runsc), "gvisor-bin")
	if st, err := os.Stat(bin); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%w: the folder %s is missing: runsc needs its helper binaries next to it", sandbox.ErrUnsupported, bin)
	}
	if b.taskset, err = sandbox.LookPath("taskset"); err != nil {
		return nil, fmt.Errorf("%w: taskset not found: %v", sandbox.ErrUnsupported, err)
	}
	b.cpus = allowedCPUs()
	if len(b.cpus) == 0 {
		return nil, fmt.Errorf("%w: cannot read the allowed CPU list from /proc/self/status: taskset needs it", sandbox.ErrUnsupported)
	}
	if n := len(o.StateDir) + len(socketTail); n > 107 {
		return nil, fmt.Errorf("%w: StateDir %q is too long for socket paths (%d of 107 bytes)", sandbox.ErrUnsupported, o.StateDir, n)
	}
	if err := b.checkUserNamespace(ctx); err != nil {
		return nil, err
	}
	for _, d := range []string{o.StateDir, o.CacheDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	if b.lock, err = lockStateDir(o.StateDir); err != nil {
		return nil, err
	}
	if err := b.sweep(); err != nil {
		_ = b.Close()
		return nil, err
	}
	return b, nil
}

// lockName is the lock file in StateDir.
const lockName = ".lock"

// lockStateDir takes the exclusive lock on StateDir without waiting. The lock
// belongs to the open file, so it ends when the file is closed or the process
// dies.
func lockStateDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("gvisor: StateDir %q is in use by another backend: use one StateDir per backend, or close the other one first", dir)
		}
		return nil, err
	}
	return f, nil
}

// sweepBudget bounds the stop of one sandbox that an earlier process left.
const sweepBudget = 30 * time.Second

// sweep stops the sandboxes that an earlier process left in StateDir and
// removes their folders. A folder is kept when its state.json says it is
// suspended and Options.KeepSuspended is set. A folder whose sandbox cannot be
// stopped is kept too, with a log line, so that the runsc state stays
// reachable.
func (b *Backend) sweep() error {
	ents, err := os.ReadDir(b.opts.StateDir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() || validID(e.Name()) != nil {
			continue
		}
		dir := filepath.Join(b.opts.StateDir, e.Name())
		if st, err := loadState(dir); err == nil && st.Suspended && b.opts.KeepSuspended {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), sweepBudget)
		err := b.deleteForce(ctx, filepath.Join(dir, "runsc"), e.Name())
		cancel()
		if err != nil {
			log.Printf("gvisor: cannot stop the sandbox of the old folder %s: %v", e.Name(), err)
			continue
		}
		if err := removeAll(dir); err != nil {
			log.Printf("gvisor: cannot remove the old folder %s: %v", e.Name(), err)
		}
	}
	return nil
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

// checkUserNamespace starts a minimal sandbox that runs "true" on the root
// of the host, in a user namespace with one id. A host that denies user
// namespaces to runsc (AppArmor) fails here. The probe leaves nothing behind.
func (b *Backend) checkUserNamespace(ctx context.Context) error {
	truePath := ""
	for _, p := range []string{"/bin/true", "/usr/bin/true"} {
		if _, err := os.Stat(p); err == nil {
			truePath = p
			break
		}
	}
	if truePath == "" {
		return fmt.Errorf("%w: no /bin/true to check user namespaces with", sandbox.ErrUnsupported)
	}
	tmp, err := probeDir(b.opts.StateDir)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.MkdirAll(filepath.Join(tmp, "bundle"), 0o700); err != nil {
		return err
	}
	cfg, err := checkConfig(truePath, os.Getuid(), os.Getgid())
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "bundle", "config.json"), cfg, 0o600); err != nil {
		return err
	}
	root := filepath.Join(tmp, "runsc")
	out, err := b.call(ctx, root, "run", "--bundle="+filepath.Join(tmp, "bundle"), probeID)
	if err == nil {
		return nil
	}
	// A half-started sandbox must not outlive the probe.
	cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ccancel()
	_ = b.deleteForce(cctx, root, probeID)
	if strings.Contains(strings.ToLower(string(out)), "permission denied") {
		return fmt.Errorf("%w: runsc cannot make a user namespace (permission denied): an administrator must load the AppArmor profile that gvisor.Profile(%q) returns", sandbox.ErrUnsupported, b.runsc)
	}
	return fmt.Errorf("%w: runsc cannot start a sandbox (check folder %s): %v", sandbox.ErrUnsupported, tmp, err)
}

// probeDir makes the scratch folder of the user-namespace probe. It lies
// beside StateDir when the parent exists: New has checked that StateDir is
// short enough for socket paths, so the probe is too. Otherwise (and when the
// parent is not writable) it lies in the temporary folder, whose path can be
// long.
func probeDir(stateDir string) (string, error) {
	if parent := filepath.Dir(stateDir); parent != stateDir {
		if st, err := os.Stat(parent); err == nil && st.IsDir() {
			if d, err := os.MkdirTemp(parent, ".gvcheck-"); err == nil {
				return d, nil
			}
		}
	}
	return os.MkdirTemp("", "gvcheck-")
}

// allowedCPUs reads the CPU list of this process from /proc/self/status. It
// is a variable so that a test can make the list unreadable.
var allowedCPUs = readAllowedCPUs

func readAllowedCPUs() []int {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "Cpus_allowed_list:"); ok {
			return parseCPUList(strings.TrimSpace(v))
		}
	}
	return nil
}

// parseCPUList reads "0-3,8,10-11".
func parseCPUList(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		z := a
		if isRange {
			if z, err = strconv.Atoi(hi); err != nil || z < a {
				continue
			}
		}
		for c := a; c <= z; c++ {
			out = append(out, c)
		}
	}
	return out
}
