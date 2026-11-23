package cluster

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// A join token lets one worker, or a few, register with the cluster. An admin
// mints it, it expires, it has a number of uses, and the database holds only its
// hash. It replaces the shared registration token for the step where a worker
// joins: after that step the worker holds its own credential.
//
// The token is "ljt_<id>.<secret>". The id is public: it names the row, so a
// lookup needs no search over secrets and the list view can show a token without
// showing its secret. The secret is 256 random bits.
const (
	joinTokenPrefix = "ljt_"
	joinIDBytes     = 8
	joinSecretBytes = 32
)

// Limits and defaults of a join token.
const (
	// DefaultJoinTTL and DefaultJoinUses are for a single worker: the token is
	// good for ten minutes and one registration.
	DefaultJoinTTL  = 10 * time.Minute
	DefaultJoinUses = 1
	// DefaultMultiJoinUses is the default number of uses of a token that an admin
	// asks to be good for several workers (Kubernetes, Compose).
	DefaultMultiJoinUses = 5
	// MaxJoinTTL and MaxJoinUses bound what an admin may ask for.
	MaxJoinTTL  = 24 * time.Hour
	MaxJoinUses = 100
)

// The connections a join token can allow. P2P workers keep the invite flow.
const (
	JoinRouteNATS      = "nats"
	JoinRouteWebSocket = "websocket"
)

// JoinTokenRoutes lists the connections that a join token may name.
var JoinTokenRoutes = []string{JoinRouteNATS, JoinRouteWebSocket}

// Join token states, as the list shows them.
const (
	JoinStateUnused     = "unused"
	JoinStatePartlyUsed = "partly_used"
	JoinStateUsedUp     = "used_up"
	JoinStateExpired    = "expired"
	JoinStateRevoked    = "revoked"
)

// Errors of the join token store. A bad token, an expired one, a revoked one and
// a used up one all look the same to the caller that presented it
// (ErrJoinTokenInvalid): the answer must not tell a guesser which part was wrong.
// The checks that need a distinction for an admin use the state of the row.
var (
	ErrJoinTokenInvalid = errors.New("join token is not valid")
	// ErrJoinRouteNotAllowed means the token is good but does not allow the
	// connection that the worker asked for.
	ErrJoinRouteNotAllowed = errors.New("join token does not allow this connection")
	ErrJoinTokenNotFound   = errors.New("join token not found")
	ErrJoinBadRequest      = errors.New("bad join token request")
)

// JoinToken is one row of table join_tokens. The secret is not in it.
type JoinToken struct {
	ID          string     `gorm:"primaryKey;size:32" json:"id"`
	TokenHash   string     `gorm:"size:64;not null" json:"-"`
	Routes      string     `gorm:"size:64" json:"-"` // comma separated; empty means any
	Label       string     `gorm:"size:128" json:"label,omitempty"`
	MaxUses     int        `gorm:"not null" json:"max_uses"`
	Uses        int        `gorm:"not null;default:0" json:"uses"`
	AutoApprove bool       `gorm:"not null;default:false" json:"auto_approve"`
	CreatedBy   string     `gorm:"size:255" json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `gorm:"index" json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	RevokedBy   string     `gorm:"size:255" json:"revoked_by,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

func (JoinToken) TableName() string { return "join_tokens" }

// AllowedRoutes returns the connections the token allows. Empty means any.
func (t JoinToken) AllowedRoutes() []string {
	if t.Routes == "" {
		return nil
	}
	return strings.Split(t.Routes, ",")
}

// Allows reports whether the token allows the connection.
func (t JoinToken) Allows(route string) bool {
	r := t.AllowedRoutes()
	return len(r) == 0 || slices.Contains(r, route)
}

// State is the state of the token at now.
func (t JoinToken) State(now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return JoinStateRevoked
	case !now.Before(t.ExpiresAt):
		return JoinStateExpired
	case t.Uses >= t.MaxUses:
		return JoinStateUsedUp
	case t.Uses > 0:
		return JoinStatePartlyUsed
	default:
		return JoinStateUnused
	}
}

// JoinTokenRequest is what an admin asks for. A zero field takes the default.
type JoinTokenRequest struct {
	TTL         time.Duration
	Uses        int
	Routes      []string
	Label       string
	AutoApprove bool
	CreatedBy   string
}

// JoinTokenStore keeps the join tokens.
type JoinTokenStore struct {
	db  *gorm.DB
	now func() time.Time
}

// NewJoinTokenStore creates the table when it is missing.
func NewJoinTokenStore(db *gorm.DB) (*JoinTokenStore, error) {
	if err := db.AutoMigrate(&JoinToken{}); err != nil {
		return nil, fmt.Errorf("migrating join_tokens: %w", err)
	}
	return &JoinTokenStore{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

// HashJoinSecret is the hash that the database holds for a secret.
func hashJoinSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func randomText(n int, enc func([]byte) string) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return enc(b), nil
}

// NormalizeJoinRoutes checks and orders the connections of a request. An empty
// list stays empty: any connection.
func NormalizeJoinRoutes(routes []string) ([]string, error) {
	var out []string
	for _, r := range routes {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if r == "tunnel" {
			r = JoinRouteWebSocket
		}
		if !slices.Contains(JoinTokenRoutes, r) {
			return nil, fmt.Errorf("%w: unknown connection %q: use nats or websocket", ErrJoinBadRequest, r)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Mint stores a new token and returns its plain text. The plain text is not kept
// anywhere: this return is the only time it exists.
func (s *JoinTokenStore) Mint(ctx context.Context, req JoinTokenRequest) (string, *JoinToken, error) {
	ttl, uses := req.TTL, req.Uses
	if ttl == 0 {
		ttl = DefaultJoinTTL
	}
	if uses == 0 {
		uses = DefaultJoinUses
	}
	if ttl < 0 || ttl > MaxJoinTTL {
		return "", nil, fmt.Errorf("%w: the lifetime must be between 1 second and %s", ErrJoinBadRequest, MaxJoinTTL)
	}
	if uses < 0 || uses > MaxJoinUses {
		return "", nil, fmt.Errorf("%w: uses must be between 1 and %d", ErrJoinBadRequest, MaxJoinUses)
	}
	routes, err := NormalizeJoinRoutes(req.Routes)
	if err != nil {
		return "", nil, err
	}
	if len(req.Label) > 128 {
		return "", nil, fmt.Errorf("%w: the label is longer than 128 characters", ErrJoinBadRequest)
	}
	id, err := randomText(joinIDBytes, hex.EncodeToString)
	if err != nil {
		return "", nil, fmt.Errorf("minting a join token: %w", err)
	}
	secret, err := randomText(joinSecretBytes, base64.RawURLEncoding.EncodeToString)
	if err != nil {
		return "", nil, fmt.Errorf("minting a join token: %w", err)
	}
	now := s.now()
	row := &JoinToken{
		ID:          id,
		TokenHash:   hashJoinSecret(secret),
		Routes:      strings.Join(routes, ","),
		Label:       req.Label,
		MaxUses:     uses,
		AutoApprove: req.AutoApprove,
		CreatedBy:   req.CreatedBy,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return "", nil, fmt.Errorf("storing a join token: %w", err)
	}
	return joinTokenPrefix + id + "." + secret, row, nil
}

// parseJoinToken splits a token into its id and secret. A string that is not
// shaped like a token returns false.
func parseJoinToken(token string) (id, secret string, ok bool) {
	rest, found := strings.CutPrefix(token, joinTokenPrefix)
	if !found {
		return "", "", false
	}
	id, secret, found = strings.Cut(rest, ".")
	if !found || len(id) != joinIDBytes*2 || len(secret) == 0 || len(secret) > 128 {
		return "", "", false
	}
	return id, secret, true
}

// lookup returns the row of a token whose secret matches, or ErrJoinTokenInvalid.
// The state of the row is not checked.
func (s *JoinTokenStore) lookup(ctx context.Context, token string) (*JoinToken, error) {
	id, secret, ok := parseJoinToken(token)
	if !ok {
		// Do the same work as for a real token, so the time does not tell a
		// string of the wrong shape from a wrong secret.
		_ = subtle.ConstantTimeCompare([]byte(hashJoinSecret(token)), []byte(hashJoinSecret("")))
		return nil, ErrJoinTokenInvalid
	}
	var row JoinToken
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	got := hashJoinSecret(secret)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		_ = subtle.ConstantTimeCompare([]byte(got), []byte(hashJoinSecret("")))
		return nil, ErrJoinTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("reading a join token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(row.TokenHash)) != 1 {
		return nil, ErrJoinTokenInvalid
	}
	return &row, nil
}

// Peek checks a token without using it: the secret matches, it is not revoked,
// not expired and has a use left. The script endpoints call it, because serving
// a script must not spend the token.
func (s *JoinTokenStore) Peek(ctx context.Context, token string) (*JoinToken, error) {
	row, err := s.lookup(ctx, token)
	if err != nil {
		return nil, err
	}
	if st := row.State(s.now()); st != JoinStateUnused && st != JoinStatePartlyUsed {
		return nil, ErrJoinTokenInvalid
	}
	return row, nil
}

// Consume uses the token once, atomically. It checks the secret, the state and
// that the token allows every connection in routes, and it records the use in a
// single UPDATE whose condition holds all of it, so that two registrations that
// race for the last use cannot both win. It returns the row as it is after the
// use.
func (s *JoinTokenStore) Consume(ctx context.Context, token string, routes ...string) (*JoinToken, error) {
	row, err := s.Peek(ctx, token)
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		if !row.Allows(r) {
			return nil, ErrJoinRouteNotAllowed
		}
	}
	now := s.now()
	res := s.db.WithContext(ctx).Model(&JoinToken{}).
		Where("id = ? AND token_hash = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses", row.ID, row.TokenHash, now).
		Updates(map[string]any{"uses": gorm.Expr("uses + 1"), "last_used_at": now})
	if res.Error != nil {
		return nil, fmt.Errorf("using a join token: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return nil, ErrJoinTokenInvalid
	}
	row.Uses++
	row.LastUsedAt = &now
	return row, nil
}

// Refund gives back one use of a token whose registration failed after the use
// was taken, so that a refusal that is not the worker's fault does not burn it.
func (s *JoinTokenStore) Refund(ctx context.Context, id string) {
	_ = s.db.WithContext(ctx).Model(&JoinToken{}).Where("id = ? AND uses > 0", id).
		UpdateColumn("uses", gorm.Expr("uses - 1")).Error
}

// List returns the tokens, newest first. Revoked and expired tokens stay in the
// list for a day after they end, so an admin sees what happened to a token.
func (s *JoinTokenStore) List(ctx context.Context) ([]JoinToken, error) {
	var rows []JoinToken
	keep := s.now().Add(-24 * time.Hour)
	err := s.db.WithContext(ctx).
		Where("expires_at > ? OR (revoked_at IS NOT NULL AND revoked_at > ?) OR (last_used_at IS NOT NULL AND last_used_at > ?)", keep, keep, keep).
		Order("created_at DESC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing join tokens: %w", err)
	}
	return rows, nil
}

// Revoke ends a token at once. Revoking a token that is already revoked is not
// an error; an unknown id is ErrJoinTokenNotFound.
func (s *JoinTokenStore) Revoke(ctx context.Context, id, by string) error {
	now := s.now()
	res := s.db.WithContext(ctx).Model(&JoinToken{}).Where("id = ? AND revoked_at IS NULL", id).
		Updates(map[string]any{"revoked_at": now, "revoked_by": by})
	if res.Error != nil {
		return fmt.Errorf("revoking a join token: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		var n int64
		if err := s.db.WithContext(ctx).Model(&JoinToken{}).Where("id = ?", id).Count(&n).Error; err != nil {
			return fmt.Errorf("revoking a join token: %w", err)
		}
		if n == 0 {
			return ErrJoinTokenNotFound
		}
	}
	return nil
}

// Purge deletes the tokens that ended more than a week ago.
func (s *JoinTokenStore) Purge(ctx context.Context) error {
	old := s.now().Add(-7 * 24 * time.Hour)
	return s.db.WithContext(ctx).
		Where("expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)", old, old).
		Delete(&JoinToken{}).Error
}

// JoinFailureLimiter throttles the callers that present a join token that is not
// valid. It counts failures per source in a sliding window and refuses the
// source once it reaches the limit. A valid token is never counted.
type JoinFailureLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	limit  int
	window time.Duration
	fails  map[string][]time.Time
}

// NewJoinFailureLimiter allows limit failures per window for one source.
func NewJoinFailureLimiter(limit int, window time.Duration) *JoinFailureLimiter {
	return &JoinFailureLimiter{now: time.Now, limit: limit, window: window, fails: map[string][]time.Time{}}
}

const maxTrackedSources = 10000

// Allowed reports whether the source may try a token now.
func (l *JoinFailureLimiter) Allowed(source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(source)) < l.limit
}

// Fail records a failed attempt of the source.
func (l *JoinFailureLimiter) Fail(source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) >= maxTrackedSources {
		for k := range l.fails {
			if len(l.prune(k)) == 0 {
				delete(l.fails, k)
			}
		}
		if len(l.fails) >= maxTrackedSources {
			clear(l.fails)
		}
	}
	l.fails[source] = append(l.prune(source), l.now())
}

func (l *JoinFailureLimiter) prune(source string) []time.Time {
	now := l.now()
	stamps := l.fails[source]
	i := 0
	for i < len(stamps) && now.Sub(stamps[i]) >= l.window {
		i++
	}
	stamps = stamps[i:]
	if len(stamps) == 0 {
		delete(l.fails, source)
		return nil
	}
	l.fails[source] = stamps
	return stamps
}
