package session_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func openChat(h *sessiontest.FakeHarness, mutate func(*session.Options)) {
	o := session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: GinkgoT().TempDir()}
	if mutate != nil {
		mutate(&o)
	}
	m, err := session.New(o)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = m.Close(context.Background()) })
	_, err = m.Open(context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("the host environment of a harness", func() {
	// The harness needs to know whose chat it serves (to pick the model client and
	// the user) and gets the host's source of pending messages.
	It("gets the actor and the input source", func() {
		h := &sessiontest.FakeHarness{}
		asked := 0
		openChat(h, func(o *session.Options) {
			o.Inputs = func(a workspace.Actor) func() ([]string, func(error)) {
				asked++
				Expect(a.Principal).To(Equal("alice"), "Inputs got actor %+v", a)
				Expect(a.ChatID).To(Equal("c1"), "Inputs got actor %+v", a)
				return func() ([]string, func(error)) { return []string{"late"}, nil }
			}
		})
		starts := h.Starts()
		Expect(starts).To(HaveLen(1), "harness started %d times", len(starts))
		env := starts[0]
		Expect(env.Actor.Principal).To(Equal("alice"))
		Expect(env.Actor.ChatID).To(Equal("c1"))
		Expect(env.Inputs).NotTo(BeNil())
		texts, _ := env.Inputs()
		Expect(asked).To(Equal(1))
		Expect(texts).To(Equal([]string{"late"}))
	})

	// Without a sandbox the six sandbox tools could only answer "cannot run
	// commands", so the model is not offered them.
	It("offers no sandbox tools without a sandbox", func() {
		h := &sessiontest.FakeHarness{}
		openChat(h, func(o *session.Options) {
			o.ChatTools = func(context.Context, workspace.Actor) (session.TurnTools, error) {
				return session.TurnTools{Tools: []session.Tool{{Name: "only_host_tool", Description: "x", Schema: []byte(`{"type":"object"}`),
					Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}}, nil
			}
		})
		var names []string
		for _, tool := range h.Starts()[0].Tools {
			names = append(names, tool.Name)
		}
		Expect(names).To(Equal([]string{"only_host_tool"}), "want only the host tool")
	})
})
