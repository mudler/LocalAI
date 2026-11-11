// SPDX-License-Identifier: MIT

// Package actione2e runs the real Sandbox and Office add-on processes under the real
// supervisor, with the real host operations over a real workspace, and calls
// its tools the way a chat does. It proves the whole path from a tool call to a
// file in the user's files and back to an undo, with the host backend (the
// isolated backends need a guest image and a capable host, which this suite
// does not have).
package actione2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// hostPhrase is what an administrator types to accept the host backend.
const hostPhrase = "I understand that commands run on this host without isolation"

type world struct {
	f     *files.Service
	inst  addonruntime.Instance
	gen   uint64
	opSeq int
	id    string
	mgr   *session.Manager
	// scoped makes the access of a chat whose agent has a folder, by chat
	// ID. Every other chat works on the whole home of its user.
	scoped map[string]func() (session.Access, error)
}

func TestActionE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on Actions End-to-End Suite")
}

func start() *world { return startAddon("sandbox") }

// startAddon runs the add-on of that name: its executable is built, its
// manifest is the one in the source tree and its host operations are the
// workspace ones.
func startAddon(id string) *world { return startAddonFrom(id, "") }

// startAddonFrom is startAddon for a package that was built and extracted by
// the release process (package.sh and addonpkg extract), when packageDir is
// set: its executable, runtime files and manifest are used as they are.
func startAddonFrom(id, packageDir string) *world {
	if goruntime.GOOS != "linux" {
		Skip("the sandbox add-on is Linux only")
	}
	root := GinkgoT().TempDir()
	bin := filepath.Join(root, "pkg", id)
	Expect(os.MkdirAll(filepath.Dir(bin), 0o755)).To(Succeed())
	manifestPath := filepath.Join(repoRoot(), "addons", id, "manifest.json")
	if packageDir != "" {
		Expect(os.CopyFS(filepath.Dir(bin), os.DirFS(packageDir))).To(Succeed(), "copy the package")
		manifestPath = filepath.Join(packageDir, "manifest.json")
	} else {
		build := exec.Command("go", "build", "-o", bin, "./addons/"+id+"/cmd/"+id)
		build.Dir = repoRoot()
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		out, err := build.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "build: %s", out)
	}
	raw, err := os.ReadFile(manifestPath)
	Expect(err).NotTo(HaveOccurred())
	m, err := contract.ParseManifest(raw)
	Expect(err).NotTo(HaveOccurred())
	m.OS, m.Arch = goruntime.GOOS, goruntime.GOARCH

	dsn := "file:" + filepath.Join(root, "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(root, "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	f := files.New(ws)
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	limits := contract.DefaultLimits()
	limits.ReadTimeout = 20 * time.Second
	inflight := hostops.NewInFlight()
	broker := hostops.New(inflight, func(string, string) bool { return true }, limits)
	broker.SetAdminCheck(func(a contract.Actor) bool { return a.UserID == "root" })
	dataRoot := filepath.Join(root, "data")
	mgr, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Workspace: ws, Harness: &sessiontest.FakeHarness{}, StateDir: filepath.Join(root, "sessions")})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = mgr.Close(context.Background()) })
	w := &world{f: f, gen: 1, id: id, mgr: mgr, scoped: map[string]func() (session.Access, error){}}
	// As the chats service does: a call that no chat made works on the whole
	// home, a chat's call takes the chat's access. The resource of an action
	// is the chat here.
	Expect(hostops.RegisterWorkspace(broker, &hostops.WorkspaceHost{
		Store:     ws,
		Principal: func(a contract.Actor) string { return a.UserID },
		DataDir:   func(id string) string { return filepath.Join(dataRoot, id) },
		Access: func(ctx context.Context, p, chat string) (session.Access, error) {
			if err := f.EnsureHome(ctx, p); err != nil {
				return session.Access{}, err
			}
			if chat == "" {
				return mgr.PersonAccess(ctx, files.UserActor(p))
			}
			if mk, ok := w.scoped[chat]; ok {
				if p != "alice" {
					return session.Access{}, session.ErrNotFound
				}
				return mk()
			}
			a := files.UserActor(p)
			a.ChatID = chat
			return mgr.UserAccess(ctx, a)
		},
		Chat: func(_ context.Context, _, rid string) string { return rid },
	})).To(Succeed())
	sup, err := addonruntime.NewSupervisor(addonruntime.Config{
		StateRoot: filepath.Join(root, "state"), DataRoot: dataRoot, Limits: limits,
		ReadyTimeout: 20 * time.Second, StableHealth: time.Second,
		HostOps: &addonruntime.HostOps{Broker: broker, InFlight: inflight, Authority: hostops.NewStaticAuthority(map[string][]string{id: {"workspace."}})},
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sup.Close(context.Background()) })
	// The supervisor starts only a package whose files nobody else can change.
	Expect(os.Chmod(bin, 0o500)).To(Succeed())
	receipt := packages.PackageReceipt{Manifest: m, Directory: filepath.Dir(bin), Digest: "sha256:sandbox-e2e"}
	rb, _ := json.Marshal(receipt)
	Expect(os.WriteFile(filepath.Join(filepath.Dir(bin), "receipt.json"), rb, 0o400)).To(Succeed())
	inst, err := sup.Start(context.Background(), receipt, 1)
	Expect(err).NotTo(HaveOccurred(), "start the add-on")
	w.inst = inst
	return w
}

func repoRoot() string {
	dir, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	Fail("no go.mod above the test")
	return ""
}

func (w *world) envelope(tool, user, conv string, args map[string]any, act bool) contract.Envelope {
	w.opSeq++
	if args == nil {
		args = map[string]any{}
	}
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	action, scope := "read", "read"
	if act {
		action, scope = "act", "act"
	}
	return contract.Envelope{
		Request:   contract.Request{AddonID: w.id, ResourceID: conv, OperationID: "t" + strings.Repeat("0", 10) + string(rune('a'+w.opSeq%26)) + string(rune('a'+w.opSeq/26)), Generation: w.gen, Action: action, Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "chats", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func (w *world) result(res contract.Result, err error) map[string]any {
	Expect(err).NotTo(HaveOccurred(), "call")
	var out map[string]any
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed(), "payload %s", res.Payload)
	return out
}

func (w *world) read(tool, user string, args map[string]any) map[string]any {
	e := w.envelope(tool, user, "", args, false)
	res, err := w.inst.Client.Read(context.Background(), e)
	return w.result(res, err)
}

func (w *world) act(tool, user, conv string, args map[string]any) map[string]any {
	e := w.envelope(tool, user, conv, args, true)
	res, err := w.inst.Client.(contract.ActRuntime).Act(context.Background(), e)
	return w.result(res, err)
}

// readIn and actIn call a tool for a chat, as the chat turn does: the call
// is bound to the chat on the host side, and the envelope does not change.
func (w *world) readIn(tool, user, chat string, args map[string]any) map[string]any {
	e := w.envelope(tool, user, "", args, false)
	ctx := hostops.WithBinding(context.Background(), hostops.Binding{Principal: user, ChatID: chat})
	res, err := w.inst.Client.Read(ctx, e)
	return w.result(res, err)
}

func (w *world) actIn(tool, user, chat string, args map[string]any) map[string]any {
	e := w.envelope(tool, user, chat, args, true)
	ctx := hostops.WithBinding(context.Background(), hostops.Binding{Principal: user, ChatID: chat})
	res, err := w.inst.Client.(contract.ActRuntime).Act(ctx, e)
	return w.result(res, err)
}

// scopedChat makes chat a chat of alice whose agent has the folder /Acme at
// mode, with its chat folder /Chats/<chat>.
func (w *world) scopedChat(chat string, mode workspace.Mode) {
	w.put("alice", "/Chats/"+chat+"/note.txt", "chat note")
	w.scoped[chat] = func() (session.Access, error) {
		ctx := context.Background()
		stat := func(rel string) workspace.Node {
			abs, _ := w.f.Abs("alice", rel)
			n, err := w.f.Store().Stat(ctx, w.f.Actor("alice"), abs)
			Expect(err).NotTo(HaveOccurred())
			return n
		}
		acme, folder := stat("/Acme"), stat("/Chats/"+chat)
		a := files.UserActor("alice")
		a.ChatID = chat
		a = a.Fenced(workspace.FenceRoot{Node: acme.ID, Mode: mode}, workspace.FenceRoot{Node: folder.ID, Mode: workspace.ModeWrite})
		return w.mgr.ScopedAccess(ctx, a, acme.Path, folder.Path)
	}
}

func (w *world) put(user, rel, content string) {
	ctx := context.Background()
	Expect(w.f.EnsureHome(ctx, user)).To(Succeed())
	abs, _ := w.f.Abs(user, rel)
	_, err := w.f.Store().Put(ctx, w.f.Actor(user), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

func (w *world) text(user, rel string) (string, bool) {
	ctx := context.Background()
	abs, _ := w.f.Abs(user, rel)
	rc, _, err := w.f.Store().Open(ctx, w.f.Actor(user), abs)
	if err != nil {
		return "", false
	}
	defer func() { _ = rc.Close() }()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := rc.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return b.String(), true
}

var _ = Describe("Add-on actions end to end", func() {
	It("goes from a chat to a file in the user's files and back to an undo", func() {
		w := start()
		ctx := context.Background()
		w.put("alice", "/notes.txt", "alice's notes\n")
		w.put("bob", "/notes.txt", "bob's notes\n")

		// Nothing isolated works here and the host backend is not chosen: no code runs.
		got := w.act("bash", "alice", "convaaaa", map[string]any{"command": "echo no > never.txt"})
		Expect(got["output"]).To(Equal("This host cannot run commands."), "before the host is chosen: %v", got)
		_, ok := w.text("alice", "/never.txt")
		Expect(ok).To(BeFalse(), "a command ran with no backend")

		// Only an administrator can change the backend, and the host needs its phrase.
		got = w.read("set-backend", "alice", map[string]any{"backend": "host"})
		Expect(got["ok"]).To(Equal(false), "a user changed the backend: %v", got)
		got = w.read("set-backend", "root", map[string]any{"backend": "host"})
		Expect(got["ok"]).To(Equal(false), "the host was chosen without its phrase: %v", got)
		got = w.read("confirm-host", "root", map[string]any{"phrase": hostPhrase})
		Expect(got["ok"]).To(Equal(true), "confirm-host: %v", got)
		got = w.read("set-backend", "root", map[string]any{"backend": "host"})
		Expect(got["ok"]).To(Equal(true), "set-backend: %v", got)
		st := w.read("status", "alice", nil)
		Expect(st["canRun"]).To(Equal(true), "status = %v", st)
		Expect(st["hostNotice"]).To(Equal(true), "status = %v", st)
		Expect(st).NotTo(HaveKey("backends"), "a user sees the details of the host")

		// A chat runs bash: it sees the user's files and writes one.
		got = w.act("bash", "alice", "convaaaa", map[string]any{"command": "cat notes.txt; echo made by bash > made.txt"})
		Expect(got["ok"]).To(Equal(true), "bash = %v", got)
		Expect(got["output"]).To(ContainSubstring("alice's notes"))
		text, ok := w.text("alice", "/made.txt")
		Expect(ok).To(BeTrue())
		Expect(text).To(Equal("made by bash\n"), "the file in alice's files")

		// It is one change set by the chat, and the user can undo it.
		abs, _ := w.f.Abs("alice", "/made.txt")
		hist, err := w.f.Store().History(ctx, w.f.Actor("alice"), workspace.HistoryQuery{Path: abs})
		Expect(err).NotTo(HaveOccurred())
		Expect(hist).To(HaveLen(1))
		Expect(hist[0].Kind).To(Equal(workspace.ChangeAgent))
		_, _, err = w.f.Store().Undo(ctx, w.f.Actor("alice"), hist[0].Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred(), "undo")
		_, ok = w.text("alice", "/made.txt")
		Expect(ok).To(BeFalse(), "the file is still there after the undo")

		// Another user in a chat with the same ID has a sandbox of their own and
		// sees only their own files.
		other := w.act("bash", "bob", "convaaaa", map[string]any{"command": "ls; cat notes.txt"})
		text = other["output"].(string)
		Expect(text).NotTo(ContainSubstring("alice"), "bob's sandbox")
		Expect(text).To(ContainSubstring("bob's notes"), "bob's sandbox")
		_, ok = w.text("bob", "/made.txt")
		Expect(ok).To(BeFalse(), "alice's file reached bob")

		// With the backend off, the tool says so and nothing runs.
		got = w.read("set-backend", "root", map[string]any{"backend": "off"})
		Expect(got["ok"]).To(Equal(true), "set-backend off: %v", got)
		got = w.act("bash", "alice", "convaaaa", map[string]any{"command": "echo late > late.txt"})
		Expect(got["output"]).To(Equal("This host cannot run commands."), "with the backend off: %v", got)
		_, ok = w.text("alice", "/late.txt")
		Expect(ok).To(BeFalse(), "a command ran with the backend off")
	})

	It("cannot be called as another user or without a live request", func() {
		w := start()
		w.put("alice", "/a.txt", "alice-secret")
		// The host operation channel works only while a request is live: a call from
		// the add-on with no envelope is refused. The add-on has no way to start one,
		// so the check is that an envelope of one user never reads another's files.
		w.read("confirm-host", "root", map[string]any{"phrase": hostPhrase})
		w.read("set-backend", "root", map[string]any{"backend": "host"})
		// The Sandbox serves bash only; reading is the chat's own file tools.
		got := w.act("bash", "bob", "convbbbb", map[string]any{"command": "cat a.txt"})
		if got["ok"] == true {
			Expect(got["output"]).NotTo(ContainSubstring("alice-secret"), "bob read alice's file: %v", got)
		}
	})

	It("runs a background job whose files reach the user's files after the call returned, through the job lease", func() {
		w := start()
		w.put("alice", "/seed.txt", "x")
		w.read("confirm-host", "root", map[string]any{"phrase": hostPhrase})
		w.read("set-backend", "root", map[string]any{"backend": "host"})
		got := w.actIn("job", "alice", "convjob1", map[string]any{"verb": "start", "label": "late", "command": "sleep 1; echo from the job > job.txt; echo done"})
		Expect(got["ok"]).To(Equal(true), "start = %v", got)
		id := got["job"].(map[string]any)["id"].(string)
		// The call is over; only the lease keeps the start's operation live.
		var job map[string]any
		Eventually(func() string {
			r := w.actIn("job", "alice", "convjob1", map[string]any{"verb": "read", "id": id, "observe": true})
			job, _ = r["job"].(map[string]any)
			s, _ := job["state"].(string)
			return s
		}).WithTimeout(60 * time.Second).WithPolling(200 * time.Millisecond).Should(Equal("exited"))
		Expect(job["exit"]).To(BeEquivalentTo(0))
		text, ok := w.text("alice", "/job.txt")
		Expect(ok).To(BeTrue(), "the final checkpoint of the job did not reach the user's files")
		Expect(text).To(Equal("from the job\n"))
		// Another user does not see the job.
		other := w.actIn("job", "bob", "convjob1", map[string]any{"verb": "read", "id": id})
		Expect(other["ok"]).To(Equal(false))
		Expect(other["code"]).To(Equal("unknown"))
	})

	It("lets the Office add-on create, read and edit a document in the user's files", func() {
		w := startAddon("office")
		w.put("alice", "/seed.txt", "x")
		created := w.act("create-docx", "alice", "convaaaa", map[string]any{"path": "/report.docx", "markdown": "# Summary\n\nAll good."})
		Expect(created["ok"]).To(Equal(true), "create-docx = %v", created)
		Expect(created["path"]).To(Equal("/report.docx"), "create-docx = %v", created)
		read := w.read("read-docx", "alice", map[string]any{"path": "/report.docx"})
		Expect(read["ok"]).To(Equal(true), "read-docx = %v", read)
		Expect(string(mustJSON(read))).To(ContainSubstring("All good."), "read-docx = %v", read)
		edited := w.act("edit-docx", "alice", "convaaaa", map[string]any{"path": "/report.docx", "edits": []map[string]any{{"op": "replace", "find": "All good.", "text": "Mostly good."}}})
		Expect(edited["ok"]).To(Equal(true), "edit-docx = %v", edited)
		Expect(edited["conflict"]).NotTo(Equal(true), "edit-docx = %v", edited)
		again := w.read("read-docx", "alice", map[string]any{"path": "/report.docx"})
		Expect(string(mustJSON(again))).To(ContainSubstring("Mostly good."), "after the edit: %v", again)
		// The edit is a change of the user's files and can be undone like any.
		abs, _ := w.f.Abs("alice", "/report.docx")
		hist, err := w.f.Store().History(context.Background(), w.f.Actor("alice"), workspace.HistoryQuery{Path: abs})
		Expect(err).NotTo(HaveOccurred())
		Expect(len(hist)).To(BeNumerically(">=", 2), "history = %+v", hist)
		// Another user cannot read it, and a path outside the files is refused.
		got := w.read("read-docx", "bob", map[string]any{"path": "/report.docx"})
		Expect(got["ok"]).To(Equal(false), "bob read alice's document: %v", got)
		got = w.read("read-docx", "alice", map[string]any{"path": "../bob/x.docx"})
		Expect(got["ok"]).To(Equal(false), "a path outside the files was served: %v", got)
	})
})

var _ = Describe("Add-on calls of a chat whose agent has a folder", func() {
	const marker = "OUTSIDE-THE-AGENT-FOLDER-e2e"

	It("keeps the Office add-on inside the agent's folder", func() {
		w := startAddon("office")
		w.put("alice", "/Acme/seed.txt", "x")
		// Two documents made outside any chat: one in the agent's folder, one
		// outside it.
		for path, text := range map[string]string{"/Acme/in.docx": "Inside the folder.", "/Other/out.docx": marker} {
			made := w.act("create-docx", "alice", "convplain", map[string]any{"path": path, "markdown": text})
			Expect(made["ok"]).To(Equal(true), "create-docx %s = %v", path, made)
		}
		w.scopedChat("convscope", workspace.ModeWrite)

		// The folder is the add-on's "/".
		read := w.readIn("read-docx", "alice", "convscope", map[string]any{"path": "/in.docx"})
		Expect(read["ok"]).To(Equal(true), "read-docx = %v", read)
		Expect(string(mustJSON(read))).To(ContainSubstring("Inside the folder."))
		// Nothing outside it is reached, however the path is spelled.
		for _, p := range []string{"/Other/out.docx", "../Other/out.docx", "/../Other/out.docx", "/workspace/home/../Other/out.docx",
			"/users/alice/Other/out.docx", "..%2fOther/out.docx", "/Acme/../Other/out.docx"} {
			got := w.readIn("read-docx", "alice", "convscope", map[string]any{"path": p})
			Expect(got["ok"]).To(Equal(false), "read-docx %q = %v", p, got)
			Expect(string(mustJSON(got))).NotTo(ContainSubstring(marker), "read-docx %q", p)
		}
		// A document it makes lands in the folder, never at the home root.
		made := w.actIn("create-docx", "alice", "convscope", map[string]any{"path": "/new.docx", "markdown": "New."})
		Expect(made["ok"]).To(Equal(true), "create-docx = %v", made)
		Expect(made["path"]).To(Equal("/new.docx"), "the path in the add-on's terms")
		_, ok := w.text("alice", "/Acme/new.docx")
		Expect(ok).To(BeTrue(), "the new document is in the folder")
		_, ok = w.text("alice", "/new.docx")
		Expect(ok).To(BeFalse(), "the new document reached the home root")
		escaped := w.actIn("create-docx", "alice", "convscope", map[string]any{"path": "../escape.docx", "markdown": "x"})
		Expect(escaped["ok"]).To(Equal(false), "create-docx outside = %v", escaped)
		edited := w.actIn("edit-docx", "alice", "convscope", map[string]any{"path": "/Other/out.docx", "edits": []map[string]any{{"op": "replace", "find": marker, "text": "changed"}}})
		Expect(edited["ok"]).To(Equal(false), "edit-docx outside = %v", edited)
		text, _ := w.text("alice", "/Other/out.docx")
		Expect(text).NotTo(BeEmpty())

		// The same read with no chat is the person's own call: user-wide.
		own := w.read("read-docx", "alice", map[string]any{"path": "/Other/out.docx"})
		Expect(own["ok"]).To(Equal(true), "read-docx without a chat = %v", own)
	})

	It("gives the Sandbox the agent's folder at home and the chat folder next to it", func() {
		w := start()
		w.put("alice", "/Acme/a.txt", "inside\n")
		w.put("alice", "/Other/secret.txt", marker+"\n")
		w.read("confirm-host", "root", map[string]any{"phrase": hostPhrase})
		w.read("set-backend", "root", map[string]any{"backend": "host"})
		w.scopedChat("convscope", workspace.ModeWrite)
		got := w.actIn("bash", "alice", "convscope", map[string]any{"command": "cat a.txt; cat ../chat/note.txt; ls -R .. 2>&1; cat ../Other/secret.txt ../home/Other/secret.txt ../../Other/secret.txt 2>/dev/null; echo made > made.txt; echo work > ../chat/work.txt"})
		Expect(got["ok"]).To(Equal(true), "bash = %v", got)
		out := got["output"].(string)
		Expect(out).To(ContainSubstring("inside"))
		Expect(out).To(ContainSubstring("chat note"), "the chat folder is mounted next to the folder")
		Expect(out).NotTo(ContainSubstring(marker))
		Expect(out).NotTo(ContainSubstring("Other"), "no folder outside the agent's folder is listed")
		text, ok := w.text("alice", "/Acme/made.txt")
		Expect(ok).To(BeTrue(), "a file the command made lands in the folder")
		Expect(text).To(Equal("made\n"))
		_, ok = w.text("alice", "/made.txt")
		Expect(ok).To(BeFalse(), "never at the home root")
		text, ok = w.text("alice", "/Chats/convscope/work.txt")
		Expect(ok).To(BeTrue(), "a file in the chat folder lands there")
		Expect(text).To(Equal("work\n"))
	})

	It("keeps an action of a read-only agent from writing its folder", func() {
		w := startAddon("office")
		w.put("alice", "/Acme/seed.txt", "x")
		w.scopedChat("convread", workspace.ModeRead)
		made := w.actIn("create-docx", "alice", "convread", map[string]any{"path": "/new.docx", "markdown": "New."})
		Expect(made["ok"]).To(Equal(false), "create-docx in a read-only folder = %v", made)
		_, ok := w.text("alice", "/Acme/new.docx")
		Expect(ok).To(BeFalse())
		// Its chat folder stays writable.
		made = w.actIn("create-docx", "alice", "convread", map[string]any{"path": "/workspace/chat/new.docx", "markdown": "New."})
		Expect(made["ok"]).To(Equal(true), "create-docx in the chat folder = %v", made)
		_, ok = w.text("alice", "/Chats/convread/new.docx")
		Expect(ok).To(BeTrue())
	})
})

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
