package textindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/extract"
)

type textRow struct {
	NodeID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Path      string `gorm:"index:idx_studio_file_texts_path"`
	Owner     string `gorm:"size:255"`
	Hash      string `gorm:"size:64;index:idx_studio_file_texts_hash"`
	Extractor int
	Status    string `gorm:"size:16;index:idx_studio_file_texts_status"`
	Reason    string
	Kind      string `gorm:"size:16"`
	Sections  string // JSON
	Body      string
	// Folded is Body in lower case, as Go lowers it, so a match in SQL and
	// the match of the caller agree on every script, not only ASCII.
	Folded    string
	Truncated bool
	Updated   time.Time `gorm:"index:idx_studio_file_texts_status"`
}

func (textRow) TableName() string { return "studio_file_texts" }

type cursorRow struct {
	ID  int `gorm:"primaryKey;autoIncrement:false"`
	Rev int64
}

func (cursorRow) TableName() string { return "studio_file_text_cursor" }

// documentRow is the whole text read from one content hash, for a document
// whose entry keeps only the first part (read_file pages through it).
type documentRow struct {
	Hash      string `gorm:"primaryKey;size:64"`
	Extractor int    `gorm:"primaryKey;autoIncrement:false"`
	Body      string
	Updated   time.Time
}

func (documentRow) TableName() string { return "studio_file_documents" }

// search engines of the GORM store.
const (
	engineScan    = "scan"    // instr/strpos over the folded column
	engineFTS5    = "fts5"    // SQLite FTS5 with the trigram tokenizer
	enginePGTrgm  = "pg_trgm" // PostgreSQL LIKE with a trigram GIN index
	ftsTable      = "studio_file_texts_fts"
	ftsMinRunes   = 3 // the trigram tokenizer finds nothing shorter
	pgTrgmIndex   = "idx_studio_file_texts_trgm"
	cursorSingle  = 1
	likeEscapeChr = `\`
)

type gormStore struct {
	db       *gorm.DB
	postgres bool
	engine   string
}

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its
// tables. It uses the database's own text search when there is one: FTS5
// with the trigram tokenizer on SQLite, a pg_trgm index on PostgreSQL (when
// the extension can be created). Without them it scans the folded text in
// SQL, which gives the same answers, more slowly.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("textindex: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&textRow{}, &cursorRow{}, &documentRow{}); err != nil {
		return nil, fmt.Errorf("textindex: migrate: %w", err)
	}
	s := &gormStore{db: db, postgres: strings.Contains(db.Dialector.Name(), "postgres"), engine: engineScan}
	if s.postgres {
		s.setupPGTrgm()
	} else {
		s.setupFTS5()
	}
	return s, nil
}

// Engine says which search the store uses: fts5, pg_trgm or scan.
func Engine(s Store) string {
	if g, ok := s.(*gormStore); ok {
		return g.engine
	}
	return engineScan
}

func (s *gormStore) setupFTS5() {
	var n int64
	s.db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", ftsTable).Scan(&n)
	fresh := n == 0
	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS ` + ftsTable + ` USING fts5(folded, content='studio_file_texts', content_rowid='node_id', tokenize='trigram')`,
		`CREATE TRIGGER IF NOT EXISTS studio_file_texts_ai AFTER INSERT ON studio_file_texts BEGIN
			INSERT INTO ` + ftsTable + `(rowid, folded) VALUES (new.node_id, new.folded); END`,
		`CREATE TRIGGER IF NOT EXISTS studio_file_texts_ad AFTER DELETE ON studio_file_texts BEGIN
			INSERT INTO ` + ftsTable + `(` + ftsTable + `, rowid, folded) VALUES ('delete', old.node_id, old.folded); END`,
		`CREATE TRIGGER IF NOT EXISTS studio_file_texts_au AFTER UPDATE OF folded ON studio_file_texts BEGIN
			INSERT INTO ` + ftsTable + `(` + ftsTable + `, rowid, folded) VALUES ('delete', old.node_id, old.folded);
			INSERT INTO ` + ftsTable + `(rowid, folded) VALUES (new.node_id, new.folded); END`,
	}
	for _, q := range stmts {
		if err := s.db.Exec(q).Error; err != nil {
			xlog.Info("File text index: SQLite has no FTS5 trigram search, using a scan", "error", err)
			return
		}
	}
	if fresh {
		// Rows written before the FTS table existed.
		if err := s.db.Exec(`INSERT INTO ` + ftsTable + `(` + ftsTable + `) VALUES ('rebuild')`).Error; err != nil {
			xlog.Warn("File text index: could not build the FTS5 table, using a scan", "error", err)
			return
		}
	}
	s.engine = engineFTS5
}

func (s *gormStore) setupPGTrgm() {
	// Creating the extension needs a privilege the role may not have; a
	// failure only means the slower scan.
	_ = s.db.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error
	var n int64
	if err := s.db.Raw(`SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'`).Scan(&n).Error; err != nil || n == 0 {
		xlog.Info("File text index: PostgreSQL has no pg_trgm extension, using a scan")
		return
	}
	if err := s.db.Exec(`CREATE INDEX IF NOT EXISTS ` + pgTrgmIndex + ` ON studio_file_texts USING gin (folded gin_trgm_ops)`).Error; err != nil {
		xlog.Warn("File text index: could not create the trigram index, using a scan", "error", err)
		return
	}
	s.engine = enginePGTrgm
}

func toRow(e Entry) (textRow, error) {
	sec, err := json.Marshal(e.Sections)
	if err != nil {
		return textRow{}, err
	}
	return textRow{NodeID: e.NodeID, Path: e.Path, Owner: e.Owner, Hash: e.Hash, Extractor: e.Extractor,
		Status: string(e.Status), Reason: e.Reason, Kind: e.Kind, Sections: string(sec), Body: e.Text,
		Folded: strings.ToLower(e.Text), Truncated: e.Truncated, Updated: e.Updated.UTC()}, nil
}

func fromRow(r textRow) Entry {
	e := Entry{NodeID: r.NodeID, Path: r.Path, Owner: r.Owner, Hash: r.Hash, Extractor: r.Extractor,
		Status: Status(r.Status), Reason: r.Reason, Kind: r.Kind, Text: r.Body, Truncated: r.Truncated, Updated: r.Updated}
	if r.Sections != "" && r.Sections != "null" {
		var sec []extract.Section
		if json.Unmarshal([]byte(r.Sections), &sec) == nil {
			e.Sections = sec
		}
	}
	return e
}

// metaColumns are the columns of a list that leaves the text out.
var metaColumns = []string{"node_id", "path", "owner", "hash", "extractor", "status", "reason", "kind", "sections", "truncated", "updated"}

// textColumns are the columns of a search: the text without its folded copy,
// which only the database needs.
var textColumns = append(slices.Clone(metaColumns), "body")

func (s *gormStore) Put(ctx context.Context, e Entry) error {
	row, err := toRow(e)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Save(&row).Error
}

func (s *gormStore) Get(ctx context.Context, id int64) (Entry, error) {
	var row textRow
	err := s.db.WithContext(ctx).Where("node_id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return fromRow(row), nil
}

func (s *gormStore) ReadyByHash(ctx context.Context, hash string, extractor int) (Entry, bool, error) {
	var rows []textRow
	err := s.db.WithContext(ctx).Where("hash = ? AND extractor = ? AND status = ?", hash, extractor, string(StatusReady)).
		Order("node_id").Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return Entry{}, false, err
	}
	return fromRow(rows[0]), true, nil
}

func (s *gormStore) Delete(ctx context.Context, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Where("node_id IN ?", ids).Delete(&textRow{}).Error
}

func escapeLike(s string) string {
	r := strings.NewReplacer(likeEscapeChr, likeEscapeChr+likeEscapeChr, "%", likeEscapeChr+"%", "_", likeEscapeChr+"_")
	return r.Replace(s)
}

// under filters q to the rows at or below prefix.
func under(q *gorm.DB, prefix string) *gorm.DB {
	if prefix == "" || prefix == "/" {
		return q
	}
	prefix = strings.TrimSuffix(prefix, "/")
	return q.Where(`(path = ? OR path LIKE ? ESCAPE '\')`, prefix, escapeLike(prefix)+"/%")
}

func (s *gormStore) Move(ctx context.Context, from, to string) error {
	from, to = strings.TrimSuffix(from, "/"), strings.TrimSuffix(to, "/")
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&textRow{}).Where("path = ?", from).Update("path", to).Error; err != nil {
			return err
		}
		// substr counts characters on both databases; from+"/" is followed
		// by the rest of the path.
		return tx.Model(&textRow{}).Where(`path LIKE ? ESCAPE '\'`, escapeLike(from)+"/%").
			Update("path", gorm.Expr("? || substr(path, ?)", to, utf8.RuneCountInString(from)+1)).Error
	})
}

func (s *gormStore) Pending(ctx context.Context, prefix string, limit int) ([]Entry, error) {
	var rows []textRow
	q := under(s.db.WithContext(ctx).Select(metaColumns).Where("status = ?", string(StatusPending)), prefix)
	if err := q.Order("updated, node_id").Limit(limitOr(limit, 20)).Find(&rows).Error; err != nil {
		return nil, err
	}
	return fromRows(rows), nil
}

func (s *gormStore) List(ctx context.Context, prefix string, afterID int64, limit int) ([]Entry, error) {
	var rows []textRow
	q := under(s.db.WithContext(ctx).Select(metaColumns).Where("node_id > ?", afterID), prefix)
	if err := q.Order("node_id").Limit(limitOr(limit, 100)).Find(&rows).Error; err != nil {
		return nil, err
	}
	return fromRows(rows), nil
}

func (s *gormStore) Search(ctx context.Context, q Query) ([]Entry, error) {
	db := under(s.db.WithContext(ctx).Select(textColumns).Where("status = ? AND node_id > ?", string(StatusReady), q.AfterID), q.Prefix)
	if q.Contains != "" {
		db = s.contains(db, q.Contains)
	}
	var rows []textRow
	if err := db.Order("node_id").Limit(limitOr(q.Limit, 20)).Find(&rows).Error; err != nil {
		return nil, err
	}
	return fromRows(rows), nil
}

// contains keeps the rows whose folded text holds needle.
func (s *gormStore) contains(db *gorm.DB, needle string) *gorm.DB {
	switch {
	case s.engine == engineFTS5 && utf8.RuneCountInString(needle) >= ftsMinRunes:
		phrase := `"` + strings.ReplaceAll(needle, `"`, `""`) + `"`
		return db.Where("node_id IN (SELECT rowid FROM "+ftsTable+" WHERE "+ftsTable+" MATCH ?)", phrase)
	case s.postgres:
		// The trigram index serves LIKE; without it this is a scan.
		return db.Where(`folded LIKE ? ESCAPE '\'`, "%"+escapeLike(needle)+"%")
	}
	return db.Where("instr(folded, ?) > 0", needle)
}

func fromRows(rows []textRow) []Entry {
	out := make([]Entry, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out
}

func (s *gormStore) CountPending(ctx context.Context, prefix string) (int, error) {
	var n int64
	err := under(s.db.WithContext(ctx).Model(&textRow{}).Where("status = ?", string(StatusPending)), prefix).Count(&n).Error
	return int(n), err
}

func (s *gormStore) PutDocument(ctx context.Context, hash string, extractor int, text string) error {
	return s.db.WithContext(ctx).Save(&documentRow{Hash: hash, Extractor: extractor, Body: text, Updated: time.Now().UTC()}).Error
}

func (s *gormStore) Document(ctx context.Context, hash string, extractor int) (string, bool, error) {
	var rows []documentRow
	if err := s.db.WithContext(ctx).Where("hash = ? AND extractor = ?", hash, extractor).Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return "", false, err
	}
	return rows[0].Body, true, nil
}

func (s *gormStore) PruneDocuments(ctx context.Context) error {
	return s.db.WithContext(ctx).
		Where("NOT EXISTS (SELECT 1 FROM studio_file_texts t WHERE t.hash = studio_file_documents.hash)").
		Delete(&documentRow{}).Error
}

func (s *gormStore) Cursor(ctx context.Context) (int64, error) {
	var rows []cursorRow
	if err := s.db.WithContext(ctx).Where("id = ?", cursorSingle).Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Rev, nil
}

func (s *gormStore) SetCursor(ctx context.Context, rev int64) error {
	return s.db.WithContext(ctx).Save(&cursorRow{ID: cursorSingle, Rev: rev}).Error
}
