// SPDX-License-Identifier: MIT
package cluster

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// AppOptions are the application options of one role. The account database is
// the same file for every instance, so a session or key is valid on all.
func (c *Config) AppOptions(role string) ([]config.AppOption, error) {
	node, ok := c.Nodes[role]
	if !ok {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	transport, err := c.Carrier(role)
	if err != nil {
		return nil, err
	}
	addon := config.AddonConfig{
		Enabled: true, InstanceID: role, OwnerID: Owner, OwnerAddress: c.OwnerAddress(), StorageID: "disk",
		Root: node.Root, Transport: transport,
		Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: runtime.GOOS, Arch: runtime.GOARCH},
	}
	if role == Owner {
		addon.ListenAddress = node.CarrierAddr
		addon.ArtifactExpiry = time.Duration(c.ArtifactExpiryMillis) * time.Millisecond
	}
	return []config.AppOption{
		config.WithAuthEnabled(true),
		config.WithAuthDatabaseURL(c.AuthDB),
		config.WithAuthAPIKeyHMACSecret(Secret),
		config.WithAddons(addon),
	}, nil
}

// Seed creates the accounts and the catalog source once, on the first start of
// the owner, and writes the credentials. A restart keeps them.
func (c *Config) Seed(ctx context.Context, app *application.Application) error {
	if _, err := os.Stat(c.Credentials); err == nil {
		return nil
	}
	db := app.AuthDB()
	if db == nil {
		return fmt.Errorf("authentication is not enabled")
	}
	creds := Credentials{Users: map[string]User{}}
	var admin contract.Actor
	for _, u := range []struct {
		name, role string
		feature    bool
	}{{"admin", auth.RoleAdmin, true}, {"member", auth.RoleUser, true}, {"friend", auth.RoleUser, true}, {"outsider", auth.RoleUser, false},
		// Two more ordinary accounts for suites that need a viewer and an
		// editor next to the owner of a resource.
		{"viewer", auth.RoleUser, true}, {"editor", auth.RoleUser, true}} {
		user := auth.User{ID: "fnd-" + u.name, Email: u.name + "@foundation.test", Name: u.name, Provider: auth.ProviderLocal, Role: u.role, Status: auth.StatusActive}
		if err := db.Create(&user).Error; err != nil {
			return err
		}
		if u.feature && u.role != auth.RoleAdmin {
			if err := db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: user.ID, Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error; err != nil {
				return err
			}
		}
		token, key, err := auth.CreateAPIKey(db, user.ID, "foundation", u.role, Secret, nil)
		if err != nil {
			return err
		}
		session, err := auth.CreateSession(db, user.ID, Secret)
		if err != nil {
			return err
		}
		creds.Users[u.name] = User{ID: user.ID, Role: u.role, Token: token, Session: session, KeyID: key.ID}
		if u.name == "admin" {
			admin = contract.Actor{UserID: user.ID, CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}
		}
	}
	sources := app.AddonSources()
	if sources == nil {
		return fmt.Errorf("the add-on owner did not start")
	}
	if err := sources.Save(ctx, admin, catalog.Source{ID: "fixture", Name: "Fixture", URL: c.CatalogURL, Enabled: true, AllowLoopbackHTTP: true}); err != nil {
		return err
	}
	if _, err := sources.Refresh(ctx, "fixture"); err != nil {
		return err
	}
	b, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.Credentials + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Credentials)
}

// ControlServer serves the owner-only test control routes on loopback: the
// fixture has no product route that creates a resource, so a test registers
// one through the owner's own policy here. It never grants more than the
// policy itself would.
func (c *Config) ControlServer(app *application.Application) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/resource", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Foundation-Control")), []byte(c.Control)) != 1 || r.Method != http.MethodPost {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		var req struct {
			AddonID string            `json:"addonId"`
			Owner   string            `json:"owner"`
			Grants  map[string]string `json:"grants"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		creds, err := LoadCredentials(c.Credentials)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		actorOf := func(name string) (contract.Actor, bool) {
			u, ok := creds.Users[name]
			return contract.Actor{UserID: u.ID, CredentialID: "key:" + u.KeyID, Mode: auth.AddonModeNamedUser}, ok
		}
		owner, ok := actorOf(req.Owner)
		p := app.AddonPolicy()
		if !ok || p == nil {
			http.Error(w, "no owner policy", http.StatusConflict)
			return
		}
		ctx := r.Context()
		key, err := p.Register(ctx, owner, req.AddonID)
		if err == nil {
			err = p.Commit(ctx, owner, key)
		}
		for name, role := range req.Grants {
			if err != nil {
				break
			}
			u, ok := creds.Users[name]
			if !ok {
				err = fmt.Errorf("unknown user %q", name)
				break
			}
			err = p.SetGrant(ctx, owner, key, u.ID, policy.Role(role))
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"resourceId": key.ResourceID})
	})
	l, err := net.Listen("tcp", c.ControlAddr)
	if err != nil {
		return nil, err
	}
	s := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.Serve(l) }()
	return s, nil
}

// StateDir is where one instance keeps its models, data and generated files.
func (c *Config) StateDir(role string) string { return filepath.Join(c.Nodes[role].Dir, "state") }
