package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// sandboxSeeingWork is a Sandbox add-on that records the work folder the host
// sent with each call of bash.
type sandboxSeeingWork struct {
	*fakeAddons
	mu   sync.Mutex
	seen []string
}

func (s *sandboxSeeingWork) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	c, _ := contract.ExecutionCallFrom(ctx)
	s.mu.Lock()
	s.seen = append(s.seen, c.WorkDir)
	s.mu.Unlock()
	return s.fakeAddons.Call(ctx, user, conv, name, args)
}

var _ = Describe("the folder a command starts in", func() {
	bashTwice := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "run" {
			return []studiotest.ToolStep{{Tool: "bash", Args: `{"command":"pwd"}`}, {Tool: "bash", Args: `{"command":"pwd"}`}, {Say: "Done."}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}

	It("sends the add-on's bash the chat's work folder, made before the call", func() {
		f := newFiles()
		sb := &sandboxSeeingWork{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}}
		svc, h := newToolService(bashTwice, func(d *studio.Deps) { d.Files = f; d.AddonTools = sb })
		id := create(svc, alice)
		sent(svc, alice, id, "run")
		idle(svc, alice, id)

		note := strings.Join(h.SentRaw(id), "\n")
		guest := regexpFind(note, `/workspace/home/Chats/[^/]+/work/`)
		Expect(guest).NotTo(BeEmpty())
		Expect(sb.seen).To(Equal([]string{strings.TrimSuffix(guest, "/"), strings.TrimSuffix(guest, "/")}))
		Expect(note).To(ContainSubstring("The shell starts in $WORK"))
		// The note keeps the literal path for the file tools, and the folder
		// name is ASCII with no space, so a command that retypes it unquoted still works.
		Expect(guest).To(MatchRegexp(`^/workspace/home/Chats/\d{4}-\d{2}-\d{2}-[a-z0-9-]+/work/$`))
		n, err := f.Store().Stat(context.Background(), files.UserActor(alice), session.HomePath(alice)+strings.TrimPrefix(strings.TrimSuffix(guest, "/"), "/workspace/home"))
		Expect(err).NotTo(HaveOccurred(), "the folder was not made before the call")
		Expect(n.Kind).To(Equal(workspace.KindDir))
	})

	It("sends no folder when the work folder setting is off", func() {
		f := newFiles()
		sb := &sandboxSeeingWork{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}}
		svc, _ := newToolService(bashTwice, func(d *studio.Deps) {
			d.Files = f
			d.AddonTools = sb
			d.WorkFolderOn = func() bool { return false }
		})
		id := create(svc, alice)
		sent(svc, alice, id, "run")
		idle(svc, alice, id)
		Expect(sb.seen).To(Equal([]string{"", ""}), "the shell would start in a folder the setting turned off")
	})
})
