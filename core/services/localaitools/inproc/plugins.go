package inproc

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/http/auth"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

var errNoPlugins = errors.New("plugins are not set up on this server")

// pluginService returns the Plugins service and the actor of the call. The
// actor is the acting user with the rights the REST API gives that user. A
// server without authentication has one local owner, the principal the REST
// API and the chats use there. A call never carries the browser session, so
// the service refuses what only that session may do.
func (c *Client) pluginService(ctx context.Context) (*plugins.Service, plugins.Actor, error) {
	var svc *plugins.Service
	if c.Plugins != nil {
		svc = c.Plugins()
	}
	if svc == nil {
		return nil, plugins.Actor{}, errNoPlugins
	}
	if c.SingleUser {
		return svc, plugins.Actor{ID: notify.AnonymousPrincipal, Admin: true}, nil
	}
	u := localaitools.UserFromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, plugins.Actor{}, errNoActingUser
	}
	return svc, plugins.Actor{ID: u.ID, Admin: u.Role == auth.RoleAdmin}, nil
}

func pluginMarketplaceOf(v plugins.MarketplaceView) localaitools.PluginMarketplace {
	m := localaitools.PluginMarketplace{
		ID: v.ID, Name: v.Name, Kind: v.Kind, Location: v.Location, Ref: v.Ref, Enabled: v.Enabled,
		Default: v.Default, Suspended: v.Suspended, Active: v.Active, HeadSHA: v.HeadSHA, LastError: v.LastError,
	}
	if !v.RefreshedAt.IsZero() {
		t := v.RefreshedAt
		m.RefreshedAt = &t
	}
	return m
}

func (c *Client) ListPluginMarketplaces(ctx context.Context) ([]localaitools.PluginMarketplace, error) {
	svc, _, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.ListMarketplaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]localaitools.PluginMarketplace, 0, len(list))
	for _, v := range list {
		out = append(out, pluginMarketplaceOf(v))
	}
	return out, nil
}

func (c *Client) AddPluginMarketplace(ctx context.Context, source, ref, id string) (*localaitools.PluginMarketplace, error) {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	v, err := svc.AddMarketplace(ctx, a, plugins.AddMarketplaceRequest{Source: source, Ref: ref, ID: id})
	if err != nil {
		return nil, err
	}
	m := pluginMarketplaceOf(v)
	return &m, nil
}

func (c *Client) RefreshPluginMarketplace(ctx context.Context, id string) (*localaitools.PluginMarketplace, error) {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	v, err := svc.RefreshMarketplace(ctx, a, id)
	if err != nil {
		return nil, err
	}
	m := pluginMarketplaceOf(v)
	return &m, nil
}

func (c *Client) BrowsePlugins(ctx context.Context, q localaitools.PluginBrowseQuery) (*localaitools.PluginCatalog, error) {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	page, err := svc.Catalog(ctx, a, plugins.CatalogQuery{Q: q.Query, Marketplace: q.Marketplace})
	if err != nil {
		return nil, err
	}
	out := &localaitools.PluginCatalog{Items: make([]localaitools.PluginCatalogItem, 0, len(page.Items)), Total: page.Total}
	for _, it := range page.Items {
		item := localaitools.PluginCatalogItem{
			Plugin: it.PluginID, Description: it.Description, Version: it.Version, Category: it.Category,
			Tags: it.Tags, Supported: it.Supported, Reason: it.Reason, RunsCode: it.RunsCode,
		}
		for _, in := range it.Installs {
			item.Installs = append(item.Installs, in.ID)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func pluginInstallOf(v plugins.InstallView) localaitools.PluginInstall {
	return localaitools.PluginInstall{
		ID: v.ID, Plugin: v.PluginID, Version: v.Version, Scope: v.Scope, Owner: v.Owner, SHA: v.SourceSHA,
		RunsCode: v.RunsCode, State: v.State, UpdateSHA: v.UpdateSHA, Enabled: v.Enabled, NeedsReenable: v.NeedsReenable,
	}
}

func (c *Client) ListPluginInstalls(ctx context.Context, all bool) ([]localaitools.PluginInstall, error) {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.ListInstalls(ctx, a, all)
	if err != nil {
		return nil, err
	}
	out := make([]localaitools.PluginInstall, 0, len(list))
	for _, v := range list {
		out = append(out, pluginInstallOf(v))
	}
	return out, nil
}

// InstallPlugin inspects the plugin and installs what the inspect checked.
// The service refuses a plugin that runs code here, since the call has no
// browser session; the refusal names the page where an administrator
// installs it.
func (c *Client) InstallPlugin(ctx context.Context, plugin, scope string) (*localaitools.PluginInstallResult, error) {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	if scope == "" {
		scope = store.ScopeUser
	}
	p, err := svc.Inspect(ctx, a, plugin)
	if err != nil {
		return nil, err
	}
	job, err := svc.Install(ctx, a, p.Token, scope)
	if errors.Is(err, plugins.ErrNotHumanSurface) {
		return nil, &localaitools.PluginNeedsBrowserError{Plugin: p.PluginID, ReviewPath: plugins.ReviewPath(p.PluginID), Reasons: p.CodeReasons}
	}
	if err != nil {
		return nil, err
	}
	if c.PluginJobs != nil {
		c.PluginJobs(p.PluginID, job)
	}
	return &localaitools.PluginInstallResult{
		JobID: job, Plugin: p.PluginID, SHA: p.SHA, Digest: p.Digest,
		Skills: len(p.Model.Skills), Commands: len(p.Model.Commands), Agents: len(p.Model.Agents),
		MCPServers: len(p.Model.MCPServers), Hosts: p.Hosts,
	}, nil
}

func (c *Client) CheckPluginUpdate(ctx context.Context, installID string) (*localaitools.PluginUpdateCheck, error) {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	p, err := svc.PreviewUpdate(ctx, a, installID)
	if err != nil {
		return nil, err
	}
	paths := func(l []plugins.FileChange) []string {
		out := make([]string, 0, len(l))
		for _, f := range l {
			out = append(out, f.Path)
		}
		return out
	}
	return &localaitools.PluginUpdateCheck{
		Install: p.InstallID, Plugin: p.PluginID, FromSHA: p.FromSHA, ToSHA: p.ToSHA,
		Added: paths(p.Added), Removed: paths(p.Removed), Changed: paths(p.Changed),
		OldCodeReasons: p.OldCodeReasons, NewCodeReasons: p.NewCodeReasons, NeedsCodeReview: p.NeedsCodeReview,
		ReviewURL: plugins.ReviewPath(p.PluginID),
	}, nil
}

func (c *Client) SetPluginEnabled(ctx context.Context, installID string, on bool) (*localaitools.PluginState, error) {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return nil, err
	}
	st, err := svc.SetMyState(ctx, a, installID, plugins.SetMyStateRequest{Enabled: &on})
	if errors.Is(err, plugins.ErrNeedsReenable) {
		return nil, localaitools.ErrPluginNeedsReenable
	}
	if err != nil {
		return nil, err
	}
	return &localaitools.PluginState{Install: st.InstallID, Enabled: st.Enabled, NeedsReenable: st.NeedsReenable}, nil
}

func (c *Client) RemovePlugin(ctx context.Context, installID string) error {
	svc, a, err := c.pluginService(ctx)
	if err != nil {
		return err
	}
	return svc.Remove(ctx, a, installID)
}
