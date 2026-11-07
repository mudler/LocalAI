package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/connsets"
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
	Connections  string `gorm:"type:text"` // JSON object, add-on ID to a list of binding IDs (older rows: one ID)
	AgentID      string `gorm:"size:128"`
	Origin       string `gorm:"size:32"`
	OriginID     string `gorm:"size:128"`
	Assistant    bool
	// ScopedAgent came after the table existed: the default reads the rows of
	// chats made before as not scoped, so AutoMigrate is its migration.
	ScopedAgent bool   `gorm:"not null;default:false"`
	Settings    string `gorm:"type:text"` // JSON of session.ChatSettings
	// AllowedTools came after the table existed: it is a nullable text
	// column, so AutoMigrate adds it and older rows read as no list.
	AllowedTools string `gorm:"type:text"` // JSON list of the tool names of a sub-agent
	HostConsent  string `gorm:"type:text"` // JSON of session.HostConsent while a card waits
	Egress       string `gorm:"type:text"` // JSON of egress.ChatState
	EgressRev    int64  // bumped by UpdateEgress and UpdateHostConsent; their row lock
	// Goal and GoalRev came after the table existed: the defaults fill the
	// rows of chats made before, so AutoMigrate is their migration.
	Goal    string `gorm:"type:text;not null;default:''"` // JSON of session.Goal; empty: no goal
	GoalRev int64  `gorm:"not null;default:0"`            // bumped by UpdateGoal; its row lock
	// The resume fields came after the table existed: the defaults read the
	// rows of chats made before as having no resume, so AutoMigrate is their
	// migration. ResumeNotBefore is the zero time while no resume is scheduled.
	ResumeAttempts  int       `gorm:"not null;default:0"`
	ResumeNotBefore time.Time `gorm:"index"`
	InterruptedAt   time.Time
	// PartialText is the assistant text being written (session.PartialStore),
	// and PartialTurn its turn. Empty while no message is streaming.
	PartialText string `gorm:"type:text"`
	PartialTurn int
	// OwnerInstance came after the table existed: a nullable column, so
	// AutoMigrate adds it and older rows read as an unknown owner. It names
	// the process that ran the last turn (SetTurnOwner).
	OwnerInstance string `gorm:"size:128"`
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
	// Sender is the JSON of the sender of a user message that a messaging
	// connection delivered; empty otherwise. The column came after the table
	// existed, so AutoMigrate adds it and older rows read as empty.
	Sender string `gorm:"type:text"`
	At     time.Time
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
	if e.Sender != nil {
		raw, err := json.Marshal(e.Sender)
		if err != nil {
			return entryRow{}, err
		}
		r.Sender = string(raw)
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
	// Sender is the JSON of the sender of a connector message; empty otherwise.
	Sender string `gorm:"type:text"`
}

func (pendingRow) TableName() string { return "studio_pending" }

type gormStore struct {
	db   *gorm.DB
	text textIndex

	// The backfill of the chat search index runs in the background: Close
	// stops it and waits, so nothing writes once the owner of the database
	// (or of its folder) is gone.
	stopBackfill context.CancelFunc
	backfillDone chan struct{}
}

// Close stops the background indexing of earlier chats and waits for it. It
// does not close the database, which the caller owns.
func (s *gormStore) Close() error {
	if s.stopBackfill != nil {
		s.stopBackfill()
		<-s.backfillDone
	}
	return nil
}

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("studio store: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&chatRow{}, &entryRow{}, &pendingRow{}, &textRow{}, &textStateRow{}); err != nil {
		return nil, fmt.Errorf("studio store: migrate: %w", err)
	}
	s := &gormStore{db: db}
	if err := s.migrateDocumentSkillsOff(); err != nil {
		// Reads normalize the markers in memory, so the store works anyway.
		xlog.Warn("studio store: could not rewrite the legacy skill markers", "error", err)
	}
	s.setupTextIndex()
	// Indexing is true from here, so a search never reports the index as
	// complete before the first look at the history.
	s.text.indexing.Store(true)
	var ctx context.Context
	ctx, s.stopBackfill = context.WithCancel(context.Background())
	s.backfillDone = make(chan struct{})
	go func() {
		defer close(s.backfillDone)
		s.backfillTexts(ctx)
	}()
	return s, nil
}

// migrateDocumentSkillsOff rewrites the legacy OFF markers of the document
// skills (builtin:docx and the like) to their add-on names. It is idempotent.
// Each row changes only if it still holds the value that was read, so it cannot
// undo a concurrent explicit re-enable.
func (s *gormStore) migrateDocumentSkillsOff() error {
	var rows []chatRow
	if err := s.db.Select("id", "skills_off").Where("skills_off LIKE ?", "%builtin:%").Find(&rows).Error; err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		var off []string
		if json.Unmarshal([]byte(r.SkillsOff), &off) != nil {
			continue
		}
		clean, changed := normalizeDocumentSkillsOff(off)
		if !changed {
			continue
		}
		raw, err := json.Marshal(clean)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.db.Model(&chatRow{}).Where("id = ? AND skills_off = ?", r.ID, r.SkillsOff).Update("skills_off", string(raw)).Error; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r chatRow) record() (session.ChatRecord, error) {
	rec := session.ChatRecord{
		ID: r.ID, Principal: r.Principal, Title: r.Title, Folder: r.Folder,
		Created: r.Created, Updated: r.Updated, HarnessState: append([]byte(nil), r.HarnessState...),
		Turns: r.Turns, Kind: r.Kind, ParentID: r.ParentID, Model: r.Model, Status: r.Status,
		AgentID: r.AgentID, Origin: r.Origin, OriginID: r.OriginID, Assistant: r.Assistant,
		ScopedAgent: r.ScopedAgent,
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
	if r.Connections != "" {
		if err := json.Unmarshal([]byte(r.Connections), &rec.Connections); err != nil {
			return rec, err
		}
	}
	if r.AllowedTools != "" {
		if err := json.Unmarshal([]byte(r.AllowedTools), &rec.AllowedTools); err != nil {
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
		ScopedAgent: rec.ScopedAgent,
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
	if len(rec.AllowedTools) > 0 {
		raw, err := json.Marshal(rec.AllowedTools)
		if err != nil {
			return err
		}
		row.AllowedTools = string(raw)
	}
	if len(rec.Connections) > 0 {
		raw, err := json.Marshal(rec.Connections)
		if err != nil {
			return err
		}
		row.Connections = string(raw)
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
	rec, err := row.record()
	if err != nil {
		return session.ChatRecord{}, err
	}
	// A read never writes. Rows written before the document skills moved to
	// add-ons are rewritten when the store opens; this covers a row that an
	// older process wrote since.
	rec.SkillsOff, _ = normalizeDocumentSkillsOff(rec.SkillsOff)
	return rec, nil
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
	return setListDB(s.db.WithContext(ctx), column, principal, id, list)
}

func setListDB(db *gorm.DB, column, principal, id string, list []string) error {
	raw := ""
	if len(list) > 0 {
		b, err := json.Marshal(list)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Update(column, raw)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *gormStore) SetSkillsOff(ctx context.Context, principal, id string, off []string) error {
	clean, _ := normalizeDocumentSkillsOff(off)
	return s.setList(ctx, "skills_off", principal, id, clean)
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

func (s *gormStore) SetConnections(ctx context.Context, principal, id string, connections connsets.Sets) error {
	raw := ""
	if len(connections) > 0 {
		b, err := json.Marshal(connections)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	res := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Update("connections", raw)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var n int64
		if err := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return session.ErrNotFound
		}
	}
	return nil
}

func (s *gormStore) SetScopedAgent(ctx context.Context, principal, id string, scoped bool) error {
	res := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Update("scoped_agent", scoped)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// An update to the value the row already has counts no row on some
		// drivers; tell that apart from a missing chat.
		var n int64
		if err := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return session.ErrNotFound
		}
	}
	return nil
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

func (s *gormStore) SavePartial(ctx context.Context, id string, turn int, text string) error {
	text = session.CapPartial(text)
	res := s.db.WithContext(ctx).Model(&chatRow{}).Where("id = ?", id).
		Updates(map[string]any{"partial_text": text, "partial_turn": turn})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *gormStore) TakePartial(ctx context.Context, id string) (int, string, error) {
	var turn int
	var text string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row chatRow
		if err := tx.Select("partial_text", "partial_turn").Where("id = ?", id).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return session.ErrNotFound
			}
			return err
		}
		if row.PartialText == "" {
			return nil
		}
		// Conditional on the text read, so a second caller that read the same
		// text finds it gone and gets none.
		res := tx.Model(&chatRow{}).Where("id = ? AND partial_text = ?", id, row.PartialText).
			Updates(map[string]any{"partial_text": "", "partial_turn": 0})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			turn, text = row.PartialTurn, row.PartialText
		}
		return nil
	})
	return turn, text, err
}

func (s *gormStore) TruncateEntries(ctx context.Context, principal, id string, keep int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lock(tx, principal, id, "next_entry"); err != nil {
			return err
		}
		// The text of an answer that was cut belongs to the lines that are gone.
		if err := tx.Model(&chatRow{}).Where("id = ?", id).Updates(map[string]any{"partial_text": "", "partial_turn": 0}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ? AND seq > ?", id, keep).Delete(&entryRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ? AND seq > ?", id, keep).Delete(&textRow{}).Error; err != nil {
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
		if err := tx.Where("chat_id = ?", id).Delete(&textRow{}).Error; err != nil {
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
		if err := putTexts(tx, textRows(id, seq, []session.Entry{e})); err != nil {
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
		stamped := make([]session.Entry, len(entries))
		for i, e := range entries {
			if e.At.IsZero() {
				e.At = time.Now().UTC()
			}
			stamped[i] = e
			row, err := toEntryRow(id, first+int64(i), e)
			if err != nil {
				return err
			}
			rows[i] = row
		}
		if err := tx.CreateInBatches(&rows, 100).Error; err != nil {
			return err
		}
		return putTexts(tx, textRows(id, first, stamped))
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
		if r.Sender != "" {
			var sd session.Sender
			if err := json.Unmarshal([]byte(r.Sender), &sd); err != nil {
				return nil, err
			}
			e.Sender = &sd
		}
		out = append(out, e)
	}
	return out, nil
}

func toPending(r pendingRow) Pending {
	p := Pending{ID: r.ID, Text: r.Text, Seq: r.Seq, Delivered: r.Delivered, Unconfirmed: r.Unconfirmed, At: r.At}
	if r.Sender != "" {
		var sd session.Sender
		if json.Unmarshal([]byte(r.Sender), &sd) == nil {
			p.Sender = &sd
		}
	}
	return p
}

func (s *gormStore) AddPending(ctx context.Context, principal, id, text string) (Pending, error) {
	return s.AddPendingFrom(ctx, principal, id, text, nil)
}

func (s *gormStore) AddPendingFrom(ctx context.Context, principal, id, text string, sender *session.Sender) (Pending, error) {
	var out Pending
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seq, err := lock(tx, principal, id, "next_pending")
		if err != nil {
			return err
		}
		row := pendingRow{ChatID: id, ID: fmt.Sprintf("p%d", seq), Seq: seq, Text: text, At: time.Now().UTC()}
		if sender != nil {
			raw, err := json.Marshal(sender)
			if err != nil {
				return err
			}
			row.Sender = string(raw)
		}
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

// now returns the time of the database server. A turn's beat is written by the
// frontend that runs it and read by the one that reaps, so both use this one
// clock: a skew between the machines must not end a live turn.
func (s *gormStore) now(ctx context.Context) (time.Time, error) {
	switch s.db.Dialector.Name() {
	case "sqlite":
		var v string
		if err := s.db.WithContext(ctx).Raw("SELECT strftime('%Y-%m-%d %H:%M:%f', 'now')").Scan(&v).Error; err != nil {
			return time.Time{}, err
		}
		return time.ParseInLocation("2006-01-02 15:04:05.000", v, time.UTC)
	case "postgres":
		var t time.Time
		if err := s.db.WithContext(ctx).Raw("SELECT now()").Scan(&t).Error; err != nil {
			return time.Time{}, err
		}
		return t.UTC(), nil
	}
	return time.Now().UTC(), nil
}

func (s *gormStore) ClaimTurn(ctx context.Context, principal, id, runID string) error {
	now, err := s.now(ctx)
	if err != nil {
		return err
	}
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND (run_id = ? OR run_id IS NULL)", id, principal, "").
		Updates(map[string]any{"run_id": runID, "run_since": now, "run_beat": now,
			// A new turn starts the count of resumes again.
			"resume_attempts": 0, "resume_not_before": time.Time{}, "interrupted_at": time.Time{}})
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

func (s *gormStore) ClaimResume(ctx context.Context, principal, id, runID string) error {
	now, err := s.now(ctx)
	if err != nil {
		return err
	}
	db := s.db.WithContext(ctx)
	// The conditions and the count are one statement, so two sweepers cannot
	// both win and a lost race does not spend an attempt.
	res := db.Model(&chatRow{}).
		Where("id = ? AND principal = ? AND (run_id = ? OR run_id IS NULL) AND last_turn = ? AND resume_attempts < ? AND resume_not_before > ? AND resume_not_before <= ? AND interrupted_at >= ?",
			id, principal, "", string(TurnInterrupted), MaxResumeAttempts, time.Time{}, now, now.Add(-ResumeWindow)).
		Updates(map[string]any{"run_id": runID, "run_since": now, "run_beat": now,
			"resume_attempts": gorm.Expr("resume_attempts + 1"), "resume_not_before": time.Time{}})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	if err := s.owns(db, principal, id); err != nil {
		return err
	}
	var row chatRow
	if err := db.Select("run_id").Where("id = ?", id).Take(&row).Error; err == nil && row.RunID != "" {
		return ErrTurnRunning
	}
	return ErrNotResumable
}

func (s *gormStore) ListResumable(ctx context.Context) ([]ResumableTurn, error) {
	now, err := s.now(ctx)
	if err != nil {
		return nil, err
	}
	var rows []chatRow
	err = s.db.WithContext(ctx).
		Select("id", "principal", "resume_attempts", "interrupted_at", "resume_not_before").
		Where("(run_id = ? OR run_id IS NULL) AND last_turn = ? AND resume_attempts < ? AND resume_not_before > ? AND resume_not_before <= ? AND interrupted_at >= ?",
			"", string(TurnInterrupted), MaxResumeAttempts, time.Time{}, now, now.Add(-ResumeWindow)).
		Order("resume_not_before").Limit(200).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]ResumableTurn, 0, len(rows))
	for _, r := range rows {
		out = append(out, ResumableTurn{Principal: r.Principal, ChatID: r.ID, Attempts: r.ResumeAttempts, InterruptedAt: zeroTime(r.InterruptedAt)})
	}
	return out, nil
}

func (s *gormStore) CancelResume(ctx context.Context, principal, id string) error {
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ?", id, principal).
		Update("resume_not_before", time.Time{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	return s.owns(db, principal, id)
}

func (s *gormStore) MarkInterrupted(ctx context.Context, principal, id, runID string, delay time.Duration) error {
	now, err := s.now(ctx)
	if err != nil {
		return err
	}
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND run_id = ?", id, principal, runID).
		Updates(interruptedFields(now, delay))
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

// interruptedFields is the update that ends a claim as interrupted at now and
// schedules the resume after delay (none when delay is negative).
func interruptedFields(now time.Time, delay time.Duration) map[string]any {
	notBefore := time.Time{}
	if delay >= 0 {
		notBefore = now.Add(delay)
	}
	return map[string]any{"run_id": "", "run_since": time.Time{}, "run_beat": time.Time{},
		"last_turn": string(TurnInterrupted), "interrupted_at": now, "resume_not_before": notBefore}
}

func (s *gormStore) ReleaseTurn(ctx context.Context, principal, id, runID string, status TurnStatus) error {
	if status == TurnInterrupted {
		attempts := 0
		var row chatRow
		if err := s.db.WithContext(ctx).Select("resume_attempts").Where("id = ? AND principal = ?", id, principal).Take(&row).Error; err == nil {
			attempts = row.ResumeAttempts
		}
		return s.MarkInterrupted(ctx, principal, id, runID, ResumeBackoff(attempts))
	}
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND run_id = ?", id, principal, runID).
		Updates(map[string]any{"run_id": "", "run_since": time.Time{}, "run_beat": time.Time{}, "last_turn": string(status),
			"resume_not_before": time.Time{}, "interrupted_at": time.Time{}})
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
	info := TurnInfo{Running: row.RunID != "", RunID: row.RunID, Since: zeroTime(row.RunSince), Beat: zeroTime(row.RunBeat), Last: TurnStatus(row.LastTurn),
		ResumeAttempts: row.ResumeAttempts, InterruptedAt: zeroTime(row.InterruptedAt), ResumeNotBefore: zeroTime(row.ResumeNotBefore),
		Owner: row.OwnerInstance}
	if !info.Running && info.Last == TurnInterrupted {
		now, err := s.now(ctx)
		if err != nil {
			return TurnInfo{}, err
		}
		info.WillResume, _ = resumeOpen(info.ResumeAttempts, info.InterruptedAt, info.ResumeNotBefore, now)
	}
	return info, nil
}

// zeroTime gives the zero time.Time for a zero instant. PostgreSQL reads the
// zero time back in UTC, which IsZero accepts but is not the zero value the
// JSON store gives, so the two stores would answer differently.
func zeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t
}

func (s *gormStore) SetTurnOwner(ctx context.Context, principal, id, runID, instance string) error {
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND run_id = ?", id, principal, runID).
		Update("owner_instance", instance)
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

func (s *gormStore) TouchTurn(ctx context.Context, principal, id, runID string) error {
	now, err := s.now(ctx)
	if err != nil {
		return err
	}
	db := s.db.WithContext(ctx)
	res := db.Model(&chatRow{}).Where("id = ? AND principal = ? AND run_id = ?", id, principal, runID).
		Update("run_beat", now)
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

func (s *gormStore) ReapTurns(ctx context.Context, idle time.Duration) ([]ReapedTurn, error) {
	now, err := s.now(ctx)
	if err != nil {
		return nil, err
	}
	olderThan := now.Add(-idle)
	var rows []chatRow
	err = s.db.WithContext(ctx).
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
				Updates(interruptedFields(now, ResumeBackoff(r.ResumeAttempts)))
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
