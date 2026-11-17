package application

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

// Global limit of wrong invite codes, on top of the per-peer and per-source
// limits of p2ppair: many peer IDs are cheap to make.
const (
	redeemGlobalMax    = 60
	redeemGlobalWindow = time.Minute
)

// p2pPairStore opens the store of invites and devices: the auth database when
// there is one (shared by every frontend of a cluster), else a JSON file under
// the data path.
func (a *Application) p2pPairStore() (p2ppair.Store, error) {
	secret := a.ApplicationConfig().Auth.APIKeyHMACSecret
	if db := a.AuthDB(); db != nil {
		return p2ppair.NewGormStore(db, secret)
	}
	dir := a.ApplicationConfig().DataPath
	if dir == "" {
		return nil, errors.New("p2p: no data path for the pairing store")
	}
	return p2ppair.NewJSONStore(filepath.Join(dir, "p2p"), secret)
}

// clientKeyIssuer issues the API key of a remote invite through the client
// pairing flow of the auth package: it creates a one-time client pairing code
// with the invite's model scope and redeems it in the same transaction as the
// invite, so a failure leaves neither a key nor a used invite.
type clientKeyIssuer struct{ app *Application }

func (i *clientKeyIssuer) Issue(_ context.Context, tx p2ppair.Tx, inv p2ppair.Invite, dev p2ppair.Device) (p2ppair.IssuedKey, error) {
	db := tx.DB
	if db == nil {
		db = i.app.AuthDB()
	}
	if db == nil {
		return p2ppair.IssuedKey{}, p2ppair.ErrNoKeyIssuer
	}
	secret := i.app.ApplicationConfig().Auth.APIKeyHMACSecret
	features := []string{auth.FeatureClientSelf}
	if len(inv.Scope.Models) > 0 {
		features = append(features, auth.FeatureClientInference)
	}
	name := dev.Label
	if name == "" {
		name = inv.Label
	}
	var plaintext string
	var key *auth.UserAPIKey
	err := db.Transaction(func(tx *gorm.DB) error {
		code, _, err := auth.CreatePairingCode(tx, inv.OwnerID, name, features, nil, 0, 0, secret,
			auth.WithModels(inv.Scope.Models), auth.WithModelCheck(i.app.ModelConfigLoader().ClientModelProblem))
		if err != nil {
			return err
		}
		plaintext, key, err = auth.RedeemPairingCode(tx, code, name, secret)
		return err
	})
	if err != nil {
		return p2ppair.IssuedKey{}, err
	}
	return p2ppair.IssuedKey{ID: key.ID, Secret: plaintext}, nil
}

func (i *clientKeyIssuer) Revoke(ctx context.Context, keyID string) error {
	db := i.app.AuthDB()
	if db == nil {
		return nil
	}
	var k auth.UserAPIKey
	if err := db.WithContext(ctx).Where("id = ? AND kind = ?", keyID, auth.KeyKindClient).First(&k).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if err := auth.RevokeClientKey(db, k.ID, k.UserID); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	// The jobs a client started stop with it, as when the key is revoked on
	// the clients page.
	if svc := i.app.StudioService(); svc != nil && svc.Jobs() != nil {
		if _, err := svc.Jobs().CancelClientJobs(ctx, k.UserID, k.ID); err != nil {
			xlog.Warn("P2P: could not cancel the jobs of a revoked device", "error", err)
		}
	}
	return nil
}

// globalLimiter adds a cap on wrong codes from all peers together.
type globalLimiter struct {
	inner  p2ppair.Limiter
	max    int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	fails []time.Time
}

func newGlobalLimiter(inner p2ppair.Limiter, max int, window time.Duration) *globalLimiter {
	return &globalLimiter{inner: inner, max: max, window: window, now: time.Now}
}

func (g *globalLimiter) prune(now time.Time) {
	i := 0
	for i < len(g.fails) && now.Sub(g.fails[i]) >= g.window {
		i++
	}
	g.fails = g.fails[i:]
}

func (g *globalLimiter) Blocked(p peer.ID, source string) bool {
	g.mu.Lock()
	g.prune(g.now())
	full := len(g.fails) >= g.max
	g.mu.Unlock()
	return full || g.inner.Blocked(p, source)
}

func (g *globalLimiter) Fail(p peer.ID, source string) {
	g.mu.Lock()
	now := g.now()
	g.prune(now)
	g.fails = append(g.fails, now)
	g.mu.Unlock()
	g.inner.Fail(p, source)
}

// passSecret keys the relay passes. A pass is derived from the invite ID, so
// any frontend can cancel the pass of an invite another one minted.
func passSecret(a *Application) []byte {
	if s := a.ApplicationConfig().Auth.APIKeyHMACSecret; s != "" {
		return []byte("p2p-relay-pass:" + s)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("p2p: no randomness: %v", err))
	}
	return b
}

// passCode derives the relay pass of an invite.
func passCode(secret []byte, inviteID string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(inviteID))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(m.Sum(nil))[:20]
}
