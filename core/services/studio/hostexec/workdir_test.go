package hostexec_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A chat's work folder has a space and a non-ASCII character in its name.
const workGuest = "/workspace/home/Chats/2026-10-18-café · Q1/work"

var _ = Describe("the work folder of a command on this machine", func() {
	// withWork makes the folder in the user's files and tells the runner about it.
	withWork := func(create *[]bool) func(*env, *hostexec.Options) {
		return func(e *env, o *hostexec.Options) {
			e.put("/Chats/2026-10-18-café · Q1/work/keep.txt", "k")
			o.WorkDir = func(_ context.Context, _ workspace.Actor, c bool) string {
				if create != nil {
					*create = append(*create, c)
				}
				return workGuest
			}
		}
	}

	It("starts in the folder and exports $WORK", func() {
		e := newEnv(withWork(nil))
		out := e.bash(`basename "$PWD"; ls "$WORK"; cd "$WORK" && basename "$PWD"; echo "$WORK" | grep -c 'Chats/2026-10-18-café · Q1/work$'`, 0)
		Expect(out).To(Equal("work\nkeep.txt\nwork\n1\n"))
	})

	It("asks for the folder to be made for a command, and not for a check that changes no file", func() {
		var asked []bool
		e := newEnv(withWork(&asked))
		e.bash("true", 0)
		_, err := e.r.RunReadOnly(context.Background(), e.a, []byte(`{"command":"true"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(asked).To(Equal([]bool{true, false}))
	})

	It("starts in the home with no $WORK when the chat has no folder", func() {
		e := newEnv()
		Expect(e.bash(`basename "$PWD"; echo "[${WORK-unset}]"`, 0)).To(Equal("home\n[unset]\n"))
	})

	It("starts a background job in the folder with $WORK", func() {
		e := newEnv(withWork(nil))
		in, err := e.r.StartJob(context.Background(), hostexec.JobSpec{Actor: e.a, Label: "t", Command: `basename "$PWD"; cd "$WORK" && ls`})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			o, err := e.r.Jobs().Observe(context.Background(), bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: in.ID})
			return err == nil && o.State.Finished()
		}, "20s", "50ms").Should(BeTrue())
		o, err := e.r.Jobs().Observe(context.Background(), bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: in.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(o.Output))).To(Equal("work\nkeep.txt"))
	})
})
