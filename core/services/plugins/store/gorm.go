package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/services/advisorylock"
)

// The JSON columns are text, so one schema works on SQLite and PostgreSQL.

type marketplaceRow struct {
	ID            string `gorm:"primaryKey;size:64"`
	Name          string `gorm:"size:255"`
	Kind          string `gorm:"size:16"`
	Location      string `gorm:"size:2048"`
	Ref           string `gorm:"size:255"`
	Enabled       bool
	Default       bool
	Removed       bool
	AuthReference string `gorm:"size:255"`
	HeadSHA       string `gorm:"size:64"`
	RefreshedAt   time.Time
	LastError     string `gorm:"size:64"`
	CreatedBy     string `gorm:"size:255"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (marketplaceRow) TableName() string { return "plugin_marketplaces" }

type snapshotRow struct {
	MarketplaceID string `gorm:"primaryKey;size:64"`
	SHA           string `gorm:"column:sha;size:64"`
	Body          []byte
	BodySHA256    string `gorm:"column:body_sha256;size:64"`
	Entries       string
	Skipped       string
	FetchedAt     time.Time
}

func (snapshotRow) TableName() string { return "plugin_marketplace_snapshots" }

type inspectionRow struct {
	PluginID    string `gorm:"primaryKey;size:255"`
	SHA         string `gorm:"column:sha;size:64"`
	Digest      string `gorm:"column:digest;size:80"`
	RunsCode    bool
	CodeReasons string
	Hosts       string
	Model       []byte
	CheckedAt   time.Time
}

func (inspectionRow) TableName() string { return "plugin_inspections" }

type packageRow struct {
	Digest        string `gorm:"column:digest;primaryKey;size:80"`
	Name          string `gorm:"size:255"`
	Version       string `gorm:"size:255"`
	Description   string
	MarketplaceID string `gorm:"size:64"`
	Entry         string `gorm:"size:255"`
	SourceSHA     string `gorm:"size:64"`
	SizeBytes     int64
	FileCount     int
	Files         string
	Model         string
	CodeReasons   string
	CodeFiles     string
	Skipped       string
	CreatedAt     time.Time `gorm:"column:created_at;index"`
}

func (packageRow) TableName() string { return "plugin_packages" }

type installRow struct {
	ID             string `gorm:"primaryKey;size:64"`
	PluginID       string `gorm:"uniqueIndex:idx_plugin_installs_key,priority:1;size:255"`
	Scope          string `gorm:"uniqueIndex:idx_plugin_installs_key,priority:2;size:16"`
	Owner          string `gorm:"uniqueIndex:idx_plugin_installs_key,priority:3;size:255"`
	Digest         string `gorm:"column:digest;size:80;index"`
	SourceSHA      string `gorm:"size:64"`
	PreviousDigest string `gorm:"column:previous_digest;size:80"`
	RunsCode       bool
	ApprovedBy     string `gorm:"size:255"`
	ApprovedAt     time.Time
	ApprovedDigest string `gorm:"size:80"`
	UpdateDigest   string `gorm:"column:update_digest;size:80"`
	UpdateSHA      string `gorm:"column:update_sha;size:64"`
	State          string `gorm:"size:32"`
	InstalledBy    string `gorm:"size:255"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (installRow) TableName() string { return "plugin_installs" }

type userStateRow struct {
	Principal     string `gorm:"primaryKey;size:255"`
	InstallID     string `gorm:"primaryKey;size:64;index"`
	Enabled       bool
	NeedsReenable bool
	Config        string
	Secrets       []byte
	UpdatedAt     time.Time
}

func (userStateRow) TableName() string { return "plugin_user_state" }

type tokenRow struct {
	ID        string `gorm:"primaryKey;size:64"`
	Kind      string `gorm:"size:16"`
	Principal string `gorm:"size:255"`
	PluginID  string `gorm:"size:255"`
	InstallID string `gorm:"size:64"`
	SHA       string `gorm:"column:sha;size:64"`
	Digest    string `gorm:"column:digest;size:80"`
	RunsCode  bool
	CodeFiles string
	Review    bool
	Name      string    `gorm:"size:255"`
	ExpiresAt time.Time `gorm:"index"`
	CreatedAt time.Time
}

func (tokenRow) TableName() string { return "plugin_preview_tokens" }

func (r tokenRow) token() (Token, error) {
	t := Token{ID: r.ID, Kind: r.Kind, Principal: r.Principal, PluginID: r.PluginID, InstallID: r.InstallID,
		SHA: r.SHA, Digest: r.Digest, RunsCode: r.RunsCode, Review: r.Review, Name: r.Name, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt}
	return t, fromJSON(r.CodeFiles, &t.CodeFiles)
}

// GormStore keeps the records in the database that every frontend shares.
type GormStore struct{ db *gorm.DB }

// NewGormStore migrates the tables and returns the store. Several frontends
// migrate the shared database at startup, so the migration takes the
// schema-migrate lock as every other shared table does.
func NewGormStore(db *gorm.DB) (*GormStore, error) {
	if db == nil {
		return nil, errors.New("plugins store: nil *gorm.DB")
	}
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&marketplaceRow{}, &snapshotRow{}, &packageRow{}, &installRow{}, &userStateRow{}, &tokenRow{}, &inspectionRow{})
	}); err != nil {
		return nil, fmt.Errorf("plugins store: migrate: %w", err)
	}
	return &GormStore{db: db}, nil
}

// isUniqueViolation reads the error text of both databases: SQLite says
// "UNIQUE constraint failed" and PostgreSQL "duplicate key value violates
// unique constraint".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate key")
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func fromJSON(s string, v any) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}

func (r marketplaceRow) marketplace() Marketplace {
	return Marketplace{ID: r.ID, Name: r.Name, Kind: r.Kind, Location: r.Location, Ref: r.Ref,
		Enabled: r.Enabled, Default: r.Default, Removed: r.Removed, AuthReference: r.AuthReference,
		HeadSHA: r.HeadSHA, RefreshedAt: r.RefreshedAt.UTC(), LastError: r.LastError, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
}

func marketplaceToRow(m Marketplace) marketplaceRow {
	return marketplaceRow{ID: m.ID, Name: m.Name, Kind: m.Kind, Location: m.Location, Ref: m.Ref,
		Enabled: m.Enabled, Default: m.Default, Removed: m.Removed, AuthReference: m.AuthReference,
		HeadSHA: m.HeadSHA, RefreshedAt: m.RefreshedAt, LastError: m.LastError, CreatedBy: m.CreatedBy,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

func (r packageRow) pkg() (Package, error) {
	p := Package{Digest: r.Digest, Name: r.Name, Version: r.Version, Description: r.Description,
		MarketplaceID: r.MarketplaceID, Entry: r.Entry, SourceSHA: r.SourceSHA, SizeBytes: r.SizeBytes,
		FileCount: r.FileCount, CreatedAt: r.CreatedAt.UTC()}
	if r.Model != "" {
		p.Model = json.RawMessage(r.Model)
	}
	for _, f := range []struct {
		s string
		v any
	}{{r.Files, &p.Files}, {r.CodeReasons, &p.CodeReasons}, {r.CodeFiles, &p.CodeFiles}, {r.Skipped, &p.Skipped}} {
		if err := fromJSON(f.s, f.v); err != nil {
			return p, fmt.Errorf("plugins store: read package %s: %w", r.Digest, err)
		}
	}
	return p, nil
}

func packageToRow(p Package) (packageRow, error) {
	r := packageRow{Digest: p.Digest, Name: p.Name, Version: p.Version, Description: p.Description,
		MarketplaceID: p.MarketplaceID, Entry: p.Entry, SourceSHA: p.SourceSHA, SizeBytes: p.SizeBytes,
		FileCount: p.FileCount, Model: string(p.Model), CreatedAt: p.CreatedAt}
	var err error
	if r.Files, err = toJSON(p.Files); err != nil {
		return r, err
	}
	if r.CodeReasons, err = toJSON(p.CodeReasons); err != nil {
		return r, err
	}
	if r.CodeFiles, err = toJSON(p.CodeFiles); err != nil {
		return r, err
	}
	r.Skipped, err = toJSON(p.Skipped)
	return r, err
}

func (r installRow) install() Install {
	return Install{ID: r.ID, PluginID: r.PluginID, Scope: r.Scope, Owner: r.Owner, Digest: r.Digest,
		SourceSHA: r.SourceSHA, PreviousDigest: r.PreviousDigest, RunsCode: r.RunsCode,
		ApprovedBy: r.ApprovedBy, ApprovedAt: r.ApprovedAt.UTC(), ApprovedDigest: r.ApprovedDigest,
		UpdateDigest: r.UpdateDigest, UpdateSHA: r.UpdateSHA, State: r.State, InstalledBy: r.InstalledBy,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
}

func installToRow(in Install) installRow {
	return installRow{ID: in.ID, PluginID: in.PluginID, Scope: in.Scope, Owner: in.Owner, Digest: in.Digest,
		SourceSHA: in.SourceSHA, PreviousDigest: in.PreviousDigest, RunsCode: in.RunsCode,
		ApprovedBy: in.ApprovedBy, ApprovedAt: in.ApprovedAt, ApprovedDigest: in.ApprovedDigest,
		UpdateDigest: in.UpdateDigest, UpdateSHA: in.UpdateSHA, State: in.State, InstalledBy: in.InstalledBy,
		CreatedAt: in.CreatedAt, UpdatedAt: in.UpdatedAt}
}

func (r userStateRow) userState() UserState {
	us := UserState{Principal: r.Principal, InstallID: r.InstallID, Enabled: r.Enabled,
		NeedsReenable: r.NeedsReenable, Secrets: r.Secrets, UpdatedAt: r.UpdatedAt.UTC()}
	if r.Config != "" {
		us.Config = json.RawMessage(r.Config)
	}
	return us
}

func (s *GormStore) Marketplaces(ctx context.Context) ([]Marketplace, error) {
	var rows []marketplaceRow
	if err := s.db.WithContext(ctx).Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Marketplace, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.marketplace())
	}
	return out, nil
}

func (s *GormStore) SaveMarketplace(ctx context.Context, m Marketplace) error {
	if err := validMarketplace(m); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old marketplaceRow
		err := tx.Where("id = ?", m.ID).Take(&old).Error
		switch {
		case err == nil:
			if keepTombstone(old.marketplace(), m) {
				return nil
			}
			if m.CreatedAt.IsZero() {
				m.CreatedAt = old.CreatedAt
			}
			row := marketplaceToRow(m)
			return tx.Select("*").Where("id = ?", m.ID).Updates(&row).Error
		case errors.Is(err, gorm.ErrRecordNotFound):
			row := marketplaceToRow(m)
			return tx.Create(&row).Error
		default:
			return err
		}
	})
}

func (s *GormStore) DeleteMarketplace(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old marketplaceRow
		if err := tx.Where("id = ? AND removed = ?", id, false).Take(&old).Error; err != nil {
			return notFound(err)
		}
		if err := tx.Where("marketplace_id = ?", id).Delete(&snapshotRow{}).Error; err != nil {
			return err
		}
		if old.Default {
			return tx.Model(&marketplaceRow{}).Where("id = ?", id).
				Updates(map[string]any{"removed": true, "enabled": false, "updated_at": time.Now().UTC()}).Error
		}
		return tx.Where("id = ?", id).Delete(&marketplaceRow{}).Error
	})
}

func (s *GormStore) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	var r snapshotRow
	if err := s.db.WithContext(ctx).Where("marketplace_id = ?", id).Take(&r).Error; err != nil {
		return Snapshot{}, notFound(err)
	}
	snap := Snapshot{MarketplaceID: r.MarketplaceID, SHA: r.SHA, Body: r.Body, BodySHA256: r.BodySHA256,
		FetchedAt: r.FetchedAt.UTC()}
	if err := fromJSON(r.Entries, &snap.Entries); err != nil {
		return snap, fmt.Errorf("plugins store: read snapshot %s: %w", id, err)
	}
	if err := fromJSON(r.Skipped, &snap.Skipped); err != nil {
		return snap, fmt.Errorf("plugins store: read snapshot %s: %w", id, err)
	}
	return snap, nil
}

func (s *GormStore) SaveSnapshot(ctx context.Context, snap Snapshot) error {
	if err := validSnapshot(snap); err != nil {
		return err
	}
	entries, err := toJSON(snap.Entries)
	if err != nil {
		return err
	}
	skipped, err := toJSON(snap.Skipped)
	if err != nil {
		return err
	}
	row := snapshotRow{MarketplaceID: snap.MarketplaceID, SHA: snap.SHA, Body: snap.Body,
		BodySHA256: snap.BodySHA256, Entries: entries, Skipped: skipped, FetchedAt: snap.FetchedAt}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "marketplace_id"}},
		UpdateAll: true,
	}).Create(&row).Error
}

func (s *GormStore) Package(ctx context.Context, digest string) (Package, error) {
	return gormTx{s.db.WithContext(ctx)}.Package(digest)
}

func (s *GormStore) PutPackage(ctx context.Context, p Package) error {
	return gormTx{s.db.WithContext(ctx)}.PutPackage(p)
}

// unreferenced is the condition of a package that no install refers to.
const unreferenced = `NOT EXISTS (SELECT 1 FROM plugin_installs i WHERE i.digest = plugin_packages.digest
	OR i.previous_digest = plugin_packages.digest OR i.update_digest = plugin_packages.digest)`

func (s *GormStore) UnreferencedPackages(ctx context.Context, olderThan time.Time) ([]string, error) {
	var out []string
	err := s.db.WithContext(ctx).Model(&packageRow{}).
		Where("created_at < ?", olderThan).Where(unreferenced).
		Order("digest asc").Pluck("digest", &out).Error
	return out, err
}

func (s *GormStore) DeletePackage(ctx context.Context, digest string) (bool, error) {
	// One statement, so an install that refers to the package between a
	// check and the delete cannot lose it.
	res := s.db.WithContext(ctx).Where("digest = ?", digest).Where(unreferenced).Delete(&packageRow{})
	return res.RowsAffected > 0, res.Error
}

func (s *GormStore) Installs(ctx context.Context, f InstallFilter) ([]Install, error) {
	return gormTx{s.db.WithContext(ctx)}.Installs(f)
}

func (s *GormStore) InstallTx(ctx context.Context, fn func(Tx) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(gormTx{tx}) })
}

func (s *GormStore) UserState(ctx context.Context, principal, installID string) (UserState, error) {
	var r userStateRow
	if err := s.db.WithContext(ctx).Where("principal = ? AND install_id = ?", principal, installID).Take(&r).Error; err != nil {
		return UserState{}, notFound(err)
	}
	return r.userState(), nil
}

func (s *GormStore) UserStates(ctx context.Context, principal string) ([]UserState, error) {
	var rows []userStateRow
	if err := s.db.WithContext(ctx).Where("principal = ?", principal).Order("install_id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]UserState, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.userState())
	}
	return out, nil
}

func (s *GormStore) SaveUserState(ctx context.Context, us UserState) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return gormTx{tx}.SaveUserState(us) })
}

func (s *GormStore) PutToken(ctx context.Context, t Token) error {
	if err := validToken(t); err != nil {
		return err
	}
	files, err := toJSON(t.CodeFiles)
	if err != nil {
		return err
	}
	row := tokenRow{ID: t.ID, Kind: t.Kind, Principal: t.Principal, PluginID: t.PluginID, InstallID: t.InstallID,
		SHA: t.SHA, Digest: t.Digest, RunsCode: t.RunsCode, CodeFiles: files, Review: t.Review, Name: t.Name, ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (s *GormStore) Token(ctx context.Context, id string, now time.Time) (Token, error) {
	var r tokenRow
	if err := s.db.WithContext(ctx).Where("id = ? AND expires_at > ?", id, now).Take(&r).Error; err != nil {
		return Token{}, notFound(err)
	}
	return r.token()
}

func (s *GormStore) TakeToken(ctx context.Context, id string, now time.Time) (Token, error) {
	var out Token
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r tokenRow
		if err := tx.Where("id = ? AND expires_at > ?", id, now).Take(&r).Error; err != nil {
			return notFound(err)
		}
		// The delete decides which of two concurrent takes wins.
		res := tx.Where("id = ?", id).Delete(&tokenRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		var err error
		out, err = r.token()
		return err
	})
	return out, err
}

func (s *GormStore) DeleteExpiredTokens(ctx context.Context, now time.Time) (int, error) {
	res := s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&tokenRow{})
	return int(res.RowsAffected), res.Error
}

func (s *GormStore) MarkNeedsReenable(ctx context.Context, installID string) error {
	return gormTx{s.db.WithContext(ctx)}.MarkNeedsReenable(installID)
}

// gormTx runs the calls of a Tx on one *gorm.DB: a transaction inside
// InstallTx, the plain handle otherwise.
type gormTx struct{ db *gorm.DB }

func (t gormTx) Installs(f InstallFilter) ([]Install, error) {
	q := t.db.Model(&installRow{})
	if f.ID != "" {
		q = q.Where("id = ?", f.ID)
	}
	if f.PluginID != "" {
		q = q.Where("plugin_id = ?", f.PluginID)
	}
	if f.Scope != "" {
		q = q.Where("scope = ?", f.Scope)
	}
	if f.Owner != "" {
		q = q.Where("owner = ?", f.Owner)
	}
	if f.VisibleTo != "" {
		q = q.Where("scope = ? OR owner = ?", ScopeInstance, f.VisibleTo)
	}
	var rows []installRow
	if err := q.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Install, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.install())
	}
	return out, nil
}

func (t gormTx) Package(digest string) (Package, error) {
	var r packageRow
	if err := t.db.Where("digest = ?", digest).Take(&r).Error; err != nil {
		return Package{}, notFound(err)
	}
	return r.pkg()
}

func (t gormTx) PutPackage(p Package) error {
	if p.Digest == "" {
		return fmt.Errorf("%w: a package needs a digest", ErrInvalid)
	}
	row, err := packageToRow(p)
	if err != nil {
		return err
	}
	return t.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func (t gormTx) CreateInstall(in Install) error {
	if err := validInstall(in); err != nil {
		return err
	}
	if in.Scope == ScopeUser {
		var inst installRow
		err := t.db.Where("plugin_id = ? AND scope = ?", in.PluginID, ScopeInstance).Take(&inst).Error
		if err == nil {
			return &InstanceInstalledError{InstallID: inst.ID}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	row := installToRow(in)
	if err := t.db.Create(&row).Error; err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %s is installed already", ErrConflict, in.PluginID)
		}
		return err
	}
	return nil
}

func (t gormTx) UpdateInstall(in Install) error {
	if err := validInstall(in); err != nil {
		return err
	}
	var old installRow
	if err := t.db.Where("id = ?", in.ID).Take(&old).Error; err != nil {
		return notFound(err)
	}
	if old.PluginID != in.PluginID || old.Scope != in.Scope || old.Owner != in.Owner {
		return fmt.Errorf("%w: an update cannot change the plugin, scope or owner", ErrInvalid)
	}
	in.CreatedAt = old.CreatedAt
	row := installToRow(in)
	return t.db.Select("*").Where("id = ?", in.ID).Updates(&row).Error
}

func (t gormTx) DeleteInstall(id string) error {
	res := t.db.Where("id = ?", id).Delete(&installRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return t.db.Where("install_id = ?", id).Delete(&userStateRow{}).Error
}

func (t gormTx) SaveUserState(us UserState) error {
	if us.Principal == "" || us.InstallID == "" {
		return fmt.Errorf("%w: a user state needs a principal and an install", ErrInvalid)
	}
	var n int64
	if err := t.db.Model(&installRow{}).Where("id = ?", us.InstallID).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	row := userStateRow{Principal: us.Principal, InstallID: us.InstallID, Enabled: us.Enabled,
		NeedsReenable: us.NeedsReenable, Config: string(us.Config), Secrets: us.Secrets, UpdatedAt: us.UpdatedAt}
	return t.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "principal"}, {Name: "install_id"}},
		UpdateAll: true,
	}).Create(&row).Error
}

func (t gormTx) MarkNeedsReenable(installID string) error {
	return t.db.Model(&userStateRow{}).Where("install_id = ?", installID).
		Update("needs_reenable", true).Error
}

func (s *GormStore) SaveInspection(ctx context.Context, in Inspection) error {
	if in.PluginID == "" {
		return fmt.Errorf("%w: an inspection needs a plugin ID", ErrInvalid)
	}
	reasons, err := toJSON(in.CodeReasons)
	if err != nil {
		return err
	}
	hosts, err := toJSON(in.Hosts)
	if err != nil {
		return err
	}
	row := inspectionRow{PluginID: in.PluginID, SHA: in.SHA, Digest: in.Digest, RunsCode: in.RunsCode,
		CodeReasons: reasons, Hosts: hosts, Model: in.Model, CheckedAt: in.CheckedAt}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "plugin_id"}},
		UpdateAll: true,
	}).Create(&row).Error
}

func (s *GormStore) Inspections(ctx context.Context) ([]Inspection, error) {
	var rows []inspectionRow
	if err := s.db.WithContext(ctx).Order("plugin_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Inspection, 0, len(rows))
	for _, r := range rows {
		in := Inspection{PluginID: r.PluginID, SHA: r.SHA, Digest: r.Digest, RunsCode: r.RunsCode,
			Model: r.Model, CheckedAt: r.CheckedAt.UTC()}
		if err := fromJSON(r.CodeReasons, &in.CodeReasons); err != nil {
			return nil, fmt.Errorf("plugins store: read inspection %s: %w", r.PluginID, err)
		}
		if err := fromJSON(r.Hosts, &in.Hosts); err != nil {
			return nil, fmt.Errorf("plugins store: read inspection %s: %w", r.PluginID, err)
		}
		out = append(out, in)
	}
	return out, nil
}
