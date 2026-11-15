//go:build linux || darwin

package studio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// fenceToken is in the content and in the names of the files outside the
// agent's folder. The scripted model never writes it, so wherever it shows
// up (a tool result, a prompt, the transcript, an event, a card, a
// notification, a reply of a connector, a file an add-on received) something
// outside the fence leaked.
const fenceToken = "9d41c0"

// fenceMark is the content of every file outside the fence. A search for
// "FENCE-LEAK" matches it and the one file inside that says
// "FENCE-LEAK-inside".
const fenceMark = "FENCE-LEAK-" + fenceToken

// fenceRig is one frontend with every surface a chat reaches files through:
// the file and media tools on an indexed workspace, a generic and a saved
// sub-agent, the Sandbox add-on (its bash runs in a fake sandbox through the
// chat's command access), the Office add-on (its calls go through the real
// add-on workspace operations, bound to the chat), triggers, a connector and
// the inbox.
type fenceRig struct {
	*connRig
	ctx   context.Context
	f     *files.Service
	ix    *textindex.Indexer
	home  string
	fx    *fakeHostExec
	addon *fenceAddons
	m     *runner.Manager

	broker   *hostops.Broker
	inflight *hostops.InFlight
	office   http.Handler
	data     string

	mu     sync.Mutex
	steps  map[string][]studiotest.ToolStep
	events []string
	chats  []string
}

// fenceAddons is the add-on broker of the rig: the Sandbox's bash and the
// Office add-on's read and write tools.
type fenceAddons struct {
	r *fenceRig

	mu    sync.Mutex
	op    int
	convs []string // the chat of each call, as the host bound it
}

func (a *fenceAddons) Tools(context.Context, string) ([]studio.AddonTool, error) {
	bash := addonTool("sandbox", "bash")
	bash.Action, bash.Shadows = true, "bash"
	read := addonTool("office", "read-docx")
	write := addonTool("office", "write-docx")
	write.Action = true
	return []studio.AddonTool{bash, read, write}, nil
}

func (a *fenceAddons) next(conv string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.op++
	a.convs = append(a.convs, conv)
	return fmt.Sprintf("op%d", a.op)
}

func (a *fenceAddons) boundChats() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.convs...)
}

// Call runs the tool as the add-on would: the bash in a sandbox whose mounts
// come from the chat's command access, the Office tools through the host's
// workspace operations with the call bound to the chat.
func (a *fenceAddons) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	op := a.next(conv)
	switch name {
	case "addon__sandbox__bash":
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return a.r.m.Run(ctx, runner.Request{User: user, Conversation: conv, Operation: op, Tool: "bash", Args: args})
	case "addon__office__read-docx":
		var in struct{ Path string }
		_ = json.Unmarshal(args, &in)
		return a.r.officeRead(user, conv, op, in.Path)
	case "addon__office__write-docx":
		var in struct{ Path, Content string }
		_ = json.Unmarshal(args, &in)
		return a.r.officeWrite(user, conv, op, in.Path, in.Content)
	}
	return "", fmt.Errorf("no tool %s", name)
}

// hostCall makes one workspace operation of the Office add-on inside a live
// request bound to the chat, the way the runtime client records it.
func (r *fenceRig) hostCall(user, conv, op, scope, name, body string) (json.RawMessage, error) {
	env := contract.Envelope{
		Request:   contract.Request{AddonID: "office", OperationID: op, Generation: 1, Action: "start", Arguments: json.RawMessage(`{}`)},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "c1", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)},
	}
	end := r.inflight.BeginBound(env, hostops.Binding{Principal: user, ChatID: conv})
	defer end()
	b, _ := json.Marshal(contract.HostRequest{OperationID: op, Op: name, Arguments: json.RawMessage(body)})
	req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(b))
	req.Header.Set("X-Addon-Host-Credential", "secret")
	rec := httptest.NewRecorder()
	r.office.ServeHTTP(rec, req)
	var out contract.HostResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Error != nil {
		return nil, fmt.Errorf("%s: %s", out.Error.Code, out.Error.Message)
	}
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("status %d", rec.Code)
	}
	return out.Result, nil
}

// officeRead materializes the add-on's "/" and exports one file, then answers
// with the tree it saw and the file's text, as a reading tool would.
func (r *fenceRig) officeRead(user, conv, op, p string) (string, error) {
	dir := "tree-" + op
	if _, err := r.hostCall(user, conv, op, "read", "workspace.materialize", `{"mounts":[{"path":"/","at":"home"}],"dir":"`+dir+`"}`); err != nil {
		return "", err
	}
	var tree []string
	root := filepath.Join(r.data, "office", dir)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && path != root {
			rel, _ := filepath.Rel(root, path)
			tree = append(tree, rel)
		}
		return nil
	})
	out := "tree: " + strings.Join(tree, ", ")
	to := "export-" + op + ".txt"
	body, _ := json.Marshal(map[string]string{"path": p, "to": to})
	if _, err := r.hostCall(user, conv, op, "read", "workspace.export", string(body)); err != nil {
		return out, err
	}
	data, err := os.ReadFile(filepath.Join(r.data, "office", to))
	if err != nil {
		return out, err
	}
	return out + "\ntext: " + string(data), nil
}

// officeWrite writes content to the add-on's data directory and imports it
// into the files at p.
func (r *fenceRig) officeWrite(user, conv, op, p, content string) (string, error) {
	from := "import-" + op + ".txt"
	if err := os.WriteFile(filepath.Join(r.data, "office", from), []byte(content), 0o600); err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{"from": from, "path": p, "overwrite": true})
	res, err := r.hostCall(user, conv, op, "act", "workspace.import", string(body))
	return string(res), err
}

// newFenceFiles is a files service on a SQL workspace with the text index of
// the documents, as a server with a database builds it.
func newFenceFiles() (*files.Service, *textindex.Indexer) {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	st, err := textindex.NewGORM(db)
	Expect(err).NotTo(HaveOccurred())
	f := files.New(ws)
	ix := textindex.New(textindex.Options{Store: st, Workspace: ws, Admin: workspace.Actor{Principal: files.AdminPrincipal}})
	f.SetTextIndex(ix)
	return f, ix
}

func newFenceRig() *fenceRig {
	r := &fenceRig{ctx: context.Background(), home: session.HomePath(alice), fx: &fakeHostExec{}, steps: map[string][]studiotest.ToolStep{}}
	r.addon = &fenceAddons{r: r}
	r.f, r.ix = newFenceFiles()
	m, _ := newMediaService()
	sh := newShared()
	sh.files = r.f
	sh.deps = func(d *studio.Deps) {
		d.AddonTools = r.addon
		d.Media = m
		// The built-in bash is on: a scoped chat must still never get it.
		d.HostExec = r.fx
		d.HostExecMode = func() string { return "on" }
	}
	r.connRig = newConnRig(sh, connStore(), newScriptedBot(), r.script, nil)

	r.seed()

	var err error
	r.m, err = runner.New(runner.Options{
		Backend: func() (sandbox.Backend, error) { return sbtest.NewFakeBackend(GinkgoT().TempDir()), nil },
		Store:   r.f.Store(),
		Actor:   r.svc.CommandActor,
		View:    r.svc.CommandView,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = r.m.Close(context.Background()) })

	r.data = filepath.Join(GinkgoT().TempDir(), "addons")
	Expect(os.MkdirAll(filepath.Join(r.data, "office"), 0o700)).To(Succeed())
	r.inflight = hostops.NewInFlight()
	r.broker = hostops.New(r.inflight, func(string, string) bool { return true }, contract.DefaultLimits())
	Expect(hostops.RegisterWorkspace(r.broker, &hostops.WorkspaceHost{
		Store:     r.f.Store(),
		Principal: func(a contract.Actor) string { return a.UserID },
		DataDir:   func(id string) string { return filepath.Join(r.data, id) },
		Access:    r.svc.AddonAccess,
	})).To(Succeed())
	r.office = r.broker.Handler("office", "secret", []string{"workspace."})
	return r
}

// seed writes the files: the agent's folder /Projects/Acme, and around it the
// files that carry the token (a sibling whose name starts the same, the
// folder above, the home root, another folder with a document, an image,
// files whose names hold the token, more pending documents than a search
// checks one by one).
func (r *fenceRig) seed() {
	GinkgoHelper()
	Expect(r.f.EnsureHome(r.ctx, alice)).To(Succeed())
	r.put("/Projects/Acme/a.txt", "inside-a")
	r.put("/Projects/Acme/AGENTS.md", "acme rules")
	r.put("/Projects/Acme/brief.txt", "FENCE-LEAK-inside plan")
	r.put("/Projects/Acme/sub/s.txt", "inside-sub")
	r.mkdir("/Projects/Acme/In")
	r.put("/Projects/AGENTS.md", "projects rules "+fenceMark)
	r.put("/AGENTS.md", "home rules "+fenceMark)
	r.put("/Projects/Acme2/x.txt", fenceMark)
	r.put("/Vault/known.txt", fenceMark)
	r.put("/Vault/ledger-"+fenceToken+".txt", fenceMark)
	r.putBytes("/Vault/known.docx", extracttest.Docx(extracttest.Para("", fenceMark)))
	r.putBytes("/Vault/indexed-"+fenceToken+".pdf", extracttest.TextPDF("indexed plan "+fenceMark))
	r.putBytes("/Vault/photo.png", []byte("\x89PNG\r\n\x1a\n"+fenceMark))
	r.mkdir("/Vault/box-" + fenceToken)
	r.mkdir("/Vault/In")
	Expect(r.ix.Pass(r.ctx)).To(Succeed())
	// Documents that are still waiting for the index, all outside.
	for i := range 60 {
		r.putBytes(fmt.Sprintf("/Vault/draft-%s-%02d.pdf", fenceToken, i), extracttest.TextPDF(fmt.Sprintf("draft plan %d", i)))
	}
}

func (r *fenceRig) put(rel, content string) {
	GinkgoHelper()
	r.putBytes(rel, []byte(content))
}

func (r *fenceRig) putBytes(rel string, data []byte) {
	GinkgoHelper()
	n, err := r.f.Store().Put(r.ctx, r.f.Actor(alice), r.home+rel, bytes.NewReader(data), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
	r.f.Written(r.ctx, n, alice)
}

func (r *fenceRig) mkdir(rel string) {
	GinkgoHelper()
	_, err := r.f.Store().Mkdir(r.ctx, r.f.Actor(alice), r.home+rel)
	Expect(err).NotTo(HaveOccurred())
}

func (r *fenceRig) read(rel string) string {
	GinkgoHelper()
	rc, _, err := r.f.Store().Open(r.ctx, r.f.Actor(alice), r.home+rel)
	Expect(err).NotTo(HaveOccurred(), "open %s", rel)
	defer func() { _ = rc.Close() }()
	var b bytes.Buffer
	_, err = b.ReadFrom(rc)
	Expect(err).NotTo(HaveOccurred())
	return b.String()
}

func (r *fenceRig) exists(rel string) bool {
	_, err := r.f.Store().Stat(r.ctx, r.f.Actor(alice), r.home+rel)
	return err == nil
}

// script answers each message with the steps registered for a key the message
// holds (a trigger's or a connector's first message has more text around it).
func (r *fenceRig) script(_ workspace.Actor, text string) []studiotest.ToolStep {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, s := range r.steps {
		if strings.Contains(text, k) {
			return append(append([]studiotest.ToolStep(nil), s...), studiotest.ToolStep{Say: "done " + k})
		}
	}
	return []studiotest.ToolStep{{Say: "ok"}}
}

func (r *fenceRig) on(key string, steps ...studiotest.ToolStep) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps[key] = steps
}

// saveAgent saves an agent limited to folder at mode, with the add-ons and a
// model, so its triggers and connectors can run it.
func (r *fenceRig) saveAgent(name, folder, mode string) agents.Agent {
	GinkgoHelper()
	a, err := r.svc.SaveAgentWithFolder(r.ctx, alice, agents.Agent{Name: name, Model: "m", Addons: []string{"office", "sandbox"}},
		&studio.AgentFolderWrite{Path: folder, Mode: mode})
	Expect(err).NotTo(HaveOccurred())
	return a
}

// chat starts a chat with the agent and records its live events.
func (r *fenceRig) chat(agentID string) string {
	GinkgoHelper()
	rec, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: agentID})
	Expect(err).NotTo(HaveOccurred())
	ch, cancel, err := r.svc.Events(r.ctx, alice, rec.ID)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cancel)
	go func() {
		for e := range ch {
			b, _ := json.Marshal(e)
			r.mu.Lock()
			r.events = append(r.events, string(b))
			r.mu.Unlock()
		}
	}()
	r.mu.Lock()
	r.chats = append(r.chats, rec.ID)
	r.mu.Unlock()
	return rec.ID
}

// run sends the message with its steps and waits for the turn, and for any
// sub-agent it started, to end. It returns the tool results of the turn.
func (r *fenceRig) run(conv, key string, steps ...studiotest.ToolStep) []studiotest.ToolResult {
	GinkgoHelper()
	r.on(key, steps...)
	before, turns := len(r.h.Results()), len(r.h.Sent(conv))
	sent(r.svc, alice, conv, key)
	untilWithin(turnLimit, "the turn to end", func() bool {
		return len(r.h.Sent(conv)) > turns && !turn(r.svc, alice, conv).Running
	})
	r.settle()
	return r.h.Results()[before:]
}

// turnLimit bounds the wait for a probe turn to end. One turn calls about a
// hundred real file tools; that took 7 to 25 s under -race here and over 20 s
// on a CI runner, so the default 20 s bound of until was too tight.
const turnLimit = 5 * time.Minute

// settle waits until no chat of alice runs a turn.
func (r *fenceRig) settle() {
	GinkgoHelper()
	untilWithin(turnLimit, "every chat to be idle", func() bool {
		list, err := r.svc.List(r.ctx, alice, 500)
		if err != nil {
			return false
		}
		for _, c := range list {
			if turn(r.svc, alice, c.ID).Running {
				return false
			}
		}
		return true
	})
}

// corpus is every text a person or a model could see, with where it came
// from: tool results and errors, the prompts and messages of every chat, the
// transcripts with their cards, the live events, the chat file lists and tool
// panels, the notifications, the connector's replies and files, the add-on's
// data directory and the logs.
func (r *fenceRig) corpus() map[string]string {
	GinkgoHelper()
	out := map[string]string{}
	add := func(where, text string) { out[where] += text + "\n" }
	for i, x := range r.h.Results() {
		add(fmt.Sprintf("result %d %s (%s)", i, x.Tool, x.Chat), x.Out+" "+fmt.Sprint(x.Err))
	}
	list, err := r.svc.List(r.ctx, alice, 500)
	Expect(err).NotTo(HaveOccurred())
	for _, c := range list {
		j, _ := json.Marshal(c)
		add("chat record "+c.ID, string(j))
		add("system prompt "+c.ID, r.h.SystemPrompt(c.ID))
		add("turn prompt "+c.ID, r.h.TurnPrompt(c.ID))
		add("messages to the model "+c.ID, strings.Join(r.h.Sent(c.ID), "\n"))
		add("tools "+c.ID, strings.Join(r.h.ToolNames(c.ID), " "))
		if es, err := r.svc.Messages(r.ctx, alice, c.ID, 0, 0); err == nil {
			j, _ := json.Marshal(es)
			add("transcript "+c.ID, string(j))
		}
		if cf, err := r.svc.ChatFiles(r.ctx, alice, c.ID); err == nil {
			j, _ := json.Marshal(cf)
			add("chat files "+c.ID, string(j))
		}
		if p, err := r.svc.Tools(r.ctx, alice, c.ID); err == nil {
			j, _ := json.Marshal(p)
			add("tools panel "+c.ID, string(j))
		}
	}
	r.mu.Lock()
	add("events", strings.Join(r.events, "\n"))
	r.mu.Unlock()
	for _, n := range r.notes() {
		j, _ := json.Marshal(n)
		add("notification", string(j))
	}
	add("connector replies", strings.Join(r.bot.answers(), "\n"))
	for _, bf := range r.bot.sentFiles() {
		add("connector file "+bf.Name, bf.Name+" "+string(bf.Data))
	}
	_ = filepath.WalkDir(r.data, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			add("add-on data "+p, p+" "+string(b))
		}
		return nil
	})
	add("connector log", r.logs.String())
	return out
}

// expectNoLeak fails when the token is in any text of the corpus.
func (r *fenceRig) expectNoLeak() map[string]string {
	GinkgoHelper()
	c := r.corpus()
	for where, text := range c {
		Expect(text).NotTo(ContainSubstring(fenceToken), "the token leaked into %s", where)
	}
	Expect(r.fx.commands()).To(BeEmpty(), "a command ran on this machine")
	return c
}

// expectOutsideUnchanged checks that the files outside the folder are as
// seeded.
func (r *fenceRig) expectOutsideUnchanged() {
	GinkgoHelper()
	Expect(r.read("/Vault/known.txt")).To(Equal(fenceMark))
	Expect(r.read("/Projects/Acme2/x.txt")).To(Equal(fenceMark))
	Expect(r.read("/AGENTS.md")).To(Equal("home rules " + fenceMark))
	for _, rel := range []string{"/escape.txt", "/made.txt", "/Vault/made.txt", "/office.txt", "/Vault/office.txt", "/Projects/office.txt"} {
		Expect(r.exists(rel)).To(BeFalse(), "%s was written outside the folder", rel)
	}
}

func joinOut(res []studiotest.ToolResult) string {
	var b strings.Builder
	for _, x := range res {
		fmt.Fprintf(&b, "%s: %s %v\n", x.Tool, x.Out, x.Err)
	}
	return b.String()
}

// fileSteps are the calls of the file and media tools that try to reach the
// files outside: by their names (without the token), by parent references,
// guest and store spellings, listing, globbing and searching.
func fileSteps() []studiotest.ToolStep {
	var steps []studiotest.ToolStep
	for _, p := range []string{
		"/Vault/known.txt", "Vault/known.txt", "../Vault/known.txt", "/../Vault/known.txt", "../../Vault/known.txt",
		"/workspace/home/../Vault/known.txt", "/workspace/chat/../../Vault/known.txt", "/workspace/chat/../home/../../Vault/known.txt",
		"/users/" + alice + "/Vault/known.txt", "//users/" + alice + "/Vault/known.txt", "/workspace/users/" + alice + "/Vault/known.txt",
		"../Acme2/x.txt", "/workspace/home/../../Acme2/x.txt", "../AGENTS.md", "../../AGENTS.md",
		"/Workspace/Home/../Vault/known.txt", "..%2fVault%2fknown.txt", `..\Vault\known.txt`, "/workspace/shared-x/../home/../Vault/known.txt",
	} {
		steps = append(steps,
			call("read_file", fmt.Sprintf(`{"path":%q}`, p)),
			call("list_files", fmt.Sprintf(`{"path":%q}`, filepathDir(p))),
		)
	}
	steps = append(steps,
		call("read_document", `{"path":"../Vault/known.docx"}`),
		call("read_document", `{"path":"/workspace/home/../Vault/known.docx"}`),
		call("read_file", `{"path":"../Vault/known.docx"}`),
		call("describe_image", `{"path":"../Vault/photo.png"}`),
		call("describe_image", `{"path":"/Vault/photo.png"}`),
		call("list_files", `{"path":"/","pattern":"*"}`),
		call("list_files", `{"path":"/workspace","pattern":"*"}`),
		call("list_files", `{"path":"..","pattern":"*"}`),
		call("list_files", `{"path":"/workspace/chat/..","pattern":"*"}`),
		call("search_files", `{"query":"FENCE-LEAK"}`),
		call("search_files", `{"query":"FENCE-LEAK","path":"/"}`),
		call("search_files", `{"query":"plan"}`),
		call("search_files", `{"query":"draft"}`),
		call("search_files", `{"query":"ledger"}`),
		call("search_files", `{"query":"FENCE.LEAK-[0-9a-f]+","regex":true}`),
		call("search_files", `{"query":"plan","path":"/workspace/chat"}`),
		call("search_files", `{"query":"plan","path":".."}`),
	)
	return steps
}

func filepathDir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i > 0 {
		return p[:i]
	}
	return "/"
}

var _ = Describe("a chat of an agent limited to one folder, through every surface", func() {
	var r *fenceRig
	BeforeEach(func() { r = newFenceRig() })

	// The control: the same probes from a chat whose agent has no folder do
	// find the files outside, and the corpus does carry the token then. A
	// probe or a corpus that sees nothing would make every spec below pass
	// for the wrong reason.
	It("finds the token through the same probes when the agent has no folder", func() {
		a, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "Everything", Model: "m", Addons: []string{"office", "sandbox"}})
		Expect(err).NotTo(HaveOccurred())
		conv := r.chat(a.ID)
		res := r.run(conv, "k:control", append(fileSteps(),
			call("addon__office__read-docx", `{"path":"/Vault/known.txt"}`),
			call("bash", `{"command":"ls ../home/Vault; true"}`),
		)...)
		out := joinOut(res)
		Expect(out).To(ContainSubstring(fenceMark), "the probes read nothing outside")
		Expect(out).To(ContainSubstring("ledger-"+fenceToken), "the listing names no file outside")
		Expect(out).To(ContainSubstring("being indexed"), "the search counts no pending file")
		c := r.corpus()
		for _, where := range []string{"transcript " + conv, "events"} {
			Expect(c[where]).To(ContainSubstring(fenceToken), "the corpus does not see %s", where)
		}
		var addonOut string
		for _, x := range res {
			if x.Tool == "addon__office__read-docx" {
				addonOut = x.Out
			}
		}
		Expect(addonOut).To(ContainSubstring(fenceMark), "the add-on read nothing on the whole home")
	})

	It("keeps the file and media tools, search and its pending count, and both kinds of sub-agent inside the folders", func() {
		parent := r.saveAgent("Acme", "/Projects/Acme", agents.FolderWrite)
		r.saveAgent("Acme sub", "/Projects/Acme/sub", agents.FolderRead)
		r.saveAgent("Box keeper", "/Vault/box-"+fenceToken, agents.FolderWrite)
		// A trigger of the user that watches a folder outside: the chat's
		// trigger tools must not list it or its folder.
		_, err := r.host.CreateTrigger(r.ctx, alice, studio.TriggerInput{Name: "Outside watcher", Prompt: "x", Kind: triggers.KindFile,
			Folder: "/Vault/box-" + fenceToken, Quiet: time.Second, Model: "m", Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		conv := r.chat(parent.ID)
		allowSavedAgents(r.svc, conv)

		res := r.run(conv, "k:files", append(fileSteps(),
			call("read_file", `{"path":"/a.txt"}`),
			call("write_file", `{"path":"/workspace/chat/work/notes.md","content":"draft"}`),
			call("list_agents", `{}`),
			call("list_triggers", `{}`),
			call("propose_trigger", `{"name":"Up","prompt":"x","when":{"file_arrives_in":"../Vault/In"}}`),
			call("propose_trigger", `{"name":"Abs","prompt":"x","when":{"file_arrives_in":"/users/`+alice+`/Vault/In"}}`),
			call("propose_trigger", `{"name":"Guest","prompt":"x","when":{"file_arrives_in":"/workspace/home/../Vault/In"}}`),
		)...)
		out := joinOut(res)
		Expect(out).To(ContainSubstring("inside-a"), "the folder is readable")
		Expect(out).To(ContainSubstring("FENCE-LEAK-inside"), "the search finds the file inside")
		Expect(out).NotTo(ContainSubstring("being indexed"), "a search counted pending files outside the folder")
		Expect(out).NotTo(ContainSubstring("Box keeper"), "list_agents named an agent of another folder")
		Expect(out).To(ContainSubstring("Acme sub"))
		Expect(out).NotTo(ContainSubstring("Outside watcher"), "list_triggers named a trigger of another folder")

		r.on("k:generic-child",
			call("read_file", `{"path":"../Vault/known.txt"}`),
			call("list_files", `{"path":"/","pattern":"*"}`),
			call("search_files", `{"query":"FENCE-LEAK"}`),
			call("read_file", `{"path":"/a.txt"}`),
		)
		r.on("k:saved-child",
			call("read_file", `{"path":"/s.txt"}`),
			call("read_file", `{"path":"../a.txt"}`),
			call("read_file", `{"path":"../../Vault/known.txt"}`),
			call("list_files", `{"path":"..","pattern":"*"}`),
			call("search_files", `{"query":"FENCE-LEAK"}`),
		)
		res = r.run(conv, "k:spawn",
			call("spawn_agent", `{"task":"k:generic-child","wait":true}`),
			call("spawn_agent", `{"task":"k:saved-child","agent":"Acme sub","wait":true}`),
			call("spawn_agent", `{"task":"k:box","agent":"Box keeper","wait":true}`),
		)
		out = joinOut(res)
		Expect(out).To(ContainSubstring("inside-a"), "the generic child reads the parent's folder")
		Expect(out).To(ContainSubstring("inside-sub"), "the saved child reads its folder")
		Expect(childrenOf(r.svc, alice, conv)).NotTo(BeEmpty())
		var box studiotest.ToolResult
		for _, x := range res {
			if x.Tool == "spawn_agent" && x.Err != nil {
				box = x
			}
		}
		Expect(box.Err).To(HaveOccurred(), "an agent of a folder outside the chat's started: %s", out)

		r.expectNoLeak()
		r.expectOutsideUnchanged()
	})

	It("keeps a Sandbox command and the Office add-on's read and action calls inside the folders", func() {
		conv := r.chat(r.saveAgent("Acme", "/Projects/Acme", agents.FolderWrite).ID)
		res := r.run(conv, "k:addons",
			call("bash", `{"command":"ls; ls ..; cat a.txt; cat ../Vault/known.txt ../../Vault/known.txt ../AGENTS.md ../../AGENTS.md 2>&1; grep -r FENCE-LEAK .. 2>&1; ls -R .. 2>&1; true"}`),
			call("bash", `{"command":"echo made > made.txt; echo x > ../escape.txt; mkdir -p ../Vault; echo x > ../Vault/made.txt; echo kept > ../chat/kept.txt; true"}`),
			call("addon__office__read-docx", `{"path":"/a.txt"}`),
			call("addon__office__read-docx", `{"path":"/Vault/known.txt"}`),
			call("addon__office__read-docx", `{"path":"../Vault/known.txt"}`),
			call("addon__office__read-docx", `{"path":"/workspace/home/../Vault/known.txt"}`),
			call("addon__office__read-docx", `{"path":"/users/`+alice+`/Vault/known.txt"}`),
			call("addon__office__read-docx", `{"path":"/workspace/chat/../../Vault/known.txt"}`),
			call("addon__office__write-docx", `{"path":"/office.txt","content":"from office"}`),
			call("addon__office__write-docx", `{"path":"../office.txt","content":"x"}`),
			call("addon__office__write-docx", `{"path":"/users/`+alice+`/Vault/office.txt","content":"x"}`),
			call("addon__office__write-docx", `{"path":"/workspace/chat/../../Projects/office.txt","content":"x"}`),
		)
		Expect(res).To(HaveLen(12), joinOut(res))
		Expect(res[0].Err).NotTo(HaveOccurred(), joinOut(res))
		Expect(res[0].Out).To(ContainSubstring("inside-a"), "the command reads the folder")
		Expect(res[1].Out).To(ContainSubstring("was not saved"), "the command's writes outside are dropped and said so")
		Expect(res[2].Err).NotTo(HaveOccurred(), joinOut(res))
		Expect(res[2].Out).To(ContainSubstring("text: inside-a"), "the add-on reads the folder")
		Expect(res[2].Out).To(ContainSubstring("chat"), "the add-on sees the chat folder next to the agent's folder")
		Expect(res[8].Err).NotTo(HaveOccurred(), joinOut(res))
		Expect(r.read("/Projects/Acme/office.txt")).To(Equal("from office"), "the action wrote inside the folder")
		Expect(r.read("/Projects/Acme/made.txt")).To(Equal("made\n"))
		Expect(r.exists("/Projects/Acme/Vault/made.txt")).To(BeFalse())
		for _, c := range r.addon.boundChats() {
			Expect(c).To(Equal(conv), "an add-on call was not bound to its chat")
		}
		Expect(r.addon.boundChats()).To(HaveLen(12))

		r.expectNoLeak()
		r.expectOutsideUnchanged()
	})

	It("keeps a file trigger run and a connector reply inside the folder", func() {
		ag := r.saveAgent("Acme", "/Projects/Acme", agents.FolderWrite)
		r.connRig.agent = ag
		steps := append(fileSteps(),
			call("bash", `{"command":"cat ../Vault/known.txt 2>&1; grep -r FENCE-LEAK .. 2>&1; true"}`),
			call("addon__office__read-docx", `{"path":"../Vault/known.txt"}`),
			call("write_file", `{"path":"/out/report.csv","content":"a,b\n1,2\n"}`),
		)
		r.on("k:trigger", steps...)
		r.on("k:connector", steps...)

		tr, err := r.host.CreateTrigger(r.ctx, alice, studio.TriggerInput{Name: "Arrivals", Prompt: "Sort the new file. k:trigger", Kind: triggers.KindFile,
			Folder: "/Projects/Acme/In", Quiet: time.Second, AgentID: ag.ID, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.CreateTrigger(r.ctx, alice, studio.TriggerInput{Name: "Outside", Prompt: "x", Kind: triggers.KindFile,
			Folder: "/Vault/In", Quiet: time.Second, AgentID: ag.ID, Enabled: true})
		Expect(err).To(MatchError(studio.ErrBadTrigger), "a trigger of the agent watches a folder outside")
		r.pass()
		arrive := func(rel string) {
			r.put(rel, "hello")
			r.pass()
			r.sh.clock.Advance(2 * time.Second)
			r.pass()
		}
		arrive("/Vault/In/x-" + fenceToken + ".txt")
		arrive("/Projects/Acme/In/a.txt")
		run := r.waitEnded(tr.ID)
		// The run's tools are refused for want of a grant, so it ends as blocked; this spec is about something else.
		Expect(run.Status).To(BeElementOf(triggers.StatusDone, triggers.StatusBlocked), "run = %+v", run)
		runs, err := r.host.TriggerRuns(r.ctx, alice, tr.ID, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1), "a file outside the folder started a run")
		r.settle()

		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff})
		r.bot.say(1, 11, "Ada", "ada", "please k:connector")
		waitFor("the connector's answer", func() bool { return len(r.bot.answers()) >= 1 })
		r.settle()
		time.Sleep(300 * time.Millisecond) // a file or a note more would come now

		c := r.expectNoLeak()
		trigger := c["messages to the model "+run.ChatID]
		Expect(trigger).To(ContainSubstring("A new file arrived: /workspace/home/In/a.txt"))
		Expect(trigger).To(ContainSubstring("acme rules"))
		for _, bf := range r.bot.sentFiles() {
			Expect(bf.Name).To(Equal("report.csv"))
		}
		Expect(r.notes()).NotTo(BeEmpty(), "the run told nobody")
		r.expectOutsideUnchanged()
	})

	It("follows a renamed folder, and applies a mode change and a narrowing edit from the next turn", func() {
		ag := r.saveAgent("Acme", "/Projects/Acme", agents.FolderRead)
		conv := r.chat(ag.ID)
		res := r.run(conv, "k:read-only",
			call("read_file", `{"path":"/a.txt"}`),
			call("write_file", `{"path":"/n1.txt","content":"x"}`),
			call("addon__office__write-docx", `{"path":"/n1-office.txt","content":"x"}`),
			call("bash", `{"command":"echo x > n1-bash.txt; true"}`),
		)
		Expect(res[0].Out).To(ContainSubstring("inside-a"))
		Expect(res[1].Err).To(HaveOccurred(), "a read-only folder took a file")
		Expect(res[2].Err).To(HaveOccurred(), "a read-only folder took a file from an add-on")
		for _, f := range []string{"n1.txt", "n1-office.txt", "n1-bash.txt"} {
			Expect(r.exists("/Projects/Acme/"+f)).To(BeFalse(), f)
		}

		// Renamed and moved: the chat follows, and still reaches nothing else.
		r.mkdir("/Clients")
		_, err := r.f.Store().Move(r.ctx, r.f.Actor(alice), r.home+"/Projects/Acme", r.home+"/Clients/Acme Corp")
		Expect(err).NotTo(HaveOccurred())
		res = r.run(conv, "k:renamed", append(fileSteps(), call("read_file", `{"path":"/a.txt"}`))...)
		Expect(joinOut(res)).To(ContainSubstring("inside-a"))
		Expect(r.h.SystemPrompt(conv)).To(ContainSubstring(`the folder "Acme Corp" of the user (read only)`))

		// The mode changes: writes work from the next turn.
		_, err = r.svc.SaveAgentWithFolder(r.ctx, alice, ag, &studio.AgentFolderWrite{Path: "/Clients/Acme Corp", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		res = r.run(conv, "k:write",
			call("write_file", `{"path":"/n2.txt","content":"x"}`),
			call("addon__office__write-docx", `{"path":"/n2-office.txt","content":"x"}`),
		)
		Expect(res[0].Err).NotTo(HaveOccurred(), joinOut(res))
		Expect(res[1].Err).NotTo(HaveOccurred(), joinOut(res))
		Expect(r.exists("/Clients/Acme Corp/n2.txt")).To(BeTrue())
		Expect(r.exists("/Clients/Acme Corp/n2-office.txt")).To(BeTrue())

		// The edit narrows the folder: from the next turn the chat sees only
		// the subfolder, on this frontend and on another one.
		_, err = r.svc.SaveAgentWithFolder(r.ctx, alice, ag, &studio.AgentFolderWrite{Path: "/Clients/Acme Corp/sub", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		res = r.run(conv, "k:narrow",
			call("read_file", `{"path":"/s.txt"}`),
			call("read_file", `{"path":"/a.txt"}`),
			call("read_file", `{"path":"../a.txt"}`),
			call("list_files", `{"path":"/","pattern":"*"}`),
			call("addon__office__read-docx", `{"path":"../a.txt"}`),
			call("bash", `{"command":"cat ../a.txt a.txt 2>&1; ls -R .. 2>&1; true"}`),
		)
		out := joinOut(res)
		Expect(res[0].Out).To(ContainSubstring("inside-sub"))
		Expect(out).NotTo(ContainSubstring("inside-a"), "the narrowed chat still reads the old folder")
		Expect(out).NotTo(ContainSubstring("brief.txt"))
		x, err := r.svc.AddonAccess(r.ctx, alice, conv)
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Place().PrimaryWritable()).To(BeFalse())

		other := r.sh.rig(r.script, studio.TriggerOptions{})
		y, err := other.svc.ChatAccess(r.ctx, alice, conv)
		Expect(err).NotTo(HaveOccurred())
		Expect(y.Place().Primary()).To(Equal(r.home+"/Clients/Acme Corp/sub"), "another frontend resolves the edit too")

		r.expectNoLeak()
		r.expectOutsideUnchanged()
	})

	It("stops every surface when the folder is deleted, and never falls back to the home", func() {
		ag := r.saveAgent("Acme", "/Projects/Acme", agents.FolderWrite)
		r.connRig.agent = ag
		conv := r.chat(ag.ID)
		r.run(conv, "k:before", call("read_file", `{"path":"/a.txt"}`))
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID, Prompt: "Review. k:scheduled"})
		r.on("k:scheduled", fileSteps()...)
		r.on("k:after", fileSteps()...)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff})

		Expect(r.f.Store().Delete(r.ctx, r.f.Actor(alice), r.home+"/Projects/Acme")).To(Succeed())
		before := len(r.h.Results())

		_, err := r.svc.Send(r.ctx, alice, conv, "k:after")
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
		_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: ag.ID})
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
		_, err = r.svc.AddonAccess(r.ctx, alice, conv)
		Expect(err).To(HaveOccurred(), "an add-on call of the chat would run")
		_, err = r.addon.Call(r.ctx, alice, conv, "addon__office__read-docx", json.RawMessage(`{"path":"/a.txt"}`))
		Expect(err).To(HaveOccurred(), "an add-on call of the chat ran")
		_, err = r.addon.Call(r.ctx, alice, conv, "addon__sandbox__bash", json.RawMessage(`{"command":"ls -R .. 2>&1"}`))
		Expect(err).To(HaveOccurred(), "a command of the chat ran")
		_, err = r.svc.CommandActor(r.ctx, alice, conv)
		Expect(err).To(HaveOccurred())

		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.Reason).To(Equal("the agent's folder is missing"))

		r.bot.say(1, 11, "Ada", "ada", "k:after")
		waitFor("the connector's answer", func() bool { return len(r.bot.answers()) >= 1 })
		Expect(r.bot.answers()).To(Equal([]string{"This agent cannot work right now: its folder is missing."}))
		r.settle()

		Expect(r.h.Results()[before:]).To(BeEmpty(), "a tool ran after the folder was deleted")
		r.expectNoLeak()
		r.expectOutsideUnchanged()
	})
})
