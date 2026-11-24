// SPDX-License-Identifier: MIT
package hostops

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// outsideMarker is the content of the one file outside the agent's folder. No
// answer, exported file or materialized tree of a scoped chat may hold it.
const outsideMarker = "OUTSIDE-THE-AGENT-FOLDER-7f3a"

// scopedChat makes a scoped chat c1 of alice: the agent's folder /Acme at
// mode, the chat folder /Chats/c1, and a file outside both.
func (f *wsFixture) scopedChat(mode workspace.Mode) {
	ctx := context.Background()
	f.put("alice", "/Acme/a.txt", "inside")
	f.put("alice", "/Other/secret.txt", outsideMarker)
	f.put("alice", "/Chats/c1/attached.txt", "attached")
	f.chats["c1"] = func() (session.Access, error) {
		acme := f.stat("alice", "/Acme")
		chat := f.stat("alice", "/Chats/c1")
		a := files.UserActor("alice")
		a.ChatID = "c1"
		a = a.Fenced(workspace.FenceRoot{Node: acme.ID, Mode: mode}, workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite})
		return f.mgr.ScopedAccess(ctx, a, acme.Path, chat.Path)
	}
}

func (f *wsFixture) stat(user, rel string) workspace.Node {
	abs, err := f.files.Abs(user, rel)
	Expect(err).ToNot(HaveOccurred())
	n, err := f.files.Store().Stat(context.Background(), f.files.Actor(user), abs)
	Expect(err).ToNot(HaveOccurred())
	return n
}

// read returns the content of a file of the user, or "" when there is none.
func (f *wsFixture) read(user, rel string) string {
	abs, err := f.files.Abs(user, rel)
	Expect(err).ToNot(HaveOccurred())
	rc, _, err := f.files.Store().Open(context.Background(), f.files.Actor(user), abs)
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	return string(b)
}

// bound records a live request of the add-on made for a chat.
func (f *wsFixture) bound(user, chat, op, scope string) {
	e := env("sandbox", op, scope, time.Now().Add(time.Minute))
	e.Authority.Actor.UserID = user
	DeferCleanup(f.inflight.BeginBound(e, Binding{Principal: user, ChatID: chat}))
}

// outsideFiles lists the files of alice's home that are neither in the
// agent's folder nor in the chat folder, with their content.
func (f *wsFixture) outsideFiles() map[string]string {
	ctx := context.Background()
	out := map[string]string{}
	home, _ := f.files.Abs("alice", "/")
	var walk func(dir string)
	walk = func(dir string) {
		list, err := f.files.Store().List(ctx, f.files.Actor("alice"), dir)
		Expect(err).ToNot(HaveOccurred())
		for _, n := range list {
			rel := strings.TrimPrefix(n.Path, home)
			if rel == "/Acme" || strings.HasPrefix(rel, "/Acme/") || rel == "/Chats/c1" || strings.HasPrefix(rel, "/Chats/c1/") {
				continue
			}
			if n.Kind == workspace.KindDir {
				walk(n.Path)
				continue
			}
			out[rel] = f.read("alice", rel)
		}
	}
	walk(home)
	return out
}

// dataHolds reports whether any file of the add-on's data directory holds s.
func (f *wsFixture) dataHolds(s string) bool {
	found := false
	_ = filepath.WalkDir(filepath.Join(f.data, "sandbox"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(s)) {
			found = true
		}
		return nil
	})
	return found
}

// escapes are the spellings of a path out of the agent's folder that the
// add-on may send. Each is refused or names a file inside the folder.
var escapes = []string{
	"/../x", "../x", "/Other/secret.txt", "../Other/secret.txt", "/../Other/secret.txt",
	"/users/alice/Other/secret.txt", "//users/alice/Other/secret.txt",
	"/workspace/home/../Other/secret.txt", "/workspace/home//../Other/secret.txt",
	"/workspace/chat/../../Other/secret.txt", "/workspace/shared-x/../home/x",
	"/Workspace/Home/x",
	"..%2fOther/secret.txt", "%2e%2e/Other/secret.txt", `..\Other\secret.txt`, `\..\Other\secret.txt`,
	"․․/Other/secret.txt", "／Other／secret.txt",
	"/a.txt\x00", "/a\x01.txt",
}

var _ = Describe("workspace operations of a scoped chat", func() {
	home := `"mounts":[{"path":"/","at":"home","write":true}]`

	It("materializes / as the folder and adds the chat folder", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		f.bound("alice", "c1", "op1", "act")
		code, out := f.call("secret", "op1", "workspace.materialize", `{`+home+`,"dir":"run1"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		run := filepath.Join(f.data, "sandbox", "run1")
		Expect(os.ReadFile(filepath.Join(run, "home", "a.txt"))).To(BeEquivalentTo("inside"))
		Expect(os.ReadFile(filepath.Join(run, "chat", "attached.txt"))).To(BeEquivalentTo("attached"))
		Expect(filepath.Join(run, "home", "Other")).ToNot(BeADirectory())
		Expect(filepath.Join(run, "home", "Acme")).ToNot(BeADirectory(), "/ is the folder itself, not the home")
		Expect(f.dataHolds(outsideMarker)).To(BeFalse())
	})

	It("refuses a mount outside the folder", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		f.bound("alice", "c1", "op1", "read")
		for _, p := range escapes {
			body, _ := json.Marshal(map[string]any{"mounts": []map[string]any{{"path": p, "at": "x"}}, "dir": "run"})
			code, out := f.call("secret", "op1", "workspace.materialize", string(body))
			if code == http.StatusOK {
				Expect(f.dataHolds(outsideMarker)).To(BeFalse(), "mount %q", p)
				continue
			}
			Expect(code).To(BeElementOf(http.StatusBadRequest, http.StatusNotFound), "mount %q: %+v", p, out.Error)
		}
		Expect(f.dataHolds(outsideMarker)).To(BeFalse())
	})

	It("refuses export and import outside the folder", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		before := f.outsideFiles()
		f.bound("alice", "c1", "op1", "act")
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "made.txt"), []byte("from the add-on"), 0o600)).To(Succeed())
		for _, p := range escapes {
			body, _ := json.Marshal(map[string]string{"path": p, "to": "out/x.txt"})
			code, out := f.call("secret", "op1", "workspace.export", string(body))
			Expect(code).To(BeElementOf(http.StatusBadRequest, http.StatusNotFound), "export %q: %+v", p, out.Error)
			body, _ = json.Marshal(map[string]any{"from": "made.txt", "path": p, "overwrite": true})
			code, out = f.call("secret", "op1", "workspace.import", string(body))
			if code == http.StatusOK {
				// An encoded or look-alike name, or a home path spelled
				// inside, is a literal name: the file lands inside the
				// agent's folder (outsideFiles below proves it).
				var got struct{ Path string }
				Expect(json.Unmarshal(out.Result, &got)).To(Succeed())
				Expect(got.Path).To(HavePrefix("/"), "import %q", p)
				Expect(f.read("alice", "/Acme"+got.Path)).To(Equal("from the add-on"), "import %q", p)
			}
		}
		Expect(f.dataHolds(outsideMarker)).To(BeFalse())
		Expect(f.outsideFiles()).To(Equal(before), "nothing outside the folders changed")
	})

	It("works inside the folder with the agent's mode", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeRead)
		f.bound("alice", "c1", "op1", "act")
		code, out := f.call("secret", "op1", "workspace.export", `{"path":"/a.txt","to":"in.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(os.ReadFile(filepath.Join(f.data, "sandbox", "in.txt"))).To(BeEquivalentTo("inside"))
		code, out = f.call("secret", "op1", "workspace.export", `{"path":"/workspace/chat/attached.txt","to":"att.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "made.txt"), []byte("new"), 0o600)).To(Succeed())
		code, _ = f.call("secret", "op1", "workspace.import", `{"from":"made.txt","path":"/new.txt"}`)
		Expect(code).To(Equal(http.StatusBadRequest), "a read-only folder takes no file")
		Expect(f.read("alice", "/Acme/new.txt")).To(BeEmpty())
		code, out = f.call("secret", "op1", "workspace.import", `{"from":"made.txt","path":"/workspace/chat/new.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(f.read("alice", "/Chats/c1/new.txt")).To(Equal("new"))
		Expect(string(out.Result)).To(ContainSubstring(`"path":"/workspace/chat/new.txt"`))
	})

	It("imports into the folder of a writable agent, for the chat", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		f.bound("alice", "c1", "op1", "act")
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "made.txt"), []byte("new"), 0o600)).To(Succeed())
		code, out := f.call("secret", "op1", "workspace.import", `{"from":"made.txt","path":"/report.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(f.read("alice", "/Acme/report.txt")).To(Equal("new"))
		Expect(f.read("alice", "/report.txt")).To(BeEmpty(), "never at the home root")
		Expect(string(out.Result)).To(ContainSubstring(`"path":"/report.txt"`), "the path is in the add-on's terms")
		home, _ := f.files.Abs("alice", "/")
		sets, err := f.files.Store().History(context.Background(), f.files.Actor("alice"), workspace.HistoryQuery{Path: home, ChatID: "c1", Limit: 5})
		Expect(err).ToNot(HaveOccurred())
		Expect(sets).To(HaveLen(1), "the change belongs to the chat")
	})

	It("lists no change outside the folder", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		abs, _ := f.files.Abs("alice", "/Other/later.txt")
		_, err := f.files.Store().Put(context.Background(), f.files.Actor("alice"), abs, strings.NewReader("x"), workspace.PutOptions{Title: "Change outside " + outsideMarker})
		Expect(err).ToNot(HaveOccurred())
		abs, _ = f.files.Abs("alice", "/Acme/later.txt")
		_, err = f.files.Store().Put(context.Background(), f.files.Actor("alice"), abs, strings.NewReader("x"), workspace.PutOptions{Title: "Change inside"})
		Expect(err).ToNot(HaveOccurred())
		f.bound("alice", "c1", "op1", "read")
		code, out := f.call("secret", "op1", "workspace.changes", `{"since":0}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(string(out.Result)).To(ContainSubstring("Change inside"))
		Expect(string(out.Result)).ToNot(ContainSubstring(outsideMarker))
	})

	It("reports a commit outside the view as a conflict", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		before := f.outsideFiles()
		f.bound("alice", "c1", "op1", "act")
		code, _ := f.call("secret", "op1", "workspace.materialize", `{`+home+`,"dir":"run1"}`)
		Expect(code).To(Equal(http.StatusOK))
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for name, body := range map[string]string{"home/new.txt": "made", "chat/work/out.txt": "work", "Other/stolen.txt": "stolen"} {
			Expect(tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})).To(Succeed())
			_, _ = tw.Write([]byte(body))
		}
		Expect(tw.Close()).To(Succeed())
		Expect(os.WriteFile(filepath.Join(f.data, "sandbox", "run1", "upper.tar"), buf.Bytes(), 0o600)).To(Succeed())
		code, out := f.call("secret", "op1", "workspace.commit", `{`+home+`,"dir":"run1","base":0,"title":"Sandbox: bash"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		var got struct {
			Conflicts []struct{ Path, Reason, Kept string }
		}
		Expect(json.Unmarshal(out.Result, &got)).To(Succeed())
		Expect(got.Conflicts).To(ConsistOf(And(HaveField("Path", "/Other/stolen.txt"), HaveField("Reason", workspace.ReasonOutsideView))))
		Expect(f.read("alice", "/Acme/new.txt")).To(Equal("made"))
		Expect(f.read("alice", "/Chats/c1/work/out.txt")).To(Equal("work"))
		Expect(f.outsideFiles()).To(Equal(before))
	})

	It("binds a read call that has no resource ID", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		f.bound("alice", "c1", "op1", "read")
		code, _ := f.call("secret", "op1", "workspace.export", `{"path":"/Other/secret.txt","to":"x.txt"}`)
		Expect(code).To(Equal(http.StatusNotFound), "/Other is a name inside the folder, where it does not exist")
		code, out := f.call("secret", "op1", "workspace.export", `{"path":"/a.txt","to":"x.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(f.dataHolds(outsideMarker)).To(BeFalse())
	})

	It("stays user-wide for a call with no binding", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		f.live("alice", "op1", "read")
		code, out := f.call("secret", "op1", "workspace.export", `{"path":"/Other/secret.txt","to":"x.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(os.ReadFile(filepath.Join(f.data, "sandbox", "x.txt"))).To(BeEquivalentTo(outsideMarker))
	})

	It("keeps an unscoped chat on the whole home", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		f.bound("alice", "plain", "op1", "act")
		code, out := f.call("secret", "op1", "workspace.export", `{"path":"/Other/secret.txt","to":"x.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		code, out = f.call("secret", "op1", "workspace.materialize", `{`+home+`,"dir":"run1"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		run := filepath.Join(f.data, "sandbox", "run1")
		Expect(os.ReadFile(filepath.Join(run, "home", "Other", "secret.txt"))).To(BeEquivalentTo(outsideMarker))
		Expect(filepath.Join(run, "chat")).ToNot(BeADirectory(), "no chat folder is added for an unscoped chat")
	})

	It("fails closed when the chat of the call cannot be resolved", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		ops := []string{"workspace.materialize", "workspace.commit", "workspace.export", "workspace.import", "workspace.changes"}
		args := `{` + home + `,"dir":"run1","path":"/Other/secret.txt","to":"x.txt","from":"x.txt","base":0,"since":0}`
		// A chat that no longer exists, or whose agent's folder is gone.
		f.bound("alice", "gone", "op1", "act")
		for _, op := range ops {
			code, _ := f.call("secret", "op1", op, args)
			Expect(code).To(BeElementOf(http.StatusBadRequest, http.StatusNotFound), op)
		}
		// A binding of another user than the actor of the request.
		f.bound("bob", "c1", "op2", "act")
		e := env("sandbox", "op3", "act", time.Now().Add(time.Minute))
		e.Authority.Actor.UserID = "alice"
		DeferCleanup(f.inflight.BeginBound(e, Binding{Principal: "bob", ChatID: "c1"}))
		for _, op := range ops {
			code, _ := f.call("secret", "op3", op, args)
			Expect(code).To(BeElementOf(http.StatusBadRequest, http.StatusNotFound), op)
		}
		// An action is always a chat's: one that arrives without a binding
		// and names no chat of the user is refused, never user-wide.
		f.host.Chat = func(context.Context, string, string) string { return "" }
		e = env("sandbox", "op4", "act", time.Now().Add(time.Minute))
		e.Authority.Actor.UserID = "alice"
		e.Request.ResourceID = "c-unknown"
		DeferCleanup(f.inflight.Begin(e))
		for _, op := range ops {
			code, _ := f.call("secret", "op4", op, args)
			Expect(code).To(BeElementOf(http.StatusBadRequest, http.StatusNotFound), op)
		}
		Expect(f.dataHolds(outsideMarker)).To(BeFalse())
	})

	It("resolves an action without a binding by its chat", func() {
		f := newWSFixture()
		f.scopedChat(workspace.ModeWrite)
		f.host.Chat = func(_ context.Context, p, rid string) string {
			if p == "alice" && rid == "c-c1" {
				return "c1"
			}
			return ""
		}
		e := env("sandbox", "op1", "act", time.Now().Add(time.Minute))
		e.Authority.Actor.UserID = "alice"
		e.Request.ResourceID = "c-c1"
		DeferCleanup(f.inflight.Begin(e))
		code, _ := f.call("secret", "op1", "workspace.export", `{"path":"/Other/secret.txt","to":"x.txt"}`)
		Expect(code).To(Equal(http.StatusNotFound))
		code, out := f.call("secret", "op1", "workspace.export", `{"path":"/a.txt","to":"x.txt"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
	})
})
