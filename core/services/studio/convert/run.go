package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// run holds the state of one conversion.
type run struct {
	src   Sources
	tg    Targets
	marks Marks
	items []Item
	// agents maps "user|name" to what the agent became, for triggers and bindings.
	agents map[string]agentRef
}

type agentRef struct {
	id, model string
}

// Marks that are not the ID of what a source item became.
const (
	refDropped   = "dropped" // the item has no counterpart, or the target refused it for good
	refExists    = "exists"  // a skill of that name was there already
	refImporting = "importing:"
)

// Reasons of an item that an earlier run handled.
const (
	reasonConverted = "already converted"
	reasonHandled   = "already handled"
	reasonExists    = "a skill with this name exists already"
)

// Run converts what the old features left. A source that does not exist is not
// an error, and one that cannot be read is a failed item of the report, so the
// other sources still convert. It returns the report of this run, which lists
// what is news in Notable, and stores in marks the report of all runs: an item
// keeps the status and reason of the run that handled it.
func Run(ctx context.Context, src Sources, tg Targets, marks Marks) (Report, error) {
	r := &run{src: src, tg: tg, marks: marks, agents: map[string]agentRef{}}
	// No report yet means all is news. An unreadable one stops the run: saving
	// a report made without it would replace the history of earlier runs.
	prev, _, err := marks.Report(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("convert: read the report of earlier runs: %w", err)
	}
	steps := []func(context.Context) error{r.agentsStep, r.tasksStep, r.skillsStep, r.historyStep}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return Report{}, err
		}
	}
	rep := Report{At: time.Now().UTC(), Items: r.items, Notable: notable(r.items, prev)}
	if err := marks.SaveReport(ctx, mergeReports(prev, rep)); err != nil {
		return rep, err
	}
	return rep, nil
}

func itemKey(i Item) string { return i.Kind + "|" + i.User + "|" + i.Name }

// notable picks the items that are news: handled for the first time, or failed
// with a reason the report before did not hold. A failure that comes back the
// same at each start is not news.
func notable(items []Item, prev Report) []Item {
	failed := map[string]bool{}
	for _, i := range prev.Items {
		if i.Status == StatusFailed {
			failed[itemKey(i)+"|"+i.Reason] = true
		}
	}
	var out []Item
	for _, i := range items {
		switch i.Status {
		case StatusConverted, StatusDropped:
			out = append(out, i)
		case StatusFailed:
			if !failed[itemKey(i)+"|"+i.Reason] {
				out = append(out, i)
			}
		}
	}
	return out
}

func isRepeat(i Item) bool {
	return i.Status == StatusSkipped && (i.Reason == reasonConverted || i.Reason == reasonHandled || i.Reason == reasonExists)
}

// mergeReports keeps the first word on an item: one that an earlier run
// converted or dropped keeps its status and reason when a later run only finds
// it handled. Items of earlier runs that this run did not meet stay.
func mergeReports(prev, cur Report) Report {
	first := map[string]Item{}
	for _, i := range prev.Items {
		if i.Status == StatusConverted || i.Status == StatusDropped || isRepeat(i) {
			first[itemKey(i)] = i
		}
	}
	out := Report{At: cur.At}
	seen := map[string]bool{}
	for _, i := range cur.Items {
		k := itemKey(i)
		if p, ok := first[k]; ok && isRepeat(i) {
			i = p
		}
		seen[k] = true
		out.Items = append(out.Items, i)
	}
	for _, p := range prev.Items {
		if _, ok := first[itemKey(p)]; ok && !seen[itemKey(p)] {
			out.Items = append(out.Items, p)
		}
	}
	return out
}

func (r *run) add(kind, user, name, status, reason string) {
	r.items = append(r.items, Item{Kind: kind, User: user, Name: name, Status: status, Reason: reason})
}

// owner decides who an old item belongs to. A server with no accounts has one
// user, the anonymous one (""). With accounts, an item of no user belongs to
// nobody. The second result is the reason when it does not convert.
func (r *run) owner(ctx context.Context, user string) (string, string) {
	if !r.src.AuthEnabled {
		if user != "" {
			return "", "the owner has no account"
		}
		return "", ""
	}
	if user == "" {
		return "", "the item has no owner: make it again in the new pages"
	}
	ok, err := r.tg.UserExists(ctx, user)
	if err != nil {
		return "", "could not check the owner: " + err.Error()
	}
	if !ok {
		return "", "the owner has no account"
	}
	return user, ""
}

// done reports what an item became in an earlier run. A mark that cannot be
// read is an error, not "not converted": the item would be made again.
func (r *run) done(ctx context.Context, key string) (string, bool, error) {
	return r.marks.Ref(ctx, key)
}

// unread adds the failed line of an item whose mark could not be read. The
// item waits for the next run.
func (r *run) unread(kind, user, name string, err error) {
	r.add(kind, user, name, StatusFailed, "could not read the conversion marks: "+err.Error())
}

// handled reports an item that an earlier run converted or dropped, and adds
// its line to the report. An item whose mark cannot be read counts as handled
// for this run, so it is not made twice; its failed line is in the report.
func (r *run) handled(ctx context.Context, key, kind, user, name string) bool {
	ref, ok, err := r.done(ctx, key)
	if err != nil {
		r.unread(kind, user, name, err)
		return true
	}
	if !ok || strings.HasPrefix(ref, refImporting) {
		return false
	}
	switch ref {
	case refDropped:
		r.add(kind, user, name, StatusSkipped, reasonHandled)
	case refExists:
		r.add(kind, user, name, StatusSkipped, reasonExists)
	default:
		r.add(kind, user, name, StatusSkipped, reasonConverted)
	}
	return true
}

// drop marks an item that cannot convert, so that it is reported once and not
// met again at each start.
func (r *run) drop(ctx context.Context, key, kind, user, name, reason string) {
	r.mark(ctx, key, refDropped)
	r.add(kind, user, name, StatusDropped, reason)
}

// refused handles the error of a target: one that says the item is not valid
// there is final, so the item is dropped; any other is tried again at the next
// run.
func (r *run) refused(ctx context.Context, key, kind, user, name string, err error) {
	if errors.Is(err, ErrInvalid) {
		r.drop(ctx, key, kind, user, name, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
		return
	}
	r.add(kind, user, name, StatusFailed, err.Error())
}

// zoneReason says in which zone a converted schedule runs.
func (r *run) zoneReason() string {
	switch {
	case r.src.Zone != "":
		return "; runs in the zone " + r.src.Zone
	case r.src.ZoneNote != "":
		return "; " + r.src.ZoneNote
	}
	return ""
}

func (r *run) mark(ctx context.Context, key, ref string) {
	if err := r.marks.Mark(ctx, key, ref); err != nil {
		r.add("", "", key, StatusFailed, "could not remember the conversion: "+err.Error())
	}
}

// --- sources ---

func hasTable(db *gorm.DB, name string) bool { return db != nil && db.Migrator().HasTable(name) }

// readJSONFile reads a JSON file. A file that is not there is not an error. An
// error names the file, not the folder it is in, and no content: it ends up in a
// report that the user reads.
func readJSONFile(path string, out any) (bool, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is a store file the conversion owns
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return false, fmt.Errorf("could not read %s: %v", filepath.Base(path), err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("%s is not valid JSON: %v", filepath.Base(path), err)
	}
	return true, nil
}

// userDirs lists the per-user directories of the old pool state.
func (r *run) userDirs() []string {
	entries, err := os.ReadDir(filepath.Join(r.src.StateDir, "users"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// --- agents, periodic triggers and Telegram bindings ---

type oldAgent struct {
	user, name string
	cfg        map[string]any
}

// readAgents reads the agents of the pool file and of the table. A source that
// cannot be read is a failed item, and the other source is read all the same.
func (r *run) readAgents() []oldAgent {
	var out []oldAgent
	if r.src.StateDir != "" {
		pool := map[string]map[string]any{}
		if _, err := readJSONFile(filepath.Join(r.src.StateDir, "pool.json"), &pool); err != nil {
			r.add(KindAgent, "", "pool.json", StatusFailed, err.Error())
		}
		keys := make([]string, 0, len(pool))
		for k := range pool {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			user, name := "", k
			if before, after, ok := strings.Cut(k, ":"); ok {
				user, name = before, after
			}
			out = append(out, oldAgent{user: user, name: name, cfg: pool[k]})
		}
	}
	if hasTable(r.src.DB, "agent_configs") {
		var rows []struct {
			UserID string `gorm:"column:user_id"`
			Name   string `gorm:"column:name"`
			Config string `gorm:"column:config"`
		}
		q := r.src.DB.Table("agent_configs")
		// The old table deleted an agent by setting its status, not by removing the row.
		if r.src.DB.Migrator().HasColumn("agent_configs", "status") {
			q = q.Where("status IS NULL OR status <> ?", "deleted")
		}
		if err := q.Order("user_id, name").Find(&rows).Error; err != nil {
			r.add(KindAgent, "", "agent_configs", StatusFailed, "could not read the table: "+err.Error())
			return out
		}
		for _, row := range rows {
			cfg := map[string]any{}
			if json.Unmarshal([]byte(row.Config), &cfg) != nil {
				r.add(KindAgent, row.UserID, row.Name, StatusFailed, "the stored configuration is not valid JSON")
				continue
			}
			out = append(out, oldAgent{user: row.UserID, name: row.Name, cfg: cfg})
		}
	}
	return out
}

// keptAgentFields are the fields that convert; every other field with a value is
// counted as dropped.
var keptAgentFields = map[string]bool{"name": true, "description": true, "model": true, "system_prompt": true, "permanent_goal": true,
	"identity_guidance": true, "enable_kb": true, "enable_skills": true, "selected_skills": true, "skills_mode": true,
	"standalone_job": true, "periodic_runs": true, "scheduler_task_template": true, "connectors": true,
	"mcp_servers": true, "mcp_stdio_servers": true}

func str(m map[string]any, k string) string   { s, _ := m[k].(string); return strings.TrimSpace(s) }
func boolean(m map[string]any, k string) bool { b, _ := m[k].(bool); return b }

func nonZero(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func droppedFields(cfg map[string]any) []string {
	var out []string
	for k, v := range cfg {
		if !keptAgentFields[k] && nonZero(v) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func stringList(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

func (r *run) agentsStep(ctx context.Context) error {
	for _, a := range r.readAgents() {
		r.convertAgent(ctx, a)
	}
	return nil
}

func (r *run) convertAgent(ctx context.Context, a oldAgent) {
	owner, why := r.owner(ctx, a.user)
	if why != "" {
		r.add(KindAgent, a.user, a.name, StatusSkipped, why)
		return
	}
	key := "agent|" + a.user + "|" + a.name
	model := str(a.cfg, "model")
	ref, ok, err := r.done(ctx, key)
	if err != nil {
		r.unread(KindAgent, a.user, a.name, err)
		return
	}
	if ok && !strings.HasPrefix(ref, refImporting) {
		if ref == refDropped {
			r.add(KindAgent, a.user, a.name, StatusSkipped, reasonHandled)
			return
		}
		r.agents[a.user+"|"+a.name] = agentRef{id: ref, model: model}
		r.add(KindAgent, a.user, a.name, StatusSkipped, reasonConverted)
	} else {
		in := AgentIn{Name: a.name, Description: str(a.cfg, "description"), Model: model, Instructions: instructions(a.cfg)}
		// The connectors come first: the agent names them by ID. Each is marked
		// on its own, so a retry of the agent never makes one twice.
		var ok bool
		if in.Connectors, ok = r.mcpConnectors(ctx, owner, a); !ok {
			r.add(KindAgent, a.user, a.name, StatusFailed, "its MCP servers could not all be converted: tried again at the next start")
			return
		}
		if boolean(a.cfg, "enable_kb") {
			// The grant is the point of the knowledge base: an agent is never marked
			// converted without it when the collections cannot be asked.
			cols, err := r.tg.Collections(ctx, owner)
			if err != nil {
				reason := "could not list the collections: " + err.Error()
				if errors.Is(err, ErrCollectionsUnavailable) {
					reason = "the collections are not running, so the collection grant cannot be made: the agent converts at a start with them on"
				}
				r.add(KindAgent, a.user, a.name, StatusFailed, reason)
				return
			}
			for _, c := range []string{a.name, a.user + ":" + a.name} {
				for _, have := range cols {
					if have == c {
						in.Collections = append(in.Collections, c)
					}
				}
				if len(in.Collections) > 0 {
					break
				}
			}
		}
		if boolean(a.cfg, "enable_skills") {
			in.Skills = stringList(a.cfg["selected_skills"])
		}
		id, finalName, err := r.saveAgent(ctx, owner, in)
		if err != nil {
			r.refused(ctx, key, KindAgent, a.user, a.name, err)
			return
		}
		r.mark(ctx, key, id)
		r.agents[a.user+"|"+a.name] = agentRef{id: id, model: model}
		reason := ""
		if d := droppedFields(a.cfg); len(d) > 0 {
			reason = "dropped: " + strings.Join(d, ", ")
		}
		if finalName != a.name {
			reason = strings.TrimSpace("renamed to " + finalName + ". " + reason)
		}
		r.add(KindAgent, a.user, a.name, StatusConverted, reason)
	}
	r.periodicTrigger(ctx, owner, a)
	r.connectors(ctx, owner, a)
}

func instructions(cfg map[string]any) string {
	parts := []string{str(cfg, "system_prompt")}
	if g := str(cfg, "permanent_goal"); g != "" {
		parts = append(parts, "Standing goal: "+g)
	}
	if g := str(cfg, "identity_guidance"); g != "" {
		parts = append(parts, "Identity: "+g)
	}
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// saveAgent saves an agent, adding a number to the name when it is taken.
func (r *run) saveAgent(ctx context.Context, owner string, in AgentIn) (id, name string, err error) {
	base := in.Name
	for n := 1; n <= 9; n++ {
		in.Name = base
		if n > 1 {
			in.Name = fmt.Sprintf("%s (%d)", base, n)
		}
		id, err = r.tg.SaveAgent(ctx, owner, in)
		if err == nil {
			return id, in.Name, nil
		}
		if !errors.Is(err, ErrNameTaken) {
			return "", "", err
		}
	}
	return "", "", err
}

func (r *run) periodicTrigger(ctx context.Context, owner string, a oldAgent) {
	if !boolean(a.cfg, "standalone_job") {
		return
	}
	ref, ok := r.agents[a.user+"|"+a.name]
	name := a.name + " (periodic)"
	key := "trigger|" + a.user + "|agent|" + a.name
	if r.handled(ctx, key, KindTrigger, a.user, name) {
		return
	}
	if !ok || ref.model == "" {
		r.drop(ctx, key, KindTrigger, a.user, name, "the agent has no model")
		return
	}
	interval := str(a.cfg, "periodic_runs")
	if interval == "" {
		interval = "10m"
	}
	sched, ok := ScheduleFromInterval(interval)
	if !ok {
		r.drop(ctx, key, KindTrigger, a.user, name, "the interval "+interval+" has no schedule form")
		return
	}
	prompt := str(a.cfg, "scheduler_task_template")
	if prompt == "" {
		prompt = str(a.cfg, "permanent_goal")
	}
	if prompt == "" {
		prompt = "Do your periodic work."
	}
	if err := r.tg.CreateTrigger(ctx, owner, TriggerIn{Name: name, Prompt: prompt, Model: ref.model, Schedule: sched, AgentID: ref.id, TZ: r.src.Zone}); err != nil {
		r.refused(ctx, key, KindTrigger, a.user, name, err)
		return
	}
	r.mark(ctx, key, "trigger")
	r.add(KindTrigger, a.user, name, StatusConverted, "switched off; every "+interval+r.zoneReason())
}

var usernameShape = regexp.MustCompile(`^[A-Za-z0-9_]{3,63}$`)

func (r *run) connectors(ctx context.Context, owner string, a oldAgent) {
	list, _ := a.cfg["connectors"].([]any)
	for i, c := range list {
		cm, _ := c.(map[string]any)
		typ := str(cm, "type")
		name := a.name + ": " + typ
		key := fmt.Sprintf("connector|%s|%s|%s|%d", a.user, a.name, typ, i)
		if r.handled(ctx, key, KindConnector, a.user, name) {
			continue
		}
		if typ != "telegram" {
			r.drop(ctx, key, KindConnector, a.user, name, "the "+typ+" connector is not rebuilt")
			continue
		}
		ref, ok := r.agents[a.user+"|"+a.name]
		if !ok || ref.model == "" {
			r.drop(ctx, key, KindConnector, a.user, name, "the agent has no model, so it cannot be bound")
			continue
		}
		conf := map[string]any{}
		_ = json.Unmarshal([]byte(str(cm, "config")), &conf)
		token := str(conf, "token")
		if token == "" {
			r.drop(ctx, key, KindConnector, a.user, name, "the connector has no token")
			continue
		}
		var allow []string
		for u := range strings.SplitSeq(str(conf, "admins"), ",") {
			u = strings.TrimPrefix(strings.TrimSpace(u), "@")
			if usernameShape.MatchString(u) {
				allow = append(allow, "@"+u)
			}
		}
		if err := r.tg.CreateBinding(ctx, owner, BindingIn{Name: a.name + " Telegram", Token: token, AgentID: ref.id, Allow: allow}); err != nil {
			r.refused(ctx, key, KindConnector, a.user, name, err)
			continue
		}
		r.mark(ctx, key, "binding")
		r.add(KindConnector, a.user, name, StatusConverted, fmt.Sprintf("switched off; %d allowed senders", len(allow)))
	}
}
