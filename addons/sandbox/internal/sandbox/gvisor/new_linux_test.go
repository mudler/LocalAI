//go:build linux

package gvisor

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// fakeRunsc writes a shell script that answers like runsc. "--version" prints
// version; "run" prints runOut and exits with runRC; any other command exits 0.
func fakeRunsc(dir, version, runOut string, runRC int, withBin bool) string {
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in\n" +
		"    --version) echo '" + version + "'; exit 0;;\n" +
		"    run) echo '" + runOut + "'; exit " + strconv.Itoa(runRC) + ";;\n" +
		"  esac\n" +
		"done\n" +
		"exit 0\n"
	p := filepath.Join(dir, "runsc")
	Expect(os.WriteFile(p, []byte(script), 0o755)).To(Succeed())
	if withBin {
		Expect(os.MkdirAll(filepath.Join(dir, "gvisor-bin"), 0o755)).To(Succeed())
	}
	return p
}

func opts(runsc string) Options {
	return Options{StateDir: shortDir(), CacheDir: GinkgoT().TempDir(), Runsc: runsc}
}

// loggingRunsc is a fake runsc that answers like a healthy one and appends
// every "delete" call to calls.log as "delete <root flag> <last argument>".
func loggingRunsc(dir string) (runsc, logPath string) {
	logPath = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nroot=\nverb=\nfor a in \"$@\"; do\n case \"$a\" in\n --version) echo 'runsc version release-20261012.0'; exit 0;;\n --root=*) root=\"$a\";;\n run|delete|restore) [ -z \"$verb\" ] && verb=\"$a\";;\n esac\n last=\"$a\"\ndone\n" +
		"echo \"$verb $root $last\" >> " + logPath + "\nexit ${FAKE_RUNSC_RC:-0}\n"
	Expect(os.MkdirAll(filepath.Join(dir, "gvisor-bin"), 0o755)).To(Succeed())
	runsc = filepath.Join(dir, "runsc")
	Expect(os.WriteFile(runsc, []byte(script), 0o755)).To(Succeed())
	return runsc, logPath
}

func readLog(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// bootFixture is a sandbox handle that has never been created by Create, on a
// fake runsc, for the paths that need no image.
func bootFixture(r string) *sbx {
	cpus := allowedCPUs()
	taskset, err := exec.LookPath("taskset")
	if err != nil || len(cpus) == 0 {
		Skip("needs taskset")
	}
	b := &Backend{opts: Options{StateDir: shortDir()}, runsc: r, taskset: taskset, cpus: cpus, aliveEvery: time.Hour, memEvery: time.Hour, diskEvery: time.Hour}
	dir := filepath.Join(b.opts.StateDir, "eeeeeeeeeeee")
	for _, d := range []string{"upper", "ctl", "runsc"} {
		Expect(os.MkdirAll(filepath.Join(dir, d), 0o700)).To(Succeed())
	}
	return &sbx{b: b, id: "eeeeeeeeeeee", dir: dir, st: state{CPUs: 1, Memory: 1 << 30, Disk: 1 << 30}}
}

const goodRelease = "runsc version release-20261012.0"

var _ = Describe("New", func() {
	DescribeTable("returns ErrUnsupported for each failure",
		func(setup func() Options, want string) {
			_, err := New(setup())
			Expect(err).To(MatchError(sandbox.ErrUnsupported))
			Expect(err.Error()).To(ContainSubstring(want), "want a part of the reason")
		},
		Entry("root", func() Options {
			old := geteuid
			geteuid = func() int { return 0 }
			DeferCleanup(func() { geteuid = old })
			return opts(fakeRunsc(GinkgoT().TempDir(), goodRelease, "", 0, true))
		}, "must not run as root"),
		Entry("runsc missing", func() Options {
			return opts(filepath.Join(GinkgoT().TempDir(), "no-runsc"))
		}, "runsc not found"),
		Entry("old release", func() Options {
			return opts(fakeRunsc(GinkgoT().TempDir(), "runsc version release-20260101.0", "", 0, true))
		}, "older than "+MinRelease),
		Entry("no release in the output", func() Options {
			return opts(fakeRunsc(GinkgoT().TempDir(), "something else", "", 0, true))
		}, "no release"),
		Entry("gvisor-bin missing", func() Options {
			return opts(fakeRunsc(GinkgoT().TempDir(), goodRelease, "", 0, false))
		}, "gvisor-bin"),
		Entry("AppArmor denies the user namespace", func() Options {
			return opts(fakeRunsc(GinkgoT().TempDir(), goodRelease, "creating container: cannot create gofer process: gofer: fork/exec /proc/self/exe: permission denied", 128, true))
		}, "gvisor.Profile"),
		Entry("runsc cannot start a sandbox", func() Options {
			return opts(fakeRunsc(GinkgoT().TempDir(), goodRelease, "boom", 1, true))
		}, "cannot start a sandbox"),
		Entry("state folder too long", func() Options {
			o := opts(fakeRunsc(GinkgoT().TempDir(), goodRelease, "", 0, true))
			o.StateDir = filepath.Join(o.StateDir, strings.Repeat("d", 80))
			return o
		}, "too long for socket paths"),
	)

	It("accepts a good host", func() {
		o := opts(fakeRunsc(GinkgoT().TempDir(), "runsc version release-20261012.0", "", 0, true))
		b, err := New(o)
		Expect(err).ToNot(HaveOccurred())
		Expect(b.runsc).To(Equal(o.Runsc))
		Expect(b.opts.ReadyTimeout).ToNot(BeZero())
		// The check sandbox leaves nothing in the state folder.
		Expect(sessionFolders(b.opts.StateDir)).To(BeEmpty(), "New left session folders in StateDir")
		// A later release is fine too.
		o2 := opts(fakeRunsc(GinkgoT().TempDir(), "runsc version release-20270105.2", "", 0, true))
		_, err = New(o2)
		Expect(err).ToNot(HaveOccurred())
	})

	// The supervisor starts the add-on with no PATH at all.
	It("finds taskset without a PATH", func() {
		r := fakeRunsc(GinkgoT().TempDir(), "runsc version release-20261012.0", "", 0, true)
		GinkgoT().Setenv("PATH", "")
		_, err := New(opts(r))
		Expect(err).ToNot(HaveOccurred(), "with no PATH")
	})

	It("needs folders and taskset", func() {
		_, err := New(Options{})
		Expect(err).To(HaveOccurred(), "empty options")
		Expect(errors.Is(err, sandbox.ErrUnsupported)).To(BeFalse(), "empty options: %v", err)
		r := fakeRunsc(GinkgoT().TempDir(), "runsc version release-20261012.0", "", 0, true)
		old := sandbox.SystemPath
		sandbox.SystemPath = GinkgoT().TempDir() // no taskset
		DeferCleanup(func() { sandbox.SystemPath = old })
		_, err = New(opts(r))
		Expect(err).To(MatchError(sandbox.ErrUnsupported), "without taskset")
		Expect(err.Error()).To(ContainSubstring("taskset"))
	})

	DescribeTable("creates no folder when a check fails",
		func(mk func(root string) string, name string) {
			root := shortDir()
			r := mk(root)
			o := Options{StateDir: filepath.Join(root, "state-"+name), CacheDir: filepath.Join(root, "cache-"+name), Runsc: r}
			_, err := New(o)
			Expect(err).To(MatchError(sandbox.ErrUnsupported))
			for _, d := range []string{o.StateDir, o.CacheDir} {
				_, err := os.Stat(d)
				Expect(os.IsNotExist(err)).To(BeTrue(), "%s exists after a failed check: %v", d, err)
			}
		},
		Entry("missing", func(root string) string { return filepath.Join(root, "no-runsc") }, "missing"),
		Entry("old-release", func(root string) string {
			return fakeRunsc(filepath.Join(root, "old"), "runsc version release-20260101.0", "", 0, true)
		}, "old-release"),
		Entry("no-helpers", func(root string) string {
			return fakeRunsc(filepath.Join(root, "nohelp"), goodRelease, "", 0, false)
		}, "no-helpers"),
		Entry("cannot-start", func(root string) string {
			return fakeRunsc(filepath.Join(root, "bad"), goodRelease, "boom", 1, true)
		}, "cannot-start"),
		Entry("denied", func(root string) string {
			return fakeRunsc(filepath.Join(root, "denied"), goodRelease, "permission denied", 128, true)
		}, "denied"),
	)

	It("refuses a runsc path a profile cannot name", func() {
		r := fakeRunsc(filepath.Join(GinkgoT().TempDir(), "a{b"), "runsc version release-20261012.0", "", 0, true)
		_, err := New(opts(r))
		Expect(err).To(MatchError(sandbox.ErrUnsupported))
		Expect(err.Error()).To(ContainSubstring("cannot name safely"))
	})

	It("follows a symlink to runsc", func() {
		real := fakeRunsc(filepath.Join(GinkgoT().TempDir(), "real"), "runsc version release-20261012.0", "", 0, true)
		link := filepath.Join(GinkgoT().TempDir(), "runsc")
		Expect(os.Symlink(real, link)).To(Succeed())
		b, err := New(opts(link))
		Expect(err).ToNot(HaveOccurred())
		want, _ := filepath.EvalSymlinks(real)
		Expect(b.runsc).To(Equal(want))
	})

	It("needs a CPU list", func() {
		old := allowedCPUs
		allowedCPUs = func() []int { return nil }
		DeferCleanup(func() { allowedCPUs = old })
		r := fakeRunsc(GinkgoT().TempDir(), "runsc version release-20261012.0", "", 0, true)
		_, err := New(opts(r))
		Expect(err).To(MatchError(sandbox.ErrUnsupported))
		Expect(err.Error()).To(ContainSubstring("CPU list"))
	})

	It("deletes the probe sandbox when the probe fails", func() {
		dir := GinkgoT().TempDir()
		logf := filepath.Join(dir, "calls.log")
		script := "#!/bin/sh\nfor a in \"$@\"; do\n case \"$a\" in\n --version) echo 'runsc version release-20261012.0'; exit 0;;\n run) echo boom; echo run >> " + logf + "; exit 1;;\n delete) echo delete >> " + logf + "; exit 0;;\n esac\ndone\nexit 0\n"
		Expect(os.WriteFile(filepath.Join(dir, "runsc"), []byte(script), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(dir, "gvisor-bin"), 0o755)).To(Succeed())
		_, err := New(opts(filepath.Join(dir, "runsc")))
		Expect(err).To(MatchError(sandbox.ErrUnsupported))
		got, _ := os.ReadFile(logf)
		Expect(string(got)).To(Equal("run\ndelete\n"), "calls")
	})

	It("takes an exclusive lock on StateDir", func() {
		r := fakeRunsc(GinkgoT().TempDir(), "runsc version release-20261012.0", "", 0, true)
		o := opts(r)
		b, err := New(o)
		Expect(err).ToNot(HaveOccurred())
		// A second backend on the same folder, in this process, is refused with a
		// plain error that says why.
		o2 := o
		o2.CacheDir = GinkgoT().TempDir()
		_, err = New(o2)
		Expect(err).To(HaveOccurred(), "second New")
		Expect(errors.Is(err, sandbox.ErrUnsupported)).To(BeFalse(), "second New: %v", err)
		Expect(err.Error()).To(ContainSubstring("in use by another backend"))
		Expect(b.Close()).To(Succeed())
		Expect(b.Close()).To(Succeed(), "second Close")
		b2, err := New(o2)
		Expect(err).ToNot(HaveOccurred(), "New after Close")
		_ = b2.Close()
	})

	It("refuses a negative stage limit", func() {
		o := opts(fakeRunsc(GinkgoT().TempDir(), "runsc version release-20261012.0", "", 0, true))
		o.StageLimit = -1
		_, err := New(o)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("StageLimit"))
	})

	// Nobody owns a suspended sandbox of an earlier process: the manager that
	// held its reference is gone, so its checkpoint would stay for ever.
	It("removes the suspended sandboxes of an earlier process", func() {
		r, _ := loggingRunsc(GinkgoT().TempDir())
		o := opts(r)
		d := filepath.Join(o.StateDir, "bbbbbbbbbbbb")
		Expect(os.MkdirAll(filepath.Join(d, "runsc"), 0o700)).To(Succeed())
		Expect((&state{Suspended: true}).save(d)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(d, "checkpoint"), 0o700)).To(Succeed())
		b, err := New(o)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		_, err = os.Stat(d)
		Expect(os.IsNotExist(err)).To(BeTrue(), "the suspended folder is still there: %v", err)
	})

	It("stops the sandboxes of an earlier process", func() {
		r, calls := loggingRunsc(GinkgoT().TempDir())
		o := opts(r)
		o.KeepSuspended = true
		mk := func(id string, st *state) string {
			d := filepath.Join(o.StateDir, id)
			Expect(os.MkdirAll(filepath.Join(d, "runsc"), 0o700)).To(Succeed())
			if st != nil {
				Expect(st.save(d)).To(Succeed())
			}
			return d
		}
		running := mk("aaaaaaaaaaaa", &state{})
		noState := mk("cccccccccccc", nil)
		corrupt := mk("dddddddddddd", nil)
		Expect(os.WriteFile(filepath.Join(corrupt, "state.json"), []byte("{not json"), 0o600)).To(Succeed())
		suspended := mk("bbbbbbbbbbbb", &state{Suspended: true})
		Expect(os.Mkdir(filepath.Join(suspended, "checkpoint"), 0o700)).To(Succeed())
		other := filepath.Join(o.StateDir, "not-a-session")
		Expect(os.Mkdir(other, 0o700)).To(Succeed())
		b, err := New(o)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		for _, d := range []string{running, noState, corrupt} {
			_, err := os.Stat(d)
			Expect(os.IsNotExist(err)).To(BeTrue(), "%s is still there: %v", filepath.Base(d), err)
		}
		for _, d := range []string{suspended, filepath.Join(suspended, "checkpoint"), other} {
			_, err := os.Stat(d)
			Expect(err).ToNot(HaveOccurred(), "%s was removed", filepath.Base(d))
		}
		got := strings.Join(readLog(calls), "\n")
		for _, d := range []string{running, noState, corrupt} {
			want := "delete --root=" + filepath.Join(d, "runsc") + " " + filepath.Base(d)
			Expect(got).To(ContainSubstring(want), "no %q in the calls", want)
		}
		Expect(got).ToNot(ContainSubstring("bbbbbbbbbbbb"), "the suspended sandbox was deleted")
	})

	It("keeps a folder whose sandbox cannot be stopped", func() {
		r, _ := loggingRunsc(GinkgoT().TempDir())
		o := opts(r)
		d := filepath.Join(o.StateDir, "aaaaaaaaaaaa")
		Expect(os.MkdirAll(filepath.Join(d, "runsc"), 0o700)).To(Succeed())
		// The probe of New runs "run", and the delete after it: both succeed, so
		// make the delete of the old folder fail only.
		script := "#!/bin/sh\ncase \"$*\" in\n *--version*) echo 'runsc version release-20261012.0'; exit 0;;\n *aaaaaaaaaaaa*) echo cannot; exit 1;;\nesac\nexit 0\n"
		Expect(os.WriteFile(r, []byte(script), 0o755)).To(Succeed())
		b, err := New(o)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = b.Close() }()
		_, err = os.Stat(d)
		Expect(err).ToNot(HaveOccurred(), "the folder was removed although its sandbox may still run")
	})

	It("puts the probe beside StateDir and names it in the error", func() {
		root := shortDir()
		r := fakeRunsc(filepath.Join(root, "bin"), "runsc version release-20261012.0", "boom", 1, true)
		o := Options{StateDir: filepath.Join(root, "state"), CacheDir: filepath.Join(root, "cache"), Runsc: r}
		_, err := New(o)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("check folder " + root + "/.gvcheck-"))
		ents, _ := os.ReadDir(root)
		for _, e := range ents {
			Expect(e.Name()).ToNot(HavePrefix(".gvcheck-"), "the probe folder was left")
		}
	})
})

var _ = Describe("parsing and picking", func() {
	It("parses the release and the CPU list", func() {
		d, m, ok := parseRelease([]byte("runsc version release-20261012.0\nspec: 1.2.1"))
		Expect(ok).To(BeTrue())
		Expect(d).To(Equal(20261012))
		Expect(m).To(Equal(0))
		Expect(releaseAtLeast(20261012, 0)).To(BeTrue())
		Expect(releaseAtLeast(20261012, 3)).To(BeTrue())
		Expect(releaseAtLeast(20270101, 0)).To(BeTrue())
		Expect(releaseAtLeast(20261011, 9)).To(BeFalse())
		got := parseCPUList("0-2,5,7-8,x,9-3")
		Expect(got).To(HaveLen(6), "cpus = %v", got)
		Expect(got[0]).To(Equal(0))
		Expect(got[2]).To(Equal(2))
		Expect(got[3]).To(Equal(5))
		Expect(got[5]).To(Equal(8))
	})

	It("spreads the CPU picks", func() {
		b := &Backend{cpus: []int{4, 5, 6, 7}}
		a, c := b.pickCPUs(2), b.pickCPUs(2)
		Expect(a).To(Equal("4,5"))
		Expect(c).To(Equal("6,7"))
		Expect(b.pickCPUs(2)).To(Equal("4,5"), "wrap")
		Expect(strings.Count(b.pickCPUs(9), ",")).To(Equal(3), "never more than the host has")
	})

	It("validates ids and rejects empty calls", func() {
		for _, bad := range []string{"", "-h", "--version", "check", "0123456789AB", "0123456789abc"} {
			Expect(validID(bad)).ToNot(BeNil(), "%q accepted", bad)
		}
		Expect(validID("0123456789ab")).To(Succeed())
		b := &Backend{runsc: "/bin/true"}
		ctx := context.Background()
		_, err := b.start(ctx, GinkgoT().TempDir(), "--x", 1, "run")
		Expect(err).To(HaveOccurred(), "start accepted a flag as id")
		_, err = b.usage(ctx, "/r", "--x")
		Expect(err).To(HaveOccurred(), "usage accepted a flag as id")
		Expect(b.deleteForce(ctx, "/r", "--x")).ToNot(Succeed(), "deleteForce accepted a flag as id")
		_, err = b.call(ctx, "/r")
		Expect(err).To(HaveOccurred(), "call accepted no command")
	})

	It("builds the global flags", func() {
		got := globalFlags("/r")
		want := []string{"--directfs=false", "--host-uds=all", "--overlay2=root:memory", "--root=/r", "--ignore-cgroups", "--network=none"}
		Expect(strings.Join(got, " ")).To(Equal(strings.Join(want, " ")))
		for _, f := range got {
			Expect(f).ToNot(Equal("--rootless"), "forbidden flag")
			Expect(f).ToNot(HavePrefix("--overlay2=all"), "forbidden flag")
		}
	})
})

var _ = Describe("sandbox handle without an instance", func() {
	It("deletes the runsc sandbox after a failed start, even with a cancelled context", func() {
		r, calls := loggingRunsc(GinkgoT().TempDir())
		s := bootFixture(r)
		GinkgoT().Setenv("FAKE_RUNSC_RC", "1") // "run" fails after runsc forked the sandbox
		Expect(s.boot(context.Background())).ToNot(Succeed(), "boot succeeded")
		// delete (stale state), run, delete (cleanup after the failure).
		got := readLog(calls)
		Expect(got).To(HaveLen(3), "calls = %q", got)
		Expect(got[0]).To(HavePrefix("delete "))
		Expect(got[1]).To(HavePrefix("run "))
		Expect(got[2]).To(HavePrefix("delete "))
		// A cancelled context never starts runsc, but the cleanup still runs.
		Expect(os.Remove(calls)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(s.boot(ctx)).ToNot(Succeed(), "boot with a cancelled context succeeded")
		got = readLog(calls)
		Expect(got).To(HaveLen(1), "calls after a cancelled boot = %q", got)
		Expect(got[0]).To(HavePrefix("delete --root=" + s.runscRoot() + " " + s.id))
	})

	It("deletes the runsc sandbox on Destroy", func() {
		r, calls := loggingRunsc(GinkgoT().TempDir())
		s := bootFixture(r)
		Expect(s.Destroy(context.Background())).To(Succeed())
		got := readLog(calls)
		Expect(got).To(HaveLen(1), "calls = %q", got)
		Expect(got[0]).To(HavePrefix("delete --root=" + s.runscRoot() + " " + s.id))
		_, err := os.Stat(s.dir)
		Expect(os.IsNotExist(err)).To(BeTrue(), "the folder is still there: %v", err)
	})

	// Destroy waits for the folder lock without mu, so a call on the handle
	// returns ErrGone at once and does not queue behind a slow lock holder.
	It("waits for the folder lock in Destroy without holding mu", func() {
		r, _ := loggingRunsc(GinkgoT().TempDir())
		s := bootFixture(r)
		hold, err := lockFile(context.Background(), filepath.Join(s.dir, "lock"))
		Expect(err).ToNot(HaveOccurred())
		defer hold()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- s.Destroy(ctx) }()
		Eventually(func() bool {
			if s.mu.TryRLock() {
				gone := s.gone
				s.mu.RUnlock()
				return gone
			}
			return false
		}, 2*time.Second, 5*time.Millisecond).Should(BeTrue(), "Destroy holds mu while it waits for the folder lock")
		_, err = s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone), "Exec during Destroy")
		Expect(<-done).To(MatchError(context.DeadlineExceeded), "Destroy, want the deadline of its context")
	})

	// A guest agent that does not answer must not hold Suspend (and with it mu)
	// until the deadline of the caller.
	It("bounds the agent call", func() {
		r, _ := loggingRunsc(GinkgoT().TempDir())
		s := bootFixture(r)
		l, err := net.Listen("unix", s.agentSock())
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = l.Close() }()
		go func() {
			defer GinkgoRecover()
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				defer func() { _ = c.Close() }() // never answers
			}
		}()
		old := agentCallBudget
		agentCallBudget = 100 * time.Millisecond
		defer func() { agentCallBudget = old }()
		start := time.Now()
		err = s.call(context.Background(), "detach")
		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(time.Since(start)).To(BeNumerically("<=", 5*time.Second))
	})
})
