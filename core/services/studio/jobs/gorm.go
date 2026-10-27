package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// taskRow is the table of tasks. The webhooks are JSON text so one schema works
// on SQLite and PostgreSQL. The unique index on (owner, name_key) keeps names
// apart under a race.
type taskRow struct {
	ID          string `gorm:"primaryKey;size:64"`
	Owner       string `gorm:"index:idx_studio_tasks_name,unique;size:255"`
	NameKey     string `gorm:"index:idx_studio_tasks_name,unique;size:255"`
	Name        string `gorm:"size:255"`
	Description string
	Model       string `gorm:"size:255"`
	AgentID     string `gorm:"size:64"`
	Prompt      string
	Enabled     bool
	Webhooks    string
	Created     time.Time
	Updated     time.Time
}

func (taskRow) TableName() string { return "studio_tasks" }

// jobRow is the table of job records.
type jobRow struct {
	ID            string `gorm:"primaryKey;size:64"`
	Owner         string `gorm:"index:idx_studio_jobs_owner,priority:1;size:255"`
	TaskID        string `gorm:"index;size:64"`
	Parameters    string
	TriggeredBy   string `gorm:"size:32"`
	Attachments   int
	Created       time.Time `gorm:"index:idx_studio_jobs_owner,priority:2"`
	Ended         time.Time
	Error         string
	WebhooksDone  bool
	WebhookSent   bool
	WebhookSentAt time.Time
	WebhookError  string
}

func (jobRow) TableName() string { return "studio_jobs" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("jobs: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&taskRow{}, &jobRow{}); err != nil {
		return nil, fmt.Errorf("jobs: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (r taskRow) task() (Task, error) {
	t := Task{ID: r.ID, Owner: r.Owner, Name: r.Name, Description: r.Description, Model: r.Model, AgentID: r.AgentID,
		Prompt: r.Prompt, Enabled: r.Enabled, Created: r.Created.UTC(), Updated: r.Updated.UTC()}
	if r.Webhooks != "" {
		if err := json.Unmarshal([]byte(r.Webhooks), &t.Webhooks); err != nil {
			return t, fmt.Errorf("jobs: read the webhooks of task %s: %w", r.ID, err)
		}
	}
	return t, nil
}

func (r jobRow) job() (Job, error) {
	j := Job{ID: r.ID, Owner: r.Owner, TaskID: r.TaskID, TriggeredBy: r.TriggeredBy, Attachments: r.Attachments, Created: r.Created.UTC(), Ended: r.Ended.UTC(), Error: r.Error,
		WebhooksDone: r.WebhooksDone, WebhookSent: r.WebhookSent, WebhookSentAt: r.WebhookSentAt.UTC(), WebhookError: r.WebhookError}
	if r.Parameters != "" {
		if err := json.Unmarshal([]byte(r.Parameters), &j.Parameters); err != nil {
			return j, fmt.Errorf("jobs: read the parameters of job %s: %w", r.ID, err)
		}
	}
	if j.Parameters == nil {
		j.Parameters = map[string]string{}
	}
	return j, nil
}

func isUniqueViolation(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate key")
}

func (s *gormStore) PutTask(ctx context.Context, t Task) error {
	hooks, err := json.Marshal(t.Webhooks)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old taskRow
		err := tx.Where("id = ?", t.ID).Take(&old).Error
		exists := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if exists && old.Owner != t.Owner {
			return ErrNotFound
		}
		row := taskRow{ID: t.ID, Owner: t.Owner, NameKey: NameKey(t.Name), Name: t.Name, Description: t.Description, Model: t.Model,
			AgentID: t.AgentID, Prompt: t.Prompt, Enabled: t.Enabled, Webhooks: string(hooks), Created: t.Created, Updated: t.Updated}
		if exists {
			row.Created = old.Created
			err = tx.Save(&row).Error
		} else {
			err = tx.Create(&row).Error
		}
		if err != nil && isUniqueViolation(err) {
			return ErrNameTaken
		}
		return err
	})
}

func (s *gormStore) GetTask(ctx context.Context, owner, id string) (Task, error) {
	var r taskRow
	err := s.db.WithContext(ctx).Where("id = ? AND owner = ?", id, owner).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	return r.task()
}

func (s *gormStore) listTasks(q *gorm.DB) ([]Task, error) {
	var rows []taskRow
	if err := q.Order("owner, name_key, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		t, err := r.task()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *gormStore) ListTasks(ctx context.Context, owner string) ([]Task, error) {
	return s.listTasks(s.db.WithContext(ctx).Where("owner = ?", owner))
}

func (s *gormStore) AllTasks(ctx context.Context) ([]Task, error) {
	return s.listTasks(s.db.WithContext(ctx))
}

func (s *gormStore) DeleteTask(ctx context.Context, owner, id string) error {
	res := s.db.WithContext(ctx).Where("id = ? AND owner = ?", id, owner).Delete(&taskRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *gormStore) PutJob(ctx context.Context, j Job) error {
	params, err := json.Marshal(j.Parameters)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old jobRow
		err := tx.Where("id = ?", j.ID).Take(&old).Error
		exists := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if exists && old.Owner != j.Owner {
			return ErrNotFound
		}
		row := jobRow{ID: j.ID, Owner: j.Owner, TaskID: j.TaskID, Parameters: string(params), TriggeredBy: j.TriggeredBy,
			Attachments: j.Attachments, Created: j.Created, Ended: j.Ended, Error: j.Error, WebhooksDone: j.WebhooksDone, WebhookSent: j.WebhookSent,
			WebhookSentAt: j.WebhookSentAt, WebhookError: j.WebhookError}
		if exists {
			return tx.Save(&row).Error
		}
		return tx.Create(&row).Error
	})
}

func (s *gormStore) GetJob(ctx context.Context, owner, id string) (Job, error) {
	var r jobRow
	err := s.db.WithContext(ctx).Where("id = ? AND owner = ?", id, owner).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return r.job()
}

func (s *gormStore) listJobs(q *gorm.DB, limit int) ([]Job, error) {
	q = q.Order("created desc, id desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []jobRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		j, err := r.job()
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func (s *gormStore) ListJobs(ctx context.Context, owner string, f Filter) ([]Job, error) {
	q := s.db.WithContext(ctx).Where("owner = ?", owner)
	if f.TaskID != "" {
		q = q.Where("task_id = ?", f.TaskID)
	}
	return s.listJobs(q, f.Limit)
}

func (s *gormStore) AllJobs(ctx context.Context) ([]Job, error) {
	return s.listJobs(s.db.WithContext(ctx), 0)
}

func (s *gormStore) DeleteJob(ctx context.Context, owner, id string) error {
	res := s.db.WithContext(ctx).Where("id = ? AND owner = ?", id, owner).Delete(&jobRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
