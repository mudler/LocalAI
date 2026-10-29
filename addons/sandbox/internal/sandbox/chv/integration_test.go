//go:build linux

package chv

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The integration tests need a host that can run the backend and a guest
// image:
//
//	SANDBOX_TEST_IMAGE  the guest image, name@sha256:... or oci-layout:<dir>@sha256:...
//	SANDBOX_TEST_CACHE  converted images; default <tmp>/sandbox-chv-test-cache
//	SANDBOX_NO_BWRAP=1  run without bubblewrap (inside a container)
//
// They skip, and say why, when one is missing.

var cacheOnce sync.Once

func testCache() string {
	dir := os.Getenv("SANDBOX_TEST_CACHE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "sandbox-chv-test-cache")
	}
	cacheOnce.Do(func() { _ = os.MkdirAll(dir, 0o700) })
	return dir
}

func testBackend() *Backend {
	GinkgoHelper()
	img := os.Getenv("SANDBOX_TEST_IMAGE")
	if img == "" {
		Skip("set SANDBOX_TEST_IMAGE to the guest image (name@sha256:... or oci-layout:<dir>@sha256:...)")
	}
	b, err := New(Options{StateDir: shortDir(), CacheDir: testCache(), DefaultImage: img, NoBwrap: os.Getenv("SANDBOX_NO_BWRAP") == "1"})
	if errors.Is(err, sandbox.ErrUnsupported) {
		Skip("this host cannot run the backend: " + err.Error())
	}
	Expect(err).To(Succeed())
	return b
}

// fixture creates a sandbox on a store with /proj.
func fixture(b *Backend, image string) (sandbox.Sandbox, workspace.Store) {
	GinkgoHelper()
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).To(Succeed())
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).To(Succeed())
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Image: image, Memory: 1 << 30, Disk: 1 << 30})
	Expect(err).To(Succeed())
	DeferCleanup(func() { sb.Destroy(context.Background()) })
	return sb, st
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

var _ = Describe("Cloud Hypervisor backend", func() {
	Describe("the conformance suite", func() {
		sbtest.RunBackendSuite(func() sandbox.Backend { return testBackend() })
	})

	It("has the Python packages and PDF tools in the guest image", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		sh(sb, `python3 -c "import docx, openpyxl, pptx, pypdf, pandas, PIL, reportlab, pdfplumber, pdf2image"`)
		sh(sb, `pdftoppm -v`)
	})

	It("boots a converted image with every file kind", func() {
		b := testBackend()
		_, load, err := resolve(context.Background(), b.opts.DefaultImage)
		Expect(err).To(Succeed())
		img, err := load()
		Expect(err).To(Succeed())
		img, err = mutate.AppendLayers(img, attrLayer(false))
		Expect(err).To(Succeed())
		sb, _ := fixture(b, writeLayout(img))
		got := sh(sb, `cd /opt/attrs
stat -c '%n %a %u:%g %F' suid fifo
readlink link
[ "$(stat -c %i suid)" = "$(stat -c %i hard)" ] && echo hard-link
stat -c '%n %t:%T %F' null
python3 -c "import os; print(os.getxattr('xattr', 'user.demo').decode())"`)
		want := "suid 4755 0:0 regular file\nfifo 600 1000:1000 fifo\nsuid\nhard-link\nnull 1:3 character special file\nhello\n"
		Expect(got).To(Equal(want), "guest sees")
	})

	It("lets two sandboxes share one image", func() {
		b := testBackend()
		sb1, _ := fixture(b, "")
		sb2, _ := fixture(b, "")
		a, c := strings.TrimSpace(sh(sb1, "echo one")), strings.TrimSpace(sh(sb2, "echo two"))
		Expect(a).To(Equal("one"))
		Expect(c).To(Equal("two"))
	})

	It("cleans up after a ready timeout", func() {
		b := testBackend()
		b.opts.ReadyTimeout = time.Millisecond
		st := sbtest.OpenStore()
		ctx := context.Background()
		_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
		Expect(err).To(Succeed())
		v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 1 << 30})
		Expect(err).To(MatchError(ContainSubstring("not ready")), "create")
		ents, _ := os.ReadDir(b.opts.StateDir)
		Expect(ents).To(BeEmpty(), "a failed create left session folders")
	})

	It("fails a full upper only in the guest", func() {
		b := testBackend()
		ctx := context.Background()
		st := sbtest.OpenStore()
		_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
		Expect(err).To(Succeed())
		v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 64 << 20})
		Expect(err).To(Succeed())
		defer sb.Destroy(ctx)
		var errb bytes.Buffer
		p, err := sb.Exec(ctx, sandbox.Cmd{Args: []string{"sh", "-c", "head -c 200000000 /dev/zero > proj/big.bin"}, Stderr: &errb})
		Expect(err).To(Succeed())
		code, err := p.Wait()
		Expect(err).To(Succeed(), "a write past the disk size: %d %q", code, errb.String())
		Expect(code).NotTo(BeZero(), "a write past the disk size: %q", errb.String())
		Expect(errb.String()).To(ContainSubstring("No space left"), "a write past the disk size")
		Expect(strings.TrimSpace(sh(sb, "rm proj/big.bin && echo still-running"))).To(Equal("still-running"), "after the full disk")
	})

	It("gives ErrGone after a crash and still checkpoints", func() {
		b := testBackend()
		sb, st := fixture(b, "")
		sh(sb, "echo kept > proj/kept.txt && sync")
		s := sb.(*sbx)
		_ = s.vm.ch.Process.Kill()
		<-s.vm.done
		_, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone), "exec after a crash")
		_, _, err = sb.Checkpoint(context.Background(), "after crash")
		Expect(err).To(Succeed())
		r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/kept.txt")
		Expect(err).To(Succeed())
		r.Close()
	})

	// It holds the checkpoint lock, as a running checkpoint or refresh does,
	// so the second call always finds it taken.
	It("reports busy while the checkpoint lock is held", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		s := sb.(*sbx)
		Expect(s.op.TryLock()).To(BeTrue(), "the checkpoint lock was already taken")
		defer s.op.Unlock()
		_, _, err := sb.Checkpoint(context.Background(), "x")
		Expect(err).To(MatchError(sandbox.ErrBusy), "checkpoint under the lock")
		Expect(sb.Refresh(context.Background())).To(MatchError(sandbox.ErrBusy), "refresh under the lock")
	})

	// A name the workspace refuses is a bad-name conflict of its entry. It
	// does not fail the checkpoint.
	It("turns refused names into conflicts", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
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

	// After a refresh the console watcher of the old VM must not mark the new
	// VM gone.
	It("ignores a stale VM when marking gone", func() {
		old, cur := &vm{done: make(chan struct{})}, &vm{done: make(chan struct{})}
		s := &sbx{vm: cur}
		s.markGone(old)
		Expect(s.gone).To(BeFalse(), "a stale watcher marked the new VM gone")
		s.markGone(cur)
		Expect(s.gone).To(BeTrue(), "the current VM's panic did not mark the sandbox gone")
	})

	// A slow checkpoint must not let the guest thaw itself under a refresh:
	// the refresh then does not happen, the guest keeps running and the
	// commit stays.
	It("leaves the sandbox running after a refresh that follows a slow checkpoint", func() {
		b := testBackend()
		sb, st := fixture(b, "")
		s := sb.(*sbx)
		clock := time.Now()
		s.hooks.now = func() time.Time { return clock }
		s.hooks.afterCommit = func() { clock = clock.Add(50 * time.Second) }
		sh(sb, "echo slow > proj/slow.txt && sync")
		err := sb.Refresh(context.Background())
		Expect(err).To(MatchError(ContainSubstring("retry")), "refresh after a slow checkpoint")
		Expect(errors.Is(err, sandbox.ErrGone)).To(BeFalse(), "a refresh that did not happen marked the sandbox gone: %v", err)
		s.hooks.afterCommit = nil
		sh(sb, "echo more >> proj/slow.txt") // a write hangs if the guest is still frozen
		Expect(sh(sb, "cat proj/slow.txt")).To(Equal("slow\nmore\n"), "after the refresh that did not happen")
		r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/slow.txt")
		Expect(err).To(Succeed())
		r.Close()
	})

	// A freeze call that fails may still have frozen the guest (a lost
	// reply): the checkpoint must send the thaw anyway.
	It("thaws after an ambiguous freeze", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		s := sb.(*sbx)
		thaws := 0
		s.hooks.call = func(ctx context.Context, v *vm, typ string) error {
			switch typ {
			case guestagent.TypeFreeze:
				if err := s.request(ctx, v, typ); err != nil {
					return err
				}
				return errors.New("lost reply")
			case guestagent.TypeThaw:
				thaws++
			}
			return s.request(ctx, v, typ)
		}
		_, _, err := sb.Checkpoint(context.Background(), "x")
		Expect(err).To(MatchError(ContainSubstring("lost reply")), "checkpoint with a lost freeze reply")
		Expect(thaws).To(Equal(1), "thaw requests after an ambiguous freeze")
		s.hooks.call = nil
		sh(sb, "echo ok > proj/after.txt") // a write hangs if the guest is still frozen
	})

	// Stale folders of an earlier refresh must not block the next refresh.
	It("removes stale old files on refresh", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		s := sb.(*sbx)
		Expect(os.MkdirAll(filepath.Join(s.dir, "lower.old", "sub"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(s.dir, "upper.old.ext4"), []byte("stale"), 0o600)).To(Succeed())
		Expect(sb.Refresh(context.Background())).To(Succeed(), "refresh with stale old files")
		for _, p := range []string{"lower.old", "upper.old.ext4"} {
			_, err := os.Stat(filepath.Join(s.dir, p))
			Expect(os.IsNotExist(err)).To(BeTrue(), "%s survived the refresh: %v", p, err)
		}
		sh(sb, "true")
	})

	// A new lower that cannot be built boots the old lower and upper again.
	It("revives the old guest after a materialize failure", func() {
		b := testBackend()
		sb, st := fixture(b, "")
		s := sb.(*sbx)
		sh(sb, "echo kept > proj/kept.txt && sync")
		real := s.env.Store
		s.env.Store = failingMaterialize{real}
		err := sb.Refresh(context.Background())
		s.env.Store = real
		Expect(err).To(MatchError(ContainSubstring("materialize boom")), "refresh with a failing lower")
		Expect(errors.Is(err, sandbox.ErrGone)).To(BeFalse(), "refresh with a failing lower: %v", err)
		Expect(sh(sb, "cat proj/kept.txt")).To(Equal("kept\n"), "after the failed refresh")
		_, _, err = sb.Checkpoint(context.Background(), "after")
		Expect(err).To(Succeed())
		r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/kept.txt")
		Expect(err).To(Succeed())
		r.Close()
	})

	// A boot that fails after the swap puts the old files and state back and
	// marks the sandbox gone.
	It("rolls back a refresh whose boot fails after the swap", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		s := sb.(*sbx)
		sh(sb, "echo before > proj/before.txt && sync")
		oldBase := s.st.Base
		failBoot := true
		s.hooks.bootFail = func() error {
			if failBoot {
				failBoot = false
				return errors.New("boot boom")
			}
			return nil
		}
		err := sb.Refresh(context.Background())
		Expect(err).To(MatchError(sandbox.ErrGone), "refresh with a failing new guest")
		for _, p := range []string{"lower", "upper.ext4"} {
			_, err := os.Stat(filepath.Join(s.dir, p))
			Expect(err).To(Succeed(), "%s was not restored", p)
		}
		for _, p := range []string{"lower.old", "upper.old.ext4", "lower.new", "upper.new.ext4"} {
			_, err := os.Stat(filepath.Join(s.dir, p))
			Expect(os.IsNotExist(err)).To(BeTrue(), "%s is left after the rollback: %v", p, err)
		}
		saved, err := loadState(s.dir)
		Expect(err).To(Succeed(), "saved state after the rollback")
		Expect(saved.Base).To(Equal(oldBase), "saved state after the rollback")
		Expect(s.st.Base).To(Equal(oldBase), "in-memory base after the rollback")
		_, err = sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone), "exec after the rollback")
		// The restored upper still holds the content the guest wrote.
		stage := filepath.Join(s.dir, "stage")
		Expect(os.MkdirAll(stage, 0o700)).To(Succeed())
		argv, inStage := b.debugfsArgv(s.dir)
		src, _, err := upperReader{argv: argv, stage: inStage, host: stage, lim: readLimits{stage: 1 << 30}}.read(context.Background(), nil)
		Expect(err).To(Succeed())
		found := false
		for _, e := range src {
			found = found || e.Path == "proj/before.txt"
		}
		Expect(found).To(BeTrue(), "the restored upper lost proj/before.txt: %+v", src)
	})

	// A thaw that fails after a good commit still returns the change set,
	// with an error that wraps the thaw failure.
	It("joins a thaw error to the change set", func() {
		b := testBackend()
		sb, st := fixture(b, "")
		s := sb.(*sbx)
		sh(sb, "echo change > proj/change.txt")
		errThaw := errors.New("thaw boom")
		failThaw := true
		s.hooks.call = func(ctx context.Context, v *vm, typ string) error {
			if typ == guestagent.TypeThaw && failThaw {
				failThaw = false
				return errThaw
			}
			return s.request(ctx, v, typ)
		}
		cs, conflicts, err := sb.Checkpoint(context.Background(), "thaw")
		Expect(err).To(MatchError(errThaw), "checkpoint with a failing thaw")
		Expect(cs.Rev).NotTo(BeZero(), "change set = %+v", cs)
		Expect(conflicts).To(BeEmpty(), "conflicts = %+v", conflicts)
		s.hooks.call = nil
		r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/change.txt")
		Expect(err).To(Succeed())
		r.Close()
		sh(sb, "echo more >> proj/change.txt")
	})

	// If the rename back fails, the files are mixed: the sandbox is gone and
	// must not be booted again.
	It("does not revive after a failed undo", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		s := sb.(*sbx)
		sh(sb, "echo x > proj/x.txt && sync")
		boots := 0
		s.hooks.bootFail = func() error {
			boots++
			return errors.New("must not boot")
		}
		s.hooks.renameFail = func(from, to string) error {
			switch {
			case strings.HasSuffix(to, "upper.old.ext4"):
				return errors.New("rename boom")
			case strings.HasSuffix(from, "lower.old"):
				return errors.New("undo boom")
			}
			return nil
		}
		err := sb.Refresh(context.Background())
		Expect(err).To(MatchError(sandbox.ErrGone), "refresh with a failed undo")
		Expect(err).To(MatchError(ContainSubstring("rename boom")), "refresh with a failed undo")
		Expect(err).To(MatchError(ContainSubstring("undo boom")), "refresh with a failed undo")
		Expect(boots).To(BeZero(), "the sandbox was revived after a failed undo")
	})

	// A checkpoint whose read outlives the guest's thaw commits nothing,
	// changes no state, and the next checkpoint commits normally.
	It("does not commit a slow read", func() {
		b := testBackend()
		sb, st := fixture(b, "")
		s := sb.(*sbx)
		sh(sb, "echo late > proj/late.txt && sync")
		clock := time.Now()
		s.hooks.now = func() time.Time { return clock }
		s.hooks.afterRead = func() { clock = clock.Add(50 * time.Second) }
		stateBefore, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
		Expect(err).To(Succeed())
		_, _, err = sb.Checkpoint(context.Background(), "slow")
		Expect(err).To(MatchError(errFreezeOverrun), "checkpoint with a slow read")
		_, err = st.Stat(context.Background(), sbtest.Admin, "/proj/late.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "a torn read was committed")
		stateAfter, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
		Expect(err).To(Succeed(), "state changed by a checkpoint that did not commit")
		Expect(stateAfter).To(Equal(stateBefore), "state changed by a checkpoint that did not commit")
		s.hooks.afterRead = nil
		cs, _, err := sb.Checkpoint(context.Background(), "normal")
		Expect(err).To(Succeed(), "next checkpoint: %+v", cs)
		Expect(cs.Rev).NotTo(BeZero(), "next checkpoint: %+v", cs)
		_, err = st.Stat(context.Background(), sbtest.Admin, "/proj/late.txt")
		Expect(err).To(Succeed())
		sh(sb, "echo more >> proj/late.txt") // a write hangs if the guest is still frozen
	})
})

// failingMaterialize is a store whose Materialize fails.
type failingMaterialize struct{ workspace.Store }

func (failingMaterialize) Materialize(context.Context, workspace.View, string) (int64, error) {
	return 0, errors.New("materialize boom")
}
