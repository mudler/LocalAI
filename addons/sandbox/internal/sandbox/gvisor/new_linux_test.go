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
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// fakeRunsc writes a shell script that answers like runsc. "--version" prints
// version; "run" prints runOut and exits with runRC; any other command exits 0.
func fakeRunsc(t *testing.T, dir, version, runOut string, runRC int, withBin bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in\n" +
		"    --version) echo '" + version + "'; exit 0;;\n" +
		"    run) echo '" + runOut + "'; exit " + strconv.Itoa(runRC) + ";;\n" +
		"  esac\n" +
		"done\n" +
		"exit 0\n"
	p := filepath.Join(dir, "runsc")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if withBin {
		if err := os.MkdirAll(filepath.Join(dir, "gvisor-bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func opts(t *testing.T, runsc string) Options {
	t.Helper()
	return Options{StateDir: shortDir(t), CacheDir: t.TempDir(), Runsc: runsc}
}

func TestNewReturnsErrUnsupportedForEachFailure(t *testing.T) {
	good := "runsc version release-20261012.0"
	cases := []struct {
		name  string
		setup func(t *testing.T) Options
		want  string // a part of the reason
	}{
		{"root", func(t *testing.T) Options {
			old := geteuid
			geteuid = func() int { return 0 }
			t.Cleanup(func() { geteuid = old })
			return opts(t, fakeRunsc(t, t.TempDir(), good, "", 0, true))
		}, "must not run as root"},
		{"runsc missing", func(t *testing.T) Options {
			return opts(t, filepath.Join(t.TempDir(), "no-runsc"))
		}, "runsc not found"},
		{"old release", func(t *testing.T) Options {
			return opts(t, fakeRunsc(t, t.TempDir(), "runsc version release-20260101.0", "", 0, true))
		}, "older than " + MinRelease},
		{"no release in the output", func(t *testing.T) Options {
			return opts(t, fakeRunsc(t, t.TempDir(), "something else", "", 0, true))
		}, "no release"},
		{"gvisor-bin missing", func(t *testing.T) Options {
			return opts(t, fakeRunsc(t, t.TempDir(), good, "", 0, false))
		}, "gvisor-bin"},
		{"AppArmor denies the user namespace", func(t *testing.T) Options {
			return opts(t, fakeRunsc(t, t.TempDir(), good, "creating container: cannot create gofer process: gofer: fork/exec /proc/self/exe: permission denied", 128, true))
		}, "gvisor.Profile"},
		{"runsc cannot start a sandbox", func(t *testing.T) Options {
			return opts(t, fakeRunsc(t, t.TempDir(), good, "boom", 1, true))
		}, "cannot start a sandbox"},
		{"state folder too long", func(t *testing.T) Options {
			o := opts(t, fakeRunsc(t, t.TempDir(), good, "", 0, true))
			o.StateDir = filepath.Join(o.StateDir, strings.Repeat("d", 80))
			return o
		}, "too long for socket paths"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.setup(t))
			if !errors.Is(err, sandbox.ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want a part %q", err, c.want)
			}
		})
	}
}

func TestNewAcceptsAGoodHost(t *testing.T) {
	o := opts(t, fakeRunsc(t, t.TempDir(), "runsc version release-20261012.0", "", 0, true))
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if b.runsc != o.Runsc || b.opts.ReadyTimeout == 0 {
		t.Fatalf("backend = %+v", b)
	}
	// The check sandbox leaves nothing in the state folder.
	if ents := sessionFolders(b.opts.StateDir); len(ents) != 0 {
		t.Fatalf("New left %d session folders in StateDir", len(ents))
	}
	// A later release is fine too.
	o2 := opts(t, fakeRunsc(t, t.TempDir(), "runsc version release-20270105.2", "", 0, true))
	if _, err := New(o2); err != nil {
		t.Fatal(err)
	}
}

func TestNewNeedsFoldersAndTaskset(t *testing.T) {
	if _, err := New(Options{}); err == nil || errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("empty options: %v", err)
	}
	r := fakeRunsc(t, t.TempDir(), "runsc version release-20261012.0", "", 0, true)
	t.Setenv("PATH", t.TempDir()) // no taskset
	if _, err := New(opts(t, r)); !errors.Is(err, sandbox.ErrUnsupported) || !strings.Contains(err.Error(), "taskset") {
		t.Fatalf("without taskset: %v", err)
	}
}

func TestParseReleaseAndCPUList(t *testing.T) {
	d, m, ok := parseRelease([]byte("runsc version release-20261012.0\nspec: 1.2.1"))
	if !ok || d != 20261012 || m != 0 {
		t.Fatalf("release = %d.%d %v", d, m, ok)
	}
	if !releaseAtLeast(20261012, 0) || !releaseAtLeast(20261012, 3) || !releaseAtLeast(20270101, 0) || releaseAtLeast(20261011, 9) {
		t.Fatal("releaseAtLeast")
	}
	if got := parseCPUList("0-2,5,7-8,x,9-3"); len(got) != 6 || got[0] != 0 || got[2] != 2 || got[3] != 5 || got[5] != 8 {
		t.Fatalf("cpus = %v", got)
	}
}

func TestPickCPUsSpreads(t *testing.T) {
	b := &Backend{cpus: []int{4, 5, 6, 7}}
	if a, c := b.pickCPUs(2), b.pickCPUs(2); a != "4,5" || c != "6,7" {
		t.Fatalf("picks = %q %q", a, c)
	}
	if got := b.pickCPUs(2); got != "4,5" { // wraps
		t.Fatalf("wrap = %q", got)
	}
	if got := b.pickCPUs(9); strings.Count(got, ",") != 3 { // never more than the host has
		t.Fatalf("pick of 9 = %q", got)
	}
}

func TestNewCreatesNoFolderWhenACheckFails(t *testing.T) {
	good := "runsc version release-20261012.0"
	root := shortDir(t)
	for name, r := range map[string]string{
		"missing":      filepath.Join(root, "no-runsc"),
		"old-release":  fakeRunsc(t, filepath.Join(root, "old"), "runsc version release-20260101.0", "", 0, true),
		"no-helpers":   fakeRunsc(t, filepath.Join(root, "nohelp"), good, "", 0, false),
		"cannot-start": fakeRunsc(t, filepath.Join(root, "bad"), good, "boom", 1, true),
		"denied":       fakeRunsc(t, filepath.Join(root, "denied"), good, "permission denied", 128, true),
	} {
		t.Run(name, func(t *testing.T) {
			o := Options{StateDir: filepath.Join(root, "state-"+name), CacheDir: filepath.Join(root, "cache-"+name), Runsc: r}
			if _, err := New(o); !errors.Is(err, sandbox.ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
			for _, d := range []string{o.StateDir, o.CacheDir} {
				if _, err := os.Stat(d); !os.IsNotExist(err) {
					t.Errorf("%s exists after a failed check: %v", d, err)
				}
			}
		})
	}
}

func TestNewRefusesARunscPathAProfileCannotName(t *testing.T) {
	r := fakeRunsc(t, filepath.Join(t.TempDir(), "a{b"), "runsc version release-20261012.0", "", 0, true)
	_, err := New(opts(t, r))
	if !errors.Is(err, sandbox.ErrUnsupported) || !strings.Contains(err.Error(), "cannot name safely") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewFollowsASymlinkToRunsc(t *testing.T) {
	real := fakeRunsc(t, filepath.Join(t.TempDir(), "real"), "runsc version release-20261012.0", "", 0, true)
	link := filepath.Join(t.TempDir(), "runsc")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	b, err := New(opts(t, link))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(real)
	if b.runsc != want {
		t.Fatalf("runsc = %q, want %q", b.runsc, want)
	}
}

func TestNewNeedsACPUList(t *testing.T) {
	old := allowedCPUs
	allowedCPUs = func() []int { return nil }
	t.Cleanup(func() { allowedCPUs = old })
	r := fakeRunsc(t, t.TempDir(), "runsc version release-20261012.0", "", 0, true)
	_, err := New(opts(t, r))
	if !errors.Is(err, sandbox.ErrUnsupported) || !strings.Contains(err.Error(), "CPU list") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidIDAndEmptyCall(t *testing.T) {
	for _, bad := range []string{"", "-h", "--version", "check", "0123456789AB", "0123456789abc"} {
		if validID(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := validID("0123456789ab"); err != nil {
		t.Fatal(err)
	}
	b := &Backend{runsc: "/bin/true"}
	ctx := context.Background()
	if _, err := b.start(ctx, t.TempDir(), "--x", 1, "run"); err == nil {
		t.Error("start accepted a flag as id")
	}
	if _, err := b.usage(ctx, "/r", "--x"); err == nil {
		t.Error("usage accepted a flag as id")
	}
	if err := b.deleteForce(ctx, "/r", "--x"); err == nil {
		t.Error("deleteForce accepted a flag as id")
	}
	if _, err := b.call(ctx, "/r"); err == nil {
		t.Error("call accepted no command")
	}
}

func TestFailedProbeDeletesTheProbeSandbox(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nfor a in \"$@\"; do\n case \"$a\" in\n --version) echo 'runsc version release-20261012.0'; exit 0;;\n run) echo boom; echo run >> " + logf + "; exit 1;;\n delete) echo delete >> " + logf + "; exit 0;;\n esac\ndone\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "runsc"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "gvisor-bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(opts(t, filepath.Join(dir, "runsc"))); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(logf)
	if string(got) != "run\ndelete\n" {
		t.Fatalf("calls = %q", got)
	}
}

func TestGlobalFlags(t *testing.T) {
	got := globalFlags("/r")
	want := []string{"--directfs=false", "--host-uds=all", "--overlay2=root:memory", "--root=/r", "--ignore-cgroups", "--network=none"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("flags = %v, want %v", got, want)
	}
	for _, f := range got {
		if f == "--rootless" || strings.HasPrefix(f, "--overlay2=all") {
			t.Errorf("forbidden flag %s", f)
		}
	}
}

// loggingRunsc is a fake runsc that answers like a healthy one and appends
// every "delete" call to calls.log as "delete <root flag> <last argument>".
func loggingRunsc(t *testing.T, dir string) (runsc, logPath string) {
	t.Helper()
	logPath = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nroot=\nverb=\nfor a in \"$@\"; do\n case \"$a\" in\n --version) echo 'runsc version release-20261012.0'; exit 0;;\n --root=*) root=\"$a\";;\n run|delete|restore) [ -z \"$verb\" ] && verb=\"$a\";;\n esac\n last=\"$a\"\ndone\n" +
		"echo \"$verb $root $last\" >> " + logPath + "\nexit ${FAKE_RUNSC_RC:-0}\n"
	if err := os.MkdirAll(filepath.Join(dir, "gvisor-bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	runsc = filepath.Join(dir, "runsc")
	if err := os.WriteFile(runsc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return runsc, logPath
}

func readLog(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestNewTakesAnExclusiveLockOnStateDir(t *testing.T) {
	r := fakeRunsc(t, t.TempDir(), "runsc version release-20261012.0", "", 0, true)
	o := opts(t, r)
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	// A second backend on the same folder, in this process, is refused with a
	// plain error that says why.
	o2 := o
	o2.CacheDir = t.TempDir()
	_, err = New(o2)
	if err == nil || errors.Is(err, sandbox.ErrUnsupported) || !strings.Contains(err.Error(), "in use by another backend") {
		t.Fatalf("second New: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	b2, err := New(o2)
	if err != nil {
		t.Fatalf("New after Close: %v", err)
	}
	b2.Close()
}

func TestNewRefusesANegativeStageLimit(t *testing.T) {
	o := opts(t, fakeRunsc(t, t.TempDir(), "runsc version release-20261012.0", "", 0, true))
	o.StageLimit = -1
	if _, err := New(o); err == nil || !strings.Contains(err.Error(), "StageLimit") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewStopsTheSandboxesOfAnEarlierProcess(t *testing.T) {
	r, calls := loggingRunsc(t, t.TempDir())
	o := opts(t, r)
	mk := func(id string, st *state) string {
		d := filepath.Join(o.StateDir, id)
		if err := os.MkdirAll(filepath.Join(d, "runsc"), 0o700); err != nil {
			t.Fatal(err)
		}
		if st != nil {
			if err := st.save(d); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	running := mk("aaaaaaaaaaaa", &state{})
	noState := mk("cccccccccccc", nil)
	corrupt := mk("dddddddddddd", nil)
	if err := os.WriteFile(filepath.Join(corrupt, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	suspended := mk("bbbbbbbbbbbb", &state{Suspended: true})
	if err := os.Mkdir(filepath.Join(suspended, "checkpoint"), 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(o.StateDir, "not-a-session")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	for _, d := range []string{running, noState, corrupt} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", filepath.Base(d), err)
		}
	}
	for _, d := range []string{suspended, filepath.Join(suspended, "checkpoint"), other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(d), err)
		}
	}
	got := strings.Join(readLog(t, calls), "\n")
	for _, d := range []string{running, noState, corrupt} {
		want := "delete --root=" + filepath.Join(d, "runsc") + " " + filepath.Base(d)
		if !strings.Contains(got, want) {
			t.Errorf("no %q in the calls:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bbbbbbbbbbbb") {
		t.Errorf("the suspended sandbox was deleted:\n%s", got)
	}
}

func TestNewKeepsAFolderWhoseSandboxCannotBeStopped(t *testing.T) {
	r, _ := loggingRunsc(t, t.TempDir())
	o := opts(t, r)
	d := filepath.Join(o.StateDir, "aaaaaaaaaaaa")
	if err := os.MkdirAll(filepath.Join(d, "runsc"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The probe of New runs "run", and the delete after it: both succeed, so
	// make the delete of the old folder fail only.
	script := "#!/bin/sh\ncase \"$*\" in\n *--version*) echo 'runsc version release-20261012.0'; exit 0;;\n *aaaaaaaaaaaa*) echo cannot; exit 1;;\nesac\nexit 0\n"
	if err := os.WriteFile(r, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := os.Stat(d); err != nil {
		t.Fatalf("the folder was removed although its sandbox may still run: %v", err)
	}
}

// bootFixture is a sandbox handle that has never been created by Create, on a
// fake runsc, for the paths that need no image.
func bootFixture(t *testing.T, r string) *sbx {
	t.Helper()
	cpus := allowedCPUs()
	taskset, err := exec.LookPath("taskset")
	if err != nil || len(cpus) == 0 {
		t.Skip("needs taskset")
	}
	b := &Backend{opts: Options{StateDir: shortDir(t)}, runsc: r, taskset: taskset, cpus: cpus, aliveEvery: time.Hour, memEvery: time.Hour, diskEvery: time.Hour}
	dir := filepath.Join(b.opts.StateDir, "eeeeeeeeeeee")
	for _, d := range []string{"upper", "ctl", "runsc"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &sbx{b: b, id: "eeeeeeeeeeee", dir: dir, st: state{CPUs: 1, Memory: 1 << 30, Disk: 1 << 30}}
}

func TestAFailedStartDeletesTheRunscSandboxEvenWithACancelledContext(t *testing.T) {
	r, calls := loggingRunsc(t, t.TempDir())
	s := bootFixture(t, r)
	t.Setenv("FAKE_RUNSC_RC", "1") // "run" fails after runsc forked the sandbox
	if err := s.boot(context.Background()); err == nil {
		t.Fatal("boot succeeded")
	}
	// delete (stale state), run, delete (cleanup after the failure).
	got := readLog(t, calls)
	if len(got) != 3 || !strings.HasPrefix(got[0], "delete ") || !strings.HasPrefix(got[1], "run ") || !strings.HasPrefix(got[2], "delete ") {
		t.Fatalf("calls = %q", got)
	}
	// A cancelled context never starts runsc, but the cleanup still runs.
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.boot(ctx); err == nil {
		t.Fatal("boot with a cancelled context succeeded")
	}
	got = readLog(t, calls)
	if len(got) != 1 || !strings.HasPrefix(got[0], "delete --root="+s.runscRoot()+" "+s.id) {
		t.Fatalf("calls after a cancelled boot = %q", got)
	}
}

func TestDestroyOfAHandleWithoutAnInstanceDeletesTheRunscSandbox(t *testing.T) {
	r, calls := loggingRunsc(t, t.TempDir())
	s := bootFixture(t, r)
	if err := s.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, calls)
	if len(got) != 1 || !strings.HasPrefix(got[0], "delete --root="+s.runscRoot()+" "+s.id) {
		t.Fatalf("calls = %q", got)
	}
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatalf("the folder is still there: %v", err)
	}
}

func TestTheProbeLiesBesideStateDirAndNamesItInTheError(t *testing.T) {
	root := shortDir(t)
	r := fakeRunsc(t, filepath.Join(root, "bin"), "runsc version release-20261012.0", "boom", 1, true)
	o := Options{StateDir: filepath.Join(root, "state"), CacheDir: filepath.Join(root, "cache"), Runsc: r}
	_, err := New(o)
	if err == nil || !strings.Contains(err.Error(), "check folder "+root+"/.gvcheck-") {
		t.Fatalf("err = %v", err)
	}
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".gvcheck-") {
			t.Errorf("the probe folder %s was left", e.Name())
		}
	}
}

// Destroy waits for the folder lock without mu, so a call on the handle
// returns ErrGone at once and does not queue behind a slow lock holder.
func TestDestroyWaitsForTheFolderLockWithoutHoldingMu(t *testing.T) {
	r, _ := loggingRunsc(t, t.TempDir())
	s := bootFixture(t, r)
	hold, err := lockFile(context.Background(), filepath.Join(s.dir, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Destroy(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if s.mu.TryRLock() {
			gone := s.gone
			s.mu.RUnlock()
			if gone {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("Destroy holds mu while it waits for the folder lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}}); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("Exec during Destroy: %v", err)
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Destroy = %v, want the deadline of its context", err)
	}
}

// A guest agent that does not answer must not hold Suspend (and with it mu)
// until the deadline of the caller.
func TestAgentCallIsBounded(t *testing.T) {
	r, _ := loggingRunsc(t, t.TempDir())
	s := bootFixture(t, r)
	l, err := net.Listen("unix", s.agentSock())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close() // never answers
		}
	}()
	old := agentCallBudget
	agentCallBudget = 100 * time.Millisecond
	defer func() { agentCallBudget = old }()
	start := time.Now()
	err = s.call(context.Background(), "detach")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("call = %v after %s", err, time.Since(start))
	}
}
