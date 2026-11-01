package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// chatRow is the stored form of a chat record and its turn state. NextEntry and
// NextPending are counters that give transcript lines and pending messages their
// order; bumping one inside a transaction also takes the row lock, which is how
// racing callers are serialized on SQLite and PostgreSQL alike.
type chatRow struct {
	ID           string `gorm:"primaryKey;size:128"`
	Principal    string `gorm:"index;size:128;not null"`
	Title        string
	Folder       string
	Created      time.Time
	Updated      time.Time `gorm:"index"`
	Snapshot     string    `gorm:"type:text"`
	HarnessState []byte
	Turns        int
	Kind         string
	ParentID     string `gorm:"index;size:128"`
	Model        string
	Status       string
	RunID        string `gorm:"size:128"`
	RunSince     time.Time
	RunBeat      time.Time
	LastTurn     string `gorm:"size:32"`
	NextEntry    int64
	NextPending  int64
	SkillsOff    string `gorm:"type:text"` // JSON list of skill names
	AddonsOff    string `gorm:"type:text"` // JSON list of add-on IDs
	McpOff       string `gorm:"type:text"` // JSON list of MCP server names and connector:<id>
	BuiltinsOff  string `gorm:"type:text"` // JSON list of built-in tool groups
	BuiltinsOn   string `gorm:"type:text"` // JSON list of opt-in built-in tool groups
	AgentID      string `gorm:"size:128"`
	Origin       string `gorm:"size:32"`
	OriginID     string `gorm:"size:128"`
	Assistant    bool
	Settings     string `gorm:"type:text"` // JSON of session.ChatSettings
	HostConsent  string `gorm:"type:text"` // JSON of session.HostConsent while a card waits
	Egress       string `gorm:"type:text"` // JSON of egress.ChatState
	EgressRev    int64  // bumped by UpdateEgress and UpdateHostConsent; their row lock
	// Goal and GoalRev came after the table existed: the defaults fill the
	// rows of chats made before, so AutoMigrate is their migration.
	Goal    string `gorm:"type:text;not null;default:''"` // JSON of session.Goal; empty: no goal
	GoalRev int64  `gorm:"not null;default:0"`            // bumped by UpdateGoal; its row lock
}

func (chatRow) TableName() string { return "studio_chats" }

type entryRow struct {
	ChatID string `gorm:"primaryKey;size:128"`
	Seq    int64  `gorm:"primaryKey"`
	Turn   int
	Role   string `gorm:"size:32"`
	Text   string `gorm:"type:text"`
	Rev    int64
	DurMs  int64
	Card   string `gorm:"type:text"`
	// Attachments is the JSON list of the files of a user message, Note the
	// text read from them; both empty for other lines.
	Attachments string `gorm:"type:text"`
	Note        string `gorm:"type:text"`
	// Loop is the JSON of the loop run that sent a user message; empty for
	// every other line.
	Loop string `gorm:"type:text"`
	// Invoked is the JSON of the command of a user message; empty otherwise.
	Invoked string `gorm:"type:text"`
	At      time.Time
}

func (entryRow) TableName() string { return "studio_entries" }

// toEntryRow is the row of entry e of chat id with sequence number seq.
func toEntryRow(id string, seq int64, e session.Entry) (entryRow, error) {
	r := entryRow{ChatID: id, Seq: seq, Turn: e.Turn, Role: e.Role, Text: e.Text, Rev: e.Rev,
		DurMs: e.Dur.Milliseconds(), Note: e.Note, At: e.At}
	if e.Card != nil {
		raw, err := json.Marshal(e.Card)
		if err != nil {
			return entryRow{}, err
		}
		r.Card = string(raw)
	}
	if len(e.Attachments) > 0 {
		raw, err := json.Marshal(e.Attachments)
		if err != nil {
			return entryRow{}, err
		}
		r.Attachments = string(raw)
	}
	if e.Loop != nil {
		raw, err := json.Marshal(e.Loop)
		if err != nil {
			return entryRow{}, err
		}
		r.Loop = string(raw)
	}
	if e.Invoked != nil {
		raw, err := json.Marshal(e.Invoked)
		if err != nil {
			return entryRow{}, err
		}
		r.Invoked = string(raw)
	}
	return r, nil
}

type pendingRow struct {
	ChatID    string `gorm:"primaryKey;size:128"`
	ID        string `gorm:"primaryKey;size:64"`
	Seq       int64
	Text      string `gorm:"type:text"`
	Delivered bool
	// Unconfirmed: taken by a model request, not yet recorded (see Pending).
	Unconfirmed bool
	At          time.Time
}

func (pendingRow) TableName() string { return "studio_pending" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("studio store: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&chatRow{}, &entryRow{}, &pendingRow{}); err != nil {
		return nil, fmt.Errorf("studio store: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (r chatRow) record() (session.ChatRecord, error) {
	rec := session.ChatRecord{
		ID: r.ID, Principal: r.Principal, Title: r.Title, Folder: r.Folder,
		Created: r.Created, Updated: r.Updated, HarnessState: append([]byte(nil), r.HarnessState...),
		Turns: r.Turns, Kind: r.Kind, ParentID: r.ParentID, Model: r.Model, Status: r.Status,
		AgentID: r.AgentID, Origin: r.Origin, OriginID: r.OriginID, Assistant: r.Assistant,
	}
	if r.SkillsOff != "" {
		if err := json.Unmarshal([]byte(r.SkillsOff), &rec.SkillsOff); err != nil {
			return rec, err
		}
	}
	if r.AddonsOff != "" {
		if err := json.Unmarshal([]byte(r.AddonsOff), &rec.AddonsOff); err != nil {
			return rec, err
		}
	}
	if r.McpOff != "" {
		if err := json.Unmarshal([]byte(r.McpOff), &rec.McpOff); err != nil {
			return rec, err
		}
	}
	if r.BuiltinsOff != "" {
		if err := json.Unmarshal([]byte(r.BuiltinsOff), &rec.BuiltinsOff); err != nil {
			return rec, err
		}
	}
	if r.BuiltinsOn != "" {
		if err := json.Unmarshal([]byte(r.BuiltinsOn), &rec.BuiltinsOn); err != nil {
			return rec, err
		}
	}
	if r.Settings != "" {
		if err := json.Unmarshal([]byte(r.Settings), &rec.Settings); err != nil {
			return rec, err
		}
	}
	if r.Egress != "" {
		if err := json.Unmarshal([]byte(r.Egress), &rec.Egress); err != nil {
			return rec, err
		}
	}
	if r.Goal != "" {
		var g session.Goal
		if err := json.Unmarshal([]byte(r.Goal), &g); err != nil {
			return rec, err
		}
		rec.Goal = &g
	}
	if r.HostConsent != "" {
		var c session.HostConsent
		if err := json.Unmarshal([]byte(r.HostConsent), &c); err != nil {
			return rec, err
		}
		rec.HostConsent = &c
	}
	if r.Snapshot != "" {
		var ref sandbox.SnapshotRef
		if err := json.Unmarshal([]byte(r.Snapshot), &ref); err != nil {
			return rec, err
		}
		rec.Snapshot = &ref
	}
	return rec, nil
}

func snapshotJSON(ref *sandbox.SnapshotRef) (string, error) {
	if ref == nil {
		return "", nil
	}
	raw, err := json.Marshal(ref)
	return string(raw), err
}

func (s *gormStore) CreateChat(ctx context.Context, rec session.ChatRecord) error {
	snap, err := snapshotJSON(rec.Snapshot)
	if err != nil {
		return err
	}
	row := chatRow{
		ID: rec.ID, Principal: rec.Principal, Title: rec.Title, Folder: rec.Folder,
		Created: rec.Created, Updated: rec.Updated, Snapshot: snap, HarnessState: rec.HarnessState,
		Turns: rec.Turns, Kind: rec.Kind, ParentID: rec.ParentID, Model: rec.Model, Status: rec.Status,
		AgentID: rec.AgentID, Origin: rec.Origin, OriginID: rec.OriginID, Assistant: rec.Assistant,
	}
	// A chat can start with its switches (the composer of a new chat drafts
	// them), so they are written with the row.
	for _, l := range []struct {
		to   *string
		list []string
	}{{&row.SkillsOff, rec.SkillsOff}, {&row.AddonsOff, rec.AddonsOff}, {&row.McpOff, rec.McpOff}, {&row.BuiltinsOff, rec.BuiltinsOff}, {&row.BuiltinsOn, rec.BuiltinsOn}} {
		if len(l.list) == 0 {
			continue
		}
		raw, err := json.Marshal(l.list)
		if err != nil {
			return err
		}
		*l.to = string(raw)
	}
	if rec.Settings != (session.ChatSettings{}) {
		raw, err := json.Marshal(rec.Settings)
		if err != nil {
			return err
		}
		row.Settings = string(raw)
	}
	if !rec.Egress.Empty() {
		raw, err := json.Marshal(rec.Egress)
		if err != nil {
			return err
		}
		row.Egress = string(raw)
	}
	if rec.Goal != nil {
		raw, err := json.Marshal(rec.Goal)
		if err != nil {
			return err
		}
		row.Goal = string(raw)
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		var n int64
		if s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ?", rec.ID).Count(&n).Error == nil && n > 0 {
			return session.ErrExists // also when the ID belongs to another principal
		}
		return err
	}
	return nil
}

func (s *gormStore) GetChat(ctx context.Context, principal, id string) (session.ChatRecord, error) {
	var row chatRow
	err := s.db.WithContext(ctx).Where("id = ? AND principal = ?", id, principal).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return session.ChatRecord{}, session.ErrNotFound
	}
	if err != nil {
		return session.ChatRecord{}, err
	}
	return row.record()
}

func (s *gormStore) ListChats(ctx context.Context, principal string, before session.ChatCursor, limit int) ([]session.ChatRecord, error) {
	q := s.db.WithContext(ctx).Where("principal = ?", principal)
	if !before.Updated.IsZero() {
		q = q.Where("updated < ? OR (updated = ? AND id < ?)", before.Updated, before.Updated, before.ID)
	}
	q = q.Order("updated DESC, id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []chatRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]session.ChatRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := r.record()
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *gormStore) UpdateChat(ctx context.Context, rec session.ChatRecord) error {
	snap, err := snapshotJSON(rec.Snapshot)
	if err != nil {
		return err
	}
	res := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", rec.ID, rec.Principal).
		Updates(map[string]any{
			"title": rec.Title, "folder": rec.Folder, "snapshot": snap, "harness_state": rec.HarnessState,
			"turns": rec.Turns, "updated": rec.Updated, "status": rec.Status,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return session.ErrNotFound
	}
	return nil
}

// setList saves a list of names in a column of the chat row; an empty list is
// the empty text.
func (s *gormStore) setList(ctx context.Context, column, principal, id string, list []string) error {
	raw := ""
	if len(list) > 0 {
		b, err := json.Marshal(list)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	res := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Update(column, raw)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *gormStore) SetSkillsOff(ctx context.Context, principal, id string, off []string) error {
	return s.setList(ctx, "skills_off", principal, id, off)
}

func (s *gormStore) SetAddonsOff(ctx context.Context, principal, id string, off []string) error {
	return s.setList(ctx, "addons_off", principal, id, off)
}

func (s *gormStore) SetBuiltinsOff(ctx context.Context, principal, id string, off []string) error {
	return s.setList(ctx, "builtins_off", principal, id, off)
}

func (s *gormStore) SetBuiltinsOn(ctx context.Context, principal, id string, on []string) error {
	return s.setList(ctx, "builtins_on", principal, id, on)
}

func (s *gormStore) SetHostConsent(ctx context.Context, principal, id string, c *session.HostConsent) error {
	raw := ""
	if c != nil {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	res := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Update("host_consent", raw)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *gormStore) SetMcpOff(ctx context.Context, principal, id string, off []string) error {
	return s.setList(ctx, "mcp_off", principal, id, off)
}

func (s *gormStore) ListChildren(ctx context.Context, principal, parentID string) ([]session.ChatRecord, error) {
	var rows []chatRow
	err := s.db.WithContext(ctx).Where("principal = ? AND parent_id = ? AND kind = ?", principal, parentID, session.KindSubAgent).
		Order("created ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]session.ChatRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := r.record()
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *gormStore) SetChatMeta(ctx context.Context, principal, id string, m session.ChatMeta) error {
	cols := map[string]any{}
	if m.Title != nil {
		cols["title"] = *m.Title
	}
	if m.Model != nil {
		cols["model"] = *m.Model
	}
	if m.Settings != nil {
		raw := ""
		if *m.Settings != (session.ChatSettings{}) {
			b, err := json.Marshal(m.Settings)
			if err != nil {
				return err
			}
			raw = string(b)
		}
		cols["settings"] = raw
	}
	if len(cols) == 0 {
		_, err := s.GetChat(ctx, principal, id)
		return err
	}
	res := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Updates(cols)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *gormStore) UpdateHostConsent(ctx context.Context, principal, id string, change func(*session.HostConsent) (*session.HostConsent, error)) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The same row lock as the network state: the two cards of a chat
		// are rare and short, so they may wait on each other.
		if _, err := lock(tx, principal, id, "egress_rev"); err != nil {
			return err
		}
		var raw string
		if err := tx.Model(&chatRow{}).Where("id = ?", id).Select("host_consent").Row().Scan(&raw); err != nil {
			return err
		}
		var cur *session.HostConsent
		if raw != "" {
			var c session.HostConsent
			if err := json.Unmarshal([]byte(raw), &c); err != nil {
				return err
			}
			cur = &c
		}
		out, err := change(cur)
		if err != nil {
			return err
		}
		val := ""
		if out != nil {
			b, err := json.Marshal(out)
			if err != nil {
				return err
			}
			val = string(b)
		}
		return tx.Model(&chatRow{}).Where("id = ?", id).UpdateColumn("host_consent", val).Error
	})
}

func (s *gormStore) UpdateEgress(ctx context.Context, principal, id string, change func(*egress.ChatState) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The counter bump takes the row lock first, so a second frontend's
		// change waits for this one and then reads what it wrote.
		if _, err := lock(tx, principal, id, "egress_rev"); err != nil {
			return err
		}
		var raw string
		if err := tx.Model(&chatRow{}).Where("id = ?", id).Select("egress").Row().Scan(&raw); err != nil {
			return err
		}
		var st egress.ChatState
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &st); err != nil {
				return err
			}
		}
		if err := change(&st); err != nil {
			return err
		}
		out := ""
		if !st.Empty() {
			b, err := json.Marshal(st)
			if err != nil {
				return err
			}
			out = string(b)
		}
		return tx.Model(&chatRow{}).Where("id = ?", id).UpdateColumn("egress", out).Error
	})
}

func (s *gormStore) UpdateGoal(ctx context.Context, principal, id string, change func(*session.Goal) (*session.Goal, error)) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// As in UpdateEgress: the bump takes the row lock, so a second
		// frontend's change waits and then reads what this one wrote.
		if _, err := lock(tx, principal, id, "goal_rev"); err != nil {
			return err
		}
		var raw string
		if err := tx.Model(&chatRow{}).Where("id = ?", id).Select("goal").Row().Scan(&raw); err != nil {
			return err
		}
		var cur *session.Goal
		if raw != "" {
			cur = &session.Goal{}
			if err := json.Unmarshal([]byte(raw), cur); err != nil {
				return err
			}
		}
		next, err := change(cur)
		if err != nil {
			return err
		}
		out := ""
		if next != nil {
			b, err := json.Marshal(next)
			if err != nil {
				return err
			}
			out = string(b)
		}
		return tx.Model(&chatRow{}).Where("id = ?", id).UpdateColumn("goal", out).Error
	})
}

func (s *gormStore) TruncateEntries(ctx context.Context, principal, id string, keep int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lock(tx, principal, id, "next_entry"); err != nil {
			return err
		}
		if err := tx.Where("chat_id = ? AND seq > ?", id, keep).Delete(&entryRow{}).Error; err != nil {
			return err
		}
		// lock bumped the counter; the next entry is keep+1.
		return tx.Model(&chatRow{}).Where("id = ?", id).UpdateColumn("next_entry", keep).Error
	})
}

func (s *gormStore) DeleteChat(ctx context.Context, principal, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND principal = ?", id, principal).Delete(&chatRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return session.ErrNotFound
		}
		if err := tx.Where("chat_id = ?", id).Delete(&entryRow{}).Error; err != nil {
			return err
		}
		return tx.Where("chat_id = ?", id).Delete(&pendingRow{}).Error
	})
}

// lock takes the row lock of the chat by bumping a counter column and returns
// the new value. It is session.ErrNotFound when principal does not own the chat.
// principal "" means any owner (the transcript calls have none).
func lock(tx *gorm.DB, principal, id, column string) (int64, error) {
	q := tx.Model(&chatRow{}).Where("id = ?", id)
	if principal != "" {
		q = q.Where("principal = ?", principal)
	}
	res := q.UpdateColumn(column, gorm.Expr(column+" + 1"))
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, session.ErrNotFound
	}
	var n int64
	if err := tx.Model(&chatRow{}).Where("id = ?", id).Select(column).Row().Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *gormStore) AppendEntry(ctx context.Context, id string, e session.Entry) (int64, error) {
	var stored int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seq, err := lock(tx, "", id, "next_entry")
		if err != nil {
			return err
		}
		if e.At.IsZero() {
			e.At = time.Now().UTC()
		}
		row, err := toEntryRow(id, seq, e)
		if err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		stored = seq
		return nil
	})
	if err != nil {
		return 0, err
	}
	return stored, nil
}

func (s *gormStore) AppendEntries(ctx context.Context, id string, entries []session.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		first, err := lock(tx, "", id, "next_entry")
		if err != nil {
			return err
		}
		// lock took one number; take the rest of the batch with it.
		if extra := int64(len(entries) - 1); extra > 0 {
			if err := tx.Model(&chatRow{}).Where("id = ?", id).UpdateColumn("next_entry", gorm.Expr("next_entry + ?", extra)).Error; err != nil {
				return err
			}
		}
		rows := make([]entryRow, len(entries))
		for i, e := range entries {
			if e.At.IsZero() {
				e.At = time.Now().UTC()
			}
			row, err := toEntryRow(id, first+int64(i), e)
			if err != nil {
				return err
			}
			rows[i] = row
		}
		return tx.CreateInBatches(&rows, 100).Error
	})
}

func (s *gormStore) Entries(ctx context.Context, id string, after int64, limit int) ([]session.Entry, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, session.ErrNotFound
	}
	q := s.db.WithContext(ctx).Where("chat_id = ? AND seq > ?", id, after).Order("seq ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []entryRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]session.Entry, 0, len(rows))
	for _, r := range rows {
		e := session.Entry{
			Seq: r.Seq, Turn: r.Turn, Role: r.Role, Text: r.Text, Rev: r.Rev,
			Dur: time.Duration(r.DurMs) * time.Millisecond, Note: r.Note, At: r.At,
		}
		if r.Attachments != "" {
			if err := json.Unmarshal([]byte(r.Attachments), &e.Attachments); err != nil {
				return nil, err
			}
		}
		if r.Loop != "" {
			var l session.LoopMark
			if err := json.Unmarshal([]byte(r.Loop), &l); err != nil {
				return nil, err
			}
			e.Loop = &l
		}
		if r.Card != "" {
			var c session.ToolCard
			if err := json.Unmarshal([]byte(r.Card), &c); err != nil {
				return nil, err
			}
			e.Card = &c
		}
		if r.Invoked != "" {
			var inv session.Invocation
			if err := json.Unmarshal([]byte(r.Invoked), &inv); err != nil {
				return nil, err
			}
			e.Invoked = &inv
		}
		out = append(out, e)
	}
	return out, nil
}

func toPending(r pendingRow) Pending {
	return Pending{ID: r.ID, Text: r.Text, Seq: r.Seq, Delivered: r.Delivered, Unconfirmed: r.Unconfirmed, At: r.At}
}

func (s *gormStore) AddPending(ctx context.Context, principal, id, text string) (Pending, error) {
	var out Pending
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seq, err := lock(tx, principal, id, "next_pending")
		if err != nil {
			return err
		}
		row := pendingRow{ChatID: id, ID: fmt.Sprintf("p%d", seq), Seq: seq, Text: text, At: time.Now().UTC()}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		out = toPending(row)
		return nil
	})
	return out, err
}

// owns is session.ErrNotFound unless principal owns the chat.
func (s *gormStore) owns(tx *gorm.DB, principal, id string) error {
	var n int64
	if err := tx.Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *gormStore) Pending(ctx context.Context, principal, id string) ([]Pending, error) {
	db := s.db.WithContext(ctx)
	if err := s.owns(db, principal, id); err != nil {
		return nil, err
	}
	var rows []pendingRow
	if err := db.Where("chat_id = ? AND delivered = ?", id, false).Order("seq ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Pending, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPending(r))
	}
	return out, nil
}

// withChatLock runs f in a transaction that holds the chat's row lock.
func (s *gormStore) withChatLock(ctx context.Context, principal, id string, f func(tx *gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A write that changes nothing still takes the row lock.
		res := tx.Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).UpdateColumn("principal", principal)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return session.ErrNotFound
		}
		return f(tx)
	})
}

func (s *gormStore) EditPending(ctx context.Context, principal, id, pendingID, text string) error {
	return s.withChatLock(ctx, principal, id, func(tx *gorm.DB) error {
		var row pendingRow
		if err := tx.Where("chat_id = ? AND id = ?", id, pendingID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return session.ErrNotFound
			}
			return err
		}
		if row.Delivered {
			return ErrDelivered
		}
		return tx.Model(&pendingRow{}).Where("chat_id = ? AND id = ?", id, pendingID).Update("text", text).Error
	})
}

func (s *gormStore) CancelPending(ctx context.Context, principal, id, pendingID string) error {
	return s.withChatLock(ctx, principal, id, func(tx *gorm.DB) error {
		var row pendingRow
		if err := tx.Where("chat_id = ? AND id = ?", id, pendingID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return session.ErrNotFound
			}
			return err
		}
		if row.Delivered {
			return ErrDelivered
		}
		return tx.Where("chat_id = ? AND id = ?", id, pendingID).Delete(&pendingRow{}).Error
	})
}

func (s *gormStore) TakePending(ctx context.Context, principal, id string) ([]Pending, error) {
	var out []Pending
	err := s.withChatLock(ctx, principal, id, func(tx *gorm.DB) error {
		var rows []pendingRow
		if err := tx.Where("chat_id = ? AND delivered = ?", id, false).Order("seq ASC").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := tx.Model(&pendingRow{}).Where("chat_id = ? AND delivered = ?", id, false).
			Updates(map[string]any{"delivered": true, "unconfirmed": true}).Error; err != nil {
			return err
		}
		for _, r := range rows {
			p := toPending(r)
			p.Delivered, p.Unconfirmed = true, true
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

func (s *gormStore) RestorePending(ctx context.Context, principal, id string, ids []string) error {
	return s.withChatLock(ctx, principal, id, func(tx *gorm.DB) error {
		if len(ids) == 0 {
			return nil
		}
		return tx.Model(&pendingRow{}).Where("chat_id = ? AND id IN ?", id, ids).
			Updates(map[string]any{"delivered": false, "unconfirmed": false}).Error
	})
}

func (s *gormStore) ConfirmPending(ctx context.Context, principal, id string, ids []string) error {
	return s.withChatLock(ctx, principal, id, func(tx *gorm.DB) error {
		if len(ids) == 0 {
			return nil
		}
		return tx.Model(&pendingRow{}).Where("chat_id = ? AND id IN ?", id, ids).Update("unconfirmed", false).Error
	})
}

func (s *gormStore) ClaimTurn(ctx context.Context, principal, id, runID string) error {
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND (run_id = ? OR run_id IS NULL)", id, principal, "").
		Updates(map[string]any{"run_id": runID, "run_since": time.Now().UTC(), "run_beat": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	if err := s.owns(db, principal, id); err != nil {
		return err
	}
	return ErrTurnRunning
}

func (s *gormStore) ReleaseTurn(ctx context.Context, principal, id, runID string, status TurnStatus) error {
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND run_id = ?", id, principal, runID).
		Updates(map[string]any{"run_id": "", "run_since": time.Time{}, "run_beat": time.Time{}, "last_turn": string(status)})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	if err := s.owns(db, principal, id); err != nil {
		return err
	}
	return ErrNotClaimed
}

func (s *gormStore) Turn(ctx context.Context, principal, id string) (TurnInfo, error) {
	var row chatRow
	err := s.db.WithContext(ctx).Where("id = ? AND principal = ?", id, principal).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TurnInfo{}, session.ErrNotFound
	}
	if err != nil {
		return TurnInfo{}, err
	}
	return TurnInfo{Running: row.RunID != "", RunID: row.RunID, Since: row.RunSince, Beat: row.RunBeat, Last: TurnStatus(row.LastTurn)}, nil
}

func (s *gormStore) TouchTurn(ctx context.Context, principal, id, runID string) error {
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND run_id = ?", id, principal, runID).
		Update("run_beat", time.Now().UTC())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	if err := s.owns(db, principal, id); err != nil {
		return err
	}
	return ErrNotClaimed
}

func (s *gormStore) ReapTurns(ctx context.Context, olderThan time.Time) ([]ReapedTurn, error) {
	var rows []chatRow
	err := s.db.WithContext(ctx).
		Where("run_id <> ? AND run_id IS NOT NULL AND run_beat < ?", "", olderThan).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	var out []ReapedTurn
	for _, r := range rows {
		// Conditional on the same run and beat, so a turn that beat again
		// after the read, or that already ended, is left alone. The restore of
		// the lost messages shares the transaction: a reaped chat whose
		// messages stayed marked delivered would drop them for good.
		reaped := false
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&chatRow{}).
				Where("id = ? AND run_id = ? AND run_beat < ?", r.ID, r.RunID, olderThan).
				Updates(map[string]any{"run_id": "", "run_since": time.Time{}, "run_beat": time.Time{}, "last_turn": string(TurnInterrupted)})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return nil
			}
			reaped = true
			// What a request took and nobody confirmed was lost with the frontend.
			return tx.Model(&pendingRow{}).Where("chat_id = ? AND unconfirmed = ?", r.ID, true).
				Updates(map[string]any{"delivered": false, "unconfirmed": false}).Error
		})
		if err != nil {
			return out, err
		}
		if reaped {
			out = append(out, ReapedTurn{Principal: r.Principal, ChatID: r.ID, RunID: r.RunID})
		}
	}
	return out, nil
}
