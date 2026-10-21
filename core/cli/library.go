package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

// LibraryFlags are shared by every `local-ai library` command. The sources are
// stored under the data path, in the file the server reads, so the commands
// work with the server stopped.
type LibraryFlags struct {
	DataPath              string `env:"LOCALAI_DATA_PATH" type:"path" default:"${basepath}/data" help:"Path for persistent data. The library sources are stored under it, in addons/sources.json, the file the server reads" group:"storage"`
	AddonsOfficialLibrary bool   `name:"addons-official-library" env:"LOCALAI_ADDONS_OFFICIAL_LIBRARY" default:"true" help:"List the official add-on library as a default source" group:"addons"`
	AddonsBackendLibrary  bool   `name:"addons-backend-library" env:"LOCALAI_ADDONS_BACKEND_LIBRARY" default:"true" help:"List the pilot backend library as a default source" group:"addons"`
	OfficialLibraries     bool   `name:"official-libraries" env:"LOCALAI_OFFICIAL_LIBRARIES" default:"true" help:"Use the official (shipped default) library sources of every kind. Off: they stay in the list, shown as off" group:"addons"`
}

func (f LibraryFlags) open() (*catalog.Sources, error) {
	store, err := library.Open(f.DataPath, f.AddonsOfficialLibrary, f.AddonsBackendLibrary)
	if err != nil {
		return nil, err
	}
	store.SetOfficial(f.OfficialLibraries)
	return store, nil
}

// LibraryCMD manages the sources LocalAI reads add-ons, backends and models from.
type LibraryCMD struct {
	List    LibraryList    `cmd:"" default:"withargs" help:"List the library sources, with their kind, state and whether they are shipped defaults"`
	Add     LibraryAdd     `cmd:"" help:"Add a library source of your own"`
	Remove  LibraryRemove  `cmd:"" help:"Remove a source. A removed default stays removed at the next start"`
	Enable  LibraryEnable  `cmd:"" help:"Turn a source on (this also brings back a removed default)"`
	Disable LibraryDisable `cmd:"" help:"Turn a source off without removing it"`
}

type LibraryList struct {
	LibraryFlags `embed:""`
}

type LibraryAdd struct {
	URL    string `arg:"" name:"url" help:"URL of the library index"`
	Kind   string `required:"" enum:"addon,backend,model" help:"What the source provides: addon, backend or model"`
	ID     string `help:"Source id: lowercase letters, digits and dashes. Default: derived from the name or URL"`
	Name   string `help:"Display name. Default: the id"`
	Format string `default:"library" enum:"library,gallery" help:"library: an index in the shared library entry shape. gallery: a model or backend gallery index, as --galleries names"`

	LibraryFlags `embed:""`
}

type LibraryRemove struct {
	ID string `arg:"" name:"id" help:"Source id"`

	LibraryFlags `embed:""`
}

type LibraryEnable struct {
	ID string `arg:"" name:"id" help:"Source id"`

	LibraryFlags `embed:""`
}

type LibraryDisable struct {
	ID string `arg:"" name:"id" help:"Source id"`

	LibraryFlags `embed:""`
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// writeSources prints the table `library list` shows. Removed is "yes" only for
// a default the user removed; such a record is kept as a tombstone.
func writeSources(w io.Writer, entries []catalog.Source) error {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tID\tURL\tENABLED\tDEFAULT\tREMOVED")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Kind, e.ID, e.URL, yesNo(e.Enabled && !e.Suspended), yesNo(e.Default), yesNo(e.Removed))
	}
	return tw.Flush()
}

func (c *LibraryList) Run(_ *cliContext.Context) error {
	store, err := c.open()
	if err != nil {
		return err
	}
	return writeSources(os.Stdout, store.Entries())
}

func (c *LibraryAdd) Run(_ *cliContext.Context) error {
	store, err := c.open()
	if err != nil {
		return err
	}
	format := ""
	if c.Format == "gallery" {
		format = catalog.FormatGallery
	}
	src, err := library.Add(store, catalog.Kind(c.Kind), c.URL, c.ID, c.Name, format)
	if err != nil {
		if errors.Is(err, catalog.ErrInvalid) {
			return fmt.Errorf("cannot add %q as a %s source: the id must be lowercase letters, digits and dashes and start with a letter, and a library index URL must be http or https (use --format=gallery for a gallery index)", c.URL, c.Kind)
		}
		return err
	}
	fmt.Printf("added %s source %s (%s)\n", src.Kind, src.ID, src.URL)
	return nil
}

func (c *LibraryRemove) Run(_ *cliContext.Context) error {
	store, err := c.open()
	if err != nil {
		return err
	}
	wasDefault, err := library.Remove(store, c.ID)
	if err != nil {
		return notFound(err, c.ID)
	}
	if wasDefault {
		fmt.Printf("removed default source %s; it stays removed at the next start (local-ai library enable %s brings it back)\n", c.ID, c.ID)
	} else {
		fmt.Printf("removed source %s\n", c.ID)
	}
	return nil
}

func (c *LibraryEnable) Run(_ *cliContext.Context) error {
	store, err := c.open()
	if err != nil {
		return err
	}
	if err := store.SetEnabled(c.ID, true); err != nil {
		return notFound(err, c.ID)
	}
	fmt.Printf("enabled source %s\n", c.ID)
	return nil
}

func (c *LibraryDisable) Run(_ *cliContext.Context) error {
	store, err := c.open()
	if err != nil {
		return err
	}
	if err := store.SetEnabled(c.ID, false); err != nil {
		return notFound(err, c.ID)
	}
	fmt.Printf("disabled source %s\n", c.ID)
	return nil
}

func notFound(err error, id string) error {
	if errors.Is(err, catalog.ErrNotFound) {
		return fmt.Errorf("no source with id %q; run `local-ai library list` to see them", strings.TrimSpace(id))
	}
	return err
}
