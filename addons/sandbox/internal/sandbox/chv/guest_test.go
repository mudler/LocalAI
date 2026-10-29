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
	"time"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The real-guest checks of the kernel and init hardening (Tasks 4 and 5).
// They are backend checks, so they live here and not in sbtest. They run
// with the other integration tests and skip without SANDBOX_TEST_IMAGE.

// agentLayer is a layer that replaces the guest agent with a script. The
// script runs as PID 1, which is root, so it can power the VM off the way
// the agent does after a failed boot step.
func agentLayer(script string) func() (io.ReadCloser, error) {
	GinkgoHelper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	Expect(tw.WriteHeader(&tar.Header{Name: "usr/sbin/sandbox-agent", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(script))})).To(Succeed())
	_, err := io.WriteString(tw, script)
	Expect(err).To(Succeed())
	Expect(tw.Close()).To(Succeed())
	b := buf.Bytes()
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
}

var _ = Describe("guest hardening and boot failures", func() {
	// It runs the checks as the agent user inside a real guest.
	It("hardens the guest", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		out := sh(sb, `id -u
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
		Expect(strings.Join(lines, "\n")).To(Equal(strings.Join(want, "\n")), "guest hardening:\n%s", out)
	})

	// It replaces the agent with one that logs and powers off, as the real
	// agent does after a failed boot step. Create must return at once with
	// the console tail, not wait for ReadyTimeout.
	//
	// Forcing a failure inside the real agent would need a broken init step
	// in the image. The stand-in reaches the same host path: the VM powers
	// off before it sends the ready frame. An init that exits with a status,
	// which makes the kernel panic, is not covered here: it is checked by
	// hand (see the Task 8 report).
	It("ends the VM on a boot failure", func() {
		b := testBackend()
		b.opts.ReadyTimeout = 60 * time.Second
		script := "#!/bin/sh\n" +
			"echo simulated-agent-boot-failure\n" +
			"sync\n" +
			"exec python3 -c 'import ctypes, os; os.sync(); ctypes.CDLL(None).reboot(0x4321fedc)'\n"
		ctx := context.Background()
		st := sbtest.OpenStore()
		_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
		Expect(err).To(Succeed())
		v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		_, load, err := resolve(ctx, b.opts.DefaultImage)
		Expect(err).To(Succeed())
		img, err := load()
		Expect(err).To(Succeed())
		layer, err := tarball.LayerFromOpener(agentLayer(script))
		Expect(err).To(Succeed())
		img, err = mutate.AppendLayers(img, layer)
		Expect(err).To(Succeed())
		start := time.Now()
		_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v,
			Image: writeLayout(img), Memory: 1 << 30, Disk: 1 << 30})
		elapsed := time.Since(start)
		Expect(err).To(HaveOccurred(), "a guest that powers off during boot was created")
		Expect(elapsed).To(BeNumerically("<=", 30*time.Second), "create waited; ReadyTimeout is %v", b.opts.ReadyTimeout)
		Expect(err).To(MatchError(ContainSubstring("simulated-agent-boot-failure")), "the error has no console tail")
		ents, _ := os.ReadDir(b.opts.StateDir)
		Expect(ents).To(BeEmpty(), "a failed create left session folders")
	})

	// It replaces the agent with a script that exits. The kernel panics
	// ("Attempted to kill init"), and the VM does not exit on its own.
	// Create must see the panic in the console log and fail at once.
	It("fails fast when init exits", func() {
		b := testBackend()
		// The bound is generous on purpose: inside a container every boot is
		// much slower (see Dockerfile). The check is that the console panic
		// is seen well before ReadyTimeout, not that it is seen within a few
		// seconds.
		b.opts.ReadyTimeout = 60 * time.Second
		ctx := context.Background()
		st := sbtest.OpenStore()
		_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
		Expect(err).To(Succeed())
		v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		_, load, err := resolve(ctx, b.opts.DefaultImage)
		Expect(err).To(Succeed())
		img, err := load()
		Expect(err).To(Succeed())
		layer, err := tarball.LayerFromOpener(agentLayer("#!/bin/sh\necho init-exit-marker\nexit 1\n"))
		Expect(err).To(Succeed())
		img, err = mutate.AppendLayers(img, layer)
		Expect(err).To(Succeed())
		start := time.Now()
		_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v,
			Image: writeLayout(img), Memory: 1 << 30, Disk: 1 << 30})
		elapsed := time.Since(start)
		Expect(err).To(HaveOccurred(), "a guest whose init exits was created")
		Expect(elapsed).To(BeNumerically("<=", b.opts.ReadyTimeout/2), "create waited for a kernel panic; ReadyTimeout is %v", b.opts.ReadyTimeout)
		Expect(err).To(MatchError(ContainSubstring("kernel panic during boot")), "the error does not come from the console panic check")
		Expect(err).To(MatchError(ContainSubstring("Kernel panic - not syncing")), "the error has no panic line")
		ents, _ := os.ReadDir(b.opts.StateDir)
		Expect(ents).To(BeEmpty(), "a failed create left session folders")
	})

	// It checks, as the agent user in a real guest, that the console, kernel
	// log and terminal nodes are gone, so a write fails, and that a flood to
	// them does not reach console.log. The checks open the paths without
	// O_CREAT: a shell redirection would create a plain file in /dev,
	// because the agent user has CAP_DAC_OVERRIDE.
	It("cannot write the console", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		out := sh(sb, `python3 - <<'EOF'
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
		Expect(strings.TrimSpace(out)).To(Equal("checked"), "guest console access:\n%s", out)
		s := sb.(*sbx)
		before, err := os.Stat(filepath.Join(s.dir, "console.log"))
		Expect(err).To(Succeed())
		sh(sb, `python3 - <<'EOF'
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
		Expect(err).To(Succeed())
		Expect(after.Size()).To(BeNumerically("<=", before.Size()+4096), "console.log grew from %d to %d bytes", before.Size(), after.Size())
	})

	// It sets user.overlay.opaque on a folder from the guest. The guest
	// overlay ignores it, so the guest still sees the files, and a
	// checkpoint must not delete them from the workspace.
	It("does not delete anything on a user opaque xattr", func() {
		b := testBackend()
		ctx := context.Background()
		sb, st := fixture(b, "")
		_, err := st.Put(ctx, sbtest.Admin, "/proj/sub/a.txt", strings.NewReader("a\n"), workspace.PutOptions{})
		Expect(err).To(Succeed())
		Expect(sb.Refresh(ctx)).To(Succeed())
		out := sh(sb, `cd /workspace/proj && python3 -c 'import os; os.setxattr("sub", "user.overlay.opaque", b"y")' && echo set; ls sub`)
		Expect(out).To(ContainSubstring("a.txt"), "the guest does not see a.txt")
		_, _, err = sb.Checkpoint(ctx, "x")
		Expect(err).To(Succeed())
		_, err = st.Stat(ctx, sbtest.Admin, "/proj/sub/a.txt")
		Expect(err).To(Succeed(), "a.txt is gone from the workspace")
		Expect(sh(sb, "ls /workspace/proj/sub")).To(ContainSubstring("a.txt"), "the guest lost a.txt")
	})
})
