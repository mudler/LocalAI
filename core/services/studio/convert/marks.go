package convert

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gorm.io/gorm"
)

type jsonMarks struct {
	mu   sync.Mutex
	path string
	d    struct {
		Marks  map[string]string `json:"marks"`
		Report *Report           `json:"report,omitempty"`
	}
}

// reportFile is the stored form of a report: Item.User is not in the JSON the
// API returns, so the file keeps it explicitly.
type storedItem struct {
	Kind, User, Name, Status, Reason string
}

type storedReport struct {
	At    time.Time
	Items []storedItem
}

func toStored(r Report) storedReport {
	s := storedReport{At: r.At}
	for _, i := range r.Items {
		s.Items = append(s.Items, storedItem{i.Kind, i.User, i.Name, i.Status, i.Reason})
	}
	return s
}

func (s storedReport) report() Report {
	r := Report{At: s.At}
	for _, i := range s.Items {
		r.Items = append(r.Items, Item{Kind: i.Kind, User: i.User, Name: i.Name, Status: i.Status, Reason: i.Reason})
	}
	return r
}

// NewJSONMarks returns Marks kept in the file conversion.json under dir (mode
// 0600), or in memory when dir is empty.
func NewJSONMarks(dir string) (Marks, error) {
	m := &jsonMarks{}
	m.d.Marks = map[string]string{}
	if dir == "" {
		return m, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	m.path = filepath.Join(dir, "conversion.json")
	raw, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Marks  map[string]string `json:"marks"`
		Report *storedReport     `json:"report"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("convert: read %s: %w", m.path, err)
	}
	if file.Marks != nil {
		m.d.Marks = file.Marks
	}
	if file.Report != nil {
		r := file.Report.report()
		m.d.Report = &r
	}
	return m, nil
}

func (m *jsonMarks) save() error {
	if m.path == "" {
		return nil
	}
	file := struct {
		Marks  map[string]string `json:"marks"`
		Report *storedReport     `json:"report,omitempty"`
	}{Marks: m.d.Marks}
	if m.d.Report != nil {
		s := toStored(*m.d.Report)
		file.Report = &s
	}
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *jsonMarks) Ref(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, ok := m.d.Marks[key]
	return ref, ok, nil
}

func (m *jsonMarks) Mark(_ context.Context, key, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.d.Marks[key] = ref
	return m.save()
}

func (m *jsonMarks) SaveReport(_ context.Context, r Report) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.d.Report = &r
	return m.save()
}

func (m *jsonMarks) Report(context.Context) (Report, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.d.Report == nil {
		return Report{}, false, nil
	}
	return *m.d.Report, true, nil
}

type markRow struct {
	Key     string `gorm:"primaryKey;size:512"`
	Ref     string `gorm:"size:255"`
	Created time.Time
}

func (markRow) TableName() string { return "studio_conversion_marks" }

type reportRow struct {
	ID      int `gorm:"primaryKey"`
	Content string
}

func (reportRow) TableName() string { return "studio_conversion_reports" }

type gormMarks struct{ db *gorm.DB }

// NewGORMMarks returns Marks kept in the database.
func NewGORMMarks(db *gorm.DB) (Marks, error) {
	if db == nil {
		return nil, fmt.Errorf("convert: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&markRow{}, &reportRow{}); err != nil {
		return nil, fmt.Errorf("convert: migrate: %w", err)
	}
	return &gormMarks{db: db}, nil
}

func (g *gormMarks) Ref(ctx context.Context, key string) (string, bool, error) {
	var r markRow
	err := g.db.WithContext(ctx).Where("key = ?", key).Take(&r).Error
	if err == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	return r.Ref, err == nil, err
}

func (g *gormMarks) Mark(ctx context.Context, key, ref string) error {
	return g.db.WithContext(ctx).Save(&markRow{Key: key, Ref: ref, Created: time.Now().UTC()}).Error
}

func (g *gormMarks) SaveReport(ctx context.Context, r Report) error {
	raw, err := json.Marshal(toStored(r))
	if err != nil {
		return err
	}
	return g.db.WithContext(ctx).Save(&reportRow{ID: 1, Content: string(raw)}).Error
}

func (g *gormMarks) Report(ctx context.Context) (Report, bool, error) {
	var row reportRow
	err := g.db.WithContext(ctx).Where("id = 1").Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return Report{}, false, nil
	}
	if err != nil {
		return Report{}, false, err
	}
	var s storedReport
	if err := json.Unmarshal([]byte(row.Content), &s); err != nil {
		return Report{}, false, err
	}
	return s.report(), true, nil
}
