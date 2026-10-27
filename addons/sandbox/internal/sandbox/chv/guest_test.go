//go:build linux

package chv

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The real-guest checks of the kernel and init hardening (Tasks 4 and 5).
// They are backend checks, so they live here and not in sbtest. They run
// with the other integration tests and skip without SANDBOX_TEST_IMAGE.

// TestGuestHardening runs the checks as the agent user inside a real guest.
func TestGuestHardening(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	out := sh(t, sb, `id -u
if echo x > /proc/sys/kernel/core_pattern 2>/dev/null; then echo core-writable; else echo core-denied; fi
for f in $(find /sys -type f 2>/dev/null | head -n 300); do
  if (echo 1 > "$f") 2>/dev/null; then echo "sys-writable $f"; fi
done
echo sys-checked
grep '^NoNewPrivs:' /proc/self/status
grep -E '^Cap(Eff|Amb):' /proc/self/status`)
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		lines = append(lines, strings.Join(strings.Fields(l), " "))
	}
	want := []string{
		"1000",
		"core-denied",
		"sys-checked",
		"NoNewPrivs: 1",
		"CapEff: 000000000000000a",
		"CapAmb: 000000000000000a",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("guest hardening:\n%s\nwant:\n%s", out, strings.Join(want, "\n"))
	}
}

// agentLayer is a layer that replaces the guest agent with a script. The
// script runs as PID 1, which is root, so it can power the VM off the way
// the agent does after a failed boot step.
func agentLayer(t *testing.T, script string) func() (io.ReadCloser, error) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "usr/sbin/sandbox-agent", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, script); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
}

// TestBootFailureEndsTheVM replaces the agent with one that logs and powers
// off, as the real agent does after a failed boot step. Create must return
// at once with the console tail, not wait for ReadyTimeout.
//
// Forcing a failure inside the real agent would need a broken init step in
// the image. The stand-in reaches the same host path: the VM powers off
// before it sends the ready frame. An init that exits with a status, which
// makes the kernel panic, is not covered here: it is checked by hand (see
// the Task 8 report).
func TestBootFailureEndsTheVM(t *testing.T) {
	b := testBackend(t)
	b.opts.ReadyTimeout = 60 * time.Second
	script := "#!/bin/sh\n" +
		"echo simulated-agent-boot-failure\n" +
		"sync\n" +
		"exec python3 -c 'import ctypes, os; os.sync(); ctypes.CDLL(None).reboot(0x4321fedc)'\n"
	ctx := context.Background()
	st := sbtest.OpenStore(t)
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	_, load, err := resolve(ctx, b.opts.DefaultImage)
	if err != nil {
		t.Fatal(err)
	}
	img, err := load()
	if err != nil {
		t.Fatal(err)
	}
	layer, err := tarball.LayerFromOpener(agentLayer(t, script))
	if err != nil {
		t.Fatal(err)
	}
	if img, err = mutate.AppendLayers(img, layer); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v,
		Image: writeLayout(t, img), Memory: 1 << 30, Disk: 1 << 30})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a guest that powers off during boot was created")
	}
	if elapsed > 30*time.Second {
		t.Fatalf("create waited %v; ReadyTimeout is %v", elapsed, b.opts.ReadyTimeout)
	}
	if !strings.Contains(err.Error(), "simulated-agent-boot-failure") {
		t.Fatalf("the error has no console tail: %v", err)
	}
	if ents, _ := os.ReadDir(b.opts.StateDir); len(ents) != 0 {
		t.Fatalf("a failed create left %d session folders", len(ents))
	}
}

// TestInitExitFailsFast replaces the agent with a script that exits. The
// kernel panics ("Attempted to kill init"), and the VM does not exit on its
// own. Create must see the panic in the console log and fail at once.
func TestInitExitFailsFast(t *testing.T) {
	b := testBackend(t)
	// The bound is generous on purpose: inside a container every boot is much
	// slower (see Dockerfile). The check is that the console panic is seen
	// well before ReadyTimeout, not that it is seen within a few seconds.
	b.opts.ReadyTimeout = 60 * time.Second
	ctx := context.Background()
	st := sbtest.OpenStore(t)
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	_, load, err := resolve(ctx, b.opts.DefaultImage)
	if err != nil {
		t.Fatal(err)
	}
	img, err := load()
	if err != nil {
		t.Fatal(err)
	}
	layer, err := tarball.LayerFromOpener(agentLayer(t, "#!/bin/sh\necho init-exit-marker\nexit 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if img, err = mutate.AppendLayers(img, layer); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v,
		Image: writeLayout(t, img), Memory: 1 << 30, Disk: 1 << 30})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a guest whose init exits was created")
	}
	if elapsed > b.opts.ReadyTimeout/2 {
		t.Fatalf("create waited %v for a kernel panic; ReadyTimeout is %v", elapsed, b.opts.ReadyTimeout)
	}
	if !strings.Contains(err.Error(), "kernel panic during boot") {
		t.Fatalf("the error does not come from the console panic check: %v", err)
	}
	if !strings.Contains(err.Error(), "Kernel panic - not syncing") {
		t.Fatalf("the error has no panic line: %v", err)
	}
	if ents, _ := os.ReadDir(b.opts.StateDir); len(ents) != 0 {
		t.Fatalf("a failed create left %d session folders", len(ents))
	}
}

// TestGuestCannotWriteTheConsole checks, as the agent user in a real guest,
// that the console, kernel log and terminal nodes are gone, so a write fails,
// and that a flood to them does not reach console.log. The checks open the
// paths without O_CREAT: a shell redirection would create a plain file in
// /dev, because the agent user has CAP_DAC_OVERRIDE.
func TestGuestCannotWriteTheConsole(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	out := sh(t, sb, `python3 - <<'EOF'
import os
for f in ["/dev/hvc0", "/dev/hvc1", "/dev/console", "/dev/kmsg", "/dev/tty0", "/dev/tty1", "/dev/ttyS0", "/dev/tty"]:
    try:
        fd = os.open(f, os.O_WRONLY | os.O_NOCTTY)
        os.close(fd)
        print("writable", f)
    except OSError as e:
        pass
print("checked")
EOF
ls /dev | grep -E '^(hvc|console|kmsg|tty.)' || true`)
	if strings.TrimSpace(out) != "checked" {
		t.Fatalf("guest console access:\n%s", out)
	}
	s := sb.(*sbx)
	before, err := os.Stat(filepath.Join(s.dir, "console.log"))
	if err != nil {
		t.Fatal(err)
	}
	sh(t, sb, `python3 - <<'EOF'
import os, time
end = time.time() + 2
buf = b"A" * (1 << 20)
while time.time() < end:
    for f in ("/dev/hvc0", "/dev/console", "/dev/kmsg"):
        try:
            fd = os.open(f, os.O_WRONLY | os.O_NOCTTY)
            os.write(fd, buf)
        except OSError:
            pass
EOF`)
	time.Sleep(300 * time.Millisecond)
	after, err := os.Stat(filepath.Join(s.dir, "console.log"))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() > before.Size()+4096 {
		t.Fatalf("console.log grew from %d to %d bytes", before.Size(), after.Size())
	}
}

// TestGuestUserOpaqueXattrDeletesNothing sets user.overlay.opaque on a folder
// from the guest. The guest overlay ignores it, so the guest still sees the
// files, and a checkpoint must not delete them from the workspace.
func TestGuestUserOpaqueXattrDeletesNothing(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	sb, st := fixture(t, b, "")
	if _, err := st.Put(ctx, sbtest.Admin, "/proj/sub/a.txt", strings.NewReader("a\n"), workspace.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := sb.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	out := sh(t, sb, `cd /workspace/proj && python3 -c 'import os; os.setxattr("sub", "user.overlay.opaque", b"y")' && echo set; ls sub`)
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("the guest does not see a.txt: %q", out)
	}
	if _, _, err := sb.Checkpoint(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Stat(ctx, sbtest.Admin, "/proj/sub/a.txt"); err != nil {
		t.Fatalf("a.txt is gone from the workspace: %v", err)
	}
	if out := sh(t, sb, "ls /workspace/proj/sub"); !strings.Contains(out, "a.txt") {
		t.Fatalf("the guest lost a.txt: %q", out)
	}
}
