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

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// hostPhrase is what an administrator types to accept the host backend.
const hostPhrase = "I understand that commands run on this host without isolation"

type world struct {
	t     *testing.T
	f     *files.Service
	inst  addonruntime.Instance
	gen   uint64
	opSeq int
	id    string
}

func start(t *testing.T) *world { return startAddon(t, "sandbox") }

// startAddon runs the add-on of that name: its executable is built, its
// manifest is the one in the source tree and its host operations are the
// workspace ones.
func startAddon(t *testing.T, id string) *world { return startAddonFrom(t, id, "") }

// startAddonFrom is startAddon for a package that was built and extracted by
// the release process (package.sh and addonpkg extract), when packageDir is
// set: its executable, runtime files and manifest are used as they are.
func startAddonFrom(t *testing.T, id, packageDir string) *world {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("the sandbox add-on is Linux only")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "pkg", id)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(repoRoot(t), "addons", id, "manifest.json")
	if packageDir != "" {
		if err := os.CopyFS(filepath.Dir(bin), os.DirFS(packageDir)); err != nil {
			t.Fatalf("copy the package: %v", err)
		}
		manifestPath = filepath.Join(packageDir, "manifest.json")
	} else {
		build := exec.Command("go", "build", "-o", bin, "./addons/"+id+"/cmd/"+id)
		build.Dir = repoRoot(t)
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	m.OS, m.Arch = goruntime.GOOS, goruntime.GOARCH

	dsn := "file:" + filepath.Join(root, "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewFS(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	f := files.New(ws)
	t.Cleanup(func() {
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
	if err := hostops.RegisterWorkspace(broker, &hostops.WorkspaceHost{
		Store: ws, Files: f,
		Principal: func(a contract.Actor) string { return a.UserID },
		DataDir:   func(id string) string { return filepath.Join(dataRoot, id) },
	}); err != nil {
		t.Fatal(err)
	}
	sup, err := addonruntime.NewSupervisor(addonruntime.Config{
		StateRoot: filepath.Join(root, "state"), DataRoot: dataRoot, Limits: limits,
		ReadyTimeout: 20 * time.Second, StableHealth: time.Second,
		HostOps: &addonruntime.HostOps{Broker: broker, InFlight: inflight, Allowed: map[string][]string{id: {"workspace."}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Close(context.Background()) })
	// The supervisor starts only a package whose files nobody else can change.
	if err := os.Chmod(bin, 0o500); err != nil {
		t.Fatal(err)
	}
	receipt := packages.PackageReceipt{Manifest: m, Directory: filepath.Dir(bin), Digest: "sha256:sandbox-e2e"}
	rb, _ := json.Marshal(receipt)
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "receipt.json"), rb, 0o400); err != nil {
		t.Fatal(err)
	}
	inst, err := sup.Start(context.Background(), receipt, 1)
	if err != nil {
		t.Fatalf("start the add-on: %v", err)
	}
	return &world{t: t, f: f, inst: inst, gen: 1, id: id}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatal("no go.mod above the test")
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
	w.t.Helper()
	if err != nil {
		w.t.Fatalf("call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		w.t.Fatalf("payload %s: %v", res.Payload, err)
	}
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

func (w *world) put(user, rel, content string) {
	w.t.Helper()
	ctx := context.Background()
	if err := w.f.EnsureHome(ctx, user); err != nil {
		w.t.Fatal(err)
	}
	abs, _ := w.f.Abs(user, rel)
	if _, err := w.f.Store().Put(ctx, w.f.Actor(user), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true}); err != nil {
		w.t.Fatal(err)
	}
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

func TestFromAChatToAFileInTheUsersFilesAndBackToAnUndo(t *testing.T) {
	w := start(t)
	ctx := context.Background()
	w.put("alice", "/notes.txt", "alice's notes\n")
	w.put("bob", "/notes.txt", "bob's notes\n")

	// Nothing isolated works here and the host backend is not chosen: no code runs.
	if got := w.act("bash", "alice", "convaaaa", map[string]any{"command": "echo no > home/never.txt"}); got["output"] != "This host cannot run commands." {
		t.Fatalf("before the host is chosen: %v", got)
	}
	if _, ok := w.text("alice", "/never.txt"); ok {
		t.Fatal("a command ran with no backend")
	}

	// Only an administrator can change the backend, and the host needs its phrase.
	if got := w.read("set-backend", "alice", map[string]any{"backend": "host"}); got["ok"] != false {
		t.Fatalf("a user changed the backend: %v", got)
	}
	if got := w.read("set-backend", "root", map[string]any{"backend": "host"}); got["ok"] != false {
		t.Fatalf("the host was chosen without its phrase: %v", got)
	}
	if got := w.read("confirm-host", "root", map[string]any{"phrase": hostPhrase}); got["ok"] != true {
		t.Fatalf("confirm-host: %v", got)
	}
	if got := w.read("set-backend", "root", map[string]any{"backend": "host"}); got["ok"] != true {
		t.Fatalf("set-backend: %v", got)
	}
	st := w.read("status", "alice", nil)
	if st["canRun"] != true || st["hostNotice"] != true {
		t.Fatalf("status = %v", st)
	}
	if _, has := st["backends"]; has {
		t.Fatalf("a user sees the details of the host: %v", st)
	}

	// A chat runs bash: it sees the user's files and writes one.
	got := w.act("bash", "alice", "convaaaa", map[string]any{"command": "cat home/notes.txt; echo made by bash > home/made.txt"})
	if got["ok"] != true || !strings.Contains(got["output"].(string), "alice's notes") {
		t.Fatalf("bash = %v", got)
	}
	if text, ok := w.text("alice", "/made.txt"); !ok || text != "made by bash\n" {
		t.Fatalf("the file in alice's files = %q, %v", text, ok)
	}

	// It is one change set by the chat, and the user can undo it.
	abs, _ := w.f.Abs("alice", "/made.txt")
	hist, err := w.f.Store().History(ctx, w.f.Actor("alice"), workspace.HistoryQuery{Path: abs})
	if err != nil || len(hist) != 1 || hist[0].Kind != workspace.ChangeAgent {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if _, _, err := w.f.Store().Undo(ctx, w.f.Actor("alice"), hist[0].Rev, workspace.UndoOptions{}); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if _, ok := w.text("alice", "/made.txt"); ok {
		t.Fatal("the file is still there after the undo")
	}

	// Another user in a chat with the same ID has a sandbox of their own and
	// sees only their own files.
	other := w.act("bash", "bob", "convaaaa", map[string]any{"command": "ls home; cat home/notes.txt"})
	text := other["output"].(string)
	if strings.Contains(text, "alice") || !strings.Contains(text, "bob's notes") {
		t.Fatalf("bob's sandbox = %q", text)
	}
	if _, ok := w.text("bob", "/made.txt"); ok {
		t.Fatal("alice's file reached bob")
	}

	// With the backend off, the tool says so and nothing runs.
	if got := w.read("set-backend", "root", map[string]any{"backend": "off"}); got["ok"] != true {
		t.Fatalf("set-backend off: %v", got)
	}
	if got := w.act("bash", "alice", "convaaaa", map[string]any{"command": "echo late > home/late.txt"}); got["output"] != "This host cannot run commands." {
		t.Fatalf("with the backend off: %v", got)
	}
	if _, ok := w.text("alice", "/late.txt"); ok {
		t.Fatal("a command ran with the backend off")
	}
}

func TestACallCannotBeMadeAsAnotherUserOrWithoutALiveRequest(t *testing.T) {
	w := start(t)
	w.put("alice", "/a.txt", "x")
	// The host operation channel works only while a request is live: a call from
	// the add-on with no envelope is refused. The add-on has no way to start one,
	// so the check is that an envelope of one user never reads another's files.
	w.read("confirm-host", "root", map[string]any{"phrase": hostPhrase})
	w.read("set-backend", "root", map[string]any{"backend": "host"})
	got := w.act("read", "bob", "convbbbb", map[string]any{"path": "home/a.txt"})
	if got["ok"] == true && strings.Contains(got["output"].(string), "x") {
		t.Fatalf("bob read alice's file: %v", got)
	}
}

func TestTheOfficeAddonCreatesReadsAndEditsADocumentInTheUsersFiles(t *testing.T) {
	w := startAddon(t, "office")
	w.put("alice", "/seed.txt", "x")
	created := w.act("create-docx", "alice", "convaaaa", map[string]any{"path": "/report.docx", "blocks": []map[string]any{
		{"kind": "heading", "level": 1, "text": "Summary"},
		{"kind": "paragraph", "text": "All good."},
	}})
	if created["ok"] != true || created["path"] != "/report.docx" {
		t.Fatalf("create-docx = %v", created)
	}
	read := w.read("read-docx", "alice", map[string]any{"path": "/report.docx"})
	if read["ok"] != true || !strings.Contains(string(mustJSON(read)), "All good.") {
		t.Fatalf("read-docx = %v", read)
	}
	edited := w.act("edit-docx", "alice", "convaaaa", map[string]any{"path": "/report.docx", "edits": []map[string]any{{"op": "replace", "find": "All good.", "text": "Mostly good."}}})
	if edited["ok"] != true || edited["conflict"] == true {
		t.Fatalf("edit-docx = %v", edited)
	}
	again := w.read("read-docx", "alice", map[string]any{"path": "/report.docx"})
	if !strings.Contains(string(mustJSON(again)), "Mostly good.") {
		t.Fatalf("after the edit: %v", again)
	}
	// The edit is a change of the user's files and can be undone like any.
	abs, _ := w.f.Abs("alice", "/report.docx")
	hist, err := w.f.Store().History(context.Background(), w.f.Actor("alice"), workspace.HistoryQuery{Path: abs})
	if err != nil || len(hist) < 2 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	// Another user cannot read it, and a path outside the files is refused.
	if got := w.read("read-docx", "bob", map[string]any{"path": "/report.docx"}); got["ok"] != false {
		t.Fatalf("bob read alice's document: %v", got)
	}
	if got := w.read("read-docx", "alice", map[string]any{"path": "../bob/x.docx"}); got["ok"] != false {
		t.Fatalf("a path outside the files was served: %v", got)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
