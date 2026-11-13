package vz

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var fakeImage = guestImage{Ref: "test@sha256:" + strings.Repeat("a", 64), Root: "/guest/root.ext4", Kernel: "/guest/vmlinux", Env: []string{"PATH=/usr/bin:/bin"}}

// testBackend is a backend over fake machines.
func testBackend(f *farm, host vmm.HostLimits) *Backend {
	GinkgoHelper()
	b, err := newBackend(Options{StateDir: shortDir(), CacheDir: GinkgoT().TempDir(), ReadyTimeout: 2 * time.Second}, f.make)
	Expect(err).To(Succeed())
	b.host = host
	b.loadImage = func(context.Context, string) (guestImage, error) { return fakeImage, nil }
	DeferCleanup(func() { _ = b.Close() })
	return b
}

type fixture struct {
	b     *Backend
	store workspace.Store
	spec  sandbox.Spec
}

func newFixture(b *Backend, ports map[int]sandbox.PortHandler) *fixture {
	GinkgoHelper()
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).To(Succeed())
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).To(Succeed())
	return &fixture{b: b, store: st, spec: sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: ports}, View: v, Memory: 1 << 30, Disk: 64 << 20}}
}

func (f *fixture) create() *sbx {
	GinkgoHelper()
	sb, err := f.b.Create(context.Background(), f.spec)
	Expect(err).To(Succeed())
	DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb.(*sbx)
}

func folders(b *Backend) []string {
	ents, _ := os.ReadDir(b.opts.StateDir)
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

var _ = Describe("the backend over a fake machine", func() {
	It("creates a sandbox, runs a command and destroys it", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		fx := newFixture(b, map[int]sandbox.PortHandler{3128: sbtest.EchoHandler})
		s := fx.create()
		m := f.last()
		Expect(m.cfg.Kernel).To(Equal(fakeImage.Kernel))
		Expect(m.cfg.Root).To(Equal(fakeImage.Root))
		Expect(m.cfg.Cmdline).To(Equal("console=hvc0 root=/dev/vda ro init=/sbin/sandbox-agent sandbox.ports=3128"))
		Expect(m.cfg.CPUs).To(BeEquivalentTo(2))
		Expect(m.cfg.MemoryBytes).To(BeEquivalentTo(1 << 30))
		Expect(m.cfg.Upper).To(Equal(filepath.Join(s.dir, "upper.ext4")))
		Expect(m.cfg.Lower).To(Equal(filepath.Join(s.dir, "lower")))
		Expect(m.listeners).To(HaveKey(uint32(3128)))
		var out bytes.Buffer
		p, err := s.Exec(context.Background(), sandbox.Cmd{Args: []string{"echo", "hi"}, Stdout: &out})
		Expect(err).To(Succeed())
		code, err := p.Wait()
		Expect(err).To(Succeed())
		Expect(code).To(BeZero())
		Expect(out.String()).To(Equal("hi\n"))
		Expect(s.Destroy(context.Background())).To(Succeed())
		_, err = os.Stat(s.dir)
		Expect(os.IsNotExist(err)).To(BeTrue(), "Destroy left the folder")
		Expect(m.done).To(BeClosed(), "Destroy left the VM running")
		_, err = s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone))
	})

	It("maps the limits of a spec to what the Mac accepts", func() {
		f := &farm{}
		host := vmm.HostLimits{MinCPUs: 1, MaxCPUs: 4, MinMemory: 128 << 20, MaxMemory: 8 << 30}
		b := testBackend(f, host)
		fx := newFixture(b, nil)
		fx.spec.CPUs, fx.spec.Memory = 16, 64<<30
		fx.create()
		Expect(f.last().cfg.CPUs).To(BeEquivalentTo(4), "more CPUs than the Mac has")
		Expect(f.last().cfg.MemoryBytes).To(BeEquivalentTo(8<<30), "more memory than the Mac allows")
		fx.spec.CPUs, fx.spec.Memory = 3, (512<<20)+1
		fx.create()
		Expect(f.last().cfg.CPUs).To(BeEquivalentTo(3))
		Expect(f.last().cfg.MemoryBytes).To(BeEquivalentTo(513<<20), "memory must be a whole number of MiB")
	})

	It("removes the folder and stops the VM when the guest never reports ready", func() {
		f := &farm{configure: func(m *fakeMachine) { m.silent = true }}
		b := testBackend(f, vmm.HostLimits{})
		b.opts.ReadyTimeout = 50 * time.Millisecond
		fx := newFixture(b, nil)
		_, err := b.Create(context.Background(), fx.spec)
		Expect(err).To(MatchError(ContainSubstring("not ready")))
		Expect(folders(b)).To(BeEmpty(), "a failed create left session folders")
		Expect(f.last().done).To(BeClosed(), "a failed create left the VM running")
	})

	It("fails fast on a kernel panic during boot and keeps the console tail", func() {
		f := &farm{configure: func(m *fakeMachine) {
			m.silent = true
			m.onStart = func(c vmm.Config) {
				_ = os.WriteFile(c.Console, []byte("init-exit-marker\n[  1.0] Kernel panic - not syncing: Attempted to kill init!\n"), 0o600)
			}
		}}
		b := testBackend(f, vmm.HostLimits{})
		b.opts.ReadyTimeout = time.Minute
		fx := newFixture(b, nil)
		start := time.Now()
		_, err := b.Create(context.Background(), fx.spec)
		Expect(err).To(MatchError(ContainSubstring("kernel panic during boot")))
		Expect(err).To(MatchError(ContainSubstring("init-exit-marker")))
		Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second))
		Expect(folders(b)).To(BeEmpty())
	})

	It("reports a start error of the machine and leaves nothing", func() {
		f := &farm{configure: func(m *fakeMachine) { m.fail = errors.New("start boom") }}
		b := testBackend(f, vmm.HostLimits{})
		_, err := b.Create(context.Background(), newFixture(b, nil).spec)
		Expect(err).To(MatchError(ContainSubstring("start boom")))
		Expect(folders(b)).To(BeEmpty())
		Expect(f.last().done).To(BeClosed())
	})

	It("marks the sandbox gone when the console shows a panic after boot", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		s := newFixture(b, nil).create()
		fh, err := os.OpenFile(filepath.Join(s.dir, "console.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		Expect(err).To(Succeed())
		_, _ = fh.WriteString("Kernel panic - not syncing: boom\n")
		_ = fh.Close()
		Eventually(func() error {
			_, err := s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
			return err
		}, 3*time.Second, 20*time.Millisecond).Should(MatchError(sandbox.ErrGone))
		Expect(f.last().done).To(BeClosed(), "a panicked guest was left running")
	})

	It("kills a guest that floods the console and keeps the last bytes", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		s := newFixture(b, nil).create()
		path := filepath.Join(s.dir, "console.log")
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		Expect(err).To(Succeed())
		chunk := bytes.Repeat([]byte("A"), 1<<20)
		for range consoleLimit/(1<<20) + 1 {
			_, err = fh.Write(chunk)
			Expect(err).To(Succeed())
		}
		_, _ = fh.WriteString("TAIL-MARK")
		_ = fh.Close()
		Eventually(func() error {
			_, err := s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
			return err
		}, 3*time.Second, 20*time.Millisecond).Should(MatchError(sandbox.ErrGone))
		Expect(f.last().done).To(BeClosed())
		Eventually(func() int64 {
			st, err := os.Stat(path)
			if err != nil {
				return -1
			}
			return st.Size()
		}, 2*time.Second, 20*time.Millisecond).Should(BeNumerically("<=", consoleKeep))
		Expect(tail(path, 100)).To(HaveSuffix("TAIL-MARK"))
	})

	It("ignores a stale VM when marking gone", func() {
		old, cur := &vmHandle{b: &Backend{live: map[*vmHandle]struct{}{}}, m: newFakeMachine(vmm.Config{})}, &vmHandle{b: &Backend{live: map[*vmHandle]struct{}{}}, m: newFakeMachine(vmm.Config{})}
		s := &sbx{vm: cur}
		s.markGone(old)
		Expect(s.gone).To(BeFalse(), "a stale watcher marked the new VM gone")
		s.markGone(cur)
		Expect(s.gone).To(BeTrue())
	})

	It("reports busy while the checkpoint lock is held", func() {
		b := testBackend(&farm{}, vmm.HostLimits{})
		s := newFixture(b, nil).create()
		Expect(s.op.TryLock()).To(BeTrue())
		defer s.op.Unlock()
		_, _, err := s.Checkpoint(context.Background(), "x")
		Expect(err).To(MatchError(sandbox.ErrBusy))
		Expect(s.Refresh(context.Background())).To(MatchError(sandbox.ErrBusy))
		_, err = s.Suspend(context.Background())
		Expect(err).To(MatchError(sandbox.ErrBusy))
	})

	It("checkpoints an empty upper layer, freezing and thawing the guest", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		s := newFixture(b, nil).create()
		cs, conflicts, err := s.Checkpoint(context.Background(), "empty")
		Expect(err).To(Succeed())
		Expect(conflicts).To(BeEmpty())
		Expect(cs.Rev).To(BeZero(), "an empty layer made a change set")
		Expect(f.last().seen(guestagent.TypeFreeze)).To(Equal(1))
		Expect(f.last().seen(guestagent.TypeThaw)).To(Equal(1))
	})

	It("thaws after an ambiguous freeze", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		s := newFixture(b, nil).create()
		thaws := 0
		s.hooks.call = func(ctx context.Context, v *vmHandle, typ string) error {
			switch typ {
			case guestagent.TypeFreeze:
				return errors.New("lost reply")
			case guestagent.TypeThaw:
				thaws++
			}
			return nil
		}
		_, _, err := s.Checkpoint(context.Background(), "x")
		Expect(err).To(MatchError(ContainSubstring("lost reply")))
		Expect(thaws).To(Equal(1), "thaw requests after an ambiguous freeze")
	})

	It("refreshes: a new VM boots over a new lower and an empty upper", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		fx := newFixture(b, nil)
		s := fx.create()
		Expect(os.WriteFile(filepath.Join(s.dir, "lower", "marker"), nil, 0o600)).To(Succeed())
		first := f.last()
		Expect(s.Refresh(context.Background())).To(Succeed())
		Expect(f.count()).To(Equal(2))
		Expect(first.done).To(BeClosed(), "the old VM kept running")
		Expect(first.seen(guestagent.TypeShutdown)).To(Equal(1))
		_, err := os.Stat(filepath.Join(s.dir, "lower", "marker"))
		Expect(os.IsNotExist(err)).To(BeTrue(), "the lower layer was not rebuilt")
		for _, p := range []string{"lower.old", "upper.old.ext4", "lower.new", "upper.new.ext4"} {
			_, err := os.Stat(filepath.Join(s.dir, p))
			Expect(os.IsNotExist(err)).To(BeTrue(), "%s is left", p)
		}
		_, err = s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(Succeed())
	})

	It("rolls back a refresh whose boot fails after the swap", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		s := newFixture(b, nil).create()
		oldBase := s.st.Base
		fail := true
		s.hooks.bootFail = func() error {
			if fail {
				fail = false
				return errors.New("boot boom")
			}
			return nil
		}
		err := s.Refresh(context.Background())
		Expect(err).To(MatchError(sandbox.ErrGone))
		for _, p := range []string{"lower", "upper.ext4"} {
			_, err := os.Stat(filepath.Join(s.dir, p))
			Expect(err).To(Succeed(), "%s was not restored", p)
		}
		saved, err := loadState(s.dir)
		Expect(err).To(Succeed())
		Expect(saved.Base).To(Equal(oldBase))
		_, err = s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone))
	})

	It("does not revive after a failed undo", func() {
		b := testBackend(&farm{}, vmm.HostLimits{})
		s := newFixture(b, nil).create()
		boots := 0
		s.hooks.bootFail = func() error { boots++; return errors.New("must not boot") }
		s.hooks.renameFail = func(from, to string) error {
			switch {
			case strings.HasSuffix(to, "upper.old.ext4"):
				return errors.New("rename boom")
			case strings.HasSuffix(from, "lower.old"):
				return errors.New("undo boom")
			}
			return nil
		}
		err := s.Refresh(context.Background())
		Expect(err).To(MatchError(sandbox.ErrGone))
		Expect(err).To(MatchError(ContainSubstring("undo boom")))
		Expect(boots).To(BeZero())
	})

	Describe("suspend and resume", func() {
		It("stops the VM and boots a new one over the same files", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			fx := newFixture(b, nil)
			s := fx.create()
			first := f.last()
			p, err := s.Exec(context.Background(), sandbox.Cmd{Args: []string{"sleep", "60"}})
			Expect(err).To(Succeed())
			ref, err := s.Suspend(context.Background())
			Expect(err).To(Succeed())
			Expect(ref).To(Equal(sandbox.SnapshotRef{Backend: "vz", Path: s.dir}))
			Expect(first.done).To(BeClosed(), "a suspended sandbox keeps its VM")
			_, err = s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
			Expect(err).To(MatchError(sandbox.ErrGone))
			// The process of the old guest is gone.
			_, err = p.Wait()
			Expect(err).To(HaveOccurred())
			st, err := loadState(s.dir)
			Expect(err).To(Succeed())
			Expect(st.Suspended).To(BeTrue())

			r, err := b.Resume(context.Background(), ref, fx.spec.Env)
			Expect(err).To(Succeed())
			rs := r.(*sbx)
			DeferCleanup(func() { _ = rs.Destroy(context.Background()) })
			Expect(f.count()).To(Equal(2))
			Expect(rs.dir).To(Equal(s.dir))
			st, _ = loadState(s.dir)
			Expect(st.Suspended).To(BeFalse())
			Expect(st.Epoch).To(BeEquivalentTo(1))
			_, err = rs.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
			Expect(err).To(Succeed())

			// The old handle owns nothing any more: it changes nothing.
			_, _, err = s.Checkpoint(context.Background(), "stale")
			Expect(err).To(MatchError(sandbox.ErrGone))
			Expect(s.Destroy(context.Background())).To(Succeed())
			_, err = os.Stat(s.dir)
			Expect(err).To(Succeed(), "the stale handle deleted the resumed sandbox")
			// A second resume of the same reference finds it running.
			_, err = b.Resume(context.Background(), ref, fx.spec.Env)
			Expect(err).To(HaveOccurred())
		})

		It("refuses a wrong actor, wrong ports and a folder outside the state folder", func() {
			b := testBackend(&farm{}, vmm.HostLimits{})
			fx := newFixture(b, nil)
			s := fx.create()
			ref, err := s.Suspend(context.Background())
			Expect(err).To(Succeed())
			env := fx.spec.Env
			env.Actor = workspace.Actor{Principal: "someone-else"}
			_, err = b.Resume(context.Background(), ref, env)
			Expect(err).To(MatchError(workspace.ErrInvalid))
			env = fx.spec.Env
			env.Ports = map[int]sandbox.PortHandler{3128: sbtest.EchoHandler}
			_, err = b.Resume(context.Background(), ref, env)
			Expect(err).To(MatchError(workspace.ErrInvalid))
			_, err = b.Resume(context.Background(), sandbox.SnapshotRef{Backend: "vz", Path: "/etc"}, fx.spec.Env)
			Expect(err).To(MatchError(workspace.ErrInvalid))
			_, err = b.Resume(context.Background(), sandbox.SnapshotRef{Backend: "chv", Path: ref.Path}, fx.spec.Env)
			Expect(err).To(MatchError(workspace.ErrInvalid))
		})

		It("keeps the snapshot when the new guest does not boot, so that a retry works", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			fx := newFixture(b, nil)
			s := fx.create()
			ref, err := s.Suspend(context.Background())
			Expect(err).To(Succeed())
			f.configure = func(m *fakeMachine) { m.fail = errors.New("start boom") }
			_, err = b.Resume(context.Background(), ref, fx.spec.Env)
			Expect(err).To(MatchError(ContainSubstring("start boom")))
			st, err := loadState(s.dir)
			Expect(err).To(Succeed())
			Expect(st.Suspended).To(BeTrue(), "a failed resume lost the snapshot")
			f.configure = nil
			r, err := b.Resume(context.Background(), ref, fx.spec.Env)
			Expect(err).To(Succeed())
			DeferCleanup(func() { _ = r.Destroy(context.Background()) })
		})

		It("leaves the guest running and thawed when the state cannot be saved", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			s := newFixture(b, nil).create()
			s.hooks.suspendSave = func() error { return errors.New("save boom") }
			_, err := s.Suspend(context.Background())
			Expect(err).To(MatchError(ContainSubstring("save boom")))
			Expect(f.last().seen(guestagent.TypeThaw)).To(Equal(1))
			_, err = s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
			Expect(err).To(Succeed())
			st, _ := loadState(s.dir)
			Expect(st.Suspended).To(BeFalse())
		})
	})

	Describe("the owner process", func() {
		It("removes the folders that a closed backend left", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			fx := newFixture(b, nil)
			running := fx.create()
			susp := fx.create()
			_, err := susp.Suspend(context.Background())
			Expect(err).To(Succeed())
			other := filepath.Join(b.opts.StateDir, "not-a-session")
			Expect(os.Mkdir(other, 0o700)).To(Succeed())
			Expect(b.Close()).To(Succeed())
			// A new process finds the folders of the old one.
			b2, err := newBackend(Options{StateDir: b.opts.StateDir, CacheDir: b.opts.CacheDir}, f.make)
			Expect(err).To(Succeed())
			DeferCleanup(func() { _ = b2.Close() })
			Expect(folders(b2)).To(ConsistOf("not-a-session"), "the sweep removed a folder that is not a session, or kept a session")
			_, err = os.Stat(running.dir)
			Expect(os.IsNotExist(err)).To(BeTrue())
			_, err = os.Stat(susp.dir)
			Expect(os.IsNotExist(err)).To(BeTrue())
			// The handle of the old process reports gone.
			_, _, err = running.Checkpoint(context.Background(), "x")
			Expect(err).To(MatchError(sandbox.ErrGone))
		})

		// A crashed owner does not run Close: its VMs die with the process,
		// and the kernel drops its lock. The test drops only the lock, so the
		// old handles are still alive when the new backend starts.
		It("removes the folders that a crashed owner left", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			fx := newFixture(b, nil)
			running := fx.create()
			susp := fx.create()
			ref, err := susp.Suspend(context.Background())
			Expect(err).To(Succeed())
			Expect(b.lock.Close()).To(Succeed())
			b2, err := newBackend(Options{StateDir: b.opts.StateDir, CacheDir: b.opts.CacheDir}, f.make)
			Expect(err).To(Succeed())
			DeferCleanup(func() { _ = b2.Close() })
			Expect(folders(b2)).To(BeEmpty(), "the sweep left a session folder")
			_, _, err = running.Checkpoint(context.Background(), "x")
			Expect(err).To(MatchError(sandbox.ErrGone))
			// A snapshot that the sweep removed is gone, not a file error.
			_, err = b2.Resume(context.Background(), ref, fx.spec.Env)
			Expect(err).To(MatchError(sandbox.ErrGone))
		})

		It("keeps suspended folders on request", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			fx := newFixture(b, nil)
			susp := fx.create()
			_, err := susp.Suspend(context.Background())
			Expect(err).To(Succeed())
			running := fx.create()
			Expect(b.Close()).To(Succeed())
			b2, err := newBackend(Options{StateDir: b.opts.StateDir, CacheDir: b.opts.CacheDir, KeepSuspended: true}, f.make)
			Expect(err).To(Succeed())
			DeferCleanup(func() { _ = b2.Close() })
			Expect(folders(b2)).To(ConsistOf(filepath.Base(susp.dir)))
			_, err = os.Stat(running.dir)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("takes one lock on the state folder", func() {
			b := testBackend(&farm{}, vmm.HostLimits{})
			_, err := newBackend(Options{StateDir: b.opts.StateDir, CacheDir: b.opts.CacheDir}, (&farm{}).make)
			Expect(err).To(MatchError(ContainSubstring("in use by another backend")))
			Expect(errors.Is(err, sandbox.ErrUnsupported)).To(BeFalse())
			Expect(b.Close()).To(Succeed())
			Expect(b.Close()).To(Succeed(), "Close twice")
			b3, err := newBackend(Options{StateDir: b.opts.StateDir, CacheDir: b.opts.CacheDir}, (&farm{}).make)
			Expect(err).To(Succeed())
			Expect(b3.Close()).To(Succeed())
		})

		It("stops its VMs on Close", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			fx := newFixture(b, nil)
			fx.create()
			fx.create()
			Expect(b.Close()).To(Succeed())
			for _, m := range f.machines {
				Expect(m.done).To(BeClosed(), "a VM outlived its backend")
			}
		})

		It("refuses Create and Resume after Close", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			fx := newFixture(b, nil)
			susp := fx.create()
			ref, err := susp.Suspend(context.Background())
			Expect(err).To(Succeed())
			made := f.count()
			Expect(b.Close()).To(Succeed())
			_, err = b.Create(context.Background(), fx.spec)
			Expect(err).To(MatchError(sandbox.ErrUnsupported))
			Expect(err).To(MatchError(ContainSubstring("closed")))
			_, err = b.Resume(context.Background(), ref, fx.spec.Env)
			Expect(err).To(MatchError(sandbox.ErrUnsupported))
			Expect(f.count()).To(Equal(made), "a closed backend made a machine")
			Expect(folders(b)).To(ConsistOf(filepath.Base(susp.dir)), "a closed backend made a folder")
		})

		It("refuses a Resume of a folder that does not exist", func() {
			b := testBackend(&farm{}, vmm.HostLimits{})
			fx := newFixture(b, nil)
			_, err := b.Resume(context.Background(), sandbox.SnapshotRef{Backend: snapshotBackend, Path: filepath.Join(b.opts.StateDir, "0123456789ab")}, fx.spec.Env)
			Expect(err).To(MatchError(sandbox.ErrGone))
		})

		It("removes the state on Destroy even after the VM died", func() {
			f := &farm{}
			b := testBackend(f, vmm.HostLimits{})
			s := newFixture(b, nil).create()
			f.last().Stop()
			_, err := s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
			Expect(err).To(MatchError(sandbox.ErrGone), "exec after the VM died")
			Expect(s.Destroy(context.Background())).To(Succeed())
			Expect(folders(b)).To(BeEmpty())
			Expect(s.Destroy(context.Background())).To(Succeed(), "Destroy twice")
		})
	})

	It("serves at most 64 port connections at once", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		release := make(chan struct{})
		running := make(chan struct{}, 200)
		fx := newFixture(b, map[int]sandbox.PortHandler{3128: func(ctx context.Context, c net.Conn) {
			running <- struct{}{}
			<-release
			_ = c.Close()
		}})
		fx.create()
		accept := f.last().listeners[3128]
		Expect(accept).NotTo(BeNil())
		for range 70 {
			host, guest := net.Pipe()
			accept(host)
			DeferCleanup(func() { _ = guest.Close() })
		}
		Eventually(func() int { return len(running) }, 2*time.Second).Should(Equal(maxPortConns))
		Consistently(func() int { return len(running) }, 200*time.Millisecond).Should(Equal(maxPortConns))
		close(release)
	})
})
