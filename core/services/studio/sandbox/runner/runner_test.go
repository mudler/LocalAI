//go:build linux || darwin

package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// outside is in every file outside the chat's folders: no command output may
// hold it.
const outside = "OUTSIDE-5d1f"

// scopedRig is a store where alice's chat c1 works in /users/alice/Proj and
// its chat folder /users/alice/Chats/c1, with files outside both, and a
// manager whose actor and view come from hooks, as the server wires them to
// the chat's resolved access.
type scopedRig struct {
	ctx  context.Context
	st   workspace.Store
	mode workspace.Mode // of the agent's folder
	mu   sync.Mutex
	fail error           // the resolver's error, when set
	wide bool            // the view hook names the whole home
	m    *runner.Manager // the manager under test
}

func newScopedRig(mode workspace.Mode) *scopedRig {
	r := &scopedRig{ctx: context.Background(), st: sbtest.OpenStore(), mode: mode}
	for p, body := range map[string]string{
		"/users/alice/Proj/in.txt":       "inside\n",
		"/users/alice/Chats/c1/note.txt": "chat note\n",
		"/users/alice/secret.txt":        outside + "\n",
		"/users/alice/Other/x.txt":       outside + "\n",
	} {
		_, err := r.st.Put(r.ctx, admin, p, strings.NewReader(body), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.st.Grant(r.ctx, admin, "alice", "/users/alice", workspace.ModeWrite)).To(Succeed())
	m, err := runner.New(runner.Options{
		Backend: func() (sandbox.Backend, error) { return sbtest.NewFakeBackend(GinkgoT().TempDir()), nil },
		Store:   r.st,
		Actor:   r.actor,
		View:    r.view,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = m.Close(context.Background()) })
	r.m = m
	return r
}

func (r *scopedRig) node(p string) int64 {
	GinkgoHelper()
	n, err := r.st.Stat(r.ctx, admin, p)
	Expect(err).NotTo(HaveOccurred())
	return n.ID
}

// actor is the resolver: alice in c1, fenced to the folder at the rig's mode
// and the chat folder.
func (r *scopedRig) actor(_ context.Context, user, conv string) (workspace.Actor, error) {
	r.mu.Lock()
	fail, mode := r.fail, r.mode
	r.mu.Unlock()
	if fail != nil {
		return workspace.Actor{}, fail
	}
	return alice.Fenced(
		workspace.FenceRoot{Node: r.node("/users/alice/Proj"), Mode: mode},
		workspace.FenceRoot{Node: r.node("/users/alice/Chats/c1"), Mode: workspace.ModeWrite},
	), nil
}

// view is the chat's view: the folder at home, the chat folder at chat. With
// wide set it asks for the whole home, as a broken resolver would.
func (r *scopedRig) view(ctx context.Context, a workspace.Actor) (workspace.View, error) {
	r.mu.Lock()
	wide, mode := r.wide, r.mode
	r.mu.Unlock()
	if wide {
		return r.st.View(ctx, a, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}})
	}
	return r.st.View(ctx, a, []workspace.MountRequest{
		{Path: "/users/alice/Proj", At: "home", Mode: mode},
		{Path: "/users/alice/Chats/c1", At: "chat", Mode: workspace.ModeWrite},
	})
}

var opN int

func (r *scopedRig) bash(cmd string) (string, error) {
	opN++
	raw, _ := json.Marshal(map[string]any{"command": cmd})
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	return r.m.Run(ctx, runner.Request{User: alice.Principal, Conversation: alice.ChatID, Operation: fmt.Sprintf("op%d", opN), Tool: "bash", Args: raw})
}

func (r *scopedRig) must(cmd string) string {
	GinkgoHelper()
	out, err := r.bash(cmd)
	Expect(err).NotTo(HaveOccurred(), cmd)
	return out
}

func (r *scopedRig) read(p string) (string, error) {
	rc, _, err := r.st.Open(r.ctx, admin, p)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	return string(b), err
}

func (r *scopedRig) exists(p string) bool {
	_, err := r.st.Stat(r.ctx, admin, p)
	return err == nil
}

var _ = Describe("the sandbox of a scoped chat", func() {
	It("creates the sandbox with the chat's view", func() {
		r := newScopedRig(workspace.ModeWrite)
		Expect(r.must("pwd >/dev/null; ls; echo ---; ls ..; echo ---; cat in.txt ../chat/note.txt")).
			To(Equal("in.txt\n---\nchat\nhome\n---\ninside\nchat note\n"))
		out := r.must("cat ../../secret.txt ../secret.txt secret.txt Other/x.txt 2>&1; grep -r " + outside + " .. 2>&1; true")
		Expect(out).NotTo(ContainSubstring(outside))
	})

	It("saves what a command writes in the folder and in the chat folder", func() {
		r := newScopedRig(workspace.ModeWrite)
		r.must("echo made > made.txt; echo kept > ../chat/kept.txt")
		Expect(r.read("/users/alice/Proj/made.txt")).To(Equal("made\n"))
		Expect(r.read("/users/alice/Chats/c1/kept.txt")).To(Equal("kept\n"))
		Expect(r.exists("/users/alice/made.txt")).To(BeFalse())
	})

	It("cannot commit outside the fence", func() {
		r := newScopedRig(workspace.ModeRead)
		out := r.must("echo x > ../escape.txt; mkdir -p ../Other; echo x > ../Other/x.txt; echo x > changed.txt; echo y > in.txt; echo ok > ../chat/ok.txt")
		Expect(out).To(ContainSubstring("/workspace/escape.txt was not saved: it is outside the user's files"))
		Expect(out).To(ContainSubstring("/workspace/home/changed.txt was not saved: that folder is read-only"))
		Expect(r.exists("/users/alice/escape.txt")).To(BeFalse())
		Expect(r.exists("/users/alice/Proj/changed.txt")).To(BeFalse())
		Expect(r.read("/users/alice/Proj/in.txt")).To(Equal("inside\n"))
		Expect(r.read("/users/alice/Other/x.txt")).To(Equal(outside + "\n"))
		Expect(r.read("/users/alice/Chats/c1/ok.txt")).To(Equal("ok\n"), "the chat folder is writable at any mode")
	})

	It("runs a read-only call in a sandbox of its own whose folders are all read-only, and saves nothing it writes", func() {
		r := newScopedRig(workspace.ModeWrite)
		opN++
		raw, _ := json.Marshal(map[string]any{"command": "cat in.txt; echo x > made.txt; echo y > in.txt; echo z > ../chat/z.txt"})
		out, err := r.m.Run(r.ctx, runner.Request{User: alice.Principal, Conversation: alice.ChatID, Operation: fmt.Sprintf("op%d", opN),
			Tool: "bash", Args: raw, Call: sandbox.CallOptions{ReadOnly: true}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("inside\n"), "a read-only call can read the files")
		Expect(out).To(ContainSubstring("was not saved: that folder is read-only"))
		Expect(r.exists("/users/alice/Proj/made.txt")).To(BeFalse())
		Expect(r.exists("/users/alice/Chats/c1/z.txt")).To(BeFalse())
		Expect(r.read("/users/alice/Proj/in.txt")).To(Equal("inside\n"))
		// The sandbox that commits is another one: it is not held back.
		r.must("echo made > made.txt")
		Expect(r.read("/users/alice/Proj/made.txt")).To(Equal("made\n"))
		Expect(r.m.Count("")).To(Equal(2))
	})

	It("never makes a sandbox with a view wider than the fence", func() {
		r := newScopedRig(workspace.ModeWrite)
		r.wide = true
		out, err := r.bash("cat secret.txt; echo x > escape.txt")
		Expect(err).To(HaveOccurred())
		Expect(out).NotTo(ContainSubstring(outside))
		Expect(r.exists("/users/alice/escape.txt")).To(BeFalse())
		Expect(r.m.Count("")).To(BeZero(), "a failed call keeps no sandbox")
	})

	It("refuses a view of another actor than the chat's", func() {
		r := newScopedRig(workspace.ModeWrite)
		m, err := runner.New(runner.Options{
			Backend: func() (sandbox.Backend, error) { return sbtest.NewFakeBackend(GinkgoT().TempDir()), nil },
			Store:   r.st,
			Actor:   r.actor,
			View: func(ctx context.Context, _ workspace.Actor) (workspace.View, error) {
				return r.st.View(ctx, alice, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}})
			},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		raw, _ := json.Marshal(map[string]any{"command": "cat secret.txt"})
		out, err := m.Run(r.ctx, runner.Request{User: "alice", Conversation: "chat-1", Operation: "o1", Tool: "bash", Args: raw})
		Expect(err).To(HaveOccurred())
		Expect(out).NotTo(ContainSubstring(outside))
	})

	It("fails the call when the resolver fails, also with a sandbox running", func() {
		r := newScopedRig(workspace.ModeWrite)
		r.must("true")
		r.mu.Lock()
		r.fail = errors.New("the folder of this agent is missing")
		r.mu.Unlock()
		out, err := r.bash("cat in.txt")
		Expect(err).To(MatchError(ContainSubstring("folder of this agent is missing")))
		Expect(out).To(BeEmpty())
	})

	It("rebuilds the sandbox when the chat's access changed", func() {
		r := newScopedRig(workspace.ModeWrite)
		r.must("echo one > one.txt")
		Expect(r.read("/users/alice/Proj/one.txt")).To(Equal("one\n"))
		r.mu.Lock()
		r.mode = workspace.ModeRead
		r.mu.Unlock()
		out := r.must("echo two > two.txt")
		Expect(out).To(ContainSubstring("that folder is read-only"))
		Expect(r.exists("/users/alice/Proj/two.txt")).To(BeFalse())
	})
})

var _ = Describe("the hooks of a manager", func() {
	It("needs the actor and the view of a chat", func() {
		st := sbtest.OpenStore()
		backend := func() (sandbox.Backend, error) { return nil, runner.ErrNoSandbox }
		_, err := runner.New(runner.Options{Backend: backend, Store: st, View: func(context.Context, workspace.Actor) (workspace.View, error) { return workspace.View{}, nil }})
		Expect(err).To(HaveOccurred())
		_, err = runner.New(runner.Options{Backend: backend, Store: st, Actor: func(context.Context, string, string) (workspace.Actor, error) { return alice, nil }})
		Expect(err).To(HaveOccurred())
	})
})
