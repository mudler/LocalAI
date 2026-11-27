package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/plugins"
)

// The answers of /api/plugins, decoded into the fields the tool shows.

type restPluginMarketplace struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Location    string     `json:"location"`
	Ref         string     `json:"ref"`
	Enabled     bool       `json:"enabled"`
	Default     bool       `json:"default"`
	Suspended   bool       `json:"suspended"`
	Active      bool       `json:"active"`
	HeadSHA     string     `json:"headSha"`
	RefreshedAt *time.Time `json:"refreshedAt"`
	LastError   string     `json:"lastError"`
}

func (m restPluginMarketplace) dto() *localaitools.PluginMarketplace {
	return &localaitools.PluginMarketplace{
		ID: m.ID, Name: m.Name, Kind: m.Kind, Location: m.Location, Ref: m.Ref, Enabled: m.Enabled, Default: m.Default,
		Suspended: m.Suspended, Active: m.Active, HeadSHA: m.HeadSHA, RefreshedAt: m.RefreshedAt, LastError: m.LastError,
	}
}

type restPluginInstall struct {
	ID            string `json:"id"`
	PluginID      string `json:"plugin"`
	Version       string `json:"version"`
	Scope         string `json:"scope"`
	Owner         string `json:"owner"`
	SHA           string `json:"sha"`
	RunsCode      bool   `json:"runsCode"`
	State         string `json:"state"`
	UpdateSHA     string `json:"updateSha"`
	Enabled       bool   `json:"enabled"`
	NeedsReenable bool   `json:"needsReenable"`
}

// pluginErrorType returns error.type of an error answer of the API, or "".
func pluginErrorType(err error) string {
	var he *HTTPError
	if !errors.As(err, &he) {
		return ""
	}
	var body struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(he.Body), &body) != nil {
		return ""
	}
	return body.Error.Type
}

func (c *Client) ListPluginMarketplaces(ctx context.Context) ([]localaitools.PluginMarketplace, error) {
	var out struct {
		Marketplaces []restPluginMarketplace `json:"marketplaces"`
	}
	if err := c.do(ctx, http.MethodGet, routePluginMarketplaces, nil, &out); err != nil {
		return nil, err
	}
	list := make([]localaitools.PluginMarketplace, 0, len(out.Marketplaces))
	for _, m := range out.Marketplaces {
		list = append(list, *m.dto())
	}
	return list, nil
}

func (c *Client) AddPluginMarketplace(ctx context.Context, source, ref, id string) (*localaitools.PluginMarketplace, error) {
	body := map[string]string{"source": source}
	if ref != "" {
		body["ref"] = ref
	}
	if id != "" {
		body["id"] = id
	}
	var m restPluginMarketplace
	if err := c.do(ctx, http.MethodPost, routePluginMarketplaces, body, &m); err != nil {
		return nil, err
	}
	return m.dto(), nil
}

func (c *Client) RefreshPluginMarketplace(ctx context.Context, id string) (*localaitools.PluginMarketplace, error) {
	var m restPluginMarketplace
	if err := c.do(ctx, http.MethodPost, routePluginMarketplaces+"/"+url.PathEscape(id)+"/refresh", nil, &m); err != nil {
		return nil, err
	}
	return m.dto(), nil
}

func (c *Client) BrowsePlugins(ctx context.Context, q localaitools.PluginBrowseQuery) (*localaitools.PluginCatalog, error) {
	v := url.Values{}
	if q.Query != "" {
		v.Set("q", q.Query)
	}
	if q.Marketplace != "" {
		v.Set("marketplace", q.Marketplace)
	}
	path := routePluginCatalog
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var page struct {
		Items []struct {
			PluginID    string   `json:"plugin"`
			Description string   `json:"description"`
			Version     string   `json:"version"`
			Category    string   `json:"category"`
			Tags        []string `json:"tags"`
			Supported   bool     `json:"supported"`
			Reason      string   `json:"reason"`
			RunsCode    *bool    `json:"runsCode"`
			Installs    []struct {
				ID string `json:"id"`
			} `json:"installs"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
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

func (c *Client) ListPluginInstalls(ctx context.Context, all bool) ([]localaitools.PluginInstall, error) {
	path := routePluginInstalls
	if all {
		path += "?all=true"
	}
	var out struct {
		Installs []restPluginInstall `json:"installs"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	list := make([]localaitools.PluginInstall, 0, len(out.Installs))
	for _, in := range out.Installs {
		list = append(list, localaitools.PluginInstall{
			ID: in.ID, Plugin: in.PluginID, Version: in.Version, Scope: in.Scope, Owner: in.Owner, SHA: in.SHA,
			RunsCode: in.RunsCode, State: in.State, UpdateSHA: in.UpdateSHA, Enabled: in.Enabled, NeedsReenable: in.NeedsReenable,
		})
	}
	return list, nil
}

// InstallPlugin inspects, then installs with the preview token. An API key
// is never the administrator's browser session, so the server answers 403
// not_human_surface for a plugin that runs code.
func (c *Client) InstallPlugin(ctx context.Context, plugin, scope string) (*localaitools.PluginInstallResult, error) {
	var p plugins.Preview
	if err := c.do(ctx, http.MethodPost, routePluginInspect, map[string]string{"plugin": plugin}, &p); err != nil {
		return nil, err
	}
	if scope == "" {
		scope = "user"
	}
	var job struct {
		JobID  string `json:"jobID"`
		Plugin string `json:"plugin"`
	}
	err := c.do(ctx, http.MethodPost, routePluginInstalls, map[string]string{"token": p.Token, "scope": scope}, &job)
	if pluginErrorType(err) == "not_human_surface" {
		return nil, &localaitools.PluginNeedsBrowserError{Plugin: p.PluginID, ReviewPath: plugins.ReviewPath(p.PluginID), Reasons: p.CodeReasons}
	}
	if err != nil {
		return nil, err
	}
	return &localaitools.PluginInstallResult{
		JobID: job.JobID, Plugin: p.PluginID, SHA: p.SHA, Digest: p.Digest,
		Skills: len(p.Model.Skills), Commands: len(p.Model.Commands), Agents: len(p.Model.Agents),
		MCPServers: len(p.Model.MCPServers), Hosts: p.Hosts,
	}, nil
}

func (c *Client) CheckPluginUpdate(ctx context.Context, installID string) (*localaitools.PluginUpdateCheck, error) {
	var p plugins.UpdatePreview
	if err := c.do(ctx, http.MethodPost, routePluginInstalls+"/"+url.PathEscape(installID)+"/update/preview", nil, &p); err != nil {
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
	var raw struct {
		InstallID     string `json:"install"`
		Enabled       bool   `json:"enabled"`
		NeedsReenable bool   `json:"needsReenable"`
	}
	err := c.do(ctx, http.MethodPut, routePluginInstalls+"/"+url.PathEscape(installID)+"/me", map[string]bool{"enabled": on}, &raw)
	if pluginErrorType(err) == "needs_reenable" {
		return nil, localaitools.ErrPluginNeedsReenable
	}
	if err != nil {
		return nil, err
	}
	return &localaitools.PluginState{Install: raw.InstallID, Enabled: raw.Enabled, NeedsReenable: raw.NeedsReenable}, nil
}

func (c *Client) RemovePlugin(ctx context.Context, installID string) error {
	return c.do(ctx, http.MethodDelete, routePluginInstalls+"/"+url.PathEscape(installID), nil, nil)
}
