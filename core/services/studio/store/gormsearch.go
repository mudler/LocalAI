package store

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// textRow is the search index of one message: the text the person reads,
// folded to lower case. It carries no principal: a query joins the chat row,
// so the owner of a chat can never be out of step with its index rows. The
// row is written in the transaction that writes the entry and removed in the
// one that removes it.
type textRow struct {
	ID     int64     `gorm:"primaryKey;autoIncrement"`
	ChatID string    `gorm:"size:128;not null;uniqueIndex:idx_studio_entry_texts_pos,priority:1"`
	Seq    int64     `gorm:"not null;uniqueIndex:idx_studio_entry_texts_pos,priority:2"`
	Role   string    `gorm:"size:16"`
	At     time.Time `gorm:"index:idx_studio_entry_texts_at"`
	Folded string    `gorm:"type:text"`
}

func (textRow) TableName() string { return "studio_entry_texts" }

// textStateRow remembers how far the backfill of the messages written before
// the index existed has come, so it resumes after a restart.
type textStateRow struct {
	ID       int    `gorm:"primaryKey;autoIncrement:false"`
	LastChat string `gorm:"size:128"`
	Done     bool
}

func (textStateRow) TableName() string { return "studio_entry_text_state" }

// Engines of the text search.
const (
	engineScan   = "scan"    // instr or LIKE over the folded column
	engineFTS5   = "fts5"    // SQLite FTS5 with the trigram tokenizer
	enginePGTrgm = "pg_trgm" // PostgreSQL LIKE with a trigram GIN index

	ftsTable     = "studio_entry_texts_fts"
	pgTrgmIndex  = "idx_studio_entry_texts_trgm"
	titlePool    = 200
	backfillPage = 200
	backfillStep = 50
	likeEscape   = `\`
)

// textIndex is the state of the search of a GORM store.
type textIndex struct {
	engine   string
	postgres bool
	indexing atomic.Bool
	done     atomic.Int64
	total    atomic.Int64
}

func (s *gormStore) setupTextIndex() {
	s.text.postgres = strings.Contains(s.db.Dialector.Name(), "postgres")
	s.text.engine = engineScan
	if s.text.postgres {
		s.setupPGTrgm()
	} else {
		s.setupFTS5()
	}
}

func (s *gormStore) setupFTS5() {
	var n int64
	s.db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", ftsTable).Scan(&n)
	fresh := n == 0
	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS ` + ftsTable + ` USING fts5(folded, content='studio_entry_texts', content_rowid='id', tokenize='trigram')`,
		`CREATE TRIGGER IF NOT EXISTS studio_entry_texts_ai AFTER INSERT ON studio_entry_texts BEGIN
			INSERT INTO ` + ftsTable + `(rowid, folded) VALUES (new.id, new.folded); END`,
		`CREATE TRIGGER IF NOT EXISTS studio_entry_texts_ad AFTER DELETE ON studio_entry_texts BEGIN
			INSERT INTO ` + ftsTable + `(` + ftsTable + `, rowid, folded) VALUES ('delete', old.id, old.folded); END`,
		`CREATE TRIGGER IF NOT EXISTS studio_entry_texts_au AFTER UPDATE OF folded ON studio_entry_texts BEGIN
			INSERT INTO ` + ftsTable + `(` + ftsTable + `, rowid, folded) VALUES ('delete', old.id, old.folded);
			INSERT INTO ` + ftsTable + `(rowid, folded) VALUES (new.id, new.folded); END`,
	}
	for _, q := range stmts {
		if err := s.db.Exec(q).Error; err != nil {
			xlog.Info("Chat search: SQLite has no FTS5 trigram search, using a scan", "error", err)
			return
		}
	}
	if fresh {
		// Rows written before the FTS table existed.
		if err := s.db.Exec(`INSERT INTO ` + ftsTable + `(` + ftsTable + `) VALUES ('rebuild')`).Error; err != nil {
			xlog.Warn("Chat search: could not build the FTS5 table, using a scan", "error", err)
			return
		}
	}
	s.text.engine = engineFTS5
}

func (s *gormStore) setupPGTrgm() {
	// Creating the extension needs a privilege the role may not have; a
	// failure only means the slower scan.
	_ = s.db.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error
	var n int64
	if err := s.db.Raw(`SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'`).Scan(&n).Error; err != nil || n == 0 {
		xlog.Info("Chat search: PostgreSQL has no pg_trgm extension, using a scan")
		return
	}
	if err := s.db.Exec(`CREATE INDEX IF NOT EXISTS ` + pgTrgmIndex + ` ON studio_entry_texts USING gin (folded gin_trgm_ops)`).Error; err != nil {
		xlog.Warn("Chat search: could not create the trigram index, using a scan", "error", err)
		return
	}
	s.text.engine = enginePGTrgm
}

// TextEngine says how a GORM store searches text: fts5, pg_trgm or scan. Any
// other store gives "".
func TextEngine(s Store) string {
	if g, ok := s.(*gormStore); ok {
		return g.text.engine
	}
	return ""
}

// textRows is the index rows of the entries es, which have the sequence
// numbers first, first+1, and so on.
func textRows(id string, first int64, es []session.Entry) []textRow {
	var rows []textRow
	for i, e := range es {
		display, ok := chatsearch.IndexText(e.Role, e.Text, len(e.Attachments) > 0)
		if !ok {
			continue
		}
		rows = append(rows, textRow{ChatID: id, Seq: first + int64(i), Role: e.Role, At: e.At.UTC(), Folded: chatsearch.Fold(display)})
	}
	return rows
}

// putTexts writes index rows. A row of the same message is replaced, so a
// row left by a racing backfill never blocks the entry written now.
func putTexts(tx *gorm.DB, rows []textRow) error {
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chat_id"}, {Name: "seq"}},
		DoUpdates: clause.AssignmentColumns([]string{"role", "at", "folded"}),
	}).CreateInBatches(&rows, 100).Error
}

// SearchText implements chatsearch.Index.
func (s *gormStore) SearchText(ctx context.Context, q chatsearch.Query) (chatsearch.Result, error) {
	res := chatsearch.Result{Engine: s.text.engine, Progress: s.textProgress()}
	terms := q.Terms()
	if len(terms) == 0 {
		return res, nil
	}
	cands, err := s.candidates(ctx, q, terms)
	if err != nil {
		return res, err
	}
	var titles []session.ChatRecord
	if q.Titles {
		if titles, err = s.titleChats(ctx, q, terms); err != nil {
			return res, err
		}
	}
	res.Hits = chatsearch.Build(q, cands, titles)
	return res, nil
}

func (s *gormStore) textProgress() chatsearch.Progress {
	return chatsearch.Progress{Indexing: s.text.indexing.Load(), Done: int(s.text.done.Load()), Total: int(s.text.total.Load())}
}

// chatFilter adds the conditions on the chat row: the owner first.
func chatFilter(db *gorm.DB, q chatsearch.Query, alias string) *gorm.DB {
	db = db.Where(alias+".principal = ? AND COALESCE("+alias+".kind, '') <> ?", q.Principal, session.KindSubAgent)
	if q.ChatID != "" {
		db = db.Where(alias+".id = ?", q.ChatID)
	}
	if q.AgentID != "" {
		db = db.Where(alias+".agent_id = ?", q.AgentID)
	}
	switch q.Origin {
	case "":
	case chatsearch.OriginPerson:
		db = db.Where("COALESCE(" + alias + ".origin, '') = ''")
	case chatsearch.OriginAutomated:
		db = db.Where("COALESCE(" + alias + ".origin, '') <> ''")
	default:
		db = db.Where(alias+".origin = ?", q.Origin)
	}
	return db
}

func likeEscaped(s string) string {
	return strings.NewReplacer(likeEscape, likeEscape+likeEscape, "%", likeEscape+"%", "_", likeEscape+"_").Replace(s)
}

// matchTerms keeps the index rows whose folded text holds every term.
func (s *gormStore) matchTerms(db *gorm.DB, terms []string) *gorm.DB {
	var fts []string
	for _, t := range terms {
		switch {
		case s.text.engine == engineFTS5 && utf8.RuneCountInString(t) >= chatsearch.MinTermRunes:
			fts = append(fts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		case s.text.postgres:
			// The trigram index serves LIKE; a short word is a scan of the rows left.
			db = db.Where(`t.folded LIKE ? ESCAPE '\'`, "%"+likeEscaped(t)+"%")
		default:
			db = db.Where("instr(t.folded, ?) > 0", t)
		}
	}
	if len(fts) > 0 {
		db = db.Where("t.id IN (SELECT rowid FROM "+ftsTable+" WHERE "+ftsTable+" MATCH ?)", strings.Join(fts, " AND "))
	}
	return db
}

type candidateRow struct {
	ChatID      string
	Seq         int64
	Role        string
	At          time.Time
	Text        string
	Attachments string
}

// candidates reads the newest messages that may match, with their chats.
func (s *gormStore) candidates(ctx context.Context, q chatsearch.Query, terms []string) ([]chatsearch.Candidate, error) {
	db := s.db.WithContext(ctx).Table("studio_entry_texts AS t").
		Select("t.chat_id, t.seq, t.role, t.at, e.text, e.attachments").
		Joins("JOIN studio_chats c ON c.id = t.chat_id").
		// The entry must still exist: a row left by a race is not a hit.
		Joins("JOIN studio_entries e ON e.chat_id = t.chat_id AND e.seq = t.seq")
	db = chatFilter(db, q, "c")
	if q.Role != "" {
		db = db.Where("t.role = ?", q.Role)
	}
	if !q.Since.IsZero() {
		db = db.Where("t.at >= ?", q.Since.UTC())
	}
	if !q.Until.IsZero() {
		db = db.Where("t.at < ?", q.Until.UTC())
	}
	db = s.matchTerms(db, terms)
	var rows []candidateRow
	if err := db.Order("t.at DESC, t.id DESC").Limit(chatsearch.PoolSize).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		if !seen[r.ChatID] {
			seen[r.ChatID] = true
			ids = append(ids, r.ChatID)
		}
	}
	chats, err := s.chatsByID(ctx, q.Principal, ids)
	if err != nil {
		return nil, err
	}
	out := make([]chatsearch.Candidate, 0, len(rows))
	for _, r := range rows {
		rec, ok := chats[r.ChatID]
		if !ok {
			continue
		}
		text, ok := chatsearch.IndexText(r.Role, r.Text, r.Attachments != "")
		if !ok {
			continue
		}
		out = append(out, chatsearch.Candidate{Chat: rec, Seq: r.Seq, Role: r.Role, Text: text, At: r.At})
	}
	return out, nil
}

// chatsByID loads the chats of a principal by ID.
func (s *gormStore) chatsByID(ctx context.Context, principal string, ids []string) (map[string]session.ChatRecord, error) {
	out := make(map[string]session.ChatRecord, len(ids))
	const chunk = 400
	for len(ids) > 0 {
		n := min(chunk, len(ids))
		var rows []chatRow
		if err := s.db.WithContext(ctx).Where("principal = ? AND id IN ?", principal, ids[:n]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			rec, err := r.record()
			if err != nil {
				return nil, err
			}
			out[rec.ID] = rec
		}
		ids = ids[n:]
	}
	return out, nil
}

// titleChats reads the newest chats whose title may hold every term.
func (s *gormStore) titleChats(ctx context.Context, q chatsearch.Query, terms []string) ([]session.ChatRecord, error) {
	db := chatFilter(s.db.WithContext(ctx).Table("studio_chats AS c").Select("c.*"), q, "c")
	for _, t := range terms {
		db = db.Where(`lower(c.title) LIKE ? ESCAPE '\'`, "%"+likeEscaped(t)+"%")
	}
	if !q.Since.IsZero() {
		db = db.Where("c.updated >= ?", q.Since)
	}
	if !q.Until.IsZero() {
		db = db.Where("c.updated < ?", q.Until)
	}
	var rows []chatRow
	if err := db.Order("c.updated DESC, c.id DESC").Limit(titlePool).Find(&rows).Error; err != nil {
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

// backfillTexts indexes the messages written before the index existed, a few
// chats at a time, so a large history does not stall the start. It resumes
// from the chat it reached. A message written while it runs is indexed by the
// write itself, and a row that backfill adds for a message that was just
// removed is ignored by every search (it joins the entry).
func (s *gormStore) backfillTexts(ctx context.Context) {
	defer s.text.indexing.Store(false)
	var st textStateRow
	if err := s.db.Where("id = ?", 1).Limit(1).Find(&st).Error; err != nil {
		xlog.Warn("Chat search: could not read the backfill state", "error", err)
		return
	}
	if st.Done {
		return
	}
	var total int64
	if err := s.db.Model(&chatRow{}).Count(&total).Error; err != nil {
		return
	}
	s.text.total.Store(total)
	if total > 0 {
		xlog.Info("Chat search: indexing earlier chats in the background", "chats", total)
	}
	last, started, logged := st.LastChat, time.Now(), time.Now()
	for {
		if ctx.Err() != nil {
			return
		}
		var ids []string
		if err := s.db.Model(&chatRow{}).Where("id > ?", last).Order("id").Limit(backfillStep).Pluck("id", &ids).Error; err != nil {
			xlog.Debug("Chat search: backfill stopped", "error", err)
			return
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			if err := s.backfillChat(ctx, id); err != nil {
				xlog.Debug("Chat search: backfill stopped", "error", err)
				return
			}
			s.text.done.Add(1)
		}
		last = ids[len(ids)-1]
		if err := s.db.Save(&textStateRow{ID: 1, LastChat: last}).Error; err != nil {
			xlog.Debug("Chat search: backfill stopped", "error", err)
			return
		}
		if time.Since(logged) > 10*time.Second {
			logged = time.Now()
			xlog.Info("Chat search: indexing earlier chats", "done", s.text.done.Load(), "of", total)
		}
		select { // leave the database to the chats being written
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Rows of a chat deleted while the backfill read it.
	_ = s.db.Exec(`DELETE FROM studio_entry_texts WHERE NOT EXISTS (SELECT 1 FROM studio_chats c WHERE c.id = studio_entry_texts.chat_id)`).Error
	if err := s.db.Save(&textStateRow{ID: 1, LastChat: last, Done: true}).Error; err != nil {
		xlog.Debug("Chat search: could not save the backfill state", "error", err)
		return
	}
	if total > 0 {
		xlog.Info("Chat search: indexed earlier chats", "chats", s.text.done.Load(), "took", time.Since(started).Round(time.Second).String())
	}
}

// backfillChat indexes the messages of one chat that have no row yet.
func (s *gormStore) backfillChat(ctx context.Context, id string) error {
	var after int64
	for {
		var rows []entryRow
		err := s.db.WithContext(ctx).Select("seq", "role", "text", "attachments", "at").
			Where("chat_id = ? AND seq > ? AND role IN ?", id, after, []string{session.RoleUser, session.RoleAssistant}).
			Order("seq").Limit(backfillPage).Find(&rows).Error
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		out := make([]textRow, 0, len(rows))
		for _, r := range rows {
			if display, ok := chatsearch.IndexText(r.Role, r.Text, r.Attachments != ""); ok {
				out = append(out, textRow{ChatID: id, Seq: r.Seq, Role: r.Role, At: r.At.UTC(), Folded: chatsearch.Fold(display)})
			}
		}
		if len(out) > 0 {
			err = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&out, 100).Error
			if err != nil {
				return fmt.Errorf("chat %s: %w", id, err)
			}
		}
		after = rows[len(rows)-1].Seq
	}
}

var _ chatsearch.Index = (*gormStore)(nil)
