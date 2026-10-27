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
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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

func testCache(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("SANDBOX_TEST_CACHE")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "sandbox-chv-test-cache")
	}
	cacheOnce.Do(func() { _ = os.MkdirAll(dir, 0o700) })
	return dir
}

func testBackend(t *testing.T) *Backend {
	t.Helper()
	img := os.Getenv("SANDBOX_TEST_IMAGE")
	if img == "" {
		t.Skip("set SANDBOX_TEST_IMAGE to the guest image (name@sha256:... or oci-layout:<dir>@sha256:...)")
	}
	b, err := New(Options{StateDir: shortDir(t), CacheDir: testCache(t), DefaultImage: img, NoBwrap: os.Getenv("SANDBOX_NO_BWRAP") == "1"})
	if errors.Is(err, sandbox.ErrUnsupported) {
		t.Skipf("this host cannot run the backend: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBackendSuite(t *testing.T) {
	sbtest.RunBackendSuite(t, func(t *testing.T) sandbox.Backend { return testBackend(t) })
}

// fixture creates a sandbox on a store with /proj.
func fixture(t *testing.T, b *Backend, image string) (sandbox.Sandbox, workspace.Store) {
	t.Helper()
	ctx := context.Background()
	st := sbtest.OpenStore(t)
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Image: image, Memory: 1 << 30, Disk: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sb.Destroy(context.Background()) })
	return sb, st
}

func sh(t *testing.T, sb sandbox.Sandbox, script string) string {
	t.Helper()
	var out, errb bytes.Buffer
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out, Stderr: &errb})
	if err != nil {
		t.Fatal(err)
	}
	if code, err := p.Wait(); err != nil || code != 0 {
		t.Fatalf("%q: %d %v\n%s%s", script, code, err, out.String(), errb.String())
	}
	return out.String()
}

func TestGuestImagePython(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	sh(t, sb, `python3 -c "import docx, openpyxl, pptx, pypdf, pandas, PIL, reportlab, pdfplumber, pdf2image"`)
	sh(t, sb, `pdftoppm -v`)
}

func TestConvertedImageBootsWithFileKinds(t *testing.T) {
	b := testBackend(t)
	_, load, err := resolve(context.Background(), b.opts.DefaultImage)
	if err != nil {
		t.Fatal(err)
	}
	img, err := load()
	if err != nil {
		t.Fatal(err)
	}
	img, err = mutate.AppendLayers(img, attrLayer(t, false))
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := fixture(t, b, writeLayout(t, img))
	got := sh(t, sb, `cd /opt/attrs
stat -c '%n %a %u:%g %F' suid fifo
readlink link
[ "$(stat -c %i suid)" = "$(stat -c %i hard)" ] && echo hard-link
stat -c '%n %t:%T %F' null
python3 -c "import os; print(os.getxattr('xattr', 'user.demo').decode())"`)
	want := "suid 4755 0:0 regular file\nfifo 600 1000:1000 fifo\nsuid\nhard-link\nnull 1:3 character special file\nhello\n"
	if got != want {
		t.Fatalf("guest sees:\n%s\nwant:\n%s", got, want)
	}
}

func TestTwoSandboxesShareOneImage(t *testing.T) {
	b := testBackend(t)
	sb1, _ := fixture(t, b, "")
	sb2, _ := fixture(t, b, "")
	if a, c := strings.TrimSpace(sh(t, sb1, "echo one")), strings.TrimSpace(sh(t, sb2, "echo two")); a != "one" || c != "two" {
		t.Fatalf("got %q %q", a, c)
	}
}

func TestReadyTimeoutCleansUp(t *testing.T) {
	b := testBackend(t)
	b.opts.ReadyTimeout = time.Millisecond
	st := sbtest.OpenStore(t)
	ctx := context.Background()
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	_, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 1 << 30})
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("create = %v", err)
	}
	if ents, _ := os.ReadDir(b.opts.StateDir); len(ents) != 0 {
		t.Fatalf("a failed create left %d session folders", len(ents))
	}
}

func TestFullUpperFailsOnlyInTheGuest(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	st := sbtest.OpenStore(t)
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, _ := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Destroy(ctx)
	var errb bytes.Buffer
	p, err := sb.Exec(ctx, sandbox.Cmd{Args: []string{"sh", "-c", "head -c 200000000 /dev/zero > proj/big.bin"}, Stderr: &errb})
	if err != nil {
		t.Fatal(err)
	}
	if code, err := p.Wait(); err != nil || code == 0 || !strings.Contains(errb.String(), "No space left") {
		t.Fatalf("a write past the disk size: %d %v %q", code, err, errb.String())
	}
	if got := strings.TrimSpace(sh(t, sb, "rm proj/big.bin && echo still-running")); got != "still-running" {
		t.Fatalf("after the full disk: %q", got)
	}
}

func TestCrashGivesErrGoneAndCheckpointStillWorks(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, "")
	sh(t, sb, "echo kept > proj/kept.txt && sync")
	s := sb.(*sbx)
	_ = s.vm.ch.Process.Kill()
	<-s.vm.done
	if _, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}}); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("exec after a crash: %v", err)
	}
	if _, _, err := sb.Checkpoint(context.Background(), "after crash"); err != nil {
		t.Fatal(err)
	}
	r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/kept.txt")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

// TestBusy holds the checkpoint lock, as a running checkpoint or refresh
// does, so the second call always finds it taken.
func TestBusy(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	s := sb.(*sbx)
	if !s.op.TryLock() {
		t.Fatal("the checkpoint lock was already taken")
	}
	defer s.op.Unlock()
	if _, _, err := sb.Checkpoint(context.Background(), "x"); !errors.Is(err, sandbox.ErrBusy) {
		t.Fatalf("checkpoint under the lock: %v", err)
	}
	if err := sb.Refresh(context.Background()); !errors.Is(err, sandbox.ErrBusy) {
		t.Fatalf("refresh under the lock: %v", err)
	}
}

// TestRefusedNamesAreConflicts: a name the workspace refuses is a bad-name
// conflict of its entry. It does not fail the checkpoint.
func TestRefusedNamesAreConflicts(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	sh(t, sb, `cd proj && printf x > "$(printf 'new\nline')" && printf y > "$(printf 'bad\377name')" && sync`)
	_, conflicts, err := sb.Checkpoint(context.Background(), "names")
	if err != nil {
		t.Fatal(err)
	}
	bad := 0
	for _, c := range conflicts {
		if c.Reason == workspace.ReasonBadName {
			bad++
		}
	}
	if bad != 2 {
		t.Fatalf("bad-name conflicts = %d, want 2: %+v", bad, conflicts)
	}
}

// TestMarkGoneIgnoresAStaleVM: after a refresh the console watcher of the old
// VM must not mark the new VM gone.
func TestMarkGoneIgnoresAStaleVM(t *testing.T) {
	old, cur := &vm{done: make(chan struct{})}, &vm{done: make(chan struct{})}
	s := &sbx{vm: cur}
	s.markGone(old)
	if s.gone {
		t.Fatal("a stale watcher marked the new VM gone")
	}
	s.markGone(cur)
	if !s.gone {
		t.Fatal("the current VM's panic did not mark the sandbox gone")
	}
}

// A slow checkpoint must not let the guest thaw itself under a refresh: the
// refresh then does not happen, the guest keeps running and the commit stays.
func TestRefreshAfterSlowCheckpointLeavesSandboxRunning(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, "")
	s := sb.(*sbx)
	clock := time.Now()
	s.hooks.now = func() time.Time { return clock }
	s.hooks.afterCommit = func() { clock = clock.Add(50 * time.Second) }
	sh(t, sb, "echo slow > proj/slow.txt && sync")
	err := sb.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("refresh after a slow checkpoint: %v", err)
	}
	if errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("a refresh that did not happen marked the sandbox gone: %v", err)
	}
	s.hooks.afterCommit = nil
	sh(t, sb, "echo more >> proj/slow.txt") // a write hangs if the guest is still frozen
	if got := sh(t, sb, "cat proj/slow.txt"); got != "slow\nmore\n" {
		t.Fatalf("after the refresh that did not happen: %q", got)
	}
	r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/slow.txt")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

// A freeze call that fails may still have frozen the guest (a lost reply):
// the checkpoint must send the thaw anyway.
func TestAmbiguousFreezeIsThawed(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
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
	if _, _, err := sb.Checkpoint(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "lost reply") {
		t.Fatalf("checkpoint with a lost freeze reply: %v", err)
	}
	if thaws != 1 {
		t.Fatalf("thaw requests after an ambiguous freeze = %d, want 1", thaws)
	}
	s.hooks.call = nil
	sh(t, sb, "echo ok > proj/after.txt") // a write hangs if the guest is still frozen
}

// Stale folders of an earlier refresh must not block the next refresh.
func TestRefreshRemovesStaleOldFiles(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	s := sb.(*sbx)
	if err := os.MkdirAll(filepath.Join(s.dir, "lower.old", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "upper.old.ext4"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sb.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh with stale old files: %v", err)
	}
	for _, p := range []string{"lower.old", "upper.old.ext4"} {
		if _, err := os.Stat(filepath.Join(s.dir, p)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the refresh: %v", p, err)
		}
	}
	sh(t, sb, "true")
}

// failingMaterialize is a store whose Materialize fails.
type failingMaterialize struct{ workspace.Store }

func (failingMaterialize) Materialize(context.Context, workspace.View, string) (int64, error) {
	return 0, errors.New("materialize boom")
}

// A new lower that cannot be built boots the old lower and upper again.
func TestRefreshRevivesAfterMaterializeFailure(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, "")
	s := sb.(*sbx)
	sh(t, sb, "echo kept > proj/kept.txt && sync")
	real := s.env.Store
	s.env.Store = failingMaterialize{real}
	err := sb.Refresh(context.Background())
	s.env.Store = real
	if err == nil || !strings.Contains(err.Error(), "materialize boom") || errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("refresh with a failing lower: %v", err)
	}
	if got := sh(t, sb, "cat proj/kept.txt"); got != "kept\n" {
		t.Fatalf("after the failed refresh: %q", got)
	}
	if _, _, err := sb.Checkpoint(context.Background(), "after"); err != nil {
		t.Fatal(err)
	}
	r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/kept.txt")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

// A boot that fails after the swap puts the old files and state back and
// marks the sandbox gone.
func TestRefreshRollsBackAfterSwap(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	s := sb.(*sbx)
	sh(t, sb, "echo before > proj/before.txt && sync")
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
	if !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("refresh with a failing new guest: %v", err)
	}
	for _, p := range []string{"lower", "upper.ext4"} {
		if _, err := os.Stat(filepath.Join(s.dir, p)); err != nil {
			t.Fatalf("%s was not restored: %v", p, err)
		}
	}
	for _, p := range []string{"lower.old", "upper.old.ext4", "lower.new", "upper.new.ext4"} {
		if _, err := os.Stat(filepath.Join(s.dir, p)); !os.IsNotExist(err) {
			t.Fatalf("%s is left after the rollback: %v", p, err)
		}
	}
	if st, err := loadState(s.dir); err != nil || st.Base != oldBase {
		t.Fatalf("saved state after the rollback: base %d (want %d), %v", st.Base, oldBase, err)
	}
	if s.st.Base != oldBase {
		t.Fatalf("in-memory base after the rollback: %d, want %d", s.st.Base, oldBase)
	}
	if _, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}}); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("exec after the rollback: %v", err)
	}
	// The restored upper still holds the content the guest wrote.
	stage := filepath.Join(s.dir, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	argv, inStage := b.debugfsArgv(s.dir)
	src, _, err := upperReader{argv: argv, stage: inStage, host: stage, lim: readLimits{stage: 1 << 30}}.read(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range src {
		found = found || e.Path == "proj/before.txt"
	}
	if !found {
		t.Fatalf("the restored upper lost proj/before.txt: %+v", src)
	}
}

// A thaw that fails after a good commit still returns the change set, with
// an error that wraps the thaw failure.
func TestThawErrorJoinsTheChangeSet(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, "")
	s := sb.(*sbx)
	sh(t, sb, "echo change > proj/change.txt")
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
	if !errors.Is(err, errThaw) {
		t.Fatalf("checkpoint with a failing thaw: %v", err)
	}
	if cs.Rev == 0 || len(conflicts) != 0 {
		t.Fatalf("change set = %+v, conflicts = %+v", cs, conflicts)
	}
	s.hooks.call = nil
	r, _, err := st.Open(context.Background(), sbtest.Admin, "/proj/change.txt")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	sh(t, sb, "echo more >> proj/change.txt")
}

// If the rename back fails, the files are mixed: the sandbox is gone and
// must not be booted again.
func TestFailedUndoDoesNotRevive(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, "")
	s := sb.(*sbx)
	sh(t, sb, "echo x > proj/x.txt && sync")
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
	if !errors.Is(err, sandbox.ErrGone) || !strings.Contains(err.Error(), "rename boom") || !strings.Contains(err.Error(), "undo boom") {
		t.Fatalf("refresh with a failed undo: %v", err)
	}
	if boots != 0 {
		t.Fatalf("the sandbox was revived %d times after a failed undo", boots)
	}
}

// A checkpoint whose read outlives the guest's thaw commits nothing, changes
// no state, and the next checkpoint commits normally.
func TestSlowReadIsNotCommitted(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, "")
	s := sb.(*sbx)
	sh(t, sb, "echo late > proj/late.txt && sync")
	clock := time.Now()
	s.hooks.now = func() time.Time { return clock }
	s.hooks.afterRead = func() { clock = clock.Add(50 * time.Second) }
	stateBefore, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sb.Checkpoint(context.Background(), "slow"); !errors.Is(err, errFreezeOverrun) {
		t.Fatalf("checkpoint with a slow read: %v", err)
	}
	if _, err := st.Stat(context.Background(), sbtest.Admin, "/proj/late.txt"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("a torn read was committed: %v", err)
	}
	stateAfter, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatalf("state changed by a checkpoint that did not commit: %v", err)
	}
	s.hooks.afterRead = nil
	if cs, _, err := sb.Checkpoint(context.Background(), "normal"); err != nil || cs.Rev == 0 {
		t.Fatalf("next checkpoint: %+v, %v", cs, err)
	}
	if _, err := st.Stat(context.Background(), sbtest.Admin, "/proj/late.txt"); err != nil {
		t.Fatal(err)
	}
	sh(t, sb, "echo more >> proj/late.txt") // a write hangs if the guest is still frozen
}
