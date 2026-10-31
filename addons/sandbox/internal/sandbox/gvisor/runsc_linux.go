//go:build linux

package gvisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// globalFlags are the runsc flags of every call, for a sandbox whose runsc
// state is in root.
//
//	--host-uds=all       the agent socket and the port sockets are host sockets
//	--overlay2=root:memory   a writable root in memory; never all:..., because writes to
//	                     the upper bind would then stay in memory too
//	--directfs=false     the gofer, not the sentry, opens host files. The gofer keeps
//	                     CAP_DAC_OVERRIDE in the user namespace, so a command with that
//	                     capability can write a 0444 file; with directfs the unprivileged
//	                     sentry gets EACCES from the host kernel on the copy of a blob
//	--ignore-cgroups     a normal user has no delegated cgroup
//	--network=none       the guest has loopback only
//
// There is no --rootless: runsc refuses to restore a checkpoint with it.
func globalFlags(root string) []string {
	return []string{
		"--directfs=false",
		"--host-uds=all",
		"--overlay2=root:memory",
		"--root=" + root,
		"--ignore-cgroups",
		"--network=none",
	}
}

var releaseRE = regexp.MustCompile(`release-(\d{8})(?:\.(\d+))?`)

// parseRelease reads "runsc version release-20261012.0".
func parseRelease(out []byte) (date, minor int, ok bool) {
	m := releaseRE.FindSubmatch(out)
	if m == nil {
		return 0, 0, false
	}
	date, _ = strconv.Atoi(string(m[1]))
	if len(m[2]) > 0 {
		minor, _ = strconv.Atoi(string(m[2]))
	}
	return date, minor, true
}

// releaseAtLeast reports whether date.minor is at least MinRelease.
func releaseAtLeast(date, minor int) bool {
	d, m, _ := parseRelease([]byte("release-" + MinRelease))
	return date > d || (date == d && minor >= m)
}

// output runs cmd and returns its combined output.
func output(cmd *exec.Cmd) ([]byte, error) {
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// runscErr is the error of a failed runsc call, with the first lines of its output.
func runscErr(what string, out []byte, err error) error {
	s := strings.TrimSpace(string(out))
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return fmt.Errorf("gvisor: runsc %s: %w: %s", what, err, s)
}

// call runs one runsc command for the sandbox with runsc state in root and
// returns its output.
func (b *Backend) call(ctx context.Context, root string, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("gvisor: runsc call without a command")
	}
	cmd := exec.CommandContext(ctx, b.runsc, append(globalFlags(root), args...)...) // #nosec G204 -- a pinned helper program that the add-on resolved; the add-on builds the arguments
	cmd.Stdin = nil
	out, err := output(cmd)
	if err != nil {
		return out, runscErr(args[0], out, err)
	}
	return out, nil
}

var idRE = regexp.MustCompile(`^[0-9a-f]{12}$`)

// validID reports whether id has the format of the sandbox ids this backend
// generates (12 lower-case hex digits). It keeps an id from being read as a
// runsc flag.
func validID(id string) error {
	if !idRE.MatchString(id) {
		return fmt.Errorf("gvisor: invalid sandbox id %q", id)
	}
	return nil
}

// pickCPUs returns n CPUs of the allowed set, as a taskset list. Successive
// calls start at different CPUs, so sandboxes spread over the host.
func (b *Backend) pickCPUs(n int) string {
	all := b.cpus
	if len(all) == 0 {
		return ""
	}
	n = min(n, len(all))
	start := int(b.next.Add(uint32(n))-uint32(n)) % len(all) // #nosec G115 -- n is at most the number of CPUs of the host
	list := make([]string, 0, n)
	for i := range n {
		list = append(list, strconv.Itoa(all[(start+i)%len(all)]))
	}
	return strings.Join(list, ",")
}

// start runs "runsc run -detach" (verb "run") or "runsc restore -detach"
// (verb "restore", with the image path) under taskset. The guest agent's
// standard streams are console.log, a file: runsc cannot restore a sandbox
// whose init has a socket as a standard stream.
func (b *Backend) start(ctx context.Context, dir, id string, cpus int, verb string, extra ...string) (pid int, err error) {
	if err := validID(id); err != nil {
		return 0, err
	}
	console, err := os.OpenFile(filepath.Join(dir, "console.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return 0, err
	}
	defer func() { _ = console.Close() }()
	pidFile := filepath.Join(dir, "pid")
	_ = os.Remove(pidFile)
	args := append([]string{"-c", b.pickCPUs(cpus), b.runsc}, globalFlags(filepath.Join(dir, "runsc"))...)
	args = append(args, verb, "-detach", "--bundle="+filepath.Join(dir, "bundle"), "--pid-file="+pidFile)
	args = append(args, extra...)
	args = append(args, id)
	cmd := exec.CommandContext(ctx, b.taskset, args...) // #nosec G204 -- a pinned helper program that the add-on resolved; the add-on builds the arguments
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = console, console
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("gvisor: runsc %s: %w\n%s", verb, err, tail(filepath.Join(dir, "console.log"), 2048))
	}
	raw, err := os.ReadFile(pidFile) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return 0, err
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("gvisor: pid file: %w", err)
	}
	return pid, nil
}

// usage returns the memory use of the sandbox in bytes ("runsc usage").
func (b *Backend) usage(ctx context.Context, root, id string) (int64, error) {
	if err := validID(id); err != nil {
		return 0, err
	}
	out, err := b.call(ctx, root, "usage", id)
	if err != nil {
		return 0, err
	}
	var u struct{ Total int64 }
	if err := json.Unmarshal(out, &u); err != nil {
		return 0, fmt.Errorf("gvisor: runsc usage: %w", err)
	}
	return u.Total, nil
}

// deleteForce stops the sandbox and removes its runsc state. It works on a
// paused sandbox and on one that is already gone.
func (b *Backend) deleteForce(ctx context.Context, root, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	_, err := b.call(ctx, root, "delete", "--force", id)
	return err
}

// pidAlive reports whether the process pid exists.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// tail returns the last n bytes of the file p, for error messages.
func tail(p string, n int) string {
	b, err := os.ReadFile(p) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return ""
	}
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(b))
}

// waitGone waits until the process pid is gone or d passes.
func waitGone(pid int, d time.Duration) {
	end := time.Now().Add(d)
	for pidAlive(pid) && time.Now().Before(end) {
		time.Sleep(20 * time.Millisecond)
	}
}
