//go:build darwin && arm64

package vz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The integration tests need a Mac with Apple silicon, a signed test binary
// (the com.apple.security.virtualization entitlement) and a guest image for
// linux/arm64:
//
//	SANDBOX_TEST_IMAGE   the guest image, name@sha256:... or oci-layout:<dir>@sha256:...
//	SANDBOX_TEST_PROFILE the profile of SANDBOX_TEST_IMAGE when it is not base (media runs the media tool specs)
//	SANDBOX_TEST_CACHE   converted images; default <tmp>/sandbox-vz-test-cache
//	SANDBOX_TEST_KERNEL  a kernel file that replaces the kernel of the image
//	SANDBOX_TEST_ROOT    a root.ext4 file that replaces the root image (with SANDBOX_TEST_KERNEL,
//	                     SANDBOX_TEST_IMAGE is not needed): for a bring-up with a small root file system
//
// They skip, and say why, when SANDBOX_TEST_IMAGE is not set. When it is set,
// a host that cannot run the backend fails them.

var cacheOnce sync.Once

func testCache() string {
	dir := os.Getenv("SANDBOX_TEST_CACHE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "sandbox-vz-test-cache")
	}
	cacheOnce.Do(func() { _ = os.MkdirAll(dir, 0o700) })
	return dir
}

// testProfile is the profile that SANDBOX_TEST_IMAGE holds, "" for base.
func testProfile() string { return os.Getenv("SANDBOX_TEST_PROFILE") }

func testRealBackend() *Backend {
	GinkgoHelper()
	img := os.Getenv("SANDBOX_TEST_IMAGE")
	root := os.Getenv("SANDBOX_TEST_ROOT")
	if img == "" && root == "" {
		Skip("set SANDBOX_TEST_IMAGE to the guest image (name@sha256:... or oci-layout:<dir>@sha256:...)")
	}
	b, err := New(Options{StateDir: shortDir(), CacheDir: testCache(), DefaultImage: img})
	Expect(err).To(Succeed(), "this host cannot run the backend")
	DeferCleanup(func() { _ = b.Close() })
	if root != "" {
		b.loadImage = func(context.Context, string) (guestImage, error) {
			return guestImage{Ref: "root-only", Root: root, Kernel: os.Getenv("SANDBOX_TEST_KERNEL"), Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HTTPS_PROXY=http://127.0.0.1:3128", "HTTP_PROXY=http://127.0.0.1:3128"}}, nil
		}
	} else if k := os.Getenv("SANDBOX_TEST_KERNEL"); k != "" {
		b.loadImage = func(ctx context.Context, ref string) (guestImage, error) {
			gi, err := b.imageFromCache(ctx, ref)
			gi.Kernel = k
			return gi, err
		}
	}
	return b
}

// fixture creates a sandbox on a store with /proj.
func realFixture(b *Backend, image string) (*sbx, workspace.Store) {
	GinkgoHelper()
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).To(Succeed())
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).To(Succeed())
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Image: image, Memory: 1 << 30, Disk: 1 << 30})
	Expect(err).To(Succeed())
	DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb.(*sbx), st
}

func sh(sb sandbox.Sandbox, script string) string {
	GinkgoHelper()
	var out, errb bytes.Buffer
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out, Stderr: &errb})
	Expect(err).To(Succeed())
	code, err := p.Wait()
	Expect(err).To(Succeed(), "%q: %d\n%s%s", script, code, out.String(), errb.String())
	Expect(code).To(BeZero(), "%q: %d\n%s%s", script, code, out.String(), errb.String())
	return out.String()
}

// shRun is sh that returns the exit code and the output, and fails only when
// the command cannot run.
func shRun(sb sandbox.Sandbox, script string) (int, string) {
	GinkgoHelper()
	var out bytes.Buffer
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out, Stderr: &out})
	Expect(err).To(Succeed())
	code, err := p.Wait()
	Expect(err).To(Succeed())
	return code, out.String()
}

func newLayerCreate(b *Backend, img string) error {
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).To(Succeed())
	v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Image: img, Memory: 1 << 30, Disk: 1 << 30})
	return err
}

var _ = Describe("Virtualization.framework backend", func() {
	Describe("the conformance suite", func() {
		sbtest.RunBackendSuite(func() sandbox.Backend { return testRealBackend() })
	})

	It("has the Python packages and PDF tools in the guest image", func() {
		sb, _ := realFixture(testRealBackend(), "")
		sh(sb, `python3 -c "import docx, openpyxl, pptx, pypdf, pandas, matplotlib, PIL, reportlab, pdfplumber, pdf2image"`)
		sh(sb, `pdftoppm -v`)
	})

	// The runtime checks of the document skills and of the tools of the base
	// profile, in the guest, as the guest image runs them at build time.
	It("passes the runtime checks of the document skills in the guest", func() {
		sb, _ := realFixture(testRealBackend(), "")
		sh(sb, `python3 /usr/local/lib/localai/document-skills/check-document-skills.py`)
		sh(sb, `sh /usr/local/lib/localai/check-tools.sh base /usr/local/lib/localai/manifest.json`)
	})

	It("boots a converted image with every file kind", func() {
		b := testRealBackend()
		_, load, err := ociimage.Resolve(context.Background(), b.opts.DefaultImage)
		Expect(err).To(Succeed())
		img, err := load()
		Expect(err).To(Succeed())
		img, err = mutate.AppendLayers(img, attrLayer())
		Expect(err).To(Succeed())
		sb, _ := realFixture(b, writeLayout(img))
		got := sh(sb, `cd /opt/attrs
stat -c '%n %a %u:%g %F' suid fifo
readlink link
[ "$(stat -c %i suid)" = "$(stat -c %i hard)" ] && echo hard-link
stat -c '%n %t:%T %F' null
python3 -c "import os; print(os.getxattr('xattr', 'user.demo').decode())"`)
		want := "suid 4755 0:0 regular file\nfifo 600 1000:1000 fifo\nsuid\nhard-link\nnull 1:3 character special file\nhello\n"
		Expect(got).To(Equal(want), "guest sees")
	})

	It("lets two sandboxes run side by side", func() {
		b := testRealBackend()
		sb1, _ := realFixture(b, "")
		sb2, _ := realFixture(b, "")
		Expect(strings.TrimSpace(sh(sb1, "echo one"))).To(Equal("one"))
		Expect(strings.TrimSpace(sh(sb2, "echo two"))).To(Equal("two"))
		sh(sb1, "echo a > proj/a.txt")
		Expect(sh(sb2, "ls proj")).To(BeEmpty(), "a file of one sandbox shows in the other")
	})

	It("cleans up after a ready timeout", func() {
		b := testRealBackend()
		b.opts.ReadyTimeout = time.Millisecond
		Expect(newLayerCreate(b, "")).To(MatchError(ContainSubstring("not ready")))
		Expect(folders(b)).To(BeEmpty(), "a failed create left session folders")
	})

	It("fails a full upper only in the guest", func() {
		b := testRealBackend()
		ctx := context.Background()
		st := sbtest.OpenStore()
		_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
		Expect(err).To(Succeed())
		v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 64 << 20})
		Expect(err).To(Succeed())
		defer func() { _ = sb.Destroy(ctx) }()
		code, out := shRun(sb, "head -c 200000000 /dev/zero > proj/big.bin")
		Expect(code).NotTo(BeZero(), "a write past the disk size: %q", out)
		Expect(out).To(ContainSubstring("No space left"))
		Expect(strings.TrimSpace(sh(sb, "rm proj/big.bin && echo still-running"))).To(Equal("still-running"))
	})

	It("gives ErrGone after the VM died and still checkpoints", func() {
		sb, st := realFixture(testRealBackend(), "")
		sh(sb, "echo kept > proj/kept.txt && sync")
		sb.vm.m.Stop()
		_, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone), "exec after a crash")
		_, _, err = sb.Checkpoint(context.Background(), "after crash")
		Expect(err).To(Succeed())
		r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/kept.txt")
		Expect(err).To(Succeed())
		_ = r.Close()
	})

	It("turns refused names into conflicts", func() {
		sb, _ := realFixture(testRealBackend(), "")
		sh(sb, `cd proj && printf x > "$(printf 'new\nline')" && printf y > "$(printf 'bad\377name')" && sync`)
		_, conflicts, err := sb.Checkpoint(context.Background(), "names")
		Expect(err).To(Succeed())
		bad := 0
		for _, c := range conflicts {
			if c.Reason == workspace.ReasonBadName {
				bad++
			}
		}
		Expect(bad).To(Equal(2), "bad-name conflicts: %+v", conflicts)
	})

	It("leaves the sandbox running after a refresh that follows a slow checkpoint", func() {
		sb, st := realFixture(testRealBackend(), "")
		clock := time.Now()
		sb.hooks.now = func() time.Time { return clock }
		sb.hooks.afterCommit = func() { clock = clock.Add(50 * time.Second) }
		sh(sb, "echo slow > proj/slow.txt && sync")
		err := sb.Refresh(context.Background())
		Expect(err).To(MatchError(ContainSubstring("retry")))
		Expect(errors.Is(err, sandbox.ErrGone)).To(BeFalse(), "%v", err)
		sb.hooks.afterCommit = nil
		sh(sb, "echo more >> proj/slow.txt") // a write hangs if the guest is still frozen
		Expect(sh(sb, "cat proj/slow.txt")).To(Equal("slow\nmore\n"))
		r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/slow.txt")
		Expect(err).To(Succeed())
		_ = r.Close()
	})

	It("thaws after an ambiguous freeze", func() {
		sb, _ := realFixture(testRealBackend(), "")
		thaws := 0
		sb.hooks.call = func(ctx context.Context, v *vmHandle, typ string) error {
			c, err := v.m.Dial(ctx, guestagent.AgentPort)
			if err != nil {
				return err
			}
			if err := guestagent.Call(ctx, c, typ); err != nil {
				return err
			}
			switch typ {
			case guestagent.TypeFreeze:
				return errors.New("lost reply")
			case guestagent.TypeThaw:
				thaws++
			}
			return nil
		}
		_, _, err := sb.Checkpoint(context.Background(), "x")
		Expect(err).To(MatchError(ContainSubstring("lost reply")))
		Expect(thaws).To(Equal(1))
		sb.hooks.call = nil
		sh(sb, "echo ok > proj/after.txt") // a write hangs if the guest is still frozen
	})

	It("revives the old guest after a materialize failure", func() {
		sb, st := realFixture(testRealBackend(), "")
		sh(sb, "echo kept > proj/kept.txt && sync")
		real := sb.env.Store
		sb.env.Store = failingMaterialize{real}
		err := sb.Refresh(context.Background())
		sb.env.Store = real
		Expect(err).To(MatchError(ContainSubstring("materialize boom")))
		Expect(errors.Is(err, sandbox.ErrGone)).To(BeFalse(), "%v", err)
		Expect(sh(sb, "cat proj/kept.txt")).To(Equal("kept\n"))
		_, _, err = sb.Checkpoint(context.Background(), "after")
		Expect(err).To(Succeed())
		r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/kept.txt")
		Expect(err).To(Succeed())
		_ = r.Close()
	})

	It("does not commit a slow read", func() {
		sb, st := realFixture(testRealBackend(), "")
		sh(sb, "echo late > proj/late.txt && sync")
		clock := time.Now()
		sb.hooks.now = func() time.Time { return clock }
		sb.hooks.afterRead = func() { clock = clock.Add(50 * time.Second) }
		_, _, err := sb.Checkpoint(context.Background(), "slow")
		Expect(err).To(MatchError(errFreezeOverrun))
		_, err = st.Stat(context.Background(), sbtest.Admin, "/proj/late.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "a torn read was committed")
		sb.hooks.afterRead = nil
		cs, _, err := sb.Checkpoint(context.Background(), "normal")
		Expect(err).To(Succeed())
		Expect(cs.Rev).NotTo(BeZero())
		sh(sb, "echo more >> proj/late.txt")
	})

	Describe("suspend and resume", func() {
		It("keeps the files and drops the processes", func() {
			b := testRealBackend()
			sb, st := realFixture(b, "")
			sh(sb, "echo kept > proj/kept.txt")
			p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sleep", "600"}})
			Expect(err).To(Succeed())
			ref, err := sb.Suspend(context.Background())
			Expect(err).To(Succeed())
			// The suspend committed what the guest wrote.
			r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/kept.txt")
			Expect(err).To(Succeed())
			_ = r.Close()
			_, err = p.Wait()
			Expect(err).To(MatchError(sandbox.ErrGone))
			rs, err := b.Resume(context.Background(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
			Expect(err).To(Succeed())
			DeferCleanup(func() { _ = rs.Destroy(context.Background()) })
			Expect(sh(rs, "cat proj/kept.txt; if pgrep sleep >/dev/null; then echo alive; else echo none; fi")).To(Equal("kept\nnone\n"), "the process survived, or the file did not")
		})

		It("keeps a change of the guest that no checkpoint committed", func() {
			b := testRealBackend()
			sb, st := realFixture(b, "")
			ref, err := sb.Suspend(context.Background())
			Expect(err).To(Succeed())
			rs, err := b.Resume(context.Background(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
			Expect(err).To(Succeed())
			DeferCleanup(func() { _ = rs.Destroy(context.Background()) })
			sh(rs, "echo after > proj/after.txt")
			_, _, err = rs.Checkpoint(context.Background(), "after resume")
			Expect(err).To(Succeed())
			r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/after.txt")
			Expect(err).To(Succeed())
			_ = r.Close()
		})
	})

	Describe("the guest", func() {
		It("is hardened like the Cloud Hypervisor guest", func() {
			sb, _ := realFixture(testRealBackend(), "")
			out := sh(sb, `id -u
if echo x > /proc/sys/kernel/core_pattern 2>/dev/null; then echo core-writable; else echo core-denied; fi
for f in $(find /sys -type f 2>/dev/null | head -n 300); do
  if (echo 1 > "$f") 2>/dev/null; then echo "sys-writable $f"; fi
done
echo sys-checked
grep '^NoNewPrivs:' /proc/self/status
grep -E '^Cap(Eff|Amb):' /proc/self/status`)
			var lines []string
			for l := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
				lines = append(lines, strings.Join(strings.Fields(l), " "))
			}
			want := []string{"1000", "core-denied", "sys-checked", "NoNewPrivs: 1", "CapEff: 000000000000000a", "CapAmb: 000000000000000a"}
			Expect(strings.Join(lines, "\n")).To(Equal(strings.Join(want, "\n")), "guest hardening:\n%s", out)
		})

		It("has no network device and no way out", func() {
			sb, _ := realFixture(testRealBackend(), "")
			// The only interfaces are lo and the kernel's own tunnel
			// pseudo-device sit0 (the same kernel as under Cloud Hypervisor). None
			// has a hardware device behind it, and none is configured.
			Expect(strings.TrimSpace(sh(sb, `ls /sys/class/net | tr '\n' ' '`))).To(Equal("lo sit0"))
			Expect(strings.TrimSpace(sh(sb, `for n in /sys/class/net/*; do [ -e $n/device ] && echo nic $n; done; echo checked`))).To(Equal("checked"), "a device-backed network interface exists")
			Expect(strings.TrimSpace(sh(sb, `python3 -c "import socket, fcntl, struct; s=socket.socket(); print([ (n, socket.inet_ntoa(fcntl.ioctl(s.fileno(), 0x8915, struct.pack('256s', n.encode()))[20:24])) for n in ['lo']])" 2>&1 | tail -1`))).To(Equal("[('lo', '127.0.0.1')]"), "lo address")
			code, out := shRun(sb, `python3 - <<'EOF'
import socket
for host in ("1.1.1.1", "8.8.8.8", "192.168.1.1"):
    s = socket.socket()
    s.settimeout(3)
    try:
        s.connect((host, 80))
        print("connected", host)
    except OSError as e:
        pass
print("checked")
EOF`)
			Expect(code).To(BeZero(), out)
			Expect(strings.TrimSpace(out)).To(Equal("checked"), "the guest reached the network:\n%s", out)
		})

		It("sees only the lower tree of the host", func() {
			sb, _ := realFixture(testRealBackend(), "")
			Expect(strings.TrimSpace(sh(sb, `grep -c ' virtiofs ' /proc/mounts`))).To(Equal("1"), "more than one host share")
			Expect(sh(sb, `grep ' virtiofs ' /proc/mounts | cut -d' ' -f1,2`)).To(Equal("lower /run/lower\n"))
			code, _ := shRun(sb, `ls /Users /Volumes /private 2>/dev/null`)
			Expect(code).NotTo(BeZero(), "a host path is visible")
			// The host folder of the session is not reachable from the guest.
			code, out := shRun(sb, `find / -xdev -name state.json -o -xdev -name upper.ext4 2>/dev/null | head -3`)
			Expect(code).To(BeZero())
			Expect(strings.TrimSpace(out)).To(BeEmpty(), "the guest found a file of the session folder: %s", out)
		})

		It("copies a file up from the read-only lower tree and leaves the host file alone", func() {
			sb, _ := realFixture(testRealBackend(), "")
			ctx := context.Background()
			sh(sb, "echo orig > proj/o.txt")
			_, _, err := sb.Checkpoint(ctx, "seed")
			Expect(err).To(Succeed())
			Expect(sb.Refresh(ctx)).To(Succeed())
			hostPath := filepath.Join(sb.dir, "lower", "proj", "o.txt")
			before, err := os.ReadFile(hostPath)
			Expect(err).To(Succeed())
			// The guest sees the host file as owned by 0:0 and keeps the mode of the
			// host file. The append works through CAP_DAC_OVERRIDE of the agent
			// user and copies the file up with the same owner and mode.
			hostMode, err := os.Stat(hostPath)
			Expect(err).To(Succeed())
			mode := strings.TrimSpace(sh(sb, `stat -c '%a' proj/o.txt`))
			Expect(mode).To(Equal(strconv.FormatUint(uint64(hostMode.Mode().Perm()), 8)), "mode in the guest and on the host")
			Expect(sh(sb, `stat -c '%u:%g' proj/o.txt; echo more >> proj/o.txt; cat proj/o.txt; stat -c '%u:%g %a' proj/o.txt`)).To(Equal("0:0\norig\nmore\n0:0 "+mode+"\n"), "copy-up as the agent user")
			after, err := os.ReadFile(hostPath)
			Expect(err).To(Succeed())
			Expect(after).To(Equal(before), "the write reached the lower tree on the host")
		})

		It("cannot write the console", func() {
			sb, _ := realFixture(testRealBackend(), "")
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
			before, err := os.Stat(filepath.Join(sb.dir, "console.log"))
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
			after, err := os.Stat(filepath.Join(sb.dir, "console.log"))
			Expect(err).To(Succeed())
			Expect(after.Size()).To(BeNumerically("<=", before.Size()+4096), "console.log grew from %d to %d bytes", before.Size(), after.Size())
		})

		It("does not delete anything on a user opaque xattr", func() {
			ctx := context.Background()
			sb, st := realFixture(testRealBackend(), "")
			_, err := st.Put(ctx, sbtest.Admin, "/proj/sub/a.txt", strings.NewReader("a\n"), workspace.PutOptions{})
			Expect(err).To(Succeed())
			Expect(sb.Refresh(ctx)).To(Succeed())
			out := sh(sb, `cd /workspace/proj && python3 -c 'import os; os.setxattr("sub", "user.overlay.opaque", b"y")' && echo set; ls sub`)
			Expect(out).To(ContainSubstring("a.txt"))
			_, _, err = sb.Checkpoint(ctx, "x")
			Expect(err).To(Succeed())
			_, err = st.Stat(ctx, sbtest.Admin, "/proj/sub/a.txt")
			Expect(err).To(Succeed(), "a.txt is gone from the workspace")
		})
	})

	Describe("boot failures", func() {
		bootWith := func(script string) (time.Duration, error) {
			GinkgoHelper()
			b := testRealBackend()
			b.opts.ReadyTimeout = 60 * time.Second
			_, load, err := ociimage.Resolve(context.Background(), b.opts.DefaultImage)
			Expect(err).To(Succeed())
			img, err := load()
			Expect(err).To(Succeed())
			layer, err := tarball.LayerFromOpener(agentLayer(script))
			Expect(err).To(Succeed())
			img, err = mutate.AppendLayers(img, layer)
			Expect(err).To(Succeed())
			start := time.Now()
			err = newLayerCreate(b, writeLayout(img))
			Expect(folders(b)).To(BeEmpty(), "a failed create left session folders")
			return time.Since(start), err
		}

		It("ends the VM when the guest powers off during boot", func() {
			elapsed, err := bootWith("#!/bin/sh\necho simulated-agent-boot-failure\nsync\nexec python3 -c 'import ctypes, os; os.sync(); ctypes.CDLL(None).reboot(0x4321fedc)'\n")
			Expect(err).To(HaveOccurred())
			Expect(elapsed).To(BeNumerically("<=", 30*time.Second), "create waited for the timeout")
			Expect(err).To(MatchError(ContainSubstring("simulated-agent-boot-failure")), "the error has no console tail")
		})

		It("fails fast when init exits", func() {
			elapsed, err := bootWith("#!/bin/sh\necho init-exit-marker\nexit 1\n")
			Expect(err).To(HaveOccurred())
			Expect(elapsed).To(BeNumerically("<=", 30*time.Second))
			Expect(err).To(MatchError(ContainSubstring("kernel panic during boot")))
			Expect(err).To(MatchError(ContainSubstring("Kernel panic - not syncing")))
		})
	})

	Describe("a hostile upper image", func() {
		It("fails the checkpoint cleanly and commits nothing", func() {
			ctx := context.Background()
			sb, st := realFixture(testRealBackend(), "")
			sh(sb, "echo mine > proj/mine.txt && sync")
			sb.vm.m.Stop() // the VM is gone: the image is the only copy
			img := filepath.Join(sb.dir, "upper.ext4")
			good, err := os.ReadFile(img)
			Expect(err).To(Succeed())
			// Break the superblock magic, then the root inode table.
			bad := append([]byte(nil), good...)
			bad[1024+56], bad[1024+57] = 0, 0
			Expect(os.WriteFile(img, bad, 0o600)).To(Succeed())
			_, _, err = sb.Checkpoint(ctx, "hostile")
			Expect(err).To(HaveOccurred(), "a corrupt upper image was accepted")
			_, err = st.Stat(ctx, sbtest.Admin, "/proj/mine.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "a corrupt image committed a file")
			// The good image still works afterwards, and the stage folder is gone.
			Expect(os.WriteFile(img, good, 0o600)).To(Succeed())
			_, _, err = sb.Checkpoint(ctx, "good")
			Expect(err).To(Succeed())
			_, err = st.Stat(ctx, sbtest.Admin, "/proj/mine.txt")
			Expect(err).To(Succeed())
			_, err = os.Stat(filepath.Join(sb.dir, "stage"))
			Expect(os.IsNotExist(err)).To(BeTrue(), "the stage folder is left")
		})
	})
})

// failingMaterialize is a store whose Materialize fails.
type failingMaterialize struct{ workspace.Store }

func (failingMaterialize) Materialize(context.Context, workspace.View, string) (int64, error) {
	return 0, errors.New("materialize boom")
}

func filepathBase(p string) string { return filepath.Base(p) }

// realFixtureQuiet makes a fixture whose sandbox is not created yet: the spec
// is ready for Create. It does not register a cleanup for the sandbox.
func realFixtureQuiet(b *Backend) (*fixtureSpec, workspace.Store) {
	GinkgoHelper()
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).To(Succeed())
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).To(Succeed())
	return &fixtureSpec{spec: sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 1 << 30}}, st
}

type fixtureSpec struct{ spec sandbox.Spec }

// The owner of the VMs is the process. These two specs are the two halves of
// a check that a script runs: SANDBOX_TEST_HOLD=<state folder> makes a sandbox
// in that folder and holds it until the process is killed, and
// SANDBOX_TEST_AFTER_KILL=<state folder> starts a new backend over the same
// folder, as a restarted add-on does, and checks that nothing is left.
var _ = Describe("an owner process that is killed", func() {
	It("holds a sandbox until it is killed", func() {
		dir := os.Getenv("SANDBOX_TEST_HOLD")
		if dir == "" {
			Skip("set SANDBOX_TEST_HOLD to a state folder")
		}
		b, err := New(Options{StateDir: dir, CacheDir: testCache(), DefaultImage: os.Getenv("SANDBOX_TEST_IMAGE")})
		Expect(err).To(Succeed())
		sb, _ := realFixture(b, "")
		sh(sb, "echo working > proj/w.txt")
		fmt.Printf("HOLDING pid=%d folder=%s\n", os.Getpid(), sb.dir)
		time.Sleep(10 * time.Minute)
	})

	It("leaves a state folder that the next backend removes", func() {
		dir := os.Getenv("SANDBOX_TEST_AFTER_KILL")
		if dir == "" {
			Skip("set SANDBOX_TEST_AFTER_KILL to the state folder of the killed process")
		}
		before, _ := os.ReadDir(dir)
		fmt.Printf("AFTER-KILL folders-before=%d\n", len(before))
		b, err := New(Options{StateDir: dir, CacheDir: testCache(), DefaultImage: os.Getenv("SANDBOX_TEST_IMAGE")})
		Expect(err).To(Succeed(), "a killed owner left the lock")
		DeferCleanup(func() { _ = b.Close() })
		Expect(folders(b)).To(BeEmpty(), "the sweep left a session folder")
		fmt.Printf("AFTER-KILL folders-after=%d\n", len(folders(b)))
	})
})
