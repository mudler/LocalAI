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
	"testing"
	"time"

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

func memMarks(t *testing.T) convert.Marks {
	t.Helper()
	m, err := convert.NewJSONMarks("")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func poolFixture(t *testing.T, dir string) {
	t.Helper()
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
	write(t, filepath.Join(dir, "pool.json"), mustJSON(pool))
}

func run(t *testing.T, src convert.Sources, tg convert.Targets, marks convert.Marks) convert.Report {
	t.Helper()
	r, err := convert.Run(context.Background(), src, tg, marks)
	if err != nil {
		t.Fatal(err)
	}
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

func TestAnAgentOfThePoolBecomesASavedAgentWithItsGrantsAndTheRestIsCountedAsDropped(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	tg := newTargets()
	tg.cols[alice] = []string{"Support", "docs"}
	r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))

	got := tg.agents[alice]
	if len(got) != 1 {
		t.Fatalf("alice's agents = %+v", got)
	}
	a := got[0]
	if a.Name != "Support" || a.Description != "Answers customers" || a.Model != "llama" {
		t.Fatalf("agent = %+v", a)
	}
	if !strings.Contains(a.Instructions, "Be kind.") || !strings.Contains(a.Instructions, "Keep the queue empty.") {
		t.Fatalf("instructions = %q", a.Instructions)
	}
	if len(a.Collections) != 1 || a.Collections[0] != "Support" || len(a.Skills) != 1 || a.Skills[0] != "triage" {
		t.Fatalf("grants = %+v %+v", a.Collections, a.Skills)
	}
	it, ok := find(r, convert.KindAgent, "Support")
	if !ok || it.Status != convert.StatusConverted {
		t.Fatalf("item = %+v", it)
	}
	for _, d := range []string{"actions", "enable_planning"} {
		if !strings.Contains(it.Reason, d) {
			t.Errorf("the reason does not name the dropped field %s: %q", d, it.Reason)
		}
	}
	if strings.Contains(it.Reason, "mcp.example") || strings.Contains(it.Reason, "SECRET") {
		t.Fatalf("the reason has a value: %q", it.Reason)
	}
}

func TestAPeriodicAgentBecomesASwitchedOffTriggerAndAnAgentWithNoModelStillConverts(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	tg := newTargets()
	run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
	trs := tg.triggers[alice]
	if len(trs) != 1 || trs[0].Schedule != "*/30 * * * *" || trs[0].Model != "llama" || trs[0].AgentID != "agent-1" || !strings.Contains(trs[0].Name, "Support") || !strings.Contains(trs[0].Prompt, "Keep the queue empty.") {
		t.Fatalf("triggers = %+v", trs)
	}
	if got := tg.agents[bob]; len(got) != 1 || got[0].Name != "Plain" || got[0].Model != "" {
		t.Fatalf("bob's agents = %+v", got)
	}
	if len(tg.triggers[bob]) != 0 {
		t.Fatal("an agent that is not autonomous got a trigger")
	}
}

func TestARootAgentBelongsToTheAnonymousUserOnlyWhenThereAreNoAccounts(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	with := newTargets()
	r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, with, memMarks(t))
	if it, _ := find(r, convert.KindAgent, "Legacy"); it.Status != convert.StatusSkipped || !strings.Contains(it.Reason, "no owner") {
		t.Fatalf("with accounts: %+v", it)
	}
	if len(with.agents[""]) != 0 {
		t.Fatal("a root agent was given to someone")
	}
	without := newTargets()
	run(t, convert.Sources{StateDir: dir, AuthEnabled: false}, without, memMarks(t))
	if len(without.agents[""]) != 1 || without.agents[""][0].Name != "Legacy" {
		t.Fatalf("without accounts: %+v", without.agents)
	}
}

func TestOnlyTheTelegramConnectorConvertsAndItsTokenStaysOutOfTheReport(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	tg := newTargets()
	r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
	bs := tg.bindings[alice]
	if len(bs) != 1 || bs[0].Token != token || bs[0].AgentID != "agent-1" {
		t.Fatalf("bindings = %+v", bs)
	}
	if len(bs[0].Allow) != 2 || bs[0].Allow[0] != "@ada_l" || bs[0].Allow[1] != "@bo_b" {
		t.Fatalf("allowlist = %v, want the admins that are valid usernames", bs[0].Allow)
	}
	if it, ok := find(r, convert.KindConnector, "Support: slack"); !ok || it.Status != convert.StatusDropped {
		t.Fatalf("slack = %+v %v", it, ok)
	}
	raw, _ := json.Marshal(r)
	for _, secret := range []string{token, "xoxb-SLACK-SECRET", "SECRET", "mcp.example", "Keep the queue empty", "Be kind"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("the report has %q:\n%s", secret, raw)
		}
	}
}

func TestACronTaskBecomesASwitchedOffTriggerAndOtherTasksAreDropped(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "users", alice, "agent_tasks.json"), mustJSON(map[string]any{"tasks": []any{
		map[string]any{"id": "t1", "name": "Nightly", "model": "llama", "prompt": "Summarise {{.topic}} for me", "cron": "0 3 * * *", "cron_parameters": map[string]string{"topic": "sales"}, "enabled": true,
			"webhooks": []any{map[string]any{"url": "https://hook.example/x"}}},
		map[string]any{"id": "t2", "name": "Manual only", "model": "llama", "prompt": "x"},
		map[string]any{"id": "t3", "name": "With seconds", "model": "llama", "prompt": "x", "cron": "0 0 3 * * *"},
	}}))
	tg := newTargets()
	r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
	trs := tg.triggers[alice]
	if len(trs) != 1 || trs[0].Schedule != "0 3 * * *" || trs[0].Prompt != "Summarise sales for me" || trs[0].Model != "llama" || trs[0].Name != "Nightly" {
		t.Fatalf("triggers = %+v", trs)
	}
	if it, _ := find(r, convert.KindTrigger, "Nightly"); it.Status != convert.StatusConverted || !strings.Contains(it.Reason, "webhooks") {
		t.Fatalf("nightly = %+v", it)
	}
	for _, n := range []string{"Manual only", "With seconds"} {
		if it, ok := find(r, convert.KindTrigger, n); !ok || it.Status != convert.StatusDropped {
			t.Errorf("%s = %+v %v", n, it, ok)
		}
	}
}

func TestSkillsOfTheFoldersAndOfTheTableAreCopiedToTheRightLayer(t *testing.T) {
	dir := t.TempDir()
	skill := "---\nname: triage\ndescription: Sort the queue\n---\nRead each item.\n"
	write(t, filepath.Join(dir, "skills", "house", "SKILL.md"), "---\nname: house\ndescription: House style\n---\nWrite plainly.\n")
	write(t, filepath.Join(dir, "users", alice, "skills", "skills", "triage", "SKILL.md"), skill)
	write(t, filepath.Join(dir, "users", alice, "skills", "skills", "triage", "notes.txt"), "a resource")
	write(t, filepath.Join(dir, "users", alice, "skills", "skills", "broken", "SKILL.md"), "no frontmatter at all")
	db := newDB(t)
	exec(t, db, `CREATE TABLE skills_metadata (id text, user_id text, name text, definition text, source_type text, source_url text, enabled bool)`)
	exec(t, db, `INSERT INTO skills_metadata (id, user_id, name, definition, source_type, enabled) VALUES ('1', ?, 'from-db', ?, 'inline', 1)`, bob, "---\nname: from-db\ndescription: From the database\n---\nBody.\n")
	exec(t, db, `INSERT INTO skills_metadata (id, user_id, name, definition, source_type, source_url, enabled) VALUES ('2', ?, 'from-git', 'x', 'git', 'https://git.example/repo', 1)`, bob)
	tg := newTargets()
	r := run(t, convert.Sources{StateDir: dir, DB: db, AuthEnabled: true}, tg, memMarks(t))
	if s := tg.skills["shared/house"]; s.Description != "House style" || strings.TrimSpace(s.Body) != "Write plainly." {
		t.Fatalf("shared skill = %+v", s)
	}
	if s := tg.skills[alice+"/triage"]; s.Description != "Sort the queue" || strings.TrimSpace(s.Body) != "Read each item." {
		t.Fatalf("personal skill = %+v", s)
	}
	if s := tg.skills[bob+"/from-db"]; s.Description != "From the database" {
		t.Fatalf("table skill = %+v", s)
	}
	if it, _ := find(r, convert.KindSkill, "triage"); !strings.Contains(it.Reason, "resource") {
		t.Fatalf("the resource file was not mentioned: %+v", it)
	}
	if it, _ := find(r, convert.KindSkill, "broken"); it.Status != convert.StatusFailed && it.Status != convert.StatusDropped {
		t.Fatalf("broken = %+v", it)
	}
	if it, _ := find(r, convert.KindSkill, "from-git"); it.Status != convert.StatusDropped || !strings.Contains(it.Reason, "git") {
		t.Fatalf("git skill = %+v", it)
	}
}

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := sqlitedb.Open("file:"+filepath.Join(t.TempDir(), "c.db")+"?_busy_timeout=5000", &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			_ = s.Close()
		}
	})
	return db
}

func exec(t *testing.T, db *gorm.DB, q string, args ...any) {
	t.Helper()
	if err := db.Exec(q, args...).Error; err != nil {
		t.Fatal(err)
	}
}

func TestAgentsTasksAndHistoryOfADistributedDatabaseAreConverted(t *testing.T) {
	db := newDB(t)
	exec(t, db, `CREATE TABLE agent_configs (id text, user_id text, name text, config text, status text)`)
	exec(t, db, `CREATE TABLE tasks (id text, user_id text, name text, prompt text, model text, enabled bool, cron text, cron_parameters text, webhooks text)`)
	exec(t, db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, created_at datetime, updated_at datetime, deleted_at datetime)`)
	exec(t, db, `INSERT INTO agent_configs (id, user_id, name, config, status) VALUES ('1', ?, 'Research', ?, 'active')`, alice, mustJSON(map[string]any{"name": "Research", "model": "m1", "system_prompt": "Find things."}))
	exec(t, db, `INSERT INTO tasks (id, user_id, name, prompt, model, enabled, cron, cron_parameters) VALUES ('t', ?, 'Weekly', 'Report', 'm1', 1, '0 9 * * 1', '{}')`, bob)
	hist := mustJSON([]any{
		map[string]any{"role": "user", "content": "hello"},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hi there"}}},
		map[string]any{"role": "thinking", "content": "hmm"},
		map[string]any{"role": "tool_call", "name": "search", "content": "{}"},
		map[string]any{"role": "tool_result", "name": "search", "content": "found"},
	})
	conv := mustJSON(map[string]any{"id": "c1", "name": "A chat", "model": "m1", "history": json.RawMessage(hist), "createdAt": 1700000000000, "updatedAt": 1700000100000})
	exec(t, db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'c1', ?, 1)`, alice, conv)
	exec(t, db, `INSERT INTO chat_conversations (user_id, conv_id, content, version, deleted_at) VALUES (?, 'c2', '{}', 2, '2026-01-01')`, alice)
	tg := newTargets()
	r := run(t, convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks(t))
	if got := tg.agents[alice]; len(got) != 1 || got[0].Name != "Research" || got[0].Instructions != "Find things." {
		t.Fatalf("agents = %+v", got)
	}
	if got := tg.triggers[bob]; len(got) != 1 || got[0].Schedule != "0 9 * * 1" {
		t.Fatalf("triggers = %+v", got)
	}
	cs := tg.chats[alice]
	if len(cs) != 1 {
		t.Fatalf("chats = %+v (the deleted one must not convert)", cs)
	}
	c := cs[0]
	if c.Title != "A chat" || c.Model != "m1" || !c.Created.Equal(time.UnixMilli(1700000000000)) || !c.Updated.Equal(time.UnixMilli(1700000100000)) {
		t.Fatalf("chat = %+v", c)
	}
	roles := []string{}
	for _, e := range c.Entries {
		roles = append(roles, e.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,reasoning,tool" || c.Entries[0].Text != "hello" || c.Entries[1].Text != "hi there" {
		t.Fatalf("entries = %+v", c.Entries)
	}
	if it, ok := find(r, convert.KindChat, "A chat"); !ok || it.Status != convert.StatusConverted {
		t.Fatalf("chat item = %+v", it)
	}
}

func TestAHugeHistoryKeepsTheNewestMessages(t *testing.T) {
	db := newDB(t)
	exec(t, db, `CREATE TABLE chat_conversations (user_id text, conv_id text, content text, version integer, deleted_at datetime)`)
	var h []any
	for i := 0; i < convert.MaxMessages+50; i++ {
		h = append(h, map[string]any{"role": "user", "content": fmt.Sprintf("m%d", i)})
	}
	conv := mustJSON(map[string]any{"id": "big", "name": "Big", "model": "m", "history": h})
	exec(t, db, `INSERT INTO chat_conversations (user_id, conv_id, content, version) VALUES (?, 'big', ?, 1)`, alice, conv)
	tg := newTargets()
	run(t, convert.Sources{DB: db, AuthEnabled: true}, tg, memMarks(t))
	es := tg.chats[alice][0].Entries
	if len(es) != convert.MaxMessages || es[len(es)-1].Text != fmt.Sprintf("m%d", convert.MaxMessages+49) || es[0].Text != "m50" {
		t.Fatalf("%d entries, first %q, last %q", len(es), es[0].Text, es[len(es)-1].Text)
	}
}

func TestARepeatedRunConvertsNothingTwiceAndAFailedItemIsTriedAgain(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	tg := newTargets()
	tg.failSave["Plain"] = true
	marks := memMarks(t)
	src := convert.Sources{StateDir: dir, AuthEnabled: true}
	first := run(t, src, tg, marks)
	if it, _ := find(first, convert.KindAgent, "Plain"); it.Status != convert.StatusFailed || !strings.Contains(it.Reason, "disk full") {
		t.Fatalf("failed item = %+v", it)
	}
	nAgents, nTriggers, nBindings := len(tg.agents[alice]), len(tg.triggers[alice]), len(tg.bindings[alice])
	delete(tg.failSave, "Plain")
	second := run(t, src, tg, marks)
	if len(tg.agents[alice]) != nAgents || len(tg.triggers[alice]) != nTriggers || len(tg.bindings[alice]) != nBindings {
		t.Fatal("a second run converted an item again")
	}
	if len(tg.agents[bob]) != 1 {
		t.Fatalf("the failed agent was not tried again: %+v", tg.agents[bob])
	}
	if it, _ := find(second, convert.KindAgent, "Support"); it.Status != convert.StatusSkipped {
		t.Fatalf("an item already converted is %+v", it)
	}
	third := run(t, src, tg, marks)
	for _, it := range third.Items {
		if it.Status == convert.StatusConverted || it.Status == convert.StatusFailed {
			t.Fatalf("a third run did work: %+v", it)
		}
	}
}

func TestANameThatIsTakenGetsANumberAndAnUnknownUserIsSkipped(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	tg := newTargets()
	tg.agents[alice] = []convert.AgentIn{{Name: "Support"}}
	tg.users[bob] = false
	r := run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, tg, memMarks(t))
	names := []string{}
	for _, a := range tg.agents[alice] {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "Support,Support (2)" {
		t.Fatalf("names = %v", names)
	}
	if it, _ := find(r, convert.KindAgent, "Plain"); it.Status != convert.StatusSkipped || !strings.Contains(it.Reason, "no account") {
		t.Fatalf("unknown user = %+v", it)
	}
	if len(tg.agents[bob]) != 0 {
		t.Fatal("an agent was made for a user that does not exist")
	}
}

func TestTheReportIsKeptPerUserAndOneUserNeverSeesAnotherUsersItems(t *testing.T) {
	dir := t.TempDir()
	poolFixture(t, dir)
	marks := memMarks(t)
	run(t, convert.Sources{StateDir: dir, AuthEnabled: true}, newTargets(), marks)
	stored, ok, err := marks.Report(context.Background())
	if err != nil || !ok || len(stored.Items) == 0 {
		t.Fatalf("stored = %+v %v %v", stored, ok, err)
	}
	for _, i := range stored.ForUser(bob).Items {
		if i.User != bob {
			t.Fatalf("bob's report has %+v", i)
		}
	}
	if len(stored.ForUser("nobody").Items) != 0 {
		t.Fatal("a stranger has items")
	}
}

func TestMissingSourcesAreNotAnError(t *testing.T) {
	r := run(t, convert.Sources{StateDir: t.TempDir(), DB: newDB(t), AuthEnabled: true}, newTargets(), memMarks(t))
	if len(r.Items) != 0 {
		t.Fatalf("items = %+v", r.Items)
	}
	if _, err := convert.Run(context.Background(), convert.Sources{}, newTargets(), memMarks(t)); err != nil {
		t.Fatalf("no sources at all: %v", err)
	}
}

func TestIntervalsAndCronTexts(t *testing.T) {
	// Covered through the agent and task tests; this pins the table.
	for in, want := range map[string]string{"10m": "*/10 * * * *", "1h": "0 */1 * * *", "6h": "0 */6 * * *", "24h": "0 0 */1 * *", "48h": "0 0 */2 * *"} {
		if got, ok := convert.ScheduleFromInterval(in); !ok || got != want {
			t.Errorf("%s = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "5s", "90m", "abc", "36h"} {
		if got, ok := convert.ScheduleFromInterval(in); ok {
			t.Errorf("%q gave %q", in, got)
		}
	}
	if _, ok := convert.ScheduleFromCron("0 0 3 * * *"); ok {
		t.Error("a six-field cron was accepted")
	}
	if got, ok := convert.ScheduleFromCron("  0 3  * * * "); !ok || got != "0 3 * * *" {
		t.Errorf("cron = %q %v", got, ok)
	}
}
