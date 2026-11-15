//go:build linux || darwin

package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// specBackend records the Spec of every Create.
type specBackend struct {
	sandbox.Backend
	mu    sync.Mutex
	specs []sandbox.Spec
}

func (b *specBackend) Create(ctx context.Context, s sandbox.Spec) (sandbox.Sandbox, error) {
	b.mu.Lock()
	b.specs = append(b.specs, s)
	b.mu.Unlock()
	return b.Backend.Create(ctx, s)
}

var _ = Describe("the profile of a call", func() {
	var (
		r       *scopedRig
		backend *specBackend
		choices map[string]runner.Choice
		m       *runner.Manager
		n       int
	)
	run := func(profile string) (string, error) {
		n++
		raw, _ := json.Marshal(map[string]any{"command": "echo hi"})
		return m.Run(r.ctx, runner.Request{User: alice.Principal, Conversation: alice.ChatID, Operation: fmt.Sprintf("p%d", n), Tool: "bash", Args: raw,
			Call: sandbox.CallOptions{Profile: profile}})
	}
	BeforeEach(func() {
		r = newScopedRig(workspace.ModeWrite)
		backend = &specBackend{Backend: sbtest.NewFakeBackend(GinkgoT().TempDir())}
		choices = map[string]runner.Choice{
			"base":  {ID: "base", Image: "reg/base@sha256:1", CPUs: 2, Memory: 2 << 30, Disk: 10 << 30},
			"media": {ID: "media", Image: "reg/media@sha256:2", CPUs: 4, Memory: 4 << 30, Disk: 20 << 30},
		}
		var err error
		m, err = runner.New(runner.Options{
			Backend: func() (sandbox.Backend, error) { return backend, nil },
			Store:   r.st, Actor: r.actor, View: r.view,
			MissingTool: func(profile string) string { return "[missing in " + profile + "]" },
			Profile: func(_ context.Context, req runner.Request) (runner.Choice, error) {
				c, ok := choices[req.Call.Profile]
				if !ok {
					return runner.Choice{}, fmt.Errorf("no profile %q", req.Call.Profile)
				}
				return c, nil
			},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
	})

	It("makes the sandbox with the image and the limits of the profile", func() {
		_, err := run("media")
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.specs).To(HaveLen(1))
		Expect(backend.specs[0].Image).To(Equal("reg/media@sha256:2"))
		Expect(backend.specs[0].CPUs).To(Equal(4))
		Expect(backend.specs[0].Memory).To(Equal(int64(4 << 30)))
		profile, image, ok := m.Info(alice.Principal, alice.ChatID)
		Expect(ok).To(BeTrue())
		Expect(profile).To(Equal("media"))
		Expect(image).To(Equal("reg/media@sha256:2"))
		Expect(m.ImagesInUse()).To(HaveKey("reg/media@sha256:2"))
	})

	It("keeps the sandbox for the same profile and remakes it when the chat switches", func() {
		_, err := run("base")
		Expect(err).NotTo(HaveOccurred())
		_, err = run("base")
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.specs).To(HaveLen(1), "the same profile reuses the guest")
		_, err = run("media")
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.specs).To(HaveLen(2), "a switch restarts the guest")
		Expect(backend.specs[1].Image).To(Equal("reg/media@sha256:2"))
		Expect(m.Count("")).To(Equal(1))
	})

	It("keeps a running sandbox on its image when a newer one is current", func() {
		_, err := run("base")
		Expect(err).NotTo(HaveOccurred())
		choices["base"] = runner.Choice{ID: "base", Image: "reg/base@sha256:9"}
		_, err = run("base")
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.specs).To(HaveLen(1))
		_, image, _ := m.Info(alice.Principal, alice.ChatID)
		Expect(image).To(Equal("reg/base@sha256:1"), "a chat keeps its image until it restarts")
	})

	It("puts the notice of a fallback before the output", func() {
		choices["media"] = runner.Choice{ID: "base", Image: "reg/base@sha256:1", Notice: "[media failed, ran base]"}
		out, err := run("media")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("[media failed, ran base]\n"))
		Expect(out).To(ContainSubstring("hi"))
	})

	It("names the profile when a command is not found", func() {
		n++
		raw, _ := json.Marshal(map[string]any{"command": "no-such-program-xyz"})
		out, err := m.Run(r.ctx, runner.Request{User: alice.Principal, Conversation: alice.ChatID, Operation: fmt.Sprintf("m%d", n), Tool: "bash", Args: raw,
			Call: sandbox.CallOptions{Profile: "base"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("[exit code 127]"))
		Expect(out).To(HaveSuffix("[missing in base]"))
		ok, err := run("base")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).NotTo(ContainSubstring("missing in"), "a command that ran needs no note")
	})

	It("fails the call, and keeps no sandbox, when the profile cannot be chosen", func() {
		_, err := run("nope")
		Expect(err).To(MatchError(ContainSubstring("no profile")))
		Expect(m.Count("")).To(BeZero())
	})
})
