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

// NewRegistry returns a Registry over db. Migration is the caller's job: this
// package's tables and sequence are created by Migrate, which the nodes
// registry calls under the one advisory lock that covers every table in the
// deployment.
func NewRegistry(db *gorm.DB) *Registry {
	return &Registry{db: db}
}

// Register records this replica's address, refreshing LastSeen. It upserts on
// the primary key rather than deleting and re-inserting, so a concurrent Live
// never observes a live replica as missing.
func (r *Registry) Register(ctx context.Context, id, addr, version string) error {
	// last_seen is stamped by the database, never by this process. Liveness is
	// compared across replicas, so it has to be measured on the one clock they
	// all share; with per-replica clocks the effective Live window becomes
	// `within - writerBehind - readerAhead`, which either evicts healthy peers
	// or keeps dead ones alive.
	if err := r.db.WithContext(ctx).Model(&Instance{}).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"advertised_addr": addr,
			"version":         version,
			"last_seen":       gorm.Expr("now()"),
		}),
	}).Create(map[string]any{
		"id":              id,
		"advertised_addr": addr,
		"version":         version,
		"last_seen":       gorm.Expr("now()"),
	}).Error; err != nil {
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
		Update("last_seen", gorm.Expr("now()"))
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
	// The cutoff is computed by the database for the same reason Register stamps
	// there: a reader's clock must not decide whether another replica is alive.
	if err := r.db.WithContext(ctx).
		Where("last_seen > now() - make_interval(secs => ?)", within.Seconds()).
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
// The discovery only holds while the database is a shared, remote host. When
// PostgreSQL runs on this same host or pod (compose, single-node, any sidecar
// layout) the route to it is loopback, and advertising 127.0.0.1 would make a
// peer dialling this replica reach itself instead. So an unspecified, loopback,
// or link-local source address is rejected with an error telling the operator to
// configure the advertised address explicitly, rather than returned. There is no
// fallback string: no address is better than a wrong one.
func DiscoverAdvertisedAddr(dsn string, port int) (string, error) {
	// A port of 0 (or out of range) would produce an address nothing can dial,
	// and the caller is likelier to have passed an unset field than to mean it.
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("advertised port %d is out of range 1-65535", port)
	}
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
	if !ok || local.IP == nil {
		return "", fmt.Errorf("no local address on the route to database host %q; set the advertised address explicitly", host)
	}
	if reason := unroutableReason(local.IP, local.Zone); reason != "" {
		return "", fmt.Errorf("the route to database host %q is %s; set the advertised address explicitly", host, reason)
	}
	return net.JoinHostPort(local.IP.String(), strconv.Itoa(port)), nil
}

// unroutableReason says why ip cannot serve as an address other hosts dial, or
// "" when it can. It is the one place that decides, so the discovered address
// and the configured one are held to the same rule; they differ only in what
// they do with the answer.
func unroutableReason(ip net.IP, zone string) string {
	switch {
	case ip == nil || ip.IsUnspecified():
		return fmt.Sprintf("unspecified (%s), which is a bind address rather than one anything can connect to", ip)
	case ip.IsLoopback():
		return fmt.Sprintf("loopback (%s), which means \"this host\" to whoever dials it, so every peer would reach itself", ip)
	// A zone is only ever attached to a scoped (link-local) address, so this is
	// the same rejection stated twice; the Zone check keeps the guarantee if a
	// platform ever hands back a scoped address of another class, because
	// IP.String() would silently drop the %iface and yield an undialable host.
	case ip.IsLinkLocalUnicast() || zone != "":
		return fmt.Sprintf("link-local (%s), which peers on other hosts cannot dial", ip)
	}
	return ""
}

// CheckAdvertisedAddr validates an address an operator configured, returning a
// reason it is questionable, or an error if it is unusable.
//
// A configured address bypasses every check DiscoverAdvertisedAddr performs,
// and the value most likely to be copied is the one that works on a single
// host: "127.0.0.1:8080" on three hosts makes every peer dial itself, which
// presents as a relay loop rather than as a configuration error.
//
// The split between error and reason is deliberate. An address that cannot be
// parsed into host and port is an error, because nothing can dial it at all. An
// address that merely means "this host" is a reason to warn and no more: a
// single-host deployment, including this repository's own e2e cluster, uses one
// correctly, and refusing it would be refusing a supported topology.
func CheckAdvertisedAddr(addr string) (reason string, err error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("advertised address %q is not host:port: %w", addr, err)
	}
	if host == "" {
		return "", fmt.Errorf("advertised address %q names no host, so peers have nothing to dial", addr)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("advertised address %q has no usable port (want 1-65535)", addr)
	}
	// A name is resolved by whoever dials it, and may resolve differently
	// there, so its presence is all this side can check.
	ip := net.ParseIP(host)
	if ip == nil {
		return "", nil
	}
	return unroutableReason(ip, ""), nil
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
