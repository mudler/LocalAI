package convert

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

type oldTask struct {
	user, id, name, model, prompt, cron string
	params                              map[string]string
	webhooks, multimedia                int
}

type taskFile struct {
	Tasks []struct {
		ID         string            `json:"id"`
		Name       string            `json:"name"`
		Model      string            `json:"model"`
		Prompt     string            `json:"prompt"`
		Cron       string            `json:"cron"`
		Params     map[string]string `json:"cron_parameters"`
		Webhooks   []json.RawMessage `json:"webhooks"`
		Multimedia []json.RawMessage `json:"multimedia_sources"`
	} `json:"tasks"`
}

// readTasks reads the task files and the table. A file or table that cannot be
// read is a failed item, and the others are read all the same.
func (r *run) readTasks() []oldTask {
	var out []oldTask
	add := func(user string, f taskFile) {
		for _, t := range f.Tasks {
			out = append(out, oldTask{user: user, id: t.ID, name: t.Name, model: t.Model, prompt: t.Prompt, cron: t.Cron, params: t.Params,
				webhooks: len(t.Webhooks), multimedia: len(t.Multimedia)})
		}
	}
	root := r.src.DataDir
	if root == "" {
		root = r.src.StateDir
	}
	if root != "" {
		var f taskFile
		if ok, err := readJSONFile(filepath.Join(root, "agent_tasks.json"), &f); err != nil {
			r.add(KindTrigger, "", "agent_tasks.json", StatusFailed, err.Error())
		} else if ok {
			add("", f)
		}
	}
	for _, uid := range r.userDirs() {
		var f taskFile
		if ok, err := readJSONFile(filepath.Join(r.src.StateDir, "users", uid, "agent_tasks.json"), &f); err != nil {
			r.add(KindTrigger, uid, "agent_tasks.json", StatusFailed, err.Error())
		} else if ok {
			add(uid, f)
		}
	}
	if hasTable(r.src.DB, "tasks") {
		var rows []struct {
			UserID string `gorm:"column:user_id"`
			ID     string `gorm:"column:id"`
			Name   string `gorm:"column:name"`
			Prompt string `gorm:"column:prompt"`
			Model  string `gorm:"column:model"`
			Cron   string `gorm:"column:cron"`
			Params string `gorm:"column:cron_parameters"`
			Hooks  string `gorm:"column:webhooks"`
			Media  string `gorm:"column:multimedia_sources"`
		}
		q := r.src.DB.Table("tasks")
		if err := q.Order("user_id, name").Find(&rows).Error; err != nil {
			r.add(KindTrigger, "", "tasks", StatusFailed, "could not read the table: "+err.Error())
			return out
		}
		for _, row := range rows {
			t := oldTask{user: row.UserID, id: row.ID, name: row.Name, model: row.Model, prompt: row.Prompt, cron: row.Cron}
			_ = json.Unmarshal([]byte(row.Params), &t.params)
			var h, m []json.RawMessage
			_ = json.Unmarshal([]byte(row.Hooks), &h)
			_ = json.Unmarshal([]byte(row.Media), &m)
			t.webhooks, t.multimedia = len(h), len(m)
			out = append(out, t)
		}
	}
	return out
}

var templateVar = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)

func (r *run) tasksStep(ctx context.Context) error {
	for _, t := range r.readTasks() {
		r.convertTask(ctx, t)
	}
	return nil
}

func (r *run) convertTask(ctx context.Context, t oldTask) {
	owner, why := r.owner(ctx, t.user)
	if why != "" {
		r.add(KindTrigger, t.user, t.name, StatusSkipped, why)
		return
	}
	key := "task|" + t.user + "|" + t.id
	if r.handled(ctx, key, KindTrigger, t.user, t.name) {
		return
	}
	var dropped []string
	if t.webhooks > 0 {
		dropped = append(dropped, "webhooks")
	}
	if t.multimedia > 0 {
		dropped = append(dropped, "multimedia_sources")
	}
	sched, ok := ScheduleFromCron(t.cron)
	switch {
	case strings.TrimSpace(t.cron) == "":
		r.drop(ctx, key, KindTrigger, t.user, t.name, "the task has no cron schedule: it only ran when called")
		return
	case !ok:
		r.drop(ctx, key, KindTrigger, t.user, t.name, "the cron text has no five-field form")
		return
	case t.model == "" || strings.TrimSpace(t.prompt) == "":
		r.drop(ctx, key, KindTrigger, t.user, t.name, "the task has no model or no prompt")
		return
	}
	prompt := templateVar.ReplaceAllStringFunc(t.prompt, func(m string) string {
		if v, ok := t.params[templateVar.FindStringSubmatch(m)[1]]; ok {
			return v
		}
		return m
	})
	reason := "switched off" + r.zoneReason()
	if strings.Contains(prompt, "{{") {
		reason += "; template parts are left in the prompt as text"
	}
	if len(dropped) > 0 {
		reason += "; dropped: " + strings.Join(dropped, ", ")
	}
	if err := r.tg.CreateTrigger(ctx, owner, TriggerIn{Name: t.name, Prompt: prompt, Model: t.model, Schedule: sched, TZ: r.src.Zone}); err != nil {
		r.refused(ctx, key, KindTrigger, t.user, t.name, err)
		return
	}
	r.mark(ctx, key, "trigger")
	r.add(KindTrigger, t.user, t.name, StatusConverted, reason)
}
