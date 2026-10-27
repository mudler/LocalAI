package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Run converts what the old features left. A source that does not exist is not
// an error. It returns the report, which it also stores in marks.
func Run(ctx context.Context, src Sources, tg Targets, marks Marks) (Report, error) {
	r := &run{src: src, tg: tg, marks: marks, agents: map[string]agentRef{}}
	steps := []func(context.Context) error{r.agentsStep, r.tasksStep, r.skillsStep, r.historyStep}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return Report{}, err
		}
	}
	rep := Report{At: time.Now().UTC(), Items: r.items}
	if err := marks.SaveReport(ctx, rep); err != nil {
		return rep, err
	}
	return rep, nil
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

func (r *run) done(ctx context.Context, key string) (string, bool) {
	ref, ok, err := r.marks.Ref(ctx, key)
	return ref, err == nil && ok
}

func (r *run) mark(ctx context.Context, key, ref string) {
	if err := r.marks.Mark(ctx, key, ref); err != nil {
		r.add("", "", key, StatusFailed, "could not remember the conversion: "+err.Error())
	}
}

// --- sources ---

func hasTable(db *gorm.DB, name string) bool { return db != nil && db.Migrator().HasTable(name) }

func readJSONFile(path string, out any) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
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

func (r *run) readAgents() ([]oldAgent, error) {
	var out []oldAgent
	if r.src.StateDir != "" {
		pool := map[string]map[string]any{}
		if _, err := readJSONFile(filepath.Join(r.src.StateDir, "pool.json"), &pool); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(pool))
		for k := range pool {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			user, name := "", k
			if i := strings.Index(k, ":"); i >= 0 {
				user, name = k[:i], k[i+1:]
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
		if err := r.src.DB.Table("agent_configs").Order("user_id, name").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			cfg := map[string]any{}
			if json.Unmarshal([]byte(row.Config), &cfg) != nil {
				continue
			}
			out = append(out, oldAgent{user: row.UserID, name: row.Name, cfg: cfg})
		}
	}
	return out, nil
}

// keptAgentFields are the fields that convert; every other field with a value is
// counted as dropped.
var keptAgentFields = map[string]bool{"name": true, "description": true, "model": true, "system_prompt": true, "permanent_goal": true,
	"identity_guidance": true, "enable_kb": true, "enable_skills": true, "selected_skills": true, "skills_mode": true,
	"standalone_job": true, "periodic_runs": true, "scheduler_task_template": true, "connectors": true}

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
	olds, err := r.readAgents()
	if err != nil {
		return err
	}
	for _, a := range olds {
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
	if ref, ok := r.done(ctx, key); ok {
		r.agents[a.user+"|"+a.name] = agentRef{id: ref, model: model}
		r.add(KindAgent, a.user, a.name, StatusSkipped, "already converted")
	} else {
		in := AgentIn{Name: a.name, Description: str(a.cfg, "description"), Model: model, Instructions: instructions(a.cfg)}
		if boolean(a.cfg, "enable_kb") {
			cols, _ := r.tg.Collections(ctx, owner)
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
			r.add(KindAgent, a.user, a.name, StatusFailed, err.Error())
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
	if !ok || ref.model == "" {
		r.add(KindTrigger, a.user, name, StatusDropped, "the agent has no model")
		return
	}
	key := "trigger|" + a.user + "|agent|" + a.name
	if _, done := r.done(ctx, key); done {
		r.add(KindTrigger, a.user, name, StatusSkipped, "already converted")
		return
	}
	interval := str(a.cfg, "periodic_runs")
	if interval == "" {
		interval = "10m"
	}
	sched, ok := ScheduleFromInterval(interval)
	if !ok {
		r.add(KindTrigger, a.user, name, StatusDropped, "the interval "+interval+" has no schedule form")
		return
	}
	prompt := str(a.cfg, "scheduler_task_template")
	if prompt == "" {
		prompt = str(a.cfg, "permanent_goal")
	}
	if prompt == "" {
		prompt = "Do your periodic work."
	}
	if err := r.tg.CreateTrigger(ctx, owner, TriggerIn{Name: name, Prompt: prompt, Model: ref.model, Schedule: sched, AgentID: ref.id}); err != nil {
		r.add(KindTrigger, a.user, name, StatusFailed, err.Error())
		return
	}
	r.mark(ctx, key, "trigger")
	r.add(KindTrigger, a.user, name, StatusConverted, "switched off; every "+interval)
}

var usernameShape = regexp.MustCompile(`^[A-Za-z0-9_]{3,63}$`)

func (r *run) connectors(ctx context.Context, owner string, a oldAgent) {
	list, _ := a.cfg["connectors"].([]any)
	for i, c := range list {
		cm, _ := c.(map[string]any)
		typ := str(cm, "type")
		name := a.name + ": " + typ
		if typ != "telegram" {
			r.add(KindConnector, a.user, name, StatusDropped, "the "+typ+" connector is not rebuilt")
			continue
		}
		key := fmt.Sprintf("connector|%s|%s|telegram|%d", a.user, a.name, i)
		if _, done := r.done(ctx, key); done {
			r.add(KindConnector, a.user, name, StatusSkipped, "already converted")
			continue
		}
		ref, ok := r.agents[a.user+"|"+a.name]
		if !ok || ref.model == "" {
			r.add(KindConnector, a.user, name, StatusDropped, "the agent has no model, so it cannot be bound")
			continue
		}
		conf := map[string]any{}
		_ = json.Unmarshal([]byte(str(cm, "config")), &conf)
		token := str(conf, "token")
		if token == "" {
			r.add(KindConnector, a.user, name, StatusDropped, "the connector has no token")
			continue
		}
		var allow []string
		for _, u := range strings.Split(str(conf, "admins"), ",") {
			u = strings.TrimPrefix(strings.TrimSpace(u), "@")
			if usernameShape.MatchString(u) {
				allow = append(allow, "@"+u)
			}
		}
		if err := r.tg.CreateBinding(ctx, owner, BindingIn{Name: a.name + " Telegram", Token: token, AgentID: ref.id, Allow: allow}); err != nil {
			r.add(KindConnector, a.user, name, StatusFailed, err.Error())
			continue
		}
		r.mark(ctx, key, "binding")
		r.add(KindConnector, a.user, name, StatusConverted, fmt.Sprintf("switched off; %d allowed senders", len(allow)))
	}
}
