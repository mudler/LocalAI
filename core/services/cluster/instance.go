// Package cluster records the frontend replicas that make up one LocalAI
// deployment and, later, the links between them. It is deliberately free of
// dependencies on core/services/nodes: nodes migrates and consumes the models
// declared here, so an import in the other direction would be a cycle.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInstanceNotFound reports that no row exists for the requested instance ID.
// Callers distinguish it from a transport failure to decide whether to
// re-register or to retry.
var ErrInstanceNotFound = errors.New("cluster: instance not found")

// Instance is one live frontend replica, keyed by the ID that replica chose for
// itself. Column sizes mirror nodes.BackendNode so both tables agree on what an
// ID and a host:port look like.
type Instance struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	AdvertisedAddr string    `gorm:"size:255" json:"advertised_addr"` // host:port other replicas dial
	Version        string    `gorm:"size:64" json:"version"`
	LastSeen       time.Time `gorm:"index" json:"last_seen"`
}

// Registry reads and writes the instances table.
type Registry struct {
	db *gorm.DB
}

// NewRegistry returns a Registry over db. Migration is the caller's job; the
// nodes registry owns the AutoMigrate for every table in this deployment so
// that a single advisory lock covers them all.
func NewRegistry(db *gorm.DB) *Registry {
	return &Registry{db: db}
}

// Register records this replica's address, refreshing LastSeen. It upserts on
// the primary key rather than deleting and re-inserting, so a concurrent Live
// never observes a live replica as missing.
func (r *Registry) Register(ctx context.Context, id, addr, version string) error {
	inst := Instance{
		ID:             id,
		AdvertisedAddr: addr,
		Version:        version,
		LastSeen:       time.Now(),
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"advertised_addr", "version", "last_seen"}),
	}).Create(&inst).Error; err != nil {
		return fmt.Errorf("registering instance %q: %w", id, err)
	}
	return nil
}

// Heartbeat refreshes LastSeen for an already-registered instance. An unknown
// ID is an error rather than an insert: a heartbeat carries no address, so
// inserting would publish a replica nobody can reach.
func (r *Registry) Heartbeat(ctx context.Context, id string) error {
	// gorm reports no error when a Where matches nothing, so the miss has to be
	// read off RowsAffected.
	res := r.db.WithContext(ctx).Model(&Instance{}).
		Where("id = ?", id).
		Update("last_seen", time.Now())
	if res.Error != nil {
		return fmt.Errorf("heartbeating instance %q: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("heartbeating instance %q: %w", id, ErrInstanceNotFound)
	}
	return nil
}

// Live returns the instances whose LastSeen is newer than now-within.
func (r *Registry) Live(ctx context.Context, within time.Duration) ([]Instance, error) {
	var out []Instance
	if err := r.db.WithContext(ctx).
		Where("last_seen > ?", time.Now().Add(-within)).
		Order("id").
		Find(&out).Error; err != nil {
		return nil, fmt.Errorf("listing live instances: %w", err)
	}
	return out, nil
}

// Get returns one instance, or ErrInstanceNotFound if it is not registered.
func (r *Registry) Get(ctx context.Context, id string) (*Instance, error) {
	var inst Instance
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&inst).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("getting instance %q: %w", id, ErrInstanceNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting instance %q: %w", id, err)
	}
	return &inst, nil
}

// DiscoverAdvertisedAddr determines the address this replica should advertise
// to its peers, with no operator configuration.
//
// Every replica in a deployment reaches the same PostgreSQL server, so the
// local interface that routes to PostgreSQL is on a network all the replicas
// demonstrably share. Opening a UDP socket toward the database sends no packet;
// it only asks the kernel to pick a source address for that route, which is the
// address to advertise. The caller supplies the port, since the frontend's
// listening port has nothing to do with the database's.
//
// Failures are returned rather than papered over with a fallback such as
// 127.0.0.1, which would publish an address no peer can dial.
func DiscoverAdvertisedAddr(dsn string, port int) (string, error) {
	host, dbPort, err := dsnHostPort(dsn)
	if err != nil {
		return "", err
	}
	conn, err := net.Dial("udp", net.JoinHostPort(host, dbPort))
	if err != nil {
		return "", fmt.Errorf("resolving route to database host %q: %w", host, err)
	}
	// Nothing was ever sent on this socket, so a close failure carries no
	// information about the address we just read.
	defer func() { _ = conn.Close() }()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP == nil || local.IP.IsUnspecified() {
		return "", fmt.Errorf("no local address on the route to database host %q", host)
	}
	return net.JoinHostPort(local.IP.String(), strconv.Itoa(port)), nil
}

// dsnHostPort extracts the host and port from either DSN form gorm's postgres
// driver accepts: a URL ("postgres://user:pass@host:5432/db") or libpq keyword
// pairs ("host=... port=...").
func dsnHostPort(dsn string) (string, string, error) {
	const defaultPort = "5432"
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", "", errors.New("empty database DSN")
	}

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", "", fmt.Errorf("parsing database DSN: %w", err)
		}
		host := u.Hostname()
		if host == "" {
			return "", "", errors.New("database DSN has no host")
		}
		port := u.Port()
		if port == "" {
			port = defaultPort
		}
		return host, port, nil
	}

	host, port := "", defaultPort
	for _, field := range strings.Fields(dsn) {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch key {
		case "host":
			host = value
		case "port":
			port = value
		}
	}
	if host == "" {
		return "", "", errors.New("database DSN has no host")
	}
	// A Unix socket directory tells us nothing about which interface reaches
	// the database, so there is no address to derive.
	if strings.HasPrefix(host, "/") {
		return "", "", fmt.Errorf("database DSN uses a unix socket (%q); no routable address to advertise", host)
	}
	return host, port, nil
}
