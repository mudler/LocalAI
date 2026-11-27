package p2ppair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"gorm.io/gorm"
)

// inviteRow is one row of p2p_invites.
type inviteRow struct {
	ID          string `gorm:"primaryKey;size:64"`
	OwnerID     string `gorm:"index;size:64"`
	Kind        string `gorm:"size:16;not null"`
	Label       string `gorm:"size:255"`
	CodeHash    string `gorm:"uniqueIndex;size:64;not null"`
	Scope       string `gorm:"type:text"`
	RelayIDs    string `gorm:"type:text"`
	PassRelayID string `gorm:"size:64"`
	// OmitLANAddrs is the invite's choice to leave LAN addresses out.
	OmitLANAddrs bool `gorm:"not null;default:false"`
	ExpiresAt    time.Time
	UsedAt       *time.Time
	UsedByPeer   string `gorm:"size:128"`
	DeviceID     string `gorm:"size:64"`
	CreatedBy    string `gorm:"size:64"`
	CanceledAt   *time.Time
	CreatedAt    time.Time
	// SignIn marks an invite whose device gets a sign-in credential.
	SignIn bool `gorm:"not null;default:false"`
}

// TableName is the table of the invites.
func (inviteRow) TableName() string { return "p2p_invites" }

// deviceRow is one row of p2p_devices. The index on the peer ID is partial: a
// peer ID may repeat only among revoked rows.
type deviceRow struct {
	ID          string `gorm:"primaryKey;size:64"`
	OwnerID     string `gorm:"index;size:64"`
	Kind        string `gorm:"size:16;not null"`
	Label       string `gorm:"size:255"`
	PeerID      string `gorm:"size:128;not null;uniqueIndex:idx_p2p_devices_active_peer,where:revoked_at IS NULL"`
	ClientKeyID string `gorm:"size:64"`
	InviteID    string `gorm:"index;size:64"`
	Scope       string `gorm:"type:text"`
	CreatedAt   time.Time
	LastSeenAt  *time.Time
	LastPath    string `gorm:"size:32"`
	RevokedAt   *time.Time
	RevokedBy   string `gorm:"size:64"`
	// AllowSlowPath is the "Use anyway" choice of a split worker.
	AllowSlowPath bool `gorm:"not null;default:false"`
	// NodeID is the backend node a split device is attached to.
	NodeID string `gorm:"size:64"`
	// NodeNote is why a split device was detached from its node.
	NodeNote string `gorm:"size:255"`
	// OmitLANAddrs comes from the invite: the routes leave LAN addresses out.
	OmitLANAddrs bool `gorm:"not null;default:false"`
	// SignIn comes from the invite: the device holds a sign-in credential.
	SignIn bool `gorm:"not null;default:false"`
	// CredentialHash is the keyed hash of the sign-in credential; NULL when
	// the device has none.
	CredentialHash *string `gorm:"size:64"`
}

// TableName is the table of the devices.
func (deviceRow) TableName() string { return "p2p_devices" }

// Models returns the GORM models of the package, for a database that
// migrates all its tables in one place.
func Models() []any { return []any{&inviteRow{}, &deviceRow{}, &settingRow{}} }

type gormStore struct {
	db     *gorm.DB
	secret string
}

// NewGormStore returns a Store on db (PostgreSQL in a cluster, SQLite
// otherwise) and creates its tables. hmacSecret keys the hash of the codes.
func NewGormStore(db *gorm.DB, hmacSecret string) (Store, error) {
	if db == nil {
		return nil, errors.New("p2ppair: nil *gorm.DB")
	}
	if err := db.AutoMigrate(Models()...); err != nil {
		return nil, fmt.Errorf("p2ppair: migrate: %w", err)
	}
	return &gormStore{db: db, secret: hmacSecret}, nil
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "duplicate key")
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func (r inviteRow) invite() Invite {
	inv := Invite{
		ID: r.ID, OwnerID: r.OwnerID, Kind: Kind(r.Kind), Label: r.Label,
		PassRelayID: r.PassRelayID, ExpiresAt: r.ExpiresAt, UsedAt: r.UsedAt,
		UsedByPeer: r.UsedByPeer, DeviceID: r.DeviceID, CreatedBy: r.CreatedBy,
		CanceledAt: r.CanceledAt, CreatedAt: r.CreatedAt, OmitLANAddrs: r.OmitLANAddrs,
		SignIn: r.SignIn,
	}
	_ = json.Unmarshal([]byte(r.Scope), &inv.Scope)
	_ = json.Unmarshal([]byte(r.RelayIDs), &inv.RelayIDs)
	return inv
}

func (r deviceRow) device() Device {
	d := Device{
		ID: r.ID, OwnerID: r.OwnerID, Kind: Kind(r.Kind), Label: r.Label,
		PeerID: r.PeerID, ClientKeyID: r.ClientKeyID, InviteID: r.InviteID,
		CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, LastPath: r.LastPath,
		RevokedAt: r.RevokedAt, RevokedBy: r.RevokedBy, AllowSlowPath: r.AllowSlowPath,
		NodeID: r.NodeID, NodeNote: r.NodeNote, OmitLANAddrs: r.OmitLANAddrs,
		SignIn: r.SignIn,
	}
	if r.CredentialHash != nil {
		d.CredentialHash = *r.CredentialHash
	}
	_ = json.Unmarshal([]byte(r.Scope), &d.Scope)
	return d
}

func deviceToRow(d Device) deviceRow {
	return deviceRow{
		ID: d.ID, OwnerID: d.OwnerID, Kind: string(d.Kind), Label: d.Label,
		PeerID: d.PeerID, ClientKeyID: d.ClientKeyID, InviteID: d.InviteID,
		Scope: marshalJSON(d.Scope), CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt,
		LastPath: d.LastPath, RevokedAt: d.RevokedAt, RevokedBy: d.RevokedBy,
		AllowSlowPath: d.AllowSlowPath, NodeID: d.NodeID, NodeNote: d.NodeNote,
		OmitLANAddrs: d.OmitLANAddrs, SignIn: d.SignIn, CredentialHash: nullable(d.CredentialHash),
	}
}

// nullable maps an empty string to NULL.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *gormStore) CreateInvite(ctx context.Context, inv Invite, code string) error {
	row := inviteRow{
		ID: inv.ID, OwnerID: inv.OwnerID, Kind: string(inv.Kind), Label: inv.Label,
		CodeHash: hashCode(code, s.secret), Scope: marshalJSON(inv.Scope),
		RelayIDs: marshalJSON(inv.RelayIDs), PassRelayID: inv.PassRelayID, OmitLANAddrs: inv.OmitLANAddrs,
		ExpiresAt: inv.ExpiresAt.UTC(), CreatedBy: inv.CreatedBy, CreatedAt: inv.CreatedAt.UTC(),
		SignIn: inv.SignIn,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("p2ppair: create invite: %w", err)
	}
	return nil
}

func (s *gormStore) GetInvite(ctx context.Context, id string) (Invite, error) {
	var row inviteRow
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Invite{}, ErrNotFound
		}
		return Invite{}, err
	}
	return row.invite(), nil
}

func (s *gormStore) ListInvites(ctx context.Context) ([]Invite, error) {
	return s.listInvites(s.db.WithContext(ctx))
}

func (s *gormStore) ListInvitesByOwner(ctx context.Context, ownerID string) ([]Invite, error) {
	return s.listInvites(s.db.WithContext(ctx).Where("owner_id = ?", ownerID))
}

func (s *gormStore) CountPendingInvitesByOwner(ctx context.Context, ownerID string, now time.Time) (int, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&inviteRow{}).
		Where("owner_id = ? AND used_at IS NULL AND canceled_at IS NULL AND expires_at > ?", ownerID, now.UTC()).
		Count(&n).Error
	return int(n), err
}

func (s *gormStore) listInvites(q *gorm.DB) ([]Invite, error) {
	var rows []inviteRow
	if err := q.Order("created_at DESC, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Invite, len(rows))
	for i, r := range rows {
		out[i] = r.invite()
	}
	return out, nil
}

func (s *gormStore) CancelInvite(ctx context.Context, id string, now time.Time) error {
	now = now.UTC()
	res := s.db.WithContext(ctx).Model(&inviteRow{}).
		Where("id = ? AND used_at IS NULL AND canceled_at IS NULL AND expires_at > ?", id, now).
		Update("canceled_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	if _, err := s.GetInvite(ctx, id); err != nil {
		return err
	}
	return ErrNotPending
}

func (s *gormStore) Redeem(ctx context.Context, p RedeemParams) (Invite, Device, error) {
	hash := hashCode(p.Code, s.secret)
	now := p.Now.UTC()
	var inv Invite
	var dev Device
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row inviteRow
		if err := tx.Where("code_hash = ?", hash).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInviteInvalid
			}
			return fmt.Errorf("p2ppair: look up the invite: %w", err)
		}
		if !codeMatches(row.CodeHash, hash) {
			return ErrInviteInvalid
		}
		// Claim the invite with one conditional update, so two redeems of the
		// same code at once cannot both win.
		res := tx.Model(&inviteRow{}).
			Where("id = ? AND used_at IS NULL AND canceled_at IS NULL AND expires_at > ?", row.ID, now).
			Updates(map[string]any{"used_at": now, "used_by_peer": p.Peer.String(), "device_id": p.DeviceID})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrInviteInvalid
		}
		row.UsedAt, row.UsedByPeer, row.DeviceID = &now, p.Peer.String(), p.DeviceID
		inv = row.invite()

		label := p.Label
		if label == "" {
			label = inv.Label
		}
		dev = Device{
			ID: p.DeviceID, OwnerID: inv.OwnerID, Kind: inv.Kind, Label: label,
			PeerID: p.Peer.String(), InviteID: inv.ID, Scope: inv.Scope, CreatedAt: now,
			NodeID: inv.Scope.Labels[NodeLabel], OmitLANAddrs: inv.OmitLANAddrs,
			SignIn: inv.SignIn,
		}
		if inv.SignIn {
			dev.CredentialHash = p.CredentialHash
		}
		var active int64
		if err := tx.Model(&deviceRow{}).Where("peer_id = ? AND revoked_at IS NULL", dev.PeerID).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrPeerPaired
		}
		if inv.SignIn && p.MaxDevicesForOwner > 0 {
			// Two redeems of different invites of one owner at once may both
			// pass this count on PostgreSQL; the pending invite limit bounds
			// the excess.
			var owned int64
			if err := tx.Model(&deviceRow{}).
				Where("owner_id = ? AND revoked_at IS NULL AND sign_in = ?", inv.OwnerID, true).
				Count(&owned).Error; err != nil {
				return err
			}
			if owned >= int64(p.MaxDevicesForOwner) {
				return ErrDeviceLimit
			}
		}
		if p.Issue != nil {
			keyID, err := p.Issue(ctx, Tx{DB: tx}, inv, dev)
			if err != nil {
				return err
			}
			dev.ClientKeyID = keyID
		}
		if err := tx.Create(ptr(deviceToRow(dev))).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrPeerPaired
			}
			return err
		}
		return nil
	})
	if err != nil {
		return Invite{}, Device{}, err
	}
	return inv, dev, nil
}

func ptr[T any](v T) *T { return &v }

func (s *gormStore) GetDevice(ctx context.Context, id string) (Device, error) {
	var row deviceRow
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Device{}, ErrNotFound
		}
		return Device{}, err
	}
	return row.device(), nil
}

// GetActiveDeviceByPeer uses the partial unique index on the peer ID of the
// active rows.
func (s *gormStore) GetActiveDeviceByPeer(ctx context.Context, p peer.ID) (Device, error) {
	var row deviceRow
	if err := s.db.WithContext(ctx).Where("peer_id = ? AND revoked_at IS NULL", p.String()).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Device{}, ErrNotFound
		}
		return Device{}, err
	}
	return row.device(), nil
}

func (s *gormStore) ListDevices(ctx context.Context) ([]Device, error) {
	return s.listDevices(s.db.WithContext(ctx))
}

func (s *gormStore) ListDevicesByOwner(ctx context.Context, ownerID string) ([]Device, error) {
	return s.listDevices(s.db.WithContext(ctx).Where("owner_id = ?", ownerID))
}

func (s *gormStore) CountActiveDevicesByOwner(ctx context.Context, ownerID string) (int, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&deviceRow{}).
		Where("owner_id = ? AND revoked_at IS NULL", ownerID).Count(&n).Error
	return int(n), err
}

func (s *gormStore) SetDeviceCredential(ctx context.Context, id, hash string) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&deviceRow{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	// Update with a map: GORM skips a nil pointer in a struct update.
	return s.db.WithContext(ctx).Model(&deviceRow{}).Where("id = ?", id).
		Updates(map[string]any{"credential_hash": nullable(hash)}).Error
}

func (s *gormStore) listDevices(q *gorm.DB) ([]Device, error) {
	var rows []deviceRow
	if err := q.Order("created_at DESC, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Device, len(rows))
	for i, r := range rows {
		out[i] = r.device()
	}
	return out, nil
}

func (s *gormStore) RenameDevice(ctx context.Context, id, label string) error {
	res := s.db.WithContext(ctx).Model(&deviceRow{}).Where("id = ?", id).Update("label", label)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// SQLite reports a matched row as affected; zero means no such row.
		return ErrNotFound
	}
	return nil
}

func (s *gormStore) SetDeviceAllowSlowPath(ctx context.Context, id string, allow bool) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&deviceRow{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	// Update with a map: GORM skips a false bool in a struct update.
	return s.db.WithContext(ctx).Model(&deviceRow{}).Where("id = ?", id).
		Updates(map[string]any{"allow_slow_path": allow}).Error
}

func (s *gormStore) SetDeviceNode(ctx context.Context, id, node, note string) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&deviceRow{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	// Update with a map: GORM skips an empty string in a struct update.
	return s.db.WithContext(ctx).Model(&deviceRow{}).Where("id = ?", id).
		Updates(map[string]any{"node_id": node, "node_note": note}).Error
}

func (s *gormStore) RevokeDevice(ctx context.Context, id, by string, now time.Time) (Device, error) {
	now = now.UTC()
	res := s.db.WithContext(ctx).Model(&deviceRow{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Updates(map[string]any{"revoked_at": now, "revoked_by": by, "credential_hash": nil})
	if res.Error != nil {
		return Device{}, res.Error
	}
	return s.GetDevice(ctx, id)
}

func (s *gormStore) TouchDevice(ctx context.Context, p peer.ID, at time.Time, path string) error {
	return s.db.WithContext(ctx).Model(&deviceRow{}).
		Where("peer_id = ? AND revoked_at IS NULL", p.String()).
		Updates(map[string]any{"last_seen_at": at.UTC(), "last_path": path}).Error
}
