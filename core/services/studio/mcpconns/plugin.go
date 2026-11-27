package mcpconns

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mudler/xlog"
)

// Connectors that a plugin owns. A plugin in the Claude Code plugin format
// may declare remote MCP servers. Each user who enables the plugin gets one
// connector per server, owned by the plugin: SyncPlugin makes them, and
// DropPlugin removes them when the user turns the plugin off or the install
// goes. The owner sees them and cannot edit, switch or delete them; they
// turn the plugin off instead.
//
// Distributed mode: the connectors are rows of the store, like the others,
// and SyncPlugin is idempotent, so any frontend may run it at any time.

// ErrPluginOwned is the error of a change to a connector that a plugin owns.
var ErrPluginOwned = fmt.Errorf("%w: a plugin owns this connector; turn the plugin off instead", ErrForbidden)

// PluginServer is a remote MCP server of a plugin, with its templates
// expanded: the address and the headers its connector uses.
type PluginServer struct {
	Name    string
	URL     string
	Headers map[string]string
}

// PluginConnectorName is the name of the connector of a plugin's server:
// "<plugin>:<server>", cut to MaxNameRunes.
func PluginConnectorName(plugin, server string) string {
	name := plugin + ":" + server
	if utf8.RuneCountInString(name) <= MaxNameRunes {
		return name
	}
	return string([]rune(name)[:MaxNameRunes])
}

// SyncPlugin makes the connectors that the install installID owns for user
// match servers: one connector per server, made from the package digest. A
// connector of a server the plugin no longer has goes. A connector already
// made from the same digest is left as it is, so a call per turn writes
// nothing. A server whose address is refused is skipped and logged. A user
// who may not use MCP gets no connector.
func (s *Service) SyncPlugin(ctx context.Context, user, installID, plugin, digest string, servers []PluginServer) error {
	if installID == "" {
		return bad("a plugin connector needs an install")
	}
	if !s.o.Permitted(ctx, user) {
		return nil
	}
	own := owner(user)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	mine, err := s.o.Store.List(ctx, own)
	if err != nil {
		return err
	}
	have := map[string]Connector{}
	for _, c := range mine {
		if c.PluginInstallID == installID {
			have[c.PluginServer] = c
		}
	}
	want := map[string]bool{}
	var errs []error
	for _, srv := range servers {
		want[srv.Name] = true
		old, ok := have[srv.Name]
		if ok && old.PluginDigest == digest && old.Enabled {
			continue
		}
		if err := s.putPlugin(ctx, own, old, ok, installID, plugin, digest, srv); err != nil {
			xlog.Warn("mcpconns: plugin connector skipped", "install", installID, "server", srv.Name, "error", err)
			errs = append(errs, err)
		}
	}
	for name, c := range have {
		if want[name] {
			continue
		}
		if err := s.o.Store.Delete(ctx, c.ID); err != nil {
			return err
		}
		s.invalidate(c.ID)
	}
	return errors.Join(errs...)
}

// putPlugin creates or replaces the connector of one plugin server.
func (s *Service) putPlugin(ctx context.Context, own string, old Connector, exists bool, installID, plugin, digest string, srv PluginServer) error {
	c := Connector{ID: newID(), Owner: own, Created: s.o.Clock().UTC()}
	if exists {
		c.ID, c.Created = old.ID, old.Created
	}
	c.Name, c.Transport, c.Enabled = PluginConnectorName(plugin, srv.Name), TransportHTTP, true
	c.URL = strings.TrimSpace(srv.URL)
	c.PluginInstallID, c.PluginServer, c.PluginDigest = installID, srv.Name, digest
	if err := Validate(c, s.o.AllowPrivate()); err != nil {
		return err
	}
	sec := Secrets{Headers: srv.Headers}
	if err := ValidateSecrets(sec); err != nil {
		return err
	}
	c.HasHeaders = len(sec.Headers) > 0
	var err error
	if c.Secrets, err = s.seal(ctx, c, sec); err != nil {
		return err
	}
	c.Updated = s.o.Clock().UTC()
	if exists && !c.Updated.After(old.Updated) {
		c.Updated = old.Updated.Add(1)
	}
	if err := s.o.Store.Put(ctx, c); err != nil {
		return err
	}
	if exists {
		s.invalidate(c.ID)
	}
	return nil
}

// DropPlugin deletes the connectors that the install owns: those of user,
// or of every user when user is all.
func (s *Service) DropPlugin(ctx context.Context, user, installID string, all bool) error {
	if installID == "" {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	list, err := s.o.Store.ListPlugin(ctx, installID)
	if err != nil {
		return err
	}
	own := owner(user)
	for _, c := range list {
		if !all && c.Owner != own {
			continue
		}
		if err := s.o.Store.Delete(ctx, c.ID); err != nil {
			return err
		}
		s.invalidate(c.ID)
	}
	return nil
}

// ResyncPlugin runs SyncPlugin for every user who has connectors of the
// install, after the install changed package.
func (s *Service) ResyncPlugin(ctx context.Context, installID, plugin, digest string, servers []PluginServer) error {
	list, err := s.o.Store.ListPlugin(ctx, installID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var errs []error
	for _, c := range list {
		if seen[c.Owner] {
			continue
		}
		seen[c.Owner] = true
		user := c.Owner
		if user == AnonymousOwner {
			user = ""
		}
		if err := s.SyncPlugin(ctx, user, installID, plugin, digest, servers); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
