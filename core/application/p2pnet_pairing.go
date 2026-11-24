package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
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

// checkRemoteMint refuses a remote invite whose key could not be issued when
// it is redeemed, so the admin learns it now and not the other server later.
// The key belongs to the account of the owner, so the owner must be an active
// user: the identity of the API key set with LOCALAI_API_KEY has no row. A
// list of models must pass the same checks as at redeem time.
func (a *Application) checkRemoteMint(ctx context.Context, ownerID string, models []string) error {
	db := a.AuthDB()
	if db == nil {
		return fmt.Errorf("%w: remote invites need authentication", p2papi.ErrConflict)
	}
	if ownerID == "" {
		return p2papi.ErrNeedsAccount
	}
	var n int64
	if err := db.WithContext(ctx).Model(&auth.User{}).Where("id = ? AND status = ?", ownerID, auth.StatusActive).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return p2papi.ErrNeedsAccount
	}
	list, err := auth.NormalizeClientModels(models)
	if err != nil {
		return fmt.Errorf("%w: %s", p2papi.ErrInvalid, err.Error())
	}
	for _, m := range list {
		if err := a.ModelConfigLoader().ClientModelProblem(m); err != nil {
			return fmt.Errorf("%w: model %q: %s", p2papi.ErrInvalid, m, err.Error())
		}
	}
	return nil
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
	// A remote always uses models: the list of the invite limits them, and an
	// invite with all_models or without a list shares every model a client key
	// may use, as the admin who shares the server means.
	features := []string{auth.FeatureClientSelf, auth.FeatureClientInference}
	opts := []auth.PairingOption{auth.WithModelCheck(i.app.ModelConfigLoader().ClientModelProblem)}
	if len(inv.Scope.Models) > 0 && !inv.Scope.AllModels {
		opts = append(opts, auth.WithModels(inv.Scope.Models))
	} else {
		opts = append(opts, auth.WithAllModels())
	}
	name := dev.Label
	if name == "" {
		name = inv.Label
	}
	var plaintext string
	var key *auth.UserAPIKey
	err := db.Transaction(func(tx *gorm.DB) error {
		code, _, err := auth.CreatePairingCode(tx, inv.OwnerID, name, features, nil, 0, 0, secret, opts...)
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

// Attempt counts the attempt against the cap of all peers, then against the
// inner limiter, in one step. refund takes both back.
func (g *globalLimiter) Attempt(p p2pnet.ID, source string) (bool, func()) {
	g.mu.Lock()
	now := g.now()
	g.prune(now)
	if len(g.fails) >= g.max {
		g.mu.Unlock()
		return false, func() {}
	}
	g.fails = append(g.fails, now)
	g.mu.Unlock()
	ok, innerRefund := g.inner.Attempt(p, source)
	remove := func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for i := len(g.fails) - 1; i >= 0; i-- {
			if g.fails[i].Equal(now) {
				g.fails = append(g.fails[:i:i], g.fails[i+1:]...)
				return
			}
		}
	}
	if !ok {
		remove()
		return false, func() {}
	}
	var once sync.Once
	return true, func() {
		once.Do(func() {
			remove()
			innerRefund()
		})
	}
}
