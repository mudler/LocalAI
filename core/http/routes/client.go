// SPDX-License-Identifier: MIT

package routes

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	clienthttp "github.com/mudler/LocalAI/core/http/endpoints/client"
	"github.com/mudler/LocalAI/core/http/tlsconf"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// The client routes. Three groups, and no other route may be added under
// /api/client without joining one (the spec "has every client route classified
// and every client key route registered" in client_internal_test.go):
//
//   - public: the pairing endpoint. A client has no credential yet.
//   - admin: the owner creates and lists pairing codes and manages client keys.
//   - client: routes a client key reaches. Each one is in
//     auth.RouteFeatureRegistry with a client feature; a client key is refused
//     on every route that is not.
const (
	clientPairPath       = "/api/client/pair"
	clientSelfPath       = "/api/client/self"
	clientAddonsPath     = "/api/client/addons/:addon/*"
	clientCodesPath      = "/api/client/pairing-codes"
	clientCodePath       = "/api/client/pairing-codes/:id"
	clientKeysPath       = "/api/client/keys"
	clientKeyPath        = "/api/client/keys/:id"
	clientPairBodyLimit  = 1 << 10
	pairingAttemptsPerIP = 10
)

// ClientPublicRoutes, ClientAdminRoutes and ClientKeyRoutes list the routes of
// the three groups as "METHOD path", for the registry test.
var (
	ClientPublicRoutes = []string{"POST " + clientPairPath}
	ClientAdminRoutes  = []string{
		"POST " + clientCodesPath, "GET " + clientCodesPath, "DELETE " + clientCodePath,
		"GET " + clientKeysPath, "DELETE " + clientKeyPath, "PUT " + clientKeyPath,
	}
	ClientKeyRoutes = []string{
		"GET " + clientSelfPath,
		"GET " + clientAddonsPath, "POST " + clientAddonsPath, "PUT " + clientAddonsPath,
		"PATCH " + clientAddonsPath, "DELETE " + clientAddonsPath,
	}
)

// ClientRouteOption sets an optional part of the client routes.
type ClientRouteOption func(*clientRouteOptions)

type clientRouteOptions struct {
	tlsFingerprint func() string
}

// WithTLSFingerprint tells the routes how to read the SHA-256 fingerprint of
// the certificate of the server, empty when it serves plain HTTP. The answers
// that make or list a pairing code carry it, with its short digest, so that the
// owner can hand both to the client: the client pins the certificate by the
// fingerprint, and two people can compare the digest by eye.
func WithTLSFingerprint(f func() string) ClientRouteOption {
	return func(o *clientRouteOptions) { o.tlsFingerprint = f }
}

// addTLS adds the fingerprint and the digest to a code answer when TLS is on.
func (o clientRouteOptions) addTLS(m map[string]any) map[string]any {
	if o.tlsFingerprint == nil {
		return m
	}
	if fp := o.tlsFingerprint(); fp != "" {
		m["tlsFingerprint"] = fp
		m["tlsDigest"] = tlsconf.Digest(fp)
	}
	return m
}

func clientKeyJSON(k auth.UserAPIKey) map[string]any {
	m := map[string]any{
		"id":          k.ID,
		"name":        k.Name,
		"keyPrefix":   k.KeyPrefix,
		"features":    featureNames(k.Features),
		"addons":      addonsOrEmpty(k.Addons),
		"models":      modelsOrEmpty(k.Models),
		"readFolders": foldersOrEmpty(k.ReadFolders),
		"writeFolder": k.WriteFolder,
		"createdAt":   k.CreatedAt,
		"lastUsed":    k.LastUsed,
		"disabled":    k.Disabled,
	}
	if k.ExpiresAt != nil {
		m["expiresAt"] = k.ExpiresAt
	}
	return m
}

// RegisterClientRoutes registers the routes of paired clients. They
// need sign-in: a client credential is a row of the API key table, and with
// sign-in off there is no table and every caller is already the local owner,
// so a credential would add nothing but a way around that single principal.
func RegisterClientRoutes(e *echo.Echo, app *application.Application, adminMw echo.MiddlewareFunc) {
	fingerprint := WithTLSFingerprint(func() string { return app.ApplicationConfig().TLSFingerprint })
	modelCheck := auth.ClientModelCheck(app.ModelConfigLoader().ClientModelProblem)
	db := app.AuthDB()
	secret := app.ApplicationConfig().Auth.APIKeyHMACSecret
	host := func() clienthttp.Host {
		// A typed nil manager must not become a non-nil Host.
		if m := app.AddonManager(); m != nil {
			return m
		}
		return nil
	}
	ctx := app.ApplicationConfig().Context
	if ctx == nil {
		ctx = context.Background()
	}
	onRevoke := clientJobsCanceller(app)
	registerClientRoutes(ctx, e, db, secret, adminMw, host, onRevoke, modelCheck, fingerprint)
}

// clientJobsCanceller returns what revoking a client does to its jobs: the jobs
// it started stop with it. Every way to delete a client key calls it. The job
// service may be off.
func clientJobsCanceller(app *application.Application) func(ctx context.Context, ownerID, clientID string) {
	return func(ctx context.Context, ownerID, clientID string) {
		if svc := app.StudioService(); svc != nil && svc.Jobs() != nil {
			if _, err := svc.Jobs().CancelClientJobs(ctx, ownerID, clientID); err != nil {
				xlog.Warn("client: could not cancel the jobs of a revoked client", "client", clientID, "error", err)
			}
		}
	}
}

// RegisterClientRoutesFor registers the client routes over an auth database and
// one add-on host, for a setup that has no application (the end-to-end test of
// the client flow). The cleanup of the rate limiter stops with ctx.
func RegisterClientRoutesFor(ctx context.Context, e *echo.Echo, db *gorm.DB, hmacSecret string, adminMw echo.MiddlewareFunc, host clienthttp.Host, modelCheck auth.ClientModelCheck, opts ...ClientRouteOption) {
	registerClientRoutes(ctx, e, db, hmacSecret, adminMw, func() clienthttp.Host { return host }, nil, modelCheck, opts...)
}

func registerClientRoutes(ctx context.Context, e *echo.Echo, db *gorm.DB, secret string, adminMw echo.MiddlewareFunc, host func() clienthttp.Host, onRevoke func(ctx context.Context, ownerID, clientID string), modelCheck auth.ClientModelCheck, opts ...ClientRouteOption) {
	var ro clientRouteOptions
	for _, o := range opts {
		o(&ro)
	}
	if db == nil {
		off := func(c echo.Context) error {
			return c.JSON(http.StatusConflict, map[string]string{"error": "client pairing needs sign-in to be enabled"})
		}
		for _, r := range append(append(append([]string{}, ClientPublicRoutes...), ClientAdminRoutes...), ClientKeyRoutes...) {
			method, path, _ := strings.Cut(r, " ")
			e.Add(method, path, off)
		}
		return
	}

	// owner returns the signed-in person for an admin route. The synthetic
	// identity of a legacy environment key has no account to own a client.
	owner := func(c echo.Context) (*auth.User, error) {
		u := auth.GetUser(c)
		if u == nil || u.ID == "" || u.ID == "legacy-api-key" {
			return nil, c.JSON(http.StatusForbidden, map[string]string{"error": "a named account is required"})
		}
		return u, nil
	}

	perIP := newRateLimiter(time.Minute, pairingAttemptsPerIP)
	// Drop the entries of addresses that stopped trying.
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				perIP.cleanup()
			}
		}
	}()

	// Public: the client posts its code and receives its credential once.
	e.POST(clientPairPath, func(c echo.Context) error {
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, clientPairBodyLimit)
		var body struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		// A body that is not a code is a bad request and costs nothing: it
		// reaches no store and does not use up the tries of the address.
		if err := c.Bind(&body); err != nil || !auth.PairingCodeWellFormed(body.Code) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "a pairing code looks like 7KQ3M-X9PTD"})
		}
		// The limit is per address only. A limit for the whole instance would
		// let anyone block every owner's pairing, and the code space (about
		// 2^49, at most 5 live codes per owner, one hour) already makes
		// guessing hopeless at this rate.
		if !perIP.allow(c.RealIP()) {
			c.Response().Header().Set("Retry-After", "60")
			return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "too many attempts, try again later"})
		}
		plaintext, key, err := auth.RedeemPairingCode(db, body.Code, body.Name, secret)
		if err != nil {
			if errors.Is(err, auth.ErrTooManyClients) {
				return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
			}
			if !errors.Is(err, auth.ErrPairingInvalid) {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "pairing failed"})
			}
			// One answer for an unknown, expired or used code.
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": auth.ErrPairingInvalid.Error()})
		}
		resp := clientKeyJSON(*key)
		resp["key"] = plaintext // shown once
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusCreated, resp)
	})

	// Admin: pairing codes.
	e.POST(clientCodesPath, func(c echo.Context) error {
		u, err := owner(c)
		if u == nil {
			return err
		}
		var body struct {
			Name             string   `json:"name"`
			Features         []string `json:"features"`
			Addons           []string `json:"addons"`      // ids of the add-ons the client may reach
			Models           []string `json:"models"`      // names of the models the client may use (client_inference)
			ReadFolders      []string `json:"readFolders"` // folders of the owner's workspace the client may read (client_folders)
			WriteFolder      string   `json:"writeFolder"` // the one folder it may write (client_folders)
			ExpiresInMinutes int      `json:"expiresInMinutes"`
			KeyExpiresIn     string   `json:"keyExpiresIn"` // like "90d"; empty for no expiry
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
		var keyTTL time.Duration
		if body.KeyExpiresIn != "" {
			d, err := parseDuration(body.KeyExpiresIn)
			if err != nil || d <= 0 {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid keyExpiresIn"})
			}
			keyTTL = d
		}
		code, rec, err := auth.CreatePairingCode(db, u.ID, strings.TrimSpace(body.Name), body.Features, body.Addons,
			time.Duration(body.ExpiresInMinutes)*time.Minute, keyTTL, secret,
			auth.WithModels(body.Models), auth.WithModelCheck(modelCheck), auth.WithFolders(body.ReadFolders, body.WriteFolder))
		switch {
		case errors.Is(err, auth.ErrTooManyPairingCodes):
			return c.JSON(http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		case errors.Is(err, auth.ErrInvalidClientInput):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		case err != nil:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create pairing code"})
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusCreated, ro.addTLS(map[string]any{
			"id": rec.ID, "code": code, "name": rec.Name,
			"features": featureNames(rec.Features), "addons": addonsOrEmpty(rec.Addons), "models": modelsOrEmpty(rec.Models),
			"readFolders": foldersOrEmpty(rec.ReadFolders), "writeFolder": rec.WriteFolder,
			"expiresAt": rec.ExpiresAt,
		}))
	}, adminMw)

	e.GET(clientCodesPath, func(c echo.Context) error {
		u, err := owner(c)
		if u == nil {
			return err
		}
		codes, err := auth.ListPairingCodes(db, u.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list pairing codes"})
		}
		out := make([]map[string]any, 0, len(codes))
		for _, r := range codes {
			out = append(out, ro.addTLS(map[string]any{
				"id": r.ID, "name": r.Name, "features": featureNames(r.Features), "addons": addonsOrEmpty(r.Addons), "models": modelsOrEmpty(r.Models),
				"readFolders": foldersOrEmpty(r.ReadFolders), "writeFolder": r.WriteFolder,
				"expiresAt": r.ExpiresAt, "createdAt": r.CreatedAt,
			}))
		}
		return c.JSON(http.StatusOK, map[string]any{"codes": out})
	}, adminMw)

	e.DELETE(clientCodePath, func(c echo.Context) error {
		u, err := owner(c)
		if u == nil {
			return err
		}
		if err := auth.DeletePairingCode(db, c.Param("id"), u.ID); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "pairing code not found"})
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "pairing code cancelled"})
	}, adminMw)

	// Admin: client keys.
	e.GET(clientKeysPath, func(c echo.Context) error {
		u, err := owner(c)
		if u == nil {
			return err
		}
		keys, err := auth.ListClientKeys(db, u.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list clients"})
		}
		out := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, clientKeyJSON(k))
		}
		return c.JSON(http.StatusOK, map[string]any{"clients": out})
	}, adminMw)

	// Change the add-ons, the models or the folder grant of a paired client
	// without pairing it again. A field that is absent stays as it is; one of
	// them is needed. They are checked and written in one transaction. The
	// folder grant is the two fields readFolders and writeFolder; an empty list
	// or an empty string takes the access away.
	e.PUT(clientKeyPath, func(c echo.Context) error {
		u, err := owner(c)
		if u == nil {
			return err
		}
		var body struct {
			Addons      []string `json:"addons"`
			Models      []string `json:"models"`
			ReadFolders []string `json:"readFolders"`
			WriteFolder *string  `json:"writeFolder"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
		folders := body.ReadFolders != nil || body.WriteFolder != nil
		if body.Addons == nil && body.Models == nil && !folders {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "addons, models, readFolders or writeFolder is required"})
		}
		var addons auth.AddonList
		var models auth.ModelList
		var readFolders auth.FolderList
		var writeFolder string
		err = db.Transaction(func(tx *gorm.DB) error {
			var err error
			if body.Addons != nil {
				if addons, err = auth.SetClientKeyAddons(tx, c.Param("id"), u.ID, body.Addons); err != nil {
					return err
				}
			}
			if body.Models != nil {
				if models, err = auth.SetClientKeyModels(tx, c.Param("id"), u.ID, body.Models, modelCheck); err != nil {
					return err
				}
			}
			if folders {
				// A field that is absent stays as it is, so the other half
				// of the grant is read first.
				var k auth.UserAPIKey
				if err = tx.Where("id = ? AND user_id = ? AND kind = ?", c.Param("id"), u.ID, auth.KeyKindClient).First(&k).Error; err != nil {
					return err
				}
				read, write := []string(k.ReadFolders), k.WriteFolder
				if body.ReadFolders != nil {
					read = body.ReadFolders
				}
				if body.WriteFolder != nil {
					write = *body.WriteFolder
				}
				readFolders, writeFolder, err = auth.SetClientKeyFolders(tx, c.Param("id"), u.ID, read, write)
			}
			return err
		})
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": "client not found"})
		case errors.Is(err, auth.ErrInvalidClientInput):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		case err != nil:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to change the client"})
		}
		resp := map[string]any{}
		if body.Addons != nil {
			resp["addons"] = addonsOrEmpty(addons)
		}
		if body.Models != nil {
			resp["models"] = modelsOrEmpty(models)
		}
		if folders {
			resp["readFolders"] = foldersOrEmpty(readFolders)
			resp["writeFolder"] = writeFolder
		}
		return c.JSON(http.StatusOK, resp)
	}, adminMw)

	e.DELETE(clientKeyPath, func(c echo.Context) error {
		u, err := owner(c)
		if u == nil {
			return err
		}
		if err := auth.RevokeClientKey(db, c.Param("id"), u.ID); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "client not found"})
		}
		if onRevoke != nil {
			// The request may end before the jobs are stopped; they must stop anyway.
			onRevoke(context.WithoutCancel(c.Request().Context()), u.ID, c.Param("id"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "client revoked"})
	}, adminMw)

	// Client: who am I, and what may I use. The answer holds the features, the
	// add-ons and the models of the key and the version of this instance, so a
	// client learns in one call what it can use and whether the instance is new
	// enough for what it wants. Gated by FeatureClientSelf through
	// RouteFeatureRegistry.
	e.GET(clientSelfPath, func(c echo.Context) error {
		k := auth.ClientKey(c)
		if k == nil {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "client credential required"})
		}
		resp := clientKeyJSON(*k)
		// The tag of a release, or "dev" for a source build, with the commit.
		resp["version"] = cmp.Or(internal.Version, "dev")
		resp["commit"] = internal.Commit
		return c.JSON(http.StatusOK, resp)
	})

	// Client: the routes that an add-on declared for clients. One catch-all per
	// method; only a route in the manifest of the add-on is forwarded.
	proxy := clienthttp.NewProxy(host, func(ownerID, keyID string) bool { return auth.ClientKeyActive(db, keyID, ownerID) })
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		e.Add(method, clientAddonsPath, proxy.Handler())
	}
}

func featureNames(m auth.PermissionMap) []string {
	out := make([]string, 0, len(m))
	for f, on := range m {
		if on {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func modelsOrEmpty(l auth.ModelList) []string {
	if l == nil {
		return []string{}
	}
	return []string(l)
}

func addonsOrEmpty(l auth.AddonList) []string {
	if l == nil {
		return []string{}
	}
	return []string(l)
}

func foldersOrEmpty(l auth.FolderList) []string {
	if l == nil {
		return []string{}
	}
	return []string(l)
}
