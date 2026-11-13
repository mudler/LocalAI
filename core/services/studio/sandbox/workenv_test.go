package sandbox_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

var _ = Describe("the work folder of a shell", func() {
	const home = "/workspace/home"

	DescribeTable("WorkStart",
		func(work, wantDir string, wantEnv []string) {
			dir, env := sandbox.WorkStart(work, home)
			Expect(dir).To(Equal(wantDir))
			Expect(env).To(Equal(wantEnv))
		},
		Entry("a folder with a space and a non-ASCII name", "/workspace/home/Chats/2026-10-18 Café · Q1/work",
			"/workspace/home/Chats/2026-10-18 Café · Q1/work", []string{"WORK=/workspace/home/Chats/2026-10-18 Café · Q1/work"}),
		Entry("a trailing slash is dropped", "/workspace/chat/work/", "/workspace/chat/work", []string{"WORK=/workspace/chat/work"}),
		Entry("no folder yet", "", home, nil),
		Entry("outside the guest root", "/etc", home, nil),
		Entry("the guest root itself", "/workspace", home, nil),
		Entry("a climb out of the root", "/workspace/../etc", home, nil),
		Entry("a control character", "/workspace/home/a\nb", home, nil),
	)

	It("maps $WORK to a folder below the root for a backend that runs on the host", func() {
		in := []string{"A=1", "WORK=/workspace/home/Chats/x y/work", "B=2"}
		out := sandbox.MapWorkEnv(in, "/data/sb-1/work/")
		Expect(out).To(Equal([]string{"A=1", "WORK=/data/sb-1/work/home/Chats/x y/work", "B=2"}))
		Expect(in[1]).To(Equal("WORK=/workspace/home/Chats/x y/work"), "the input changed")
		Expect(sandbox.MapWorkEnv(nil, "/r")).To(BeEmpty())
		Expect(sandbox.MapWorkEnv([]string{"WORK=/elsewhere"}, "/r")).To(Equal([]string{"WORK=/elsewhere"}))
	})
})
