package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// PluginsCMD manages the plugins of a running server: plugins in the Claude
// Code plugin format, from marketplaces. Every subcommand but validate talks
// to the server's REST API (/api/plugins). `local-ai chat plugin` is another
// thing: it manages the plugins of the terminal agent on this machine.
type PluginsCMD struct {
	Marketplace PluginsMarketplaceCMD `cmd:"" help:"List, add, remove and refresh the marketplaces of the server"`
	Browse      PluginsBrowseCMD      `cmd:"" aliases:"search" help:"Search the plugins of the server's marketplaces"`
	Info        PluginsInfoCMD        `cmd:"" help:"Show one plugin of a marketplace and its installs"`
	Install     PluginsInstallCMD     `cmd:"" help:"Review a plugin and install it on the server. A plugin that runs code is installed from the browser"`
	List        PluginsListCMD        `cmd:"" help:"List the plugins installed on the server and whether they are on for you"`
	Update      PluginsUpdateCMD      `cmd:"" help:"List the available updates, or review and apply the update of one plugin"`
	Remove      PluginsRemoveCMD      `cmd:"" help:"Remove an installed plugin"`
	Enable      PluginsEnableCMD      `cmd:"" help:"Turn an installed plugin on for you"`
	Disable     PluginsDisableCMD     `cmd:"" help:"Turn an installed plugin off for you"`
	Validate    PluginsValidateCMD    `cmd:"" help:"Check a plugin folder, or every plugin of a marketplace.json, with the checks of the server. Works offline"`
}

// PluginsServer is the connection to the server that the subcommands share.
type PluginsServer struct {
	Target string `env:"LOCALAI_TARGET,LOCALAI_MCP_TARGET" default:"http://localhost:8080" help:"Base URL of the LocalAI server"`
	APIKey string `env:"LOCALAI_API_KEY" help:"API key of the server, when the server asks for one"`

	// Tests set these. Nil means stdout, stdin and a default client.
	output io.Writer
	input  io.Reader
	client *http.Client
	// poll is the wait between two reads of a job's progress; 0 is one
	// second.
	poll time.Duration
}

func (s *PluginsServer) out() io.Writer {
	if s.output == nil {
		return os.Stdout
	}
	return s.output
}

func (s *PluginsServer) base() (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(s.Target, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("--target: %q is not a http or https URL", s.Target)
	}
	return u, nil
}

// pluginsAPIError is an error answer of the API.
type pluginsAPIError struct {
	Status  int
	Type    string
	Message string
	Param   string
}

func (e *pluginsAPIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the server answered %d", e.Status)
	}
	if e.Type == "" {
		return fmt.Sprintf("the server answered %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("the server answered %d %s: %s", e.Status, e.Type, e.Message)
}

func apiErrorType(err error) (*pluginsAPIError, bool) {
	var e *pluginsAPIError
	ok := errors.As(err, &e)
	return e, ok
}

// call sends one request and decodes a JSON answer into out when given.
func (s *PluginsServer) call(ctx context.Context, method, path string, body, out any) error {
	base, err := s.base()
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String()+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	client := s.client
	if client == nil {
		// An inspect fetches the plugin with git, so it can take a while.
		// httpclient refuses redirects: the key never follows one to
		// another host.
		client = httpclient.NewWithTimeout(5 * time.Minute)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, base.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := &pluginsAPIError{Status: resp.StatusCode}
		var body struct {
			Error struct {
				Type    string  `json:"type"`
				Message string  `json:"message"`
				Param   *string `json:"param"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &body) == nil {
			e.Type, e.Message = body.Error.Type, body.Error.Message
			if body.Error.Param != nil {
				e.Param = *body.Error.Param
			}
		}
		return e
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the server answered with something that is not the expected JSON: %w", err)
	}
	return nil
}

// confirm asks a yes or no question. --yes answers it.
func (s *PluginsServer) confirm(question string, yes bool) bool {
	if yes {
		return true
	}
	in := s.input
	if in == nil {
		in = os.Stdin
	}
	_, _ = fmt.Fprintf(s.out(), "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// reviewURL is the page of the web UI where an administrator reviews and
// installs or updates a plugin.
func (s *PluginsServer) reviewURL(pluginID string) string {
	base, err := s.base()
	if err != nil {
		return plugins.ReviewPath(pluginID)
	}
	return base.String() + plugins.ReviewPath(pluginID)
}

// The answers of the API, with the fields the commands print.

type cliMarketplace struct {
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

type cliInstall struct {
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

type cliCatalogItem struct {
	Name        string   `json:"name"`
	PluginID    string   `json:"plugin"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Homepage    string   `json:"homepage"`
	Author      string   `json:"author"`
	Supported   bool     `json:"supported"`
	Reason      string   `json:"reason"`
	RunsCode    *bool    `json:"runsCode"`
	Installs    []struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
		State string `json:"state"`
	} `json:"installs"`
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func (s *PluginsServer) installs(ctx context.Context, all bool) ([]cliInstall, error) {
	path := "/api/plugins/installs"
	if all {
		path += "?all=true"
	}
	var out struct {
		Installs []cliInstall `json:"installs"`
	}
	if err := s.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Installs, nil
}

// resolveInstall finds the install that ref names: an install ID, or a
// plugin ID (name@marketplace). For a plugin ID the caller's own install
// comes before an install for everyone.
func (s *PluginsServer) resolveInstall(ctx context.Context, ref string) (cliInstall, error) {
	list, err := s.installs(ctx, false)
	if err != nil {
		return cliInstall{}, err
	}
	var found []cliInstall
	for _, in := range list {
		if in.ID == ref {
			return in, nil
		}
		if in.PluginID == ref {
			found = append(found, in)
		}
	}
	for _, in := range found {
		if in.Scope == "user" {
			return in, nil
		}
	}
	if len(found) > 0 {
		return found[0], nil
	}
	return cliInstall{}, fmt.Errorf("no install of %q: run local-ai plugins list", ref)
}

// signalContext is cancelled by Ctrl-C or SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// waitJob follows a queued install or update in GET /api/operations until
// it ends. A cancelled ctx (Ctrl-C) cancels the job on the server. The row of
// a job that succeeded leaves the list, so done says whether the change
// landed.
func (s *PluginsServer) waitJob(ctx context.Context, jobID string, done func(context.Context) (bool, error)) error {
	poll := s.poll
	if poll <= 0 {
		poll = time.Second
	}
	w := s.out()
	seen, last, absent := false, "", 0
	for {
		select {
		case <-ctx.Done():
			// The request context is gone; cancel with a fresh one.
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := s.call(cctx, http.MethodPost, "/api/operations/"+url.PathEscape(jobID)+"/cancel", nil, nil)
			cancel()
			if err != nil {
				return fmt.Errorf("interrupted; the cancel of job %s failed: %w", jobID, err)
			}
			return fmt.Errorf("interrupted: job %s cancelled", jobID)
		case <-time.After(poll):
		}
		var ops struct {
			Operations []struct {
				JobID    string `json:"jobID"`
				IsPlugin bool   `json:"isPlugin"`
				Progress int    `json:"progress"`
				Message  string `json:"message"`
				IsQueued bool   `json:"isQueued"`
				Error    string `json:"error"`
			} `json:"operations"`
		}
		if err := s.call(ctx, http.MethodGet, "/api/operations", nil, &ops); err != nil {
			if ctx.Err() != nil {
				continue
			}
			return err
		}
		row := -1
		for i, op := range ops.Operations {
			if op.JobID == jobID && op.IsPlugin {
				row = i
				break
			}
		}
		if row >= 0 {
			op := ops.Operations[row]
			seen, absent = true, 0
			if op.Error != "" {
				return fmt.Errorf("job %s failed: %s", jobID, op.Error)
			}
			line := op.Message
			if op.IsQueued {
				line = "queued"
			}
			if line = fmt.Sprintf("%3d%% %s", op.Progress, line); line != last {
				_, _ = fmt.Fprintln(w, line)
				last = line
			}
			continue
		}
		ok, err := done(ctx)
		if err != nil {
			return err
		}
		if ok {
			_, _ = fmt.Fprintln(w, "100% done")
			return nil
		}
		// A job can be missing from the list for a moment before its first
		// status, or for good on a server without the operations list.
		absent++
		if seen || absent >= 30 {
			return fmt.Errorf("job %s ended without the change; see the operations history of the server", jobID)
		}
	}
}

// ---- marketplaces ----

// PluginsMarketplaceCMD groups the marketplace commands.
type PluginsMarketplaceCMD struct {
	List    PluginsMarketplaceListCMD    `cmd:"" help:"List the marketplaces, with their state and last refresh"`
	Add     PluginsMarketplaceAddCMD     `cmd:"" help:"Add a marketplace (administrators): owner/repo, an https git URL or an https marketplace.json URL"`
	Remove  PluginsMarketplaceRemoveCMD  `cmd:"" help:"Remove a marketplace (administrators). Its installs stay"`
	Refresh PluginsMarketplaceRefreshCMD `cmd:"" help:"Read one marketplace, or all, now (administrators). A refresh installs nothing"`
}

type PluginsMarketplaceListCMD struct {
	PluginsServer `embed:""`
}

func (c *PluginsMarketplaceListCMD) Run(_ *cliContext.Context) error {
	var out struct {
		Marketplaces []cliMarketplace `json:"marketplaces"`
	}
	if err := c.call(context.Background(), http.MethodGet, "/api/plugins/marketplaces", nil, &out); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out(), 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tLOCATION\tSTATE\tCOMMIT\tREFRESHED")
	for _, m := range out.Marketplaces {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", m.ID, m.Location, marketplaceState(m), shortSHA(m.HeadSHA), refreshedAt(m))
	}
	return tw.Flush()
}

func marketplaceState(m cliMarketplace) string {
	state := "active"
	switch {
	case !m.Enabled:
		state = "off"
	case m.Suspended:
		state = "suspended"
	}
	if m.Default {
		state += ", curated"
	}
	if m.LastError != "" {
		state += ", last refresh failed: " + m.LastError
	}
	return state
}

func refreshedAt(m cliMarketplace) string {
	if m.RefreshedAt == nil {
		return "never"
	}
	return m.RefreshedAt.Local().Format(time.DateTime)
}

type PluginsMarketplaceAddCMD struct {
	Source string `arg:"" help:"owner/repo, an https git URL, or an https URL of a marketplace.json file"`
	Ref    string `help:"Branch or tag to follow. Empty is the default branch"`
	ID     string `name:"id" help:"ID of the marketplace. Default: the name in marketplace.json"`

	PluginsServer `embed:""`
}

func (c *PluginsMarketplaceAddCMD) Run(_ *cliContext.Context) error {
	var m cliMarketplace
	if err := c.call(context.Background(), http.MethodPost, "/api/plugins/marketplaces", map[string]string{"source": c.Source, "ref": c.Ref, "id": c.ID}, &m); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.out(), "Added marketplace %s (%s) at commit %s.\n", m.ID, m.Location, shortSHA(m.HeadSHA))
	return nil
}

type PluginsMarketplaceRemoveCMD struct {
	ID string `arg:"" help:"Marketplace ID"`

	PluginsServer `embed:""`
}

func (c *PluginsMarketplaceRemoveCMD) Run(_ *cliContext.Context) error {
	if err := c.call(context.Background(), http.MethodDelete, "/api/plugins/marketplaces/"+url.PathEscape(c.ID), nil, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.out(), "Removed marketplace %s. The plugins installed from it stay.\n", c.ID)
	return nil
}

type PluginsMarketplaceRefreshCMD struct {
	ID string `arg:"" optional:"" help:"Marketplace ID. Empty refreshes every marketplace"`

	PluginsServer `embed:""`
}

func (c *PluginsMarketplaceRefreshCMD) Run(_ *cliContext.Context) error {
	ctx := context.Background()
	ids := []string{c.ID}
	if c.ID == "" {
		var out struct {
			Marketplaces []cliMarketplace `json:"marketplaces"`
		}
		if err := c.call(ctx, http.MethodGet, "/api/plugins/marketplaces", nil, &out); err != nil {
			return err
		}
		ids = ids[:0]
		for _, m := range out.Marketplaces {
			if m.Active {
				ids = append(ids, m.ID)
			}
		}
	}
	var errs []error
	for _, id := range ids {
		var m cliMarketplace
		if err := c.call(ctx, http.MethodPost, "/api/plugins/marketplaces/"+url.PathEscape(id)+"/refresh", nil, &m); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		_, _ = fmt.Fprintf(c.out(), "Refreshed %s: commit %s.\n", m.ID, shortSHA(m.HeadSHA))
	}
	return errors.Join(errs...)
}

// ---- browse and info ----

type PluginsBrowseCMD struct {
	Query       string `arg:"" optional:"" help:"Text to match in the name, description, category and tags"`
	Marketplace string `help:"Only this marketplace"`
	Category    string `help:"Only this category"`
	TextOnly    bool   `name:"text-only" help:"Only the plugins known to run no code"`
	Limit       int    `default:"50" help:"At most this many plugins (200 at most)"`

	PluginsServer `embed:""`
}

func (c *PluginsBrowseCMD) Run(_ *cliContext.Context) error {
	v := url.Values{}
	for k, val := range map[string]string{"q": c.Query, "marketplace": c.Marketplace, "category": c.Category} {
		if val != "" {
			v.Set(k, val)
		}
	}
	if c.TextOnly {
		v.Set("runs_code", "false")
	}
	if c.Limit > 0 {
		v.Set("limit", fmt.Sprint(c.Limit))
	}
	var page struct {
		Items []cliCatalogItem `json:"items"`
		Total int              `json:"total"`
	}
	if err := c.call(context.Background(), http.MethodGet, "/api/plugins/catalog?"+v.Encode(), nil, &page); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out(), 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PLUGIN\tVERSION\tCODE\tINSTALLED\tDESCRIPTION")
	for _, it := range page.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.PluginID, dash(it.Version), runsCodeWord(it), installedWord(it), oneLine(it.Description, 70))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if page.Total > len(page.Items) {
		_, _ = fmt.Fprintf(c.out(), "%d of %d plugins. Narrow the search or raise --limit.\n", len(page.Items), page.Total)
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func runsCodeWord(it cliCatalogItem) string {
	switch {
	case !it.Supported:
		return "unsupported"
	case it.RunsCode == nil:
		return "?"
	case *it.RunsCode:
		return "yes"
	}
	return "no"
}

func installedWord(it cliCatalogItem) string {
	var scopes []string
	for _, in := range it.Installs {
		scopes = append(scopes, in.Scope)
	}
	if len(scopes) == 0 {
		return "-"
	}
	return strings.Join(scopes, ",")
}

type PluginsInfoCMD struct {
	Plugin string `arg:"" help:"name@marketplace"`

	PluginsServer `embed:""`
}

func (c *PluginsInfoCMD) Run(_ *cliContext.Context) error {
	name, mkt, ok := strings.Cut(c.Plugin, "@")
	if !ok || name == "" || mkt == "" {
		return fmt.Errorf("%q is not name@marketplace", c.Plugin)
	}
	var it cliCatalogItem
	if err := c.call(context.Background(), http.MethodGet, "/api/plugins/catalog/"+url.PathEscape(mkt)+"/"+url.PathEscape(name), nil, &it); err != nil {
		return err
	}
	w := c.out()
	_, _ = fmt.Fprintf(w, "plugin:      %s %s\n", it.PluginID, it.Version)
	if it.Description != "" {
		_, _ = fmt.Fprintf(w, "description: %s\n", oneLine(it.Description, 200))
	}
	if it.Category != "" || len(it.Tags) > 0 {
		_, _ = fmt.Fprintf(w, "category:    %s %s\n", dash(it.Category), strings.Join(it.Tags, ", "))
	}
	if it.Author != "" {
		_, _ = fmt.Fprintf(w, "author:      %s\n", it.Author)
	}
	if it.Homepage != "" {
		_, _ = fmt.Fprintf(w, "homepage:    %s\n", it.Homepage)
	}
	if !it.Supported {
		_, _ = fmt.Fprintf(w, "supported:   no (%s)\n", it.Reason)
	}
	_, _ = fmt.Fprintf(w, "runs code:   %s\n", runsCodeWord(it))
	for _, in := range it.Installs {
		_, _ = fmt.Fprintf(w, "install:     %s (%s, %s)\n", in.ID, in.Scope, in.State)
	}
	return nil
}

// ---- install ----

type PluginsInstallCMD struct {
	Plugin string `arg:"" help:"name@marketplace"`
	Scope  string `default:"user" enum:"user,instance" help:"user: for you only (a plugin that runs no code). instance: for everyone (administrators)"`
	Yes    bool   `short:"y" help:"Install without asking"`

	PluginsServer `embed:""`
}

// cliPreview is the answer of an inspect.
type cliPreview struct {
	Token       string   `json:"token"`
	PluginID    string   `json:"plugin"`
	Version     string   `json:"version"`
	SHA         string   `json:"sha"`
	Digest      string   `json:"digest"`
	SizeBytes   int64    `json:"sizeBytes"`
	FileCount   int      `json:"fileCount"`
	RunsCode    bool     `json:"runsCode"`
	CodeReasons []string `json:"codeReasons"`
	CodeFiles   []string `json:"codeFiles"`
	Hosts       []string `json:"hosts"`
	Model       struct {
		Skills     []json.RawMessage `json:"skills"`
		Commands   []json.RawMessage `json:"commands"`
		Agents     []json.RawMessage `json:"agents"`
		MCPServers []json.RawMessage `json:"mcpServers"`
		Hooks      []json.RawMessage `json:"hooks"`
	} `json:"model"`
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (c *PluginsInstallCMD) Run(_ *cliContext.Context) error {
	ctx, stop := signalContext()
	defer stop()
	w := c.out()
	var p cliPreview
	if err := c.call(ctx, http.MethodPost, "/api/plugins/inspect", map[string]string{"plugin": c.Plugin}, &p); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "plugin:      %s %s\n", p.PluginID, p.Version)
	_, _ = fmt.Fprintf(w, "commit:      %s\n", p.SHA)
	_, _ = fmt.Fprintf(w, "digest:      %s\n", p.Digest)
	_, _ = fmt.Fprintf(w, "files:       %d, %d bytes\n", p.FileCount, p.SizeBytes)
	_, _ = fmt.Fprintf(w, "adds:        %s, %s, %s, %s\n", plural(len(p.Model.Skills), "skill", "skills"),
		plural(len(p.Model.Commands), "command", "commands"), plural(len(p.Model.Agents), "agent", "agents"),
		plural(len(p.Model.MCPServers), "MCP server", "MCP servers"))
	if len(p.Hosts) > 0 {
		_, _ = fmt.Fprintf(w, "connects to: %s\n", strings.Join(p.Hosts, ", "))
	}
	if p.RunsCode {
		_, _ = fmt.Fprintf(w, "runs code:   yes (%s)\n", strings.Join(p.CodeReasons, ", "))
		for _, f := range p.CodeFiles {
			_, _ = fmt.Fprintf(w, "code file:   %s\n", f)
		}
	} else {
		_, _ = fmt.Fprintln(w, "runs code:   no")
	}
	_, _ = fmt.Fprintln(w, "origin:      community plugin, not signed. Install it only if you trust its author.")
	if p.RunsCode {
		c.printBrowserStep(p.PluginID)
		return nil
	}
	who := "for you"
	if c.Scope == "instance" {
		who = "for everyone"
	}
	if !c.confirm(fmt.Sprintf("Install %s %s?", p.PluginID, who), c.Yes) {
		return errors.New("not installed: the install was not confirmed (pass --yes to skip the question)")
	}
	var job struct {
		JobID string `json:"jobID"`
	}
	err := c.call(ctx, http.MethodPost, "/api/plugins/installs", map[string]string{"token": p.Token, "scope": c.Scope}, &job)
	if e, ok := apiErrorType(err); ok {
		switch e.Type {
		case "not_human_surface":
			c.printBrowserStep(p.PluginID)
			return nil
		case "instance_installed":
			_, _ = fmt.Fprintf(w, "%s is installed for everyone already (install %s). Turn it on for you with: local-ai plugins enable %s\n", p.PluginID, e.Param, e.Param)
			return nil
		}
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "Installing %s (job %s). Ctrl-C cancels.\n", p.PluginID, job.JobID)
	return c.waitJob(ctx, job.JobID, func(ctx context.Context) (bool, error) {
		list, err := c.installs(ctx, false)
		if err != nil {
			return false, err
		}
		for _, in := range list {
			if in.PluginID == p.PluginID && in.SHA == p.SHA {
				_, _ = fmt.Fprintf(w, "Installed %s as %s (%s).\n", p.PluginID, in.ID, in.Scope)
				return true, nil
			}
		}
		return false, nil
	})
}

// printBrowserStep says where an administrator finishes the install of a
// plugin that runs code: only the administrator's browser session may.
func (c *PluginsInstallCMD) printBrowserStep(pluginID string) {
	_, _ = fmt.Fprintf(c.out(), "\nThis plugin runs code. An administrator reviews its code files and installs it for everyone in the browser:\n  %s\n", c.reviewURL(pluginID))
}

// ---- list ----

type PluginsListCMD struct {
	All bool `help:"Every user's installs (administrators)"`

	PluginsServer `embed:""`
}

func (c *PluginsListCMD) Run(_ *cliContext.Context) error {
	list, err := c.installs(context.Background(), c.All)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.out(), 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPLUGIN\tVERSION\tSCOPE\tCOMMIT\tSTATE\tFOR YOU")
	for _, in := range list {
		scope := in.Scope
		if in.Scope == "user" && c.All && in.Owner != "" {
			scope = "user " + in.Owner
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", in.ID, in.PluginID, dash(in.Version), scope, shortSHA(in.SHA), in.State, switchWord(in))
	}
	return tw.Flush()
}

func switchWord(in cliInstall) string {
	switch {
	case in.NeedsReenable:
		return "off (runs code now)"
	case in.Enabled:
		return "on"
	}
	return "off"
}

// ---- update ----

type PluginsUpdateCMD struct {
	Plugin string `arg:"" optional:"" help:"Install ID or name@marketplace. Empty lists the installs with an update"`
	Check  bool   `help:"Show the update and apply nothing"`
	Yes    bool   `short:"y" help:"Apply without asking"`

	PluginsServer `embed:""`
}

type cliUpdatePreview struct {
	Token   string `json:"token"`
	Install string `json:"install"`
	Plugin  string `json:"plugin"`
	FromSHA string `json:"fromSha"`
	ToSHA   string `json:"toSha"`
	Added   []struct {
		Path string `json:"path"`
	} `json:"added"`
	Removed []struct {
		Path string `json:"path"`
	} `json:"removed"`
	Changed []struct {
		Path string `json:"path"`
		Code bool   `json:"code"`
	} `json:"changed"`
	OldCodeReasons  []string `json:"oldCodeReasons"`
	NewCodeReasons  []string `json:"newCodeReasons"`
	NeedsCodeReview bool     `json:"needsCodeReview"`
}

func (c *PluginsUpdateCMD) Run(_ *cliContext.Context) error {
	ctx, stop := signalContext()
	defer stop()
	w := c.out()
	if c.Plugin == "" {
		list, err := c.installs(ctx, false)
		if err != nil {
			return err
		}
		n := 0
		for _, in := range list {
			switch in.State {
			case "update_available":
				_, _ = fmt.Fprintf(w, "%s  %s  %s -> %s\n", in.ID, in.PluginID, shortSHA(in.SHA), shortSHA(in.UpdateSHA))
				n++
			case "blocked":
				_, _ = fmt.Fprintf(w, "%s  %s  blocked: the new version runs code, an administrator must install it\n", in.ID, in.PluginID)
				n++
			}
		}
		if n == 0 {
			_, _ = fmt.Fprintln(w, "Every plugin is at its latest version.")
		}
		return nil
	}
	in, err := c.resolveInstall(ctx, c.Plugin)
	if err != nil {
		return err
	}
	var p cliUpdatePreview
	if err := c.call(ctx, http.MethodPost, "/api/plugins/installs/"+url.PathEscape(in.ID)+"/update/preview", nil, &p); err != nil {
		if e, ok := apiErrorType(err); ok && e.Type == "no_update" {
			_, _ = fmt.Fprintf(w, "%s is at its latest version.\n", in.PluginID)
			return nil
		}
		return err
	}
	_, _ = fmt.Fprintf(w, "%s: %s -> %s\n", p.Plugin, shortSHA(p.FromSHA), shortSHA(p.ToSHA))
	for _, f := range p.Added {
		_, _ = fmt.Fprintf(w, "  added    %s\n", f.Path)
	}
	for _, f := range p.Removed {
		_, _ = fmt.Fprintf(w, "  removed  %s\n", f.Path)
	}
	for _, f := range p.Changed {
		mark := ""
		if f.Code {
			mark = "  (code)"
		}
		_, _ = fmt.Fprintf(w, "  changed  %s%s\n", f.Path, mark)
	}
	if strings.Join(p.OldCodeReasons, ",") != strings.Join(p.NewCodeReasons, ",") {
		_, _ = fmt.Fprintf(w, "runs code: [%s] -> [%s]\n", strings.Join(p.OldCodeReasons, ", "), strings.Join(p.NewCodeReasons, ", "))
	}
	if p.NeedsCodeReview {
		_, _ = fmt.Fprintf(w, "\nThe code of this plugin changes. An administrator reviews each changed code file and applies the update in the browser:\n  %s\n", c.reviewURL(p.Plugin))
		return nil
	}
	if c.Check {
		return nil
	}
	if !c.confirm("Apply the update?", c.Yes) {
		return errors.New("not updated: the update was not confirmed (pass --yes to skip the question)")
	}
	var job struct {
		JobID string `json:"jobID"`
	}
	err = c.call(ctx, http.MethodPost, "/api/plugins/installs/"+url.PathEscape(in.ID)+"/update", map[string]any{"token": p.Token}, &job)
	if e, ok := apiErrorType(err); ok && e.Type == "not_human_surface" {
		_, _ = fmt.Fprintf(w, "\nThis update needs the administrator's browser session:\n  %s\n", c.reviewURL(p.Plugin))
		return nil
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "Updating %s (job %s). Ctrl-C cancels.\n", p.Plugin, job.JobID)
	return c.waitJob(ctx, job.JobID, func(ctx context.Context) (bool, error) {
		list, err := c.installs(ctx, false)
		if err != nil {
			return false, err
		}
		for _, cur := range list {
			if cur.ID == in.ID && cur.SHA == p.ToSHA {
				_, _ = fmt.Fprintf(w, "Updated %s to %s.\n", p.Plugin, shortSHA(p.ToSHA))
				return true, nil
			}
		}
		return false, nil
	})
}

// ---- remove, enable, disable ----

type PluginsRemoveCMD struct {
	Plugin string `arg:"" help:"Install ID or name@marketplace"`
	Yes    bool   `short:"y" help:"Remove without asking"`

	PluginsServer `embed:""`
}

func (c *PluginsRemoveCMD) Run(_ *cliContext.Context) error {
	ctx := context.Background()
	in, err := c.resolveInstall(ctx, c.Plugin)
	if err != nil {
		return err
	}
	who := "for you"
	if in.Scope == "instance" {
		who = "for everyone"
	}
	if !c.confirm(fmt.Sprintf("Remove %s (%s, installed %s)?", in.PluginID, in.ID, who), c.Yes) {
		return errors.New("not removed: the removal was not confirmed (pass --yes to skip the question)")
	}
	if err := c.call(ctx, http.MethodDelete, "/api/plugins/installs/"+url.PathEscape(in.ID), nil, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.out(), "Removed %s.\n", in.PluginID)
	return nil
}

type PluginsEnableCMD struct {
	Plugin     string `arg:"" help:"Install ID or name@marketplace"`
	AcceptCode bool   `name:"accept-code" help:"Turn on a plugin that runs code since you last turned it on. Its code then runs in the isolated sandbox of your chats"`

	PluginsServer `embed:""`
}

func (c *PluginsEnableCMD) Run(_ *cliContext.Context) error {
	return setPluginSwitch(&c.PluginsServer, c.Plugin, true, c.AcceptCode)
}

type PluginsDisableCMD struct {
	Plugin string `arg:"" help:"Install ID or name@marketplace"`

	PluginsServer `embed:""`
}

func (c *PluginsDisableCMD) Run(_ *cliContext.Context) error {
	return setPluginSwitch(&c.PluginsServer, c.Plugin, false, false)
}

func setPluginSwitch(s *PluginsServer, ref string, on, acceptCode bool) error {
	ctx := context.Background()
	in, err := s.resolveInstall(ctx, ref)
	if err != nil {
		return err
	}
	body := map[string]any{"enabled": on}
	if acceptCode {
		body["reenable"] = true
	}
	err = s.call(ctx, http.MethodPut, "/api/plugins/installs/"+url.PathEscape(in.ID)+"/me", body, nil)
	if e, ok := apiErrorType(err); ok && e.Type == "needs_reenable" {
		return fmt.Errorf("%s runs code since you last turned it on. Review it, then turn it on with: local-ai plugins enable --accept-code %s", in.PluginID, in.ID)
	}
	if err != nil {
		return err
	}
	word := "off"
	if on {
		word = "on"
	}
	_, _ = fmt.Fprintf(s.out(), "%s is %s for you.\n", in.PluginID, word)
	return nil
}
