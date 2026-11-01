package convert_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/convert"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

const (
	alice = "11111111-1111-4111-8111-111111111111"
	bob   = "22222222-2222-4222-8222-222222222222"
	token = "999:FIXTURE-bot-token-never-in-a-report"
)

// fakeTargets records everything that is converted.
type fakeTargets struct {
	mu       sync.Mutex
	users    map[string]bool
	cols     map[string][]string
	agents   map[string][]convert.AgentIn // by user
	nextID   int
	triggers map[string][]convert.TriggerIn
	skills   map[string]convert.SkillRec
	chats    map[string][]convert.ChatIn
	bindings map[string][]convert.BindingIn
	failSave map[string]bool // agent names whose save fails
	colErr   error           // what Collections returns
	// invalidSave names the agents whose save is refused for good; trigErr is
	// what CreateTrigger returns; importErr runs at each ImportChat and may
	// leave a half chat behind, as a crash does.
	invalidSave map[string]bool
	trigErr     error
	importErr   func(f *fakeTargets, user string, in convert.ChatIn) error
	saves       int
	// admins are the users who may have a local command; mcp is what was made.
	admins map[string]bool
	mcp    map[string][]convert.MCPIn
	// mcpErr is what CreateMCPConnector returns, by connector name; it runs
	// before the name check.
	mcpErr map[string]error
}

func newTargets() *fakeTargets {
	return &fakeTargets{users: map[string]bool{alice: true, bob: true, "": true}, cols: map[string][]string{},
		agents: map[string][]convert.AgentIn{}, triggers: map[string][]convert.TriggerIn{}, skills: map[string]convert.SkillRec{},
		chats: map[string][]convert.ChatIn{}, bindings: map[string][]convert.BindingIn{}, failSave: map[string]bool{},
		invalidSave: map[string]bool{},
		admins:      map[string]bool{}, mcp: map[string][]convert.MCPIn{}, mcpErr: map[string]error{}}
}

func (f *fakeTargets) IsAdmin(_ context.Context, u string) (bool, error) { return f.admins[u], nil }
func (f *fakeTargets) CreateMCPConnector(_ context.Context, u string, in convert.MCPIn) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mcpErr[in.Name]; err != nil {
		return "", err
	}
	for _, c := range f.mcp[u] {
		if c.Name == in.Name {
			return "", fmt.Errorf("%w: %s", convert.ErrNameTaken, in.Name)
		}
	}
	f.mcp[u] = append(f.mcp[u], in)
	return fmt.Sprintf("mcp-%s-%d", u[:1], len(f.mcp[u])), nil
}

func (f *fakeTargets) UserExists(_ context.Context, u string) (bool, error) { return f.users[u], nil }
func (f *fakeTargets) Collections(_ context.Context, u string) ([]string, error) {
	if f.colErr != nil {
		return nil, f.colErr
	}
	return f.cols[u], nil
}
func (f *fakeTargets) SaveAgent(_ context.Context, u string, in convert.AgentIn) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.invalidSave[in.Name] {
		return "", fmt.Errorf("%w: name", convert.ErrInvalid)
	}
	if f.failSave[in.Name] {
		return "", errors.New("disk full")
	}
	for _, a := range f.agents[u] {
		if a.Name == in.Name {
			return "", fmt.Errorf("%w: %s", convert.ErrNameTaken, in.Name)
		}
	}
	f.agents[u] = append(f.agents[u], in)
	f.nextID++
	return fmt.Sprintf("agent-%d", f.nextID), nil
}
func (f *fakeTargets) CreateTrigger(_ context.Context, u string, in convert.TriggerIn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.trigErr != nil {
		return f.trigErr
	}
	f.triggers[u] = append(f.triggers[u], in)
	return nil
}
func skillKey(u string, shared bool, name string) string {
	if shared {
		return "shared/" + name
	}
	return u + "/" + name
}
func (f *fakeTargets) SkillExists(_ context.Context, u string, shared bool, name string) (bool, error) {
	_, ok := f.skills[skillKey(u, shared, name)]
	return ok, nil
}
func (f *fakeTargets) WriteSkill(_ context.Context, u string, shared bool, name, desc, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skills[skillKey(u, shared, name)] = convert.SkillRec{Name: name, Description: desc, Body: body}
	return nil
}
func (f *fakeTargets) ImportChat(_ context.Context, u string, in convert.ChatIn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.importErr != nil {
		return f.importErr(f, u, in)
	}
	f.chats[u] = append(f.chats[u], in)
	return nil
}
func (f *fakeTargets) DiscardChat(_ context.Context, u, title string, created time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []convert.ChatIn
	for _, c := range f.chats[u] {
		if c.Title != title || c.Created.UnixMilli() != created.UnixMilli() {
			keep = append(keep, c)
		}
	}
	f.chats[u] = keep
	return nil
}
func (f *fakeTargets) CreateBinding(_ context.Context, u string, in convert.BindingIn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[u] = append(f.bindings[u], in)
	return nil
}

func memMarks() convert.Marks {
	m, err := convert.NewJSONMarks("")
	Expect(err).NotTo(HaveOccurred())
	return m
}

func write(path, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func poolFixture(dir string) {
	pool := map[string]any{
		alice + ":Support": map[string]any{
			"name": alice + ":Support", "description": "Answers customers", "model": "llama", "system_prompt": "Be kind.",
			"permanent_goal": "Keep the queue empty.", "enable_kb": true, "enable_skills": true, "selected_skills": []string{"triage"},
			"mcp_servers": []any{map[string]any{"url": "https://mcp.example/x", "token": "SECRET"}}, "actions": []any{map[string]any{"name": "search"}},
			"enable_planning": true, "standalone_job": true, "periodic_runs": "30m",
			"connectors": []any{
				map[string]any{"type": "telegram", "config": mustJSON(map[string]any{"token": token, "admins": "ada_l, @bo_b, x"})},
				map[string]any{"type": "slack", "config": mustJSON(map[string]any{"botToken": "xoxb-SLACK-SECRET"})},
			},
		},
		bob + ":Plain": map[string]any{"name": bob + ":Plain", "model": "", "system_prompt": ""},
		"Legacy":       map[string]any{"name": "Legacy", "model": "llama", "system_prompt": "root agent"},
	}
	write(filepath.Join(dir, "pool.json"), mustJSON(pool))
}

func run(src convert.Sources, tg convert.Targets, marks convert.Marks) convert.Report {
	r, err := convert.Run(context.Background(), src, tg, marks)
	Expect(err).NotTo(HaveOccurred())
	return r
}

func find(r convert.Report, kind, name string) (convert.Item, bool) {
	for _, i := range r.Items {
		if i.Kind == kind && i.Name == name {
			return i, true
		}
	}
	return convert.Item{}, false
}

func newDB() *gorm.DB {
	db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "c.db")+"?_busy_timeout=5000", &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if s, err := db.DB(); err == nil {
			_ = s.Close()
		}
	})
	return db
}

func exec(db *gorm.DB, q string, args ...any) {
	Expect(db.Exec(q, args...).Error).To(Succeed())
}

var _ = Describe("converting the old agents", func() {
	It("turns an agent of the pool into a saved agent with its grants and counts the rest as dropped", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		tg := newTargets()
		tg.cols[alice] = []string{"Support", "docs"}
		r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())

		got := tg.agents[alice]
		Expect(got).To(HaveLen(1), "alice's agents = %+v", got)
		a := got[0]
		Expect(a.Name).To(Equal("Support"))
		Expect(a.Description).To(Equal("Answers customers"))
		Expect(a.Model).To(Equal("llama"))
		Expect(a.Instructions).To(ContainSubstring("Be kind."))
		Expect(a.Instructions).To(ContainSubstring("Keep the queue empty."))
		Expect(a.Collections).To(Equal([]string{"Support"}))
		Expect(a.Skills).To(Equal([]string{"triage"}))
		it, ok := find(r, convert.KindAgent, "Support")
		Expect(ok).To(BeTrue())
		Expect(it.Status).To(Equal(convert.StatusConverted))
		for _, d := range []string{"actions", "enable_planning"} {
			Expect(it.Reason).To(ContainSubstring(d), "the reason does not name the dropped field %s", d)
		}
		Expect(it.Reason).NotTo(ContainSubstring("mcp.example"), "the reason has a value")
		Expect(it.Reason).NotTo(ContainSubstring("SECRET"), "the reason has a value")
	})

	It("turns a periodic agent into a switched-off trigger and still converts an agent with no model", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		tg := newTargets()
		run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
		trs := tg.triggers[alice]
		Expect(trs).To(HaveLen(1))
		Expect(trs[0].Schedule).To(Equal("*/30 * * * *"))
		Expect(trs[0].Model).To(Equal("llama"))
		Expect(trs[0].AgentID).To(Equal("agent-1"))
		Expect(trs[0].Name).To(ContainSubstring("Support"))
		Expect(trs[0].Prompt).To(ContainSubstring("Keep the queue empty."))
		got := tg.agents[bob]
		Expect(got).To(HaveLen(1), "bob's agents = %+v", got)
		Expect(got[0].Name).To(Equal("Plain"))
		Expect(got[0].Model).To(BeEmpty())
		Expect(tg.triggers[bob]).To(BeEmpty(), "an agent that is not autonomous got a trigger")
	})

	It("gives a root agent to the anonymous user only when there are no accounts", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		with := newTargets()
		r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, with, memMarks())
		it, _ := find(r, convert.KindAgent, "Legacy")
		Expect(it.Status).To(Equal(convert.StatusSkipped), "with accounts: %+v", it)
		Expect(it.Reason).To(ContainSubstring("no owner"), "with accounts: %+v", it)
		Expect(with.agents[""]).To(BeEmpty(), "a root agent was given to someone")
		without := newTargets()
		run(convert.Sources{StateDir: dir, AuthEnabled: false}, without, memMarks())
		Expect(without.agents[""]).To(HaveLen(1), "without accounts: %+v", without.agents)
		Expect(without.agents[""][0].Name).To(Equal("Legacy"))
	})

	It("converts only the Telegram connector and keeps its token out of the report", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		tg := newTargets()
		r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
		bs := tg.bindings[alice]
		Expect(bs).To(HaveLen(1), "bindings = %+v", bs)
		Expect(bs[0].Token).To(Equal(token))
		Expect(bs[0].AgentID).To(Equal("agent-1"))
		Expect(bs[0].Allow).To(Equal([]string{"@ada_l", "@bo_b"}), "want the admins that are valid usernames")
		it, ok := find(r, convert.KindConnector, "Support: slack")
		Expect(ok).To(BeTrue(), "slack = %+v", it)
		Expect(it.Status).To(Equal(convert.StatusDropped))
		raw, _ := json.Marshal(r)
		for _, secret := range []string{token, "xoxb-SLACK-SECRET", "SECRET", "mcp.example", "Keep the queue empty", "Be kind"} {
			Expect(string(raw)).NotTo(ContainSubstring(secret), "the report has %q", secret)
		}
	})

	It("turns a cron task into a switched-off trigger and drops the other tasks", func() {
		dir := GinkgoT().TempDir()
		write(filepath.Join(dir, "users", alice, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
			map[string]any{"id": "t1", "name": "Nightly", "model": "llama", "prompt": "Summarise {{.topic}} for me", "cron": "0 3 * * *", "cron_parameters": map[string]string{"topic": "sales"}, "enabled": true,
				"webhooks": []any{map[string]any{"url": "https://hook.example/x"}}},
			map[string]any{"id": "t2", "name": "Manual only", "model": "llama", "prompt": "x"},
			map[string]any{"id": "t3", "name": "With seconds", "model": "llama", "prompt": "x", "cron": "0 0 3 * * *"},
		}}))
		tg := newTargets()
		r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
		trs := tg.triggers[alice]
		Expect(trs).To(HaveLen(1), "triggers = %+v", trs)
		Expect(trs[0].Schedule).To(Equal("0 3 * * *"))
		Expect(trs[0].Prompt).To(Equal("Summarise sales for me"))
		Expect(trs[0].Model).To(Equal("llama"))
		Expect(trs[0].Name).To(Equal("Nightly"))
		it, _ := find(r, convert.KindTrigger, "Nightly")
		Expect(it.Status).To(Equal(convert.StatusConverted), "nightly = %+v", it)
		Expect(it.Reason).To(ContainSubstring("webhooks"), "nightly = %+v", it)
		for _, n := range []string{"Manual only", "With seconds"} {
			it, ok := find(r, convert.KindTrigger, n)
			Expect(ok).To(BeTrue(), "%s = %+v", n, it)
			Expect(it.Status).To(Equal(convert.StatusDropped), "%s = %+v", n, it)
		}
	})

	It("copies the skills of the folders and of the table to the right layer", func() {
		dir := GinkgoT().TempDir()
		skill := "---\nname: triage\ndescription: Sort the queue\n---\nRead each item.\n"
		write(filepath.Join(dir, "skills", "house", "SKILL.md"), "---\nname: house\ndescription: House style\n---\nWrite plainly.\n")
		write(filepath.Join(dir, "users", alice, "skills", "skills", "triage", "SKILL.md"), skill)
		write(filepath.Join(dir, "users", alice, "skills", "skills", "triage", "notes.txt"), "a resource")
		write(filepath.Join(dir, "users", alice, "skills", "skills", "broken", "SKILL.md"), "no frontmatter at all")
		db := newDB()
		exec(db, `CREATE TABLE skills_metadata (id text, user_id text, name text, definition text, source_type text, source_url text, enabled bool)`)
		exec(db, `INSERT INTO skills_metadata (id, user_id, name, definition, source_type, enabled) VALUES ('1', ?, 'from-db', ?, 'inline', 1)`, bob, "---\nname: from-db\ndescription: From the database\n---\nBody.\n")
		exec(db, `INSERT INTO skills_metadata (id, user_id, name, definition, source_type, source_url, enabled) VALUES ('2', ?, 'from-git', 'x', 'git', 'https://git.example/repo', 1)`, bob)
		tg := newTargets()
		r := run(convert.Sources{StateDir: dir, DB: db, AuthEnabled: true}, tg, memMarks())
		s := tg.skills["shared/house"]
		Expect(s.Description).To(Equal("House style"), "shared skill = %+v", s)
		Expect(strings.TrimSpace(s.Body)).To(Equal("Write plainly."), "shared skill = %+v", s)
		s = tg.skills[alice+"/triage"]
		Expect(s.Description).To(Equal("Sort the queue"), "personal skill = %+v", s)
		Expect(strings.TrimSpace(s.Body)).To(Equal("Read each item."), "personal skill = %+v", s)
		s = tg.skills[bob+"/from-db"]
		Expect(s.Description).To(Equal("From the database"), "table skill = %+v", s)
		it, _ := find(r, convert.KindSkill, "triage")
		Expect(it.Reason).To(ContainSubstring("resource"), "the resource file was not mentioned: %+v", it)
		it, _ = find(r, convert.KindSkill, "broken")
		Expect(it.Status).To(BeElementOf(convert.StatusFailed, convert.StatusDropped), "broken = %+v", it)
		it, _ = find(r, convert.KindSkill, "from-git")
		Expect(it.Status).To(Equal(convert.StatusDropped), "git skill = %+v", it)
		Expect(it.Reason).To(ContainSubstring("git"), "git skill = %+v", it)
	})

	It("converts the agents, tasks and history of a distributed database", func() {
		db := newDB()
		exec(db, `CREATE TABLE agent_configs (id text, user_id text, name text, config text, status text)`)
		exec(db, `CREATE TABLE tasks (id text, user_id text, name text, prompt text, model text, enabled bool, cron text, cron_parameters text, webhooks text)`)
		exec(db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, created_at datetime, updated_at datetime, deleted_at datetime)`)
		exec(db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('1', ?, 'Research', ?, 'active')`, alice, mustJSON(map[string]any{"name": "Research", "model": "m1", "system_prompt": "Find things."}))
		exec(db, `INSERT INTO tasks (id, user_id, name, prompt, model, enabled, cron, cron_parameters) VALUES ('t', ?, 'Weekly', 'Report', 'm1', 1, '0 9 * * 1', '{}')`, bob)
		hist := mustJSON([]any{
			map[string]any{"role": "user", "content": "hello"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hi there"}}},
			map[string]any{"role": "thinking", "content": "hmm"},
			map[string]any{"role": "tool_call", "name": "search", "content": "{}"},
			map[string]any{"role": "tool_result", "name": "search", "content": "found"},
		})
		conv := mustJSON(map[string]any{"id": "c1", "name": "A chat", "model": "m1", "history": json.RawMessage(hist), "createdAt": 1700000000000, "updatedAt": 1700000100000})
		exec(db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c1', ?, 1)`, alice, conv)
		exec(db, `INSERT INTO chat_conversations (user_id, conv_id, content, version, deleted_at) VALUES (?, 'c2', '{}', 2, '2026-01-01')`, alice)
		tg := newTargets()
		r := run(convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks())
		got := tg.agents[alice]
		Expect(got).To(HaveLen(1), "agents = %+v", got)
		Expect(got[0].Name).To(Equal("Research"))
		Expect(got[0].Instructions).To(Equal("Find things."))
		trs := tg.triggers[bob]
		Expect(trs).To(HaveLen(1), "triggers = %+v", trs)
		Expect(trs[0].Schedule).To(Equal("0 9 * * 1"))
		cs := tg.chats[alice]
		Expect(cs).To(HaveLen(1), "chats = %+v (the deleted one must not convert)", cs)
		c := cs[0]
		Expect(c.Title).To(Equal("A chat"))
		Expect(c.Model).To(Equal("m1"))
		Expect(c.Created.Equal(time.UnixMilli(1700000000000))).To(BeTrue(), "chat = %+v", c)
		Expect(c.Updated.Equal(time.UnixMilli(1700000100000))).To(BeTrue(), "chat = %+v", c)
		roles := []string{}
		for _, e := range c.Entries {
			roles = append(roles, e.Role)
		}
		Expect(strings.Join(roles, ",")).To(Equal("user,assistant,reasoning,tool"), "entries = %+v", c.Entries)
		Expect(c.Entries[0].Text).To(Equal("hello"))
		Expect(c.Entries[1].Text).To(Equal("hi there"))
		it, ok := find(r, convert.KindChat, "A chat")
		Expect(ok).To(BeTrue(), "chat item = %+v", it)
		Expect(it.Status).To(Equal(convert.StatusConverted))
	})

	It("keeps the newest messages of a huge history", func() {
		db := newDB()
		exec(db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
		var h []any
		for i := range convert.MaxMessages + 50 {
			h = append(h, map[string]any{"role": "user", "content": fmt.Sprintf("m%d", i)})
		}
		conv := mustJSON(map[string]any{"id": "big", "name": "Big", "model": "m", "history": h})
		exec(db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'big', ?, 1)`, alice, conv)
		tg := newTargets()
		run(convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks())
		es := tg.chats[alice][0].Entries
		Expect(es).To(HaveLen(convert.MaxMessages))
		Expect(es[len(es)-1].Text).To(Equal(fmt.Sprintf("m%d", convert.MaxMessages+49)))
		Expect(es[0].Text).To(Equal("m50"))
	})

	It("converts nothing twice on a repeated run and tries a failed item again", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		tg := newTargets()
		tg.failSave["Plain"] = true
		marks := memMarks()
		src := convert.Sources{StateDir: dir, AuthEnabled: true}
		first := run(src, tg, marks)
		it, _ := find(first, convert.KindAgent, "Plain")
		Expect(it.Status).To(Equal(convert.StatusFailed), "failed item = %+v", it)
		Expect(it.Reason).To(ContainSubstring("disk full"), "failed item = %+v", it)
		nAgents, nTriggers, nBindings := len(tg.agents[alice]), len(tg.triggers[alice]), len(tg.bindings[alice])
		delete(tg.failSave, "Plain")
		second := run(src, tg, marks)
		Expect(tg.agents[alice]).To(HaveLen(nAgents), "a second run converted an item again")
		Expect(tg.triggers[alice]).To(HaveLen(nTriggers), "a second run converted an item again")
		Expect(tg.bindings[alice]).To(HaveLen(nBindings), "a second run converted an item again")
		Expect(tg.agents[bob]).To(HaveLen(1), "the failed agent was not tried again: %+v", tg.agents[bob])
		it, _ = find(second, convert.KindAgent, "Support")
		Expect(it.Status).To(Equal(convert.StatusSkipped), "an item already converted is %+v", it)
		third := run(src, tg, marks)
		for _, it := range third.Items {
			Expect(it.Status).NotTo(BeElementOf(convert.StatusConverted, convert.StatusFailed), "a third run did work: %+v", it)
		}
	})

	It("numbers a name that is taken and skips an unknown user", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		tg := newTargets()
		tg.agents[alice] = []convert.AgentIn{{Name: "Support"}}
		tg.users[bob] = false
		r := run(convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks())
		names := []string{}
		for _, a := range tg.agents[alice] {
			names = append(names, a.Name)
		}
		Expect(strings.Join(names, ",")).To(Equal("Support,Support (2)"))
		it, _ := find(r, convert.KindAgent, "Plain")
		Expect(it.Status).To(Equal(convert.StatusSkipped), "unknown user = %+v", it)
		Expect(it.Reason).To(ContainSubstring("no account"), "unknown user = %+v", it)
		Expect(tg.agents[bob]).To(BeEmpty(), "an agent was made for a user that does not exist")
	})

	It("keeps the report per user so one user never sees another user's items", func() {
		dir := GinkgoT().TempDir()
		poolFixture(dir)
		marks := memMarks()
		run(convert.Sources{StateDir: dir, AuthEnabled: true}, newTargets(), marks)
		stored, ok, err := marks.Report(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(stored.Items).NotTo(BeEmpty())
		for _, i := range stored.ForUser(bob).Items {
			Expect(i.User).To(Equal(bob), "bob's report has %+v", i)
		}
		Expect(stored.ForUser("nobody").Items).To(BeEmpty(), "a stranger has items")
	})

	It("does not treat missing sources as an error", func() {
		r := run(convert.Sources{StateDir: GinkgoT().TempDir(), DB: newDB(), AuthEnabled: true}, newTargets(), memMarks())
		Expect(r.Items).To(BeEmpty())
		_, err := convert.Run(context.Background(), convert.Sources{}, newTargets(), memMarks())
		Expect(err).NotTo(HaveOccurred(), "no sources at all")
	})

	// Covered through the agent and task tests; this pins the table.
	It("pins the intervals and cron texts", func() {
		for in, want := range map[string]string{"10m": "*/10 * * * *", "1h": "0 */1 * * *", "6h": "0 */6 * * *", "24h": "0 0 */1 * *", "48h": "0 0 */2 * *"} {
			got, ok := convert.ScheduleFromInterval(in)
			Expect(ok).To(BeTrue(), "%s = %q", in, got)
			Expect(got).To(Equal(want), in)
		}
		for _, in := range []string{"", "5s", "90m", "abc", "36h"} {
			got, ok := convert.ScheduleFromInterval(in)
			Expect(ok).To(BeFalse(), "%q gave %q", in, got)
		}
		_, ok := convert.ScheduleFromCron("0 0 3 * * *")
		Expect(ok).To(BeFalse(), "a six-field cron was accepted")
		got, ok := convert.ScheduleFromCron("  0 3  * * * ")
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal("0 3 * * *"))
	})
})
