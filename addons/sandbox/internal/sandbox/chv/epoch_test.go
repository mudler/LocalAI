//go:build linux

package chv

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sandbox ownership epoch", func() {
	// It checks the ownership epoch: after a Resume through another handle,
	// the handle that Suspend released neither checkpoints nor writes
	// state.json.
	It("keeps a released handle from checkpointing", func() {
		b := testBackend()
		ctx := context.Background()
		sb1, st := fixture(b, "")
		ref := suspendForTest(sb1)
		sb2, err := b.Resume(ctx, ref, resumeEnvFor(st))
		Expect(err).To(Succeed())
		DeferCleanup(func() { _ = sb2.Destroy(context.Background()) })
		sh(sb2, "echo two > /workspace/proj/two.txt")
		statePath := filepath.Join(ref.Path, "state.json")
		before, err := os.ReadFile(statePath)
		Expect(err).To(Succeed())
		_, _, err = sb1.Checkpoint(ctx, "stale")
		Expect(err).To(MatchError(sandbox.ErrGone), "checkpoint of the released handle")
		Expect(sb1.Refresh(ctx)).To(MatchError(sandbox.ErrGone), "refresh of the released handle")
		after, err := os.ReadFile(statePath)
		Expect(err).To(Succeed())
		Expect(string(after)).To(Equal(string(before)), "state.json changed")
		// The live handle still commits its own work.
		_, _, err = sb2.Checkpoint(ctx, "live")
		Expect(err).To(Succeed(), "checkpoint of the live handle")
	})

	// It checks that a late Destroy of an old handle leaves both a resumed
	// sandbox and a later snapshot of the folder.
	It("keeps a later snapshot when a stale handle is destroyed", func() {
		b := testBackend()
		ctx := context.Background()
		sb1, st := fixture(b, "")
		ref := suspendForTest(sb1)
		sb2, err := b.Resume(ctx, ref, resumeEnvFor(st))
		Expect(err).To(Succeed())
		DeferCleanup(func() { _ = sb2.Destroy(context.Background()) })
		Expect(sb1.Destroy(ctx)).To(Succeed(), "destroy of the stale handle")
		sh(sb2, "echo alive")
		ref2 := suspendForTest(sb2)
		Expect(sb1.Destroy(ctx)).To(Succeed(), "second destroy of the stale handle")
		_, err = os.Stat(filepath.Join(ref2.Path, "snapshot"))
		Expect(err).To(Succeed(), "the stale handle deleted the later snapshot")
		sb3, err := b.Resume(ctx, ref2, resumeEnvFor(st))
		Expect(err).To(Succeed(), "resume of the later snapshot")
		DeferCleanup(func() { _ = sb3.Destroy(context.Background()) })
		sh(sb3, "echo alive again")
	})
})
