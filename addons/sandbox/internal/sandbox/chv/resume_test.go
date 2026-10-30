//go:build linux

package chv

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func suspendForTest(sb sandbox.Sandbox) sandbox.SnapshotRef {
	GinkgoHelper()
	ref, err := sb.Suspend(context.Background())
	Expect(err).To(Succeed())
	return ref
}

func resumeEnvFor(st workspace.Store) sandbox.Env {
	return sandbox.Env{Store: st, Actor: sbtest.Admin}
}

func nopPort(_ context.Context, c net.Conn) { c.Close() }

// procsIn lists the host processes whose command line names dir.
func procsIn(dir string) []string {
	GinkgoHelper()
	paths, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	var out []string
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(dir)) {
			out = append(out, p)
		}
	}
	return out
}

var _ = Describe("resume", func() {
	It("refuses another actor, another port set and a live sandbox", func() {
		b := testBackend()
		sb, st := fixture(b, "")
		ref := suspendForTest(sb)
		ctx := context.Background()
		_, err := b.Resume(ctx, ref, sandbox.Env{Store: st, Actor: workspace.Actor{Principal: "user:other"}})
		Expect(err).To(MatchError(workspace.ErrInvalid), "other actor")
		_, err = b.Resume(ctx, ref, sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: map[int]sandbox.PortHandler{7000: nopPort}})
		Expect(err).To(MatchError(workspace.ErrInvalid), "other port set")
		live, err := b.Resume(ctx, ref, resumeEnvFor(st))
		Expect(err).To(Succeed())
		DeferCleanup(func() { live.Destroy(context.Background()) })
		_, err = b.Resume(ctx, ref, resumeEnvFor(st))
		Expect(err).To(MatchError(workspace.ErrInvalid), "resume while live")
		sh(live, "echo alive")
		_, err = os.Stat(filepath.Join(ref.Path, "console.prev.log"))
		Expect(err).To(Succeed(), "the old console log was not kept")
	})

	It("runs a concurrent resume of one ref once", func() {
		b := testBackend()
		sb, st := fixture(b, "")
		ref := suspendForTest(sb)
		ctx := context.Background()
		type result struct {
			sb  sandbox.Sandbox
			err error
		}
		out := make([]result, 2)
		var wg sync.WaitGroup
		for i := range out {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i].sb, out[i].err = b.Resume(ctx, ref, resumeEnvFor(st))
			}()
		}
		wg.Wait()
		var live sandbox.Sandbox
		ok := 0
		for _, r := range out {
			if r.err == nil {
				ok++
				live = r.sb
				DeferCleanup(func() { r.sb.Destroy(context.Background()) })
			} else {
				Expect(r.err).To(MatchError(workspace.ErrInvalid), "a refused resume failed")
			}
		}
		Expect(ok).To(Equal(1), "resumes succeeded, want exactly 1")
		sh(live, "echo alive")
	})

	It("keeps a resumed sandbox when the suspended handle is destroyed", func() {
		b := testBackend()
		old, st := fixture(b, "")
		ref := suspendForTest(old)
		ctx := context.Background()
		live, err := b.Resume(ctx, ref, resumeEnvFor(st))
		Expect(err).To(Succeed())
		DeferCleanup(func() { live.Destroy(context.Background()) })
		Expect(old.Destroy(ctx)).To(Succeed(), "destroy of the stale handle")
		sh(live, "echo alive")
		_, err = os.Stat(ref.Path)
		Expect(err).To(Succeed(), "the stale handle deleted the resumed sandbox")
		Expect(live.Destroy(ctx)).To(Succeed())
		_, err = os.Stat(ref.Path)
		Expect(err).To(MatchError(fs.ErrNotExist), "the folder is still there after destroy")
		Expect(procsIn(ref.Path)).To(BeEmpty(), "processes left after destroy")
	})

	It("keeps the sandbox running when the suspend save fails", func() {
		b := testBackend()
		sb, _ := fixture(b, "")
		s := sb.(*sbx)
		s.hooks.suspendSave = func() error { return errors.New("injected save failure") }
		ref, err := sb.Suspend(context.Background())
		Expect(err).To(HaveOccurred(), "suspend with a failed save succeeded")
		Expect(ref).To(Equal(sandbox.SnapshotRef{}), "a failed suspend returned a ref")
		s.hooks.suspendSave = nil
		sh(sb, "echo still running")
		suspendForTest(sb)
	})
})
