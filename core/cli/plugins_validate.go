package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
)

// PluginsValidateCMD runs the checks of an install on this machine, with no
// server: the tree checks, the content digest, the parse and the
// classification. The curated marketplace's CI runs it on every entry.
type PluginsValidateCMD struct {
	Dir         string `arg:"" optional:"" type:"existingdir" help:"Plugin folder to check"`
	Marketplace string `type:"existingfile" help:"A marketplace.json: check every plugin it lists. Relative sources are read from the files next to it; the others are fetched with git"`
	MaxBytes    int64  `name:"max-bytes" default:"20971520" help:"Largest size of the files of one plugin, as the server setting plugins_max_bytes"`
	Strict      bool   `help:"Fail when a component is skipped, not only on a refused plugin"`

	output io.Writer
	// fetcher fetches the entries that are not relative; tests set it. Nil
	// makes a hardened git fetcher with a scratch folder under TMPDIR.
	fetcher pluginFetcher
	// scratch is the folder of the fetches, removed when the run ends.
	scratch string
}

// pluginFetcher is the part of *fetch.Fetcher that validate uses.
type pluginFetcher interface {
	FetchPlugin(ctx context.Context, src fetch.MarketplaceSource, m *claudeplugin.Marketplace, e *claudeplugin.Entry, sha string) (*fetch.Fetched, error)
}

func (c *PluginsValidateCMD) out() io.Writer {
	if c.output == nil {
		return os.Stdout
	}
	return c.output
}

func (c *PluginsValidateCMD) limits() content.Limits {
	lim := content.DefaultLimits()
	if c.MaxBytes > 0 {
		lim.MaxTotalBytes = c.MaxBytes
	}
	return lim
}

func (c *PluginsValidateCMD) Run(_ *cliContext.Context) error {
	switch {
	case c.Dir == "" && c.Marketplace == "":
		return errors.New("name a plugin folder or pass --marketplace")
	case c.Dir != "" && c.Marketplace != "":
		return errors.New("name a plugin folder or pass --marketplace, not both")
	case c.Dir != "":
		if ok, _ := c.check(c.Dir, filepath.Base(filepath.Clean(c.Dir)), claudeplugin.LoadOptions{}); !ok {
			return errors.New("the plugin failed the checks")
		}
		return nil
	}
	return c.validateMarketplace()
}

// check validates one plugin folder and prints the report. It returns false
// when the plugin fails, and the code reasons of a plugin that loaded.
func (c *PluginsValidateCMD) check(dir, label string, opts claudeplugin.LoadOptions) (bool, []string) {
	w := c.out()
	v, err := plugins.CheckDir(dir, c.limits(), opts)
	if err != nil {
		_, _ = fmt.Fprintf(w, "FAIL  %s: %v\n", label, err)
		return false, nil
	}
	var size int64
	for _, f := range v.Tree.Files {
		size += f.Size
	}
	m := v.Model
	_, _ = fmt.Fprintf(w, "ok    %s %s\n", m.Name, m.Version)
	_, _ = fmt.Fprintf(w, "      digest:    %s\n", v.Digest)
	_, _ = fmt.Fprintf(w, "      files:     %d, %d bytes\n", len(v.Tree.Files), size)
	_, _ = fmt.Fprintf(w, "      adds:      %s, %s, %s, %s, %s\n", plural(len(m.Skills), "skill", "skills"),
		plural(len(m.Commands), "command", "commands"), plural(len(m.Agents), "agent", "agents"),
		plural(len(m.MCPServers), "MCP server", "MCP servers"), plural(len(m.Hooks), "hook", "hooks"))
	if len(v.CodeReasons) == 0 {
		_, _ = fmt.Fprintln(w, "      runs code: no")
	} else {
		_, _ = fmt.Fprintf(w, "      runs code: yes (%s); only an administrator installs it, for everyone\n", strings.Join(v.CodeReasons, ", "))
		for _, f := range v.CodeFiles {
			_, _ = fmt.Fprintf(w, "      code file: %s\n", f)
		}
	}
	ok := true
	for _, s := range v.Plugin.Skipped {
		word := "skipped"
		if c.Strict {
			word, ok = "SKIPPED", false
		}
		name := s.Name
		if name == "" {
			name = "-"
		}
		_, _ = fmt.Fprintf(w, "      %s:   %s %s: %s\n", word, s.Component, name, s.Reason)
	}
	if !ok {
		_, _ = fmt.Fprintf(w, "FAIL  %s: components were skipped (--strict)\n", label)
	}
	return ok, v.CodeReasons
}

// declaredRunsCode reads metadata.localai.runsCode of a marketplace entry,
// the curated marketplace's claim that a reviewer checked. ok is false when
// the entry does not declare it.
func declaredRunsCode(e *claudeplugin.Entry) (runsCode, ok bool) {
	var raw struct {
		Metadata struct {
			LocalAI struct {
				RunsCode *bool `json:"runsCode"`
			} `json:"localai"`
		} `json:"metadata"`
	}
	if len(e.Raw) == 0 || json.Unmarshal(e.Raw, &raw) != nil || raw.Metadata.LocalAI.RunsCode == nil {
		return false, false
	}
	return *raw.Metadata.LocalAI.RunsCode, true
}

func describeCode(reasons []string) string {
	if len(reasons) == 0 {
		return "no code"
	}
	return "code (" + strings.Join(reasons, ", ") + ")"
}

// marketplaceRoot is the folder that holds .claude-plugin/marketplace.json,
// against which relative sources resolve.
func marketplaceRoot(file string) (string, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)
	if filepath.Base(dir) == ".claude-plugin" {
		return filepath.Dir(dir), nil
	}
	return dir, nil
}

func (c *PluginsValidateCMD) validateMarketplace() error {
	w := c.out()
	data, err := os.ReadFile(c.Marketplace) // #nosec G304 -- the file the person named on the command line
	if err != nil {
		return err
	}
	// ParseMarketplace runs no git, unlike LoadMarketplace.
	m, err := claudeplugin.ParseMarketplace(data)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Marketplace, err)
	}
	if m.Root, err = marketplaceRoot(c.Marketplace); err != nil {
		return err
	}
	failed := 0
	for _, s := range m.Skipped {
		_, _ = fmt.Fprintf(w, "FAIL  entry %s: %s\n", s.Name, s.Reason)
		failed++
	}
	ctx := context.Background()
	defer func() {
		if c.scratch != "" {
			_ = os.RemoveAll(c.scratch)
		}
	}()
	for i := range m.Plugins {
		e := &m.Plugins[i]
		label := e.Name + "@" + m.Name
		opts := claudeplugin.LoadOptions{Entry: e, Marketplace: m.Name, Name: e.Name}
		if err := e.Source.Supported(); err != nil {
			_, _ = fmt.Fprintf(w, "FAIL  %s: %v\n", label, err)
			failed++
			continue
		}
		var dir string
		if e.Source.Kind == claudeplugin.SourceRelative {
			if dir, err = m.RelativeDir(e.Source); err != nil {
				_, _ = fmt.Fprintf(w, "FAIL  %s: %v\n", label, err)
				failed++
				continue
			}
		} else {
			f, ferr := c.fetch(ctx, m, e)
			if ferr != nil {
				_, _ = fmt.Fprintf(w, "FAIL  %s: %v\n", label, ferr)
				failed++
				continue
			}
			defer f.Done()
			dir = f.Dir
		}
		ok, reasons := c.check(dir, label, opts)
		if !ok {
			failed++
			continue
		}
		// A wrong claim would let users install code that the review
		// called text-only, so it fails like a refused plugin.
		if declared, has := declaredRunsCode(e); has && declared != (len(reasons) > 0) {
			_, _ = fmt.Fprintf(w, "FAIL  %s: metadata.localai.runsCode is %t, but the checks found %s\n",
				label, declared, describeCode(reasons))
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d entries failed the checks", failed, len(m.Plugins)+len(m.Skipped))
	}
	_, _ = fmt.Fprintf(w, "All %d plugins of %s pass.\n", len(m.Plugins), m.Name)
	return nil
}

// fetch fetches an entry that is not relative with the fetcher the server
// uses: https only, a partial clone, pinned to the entry's commit when it
// names one.
func (c *PluginsValidateCMD) fetch(ctx context.Context, m *claudeplugin.Marketplace, e *claudeplugin.Entry) (*fetch.Fetched, error) {
	if c.fetcher == nil {
		scratch, err := os.MkdirTemp("", "localai-plugins-validate-")
		if err != nil {
			return nil, err
		}
		f, err := fetch.New(fetch.Options{ScratchRoot: scratch, Limits: c.limits()})
		if err != nil {
			_ = os.RemoveAll(scratch)
			return nil, err
		}
		c.scratch, c.fetcher = scratch, f
	}
	return c.fetcher.FetchPlugin(ctx, fetch.MarketplaceSource{}, m, e, "")
}
