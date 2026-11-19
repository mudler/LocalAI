package modelupdates

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v3"
)

// Errors of an adoption.
var (
	// ErrAlreadyAdopted refuses a model whose gallery file already records
	// its origin.
	ErrAlreadyAdopted = errors.New("the model already records the gallery entry it came from")
	// ErrNoInstallRecord refuses a model that has no gallery file at all: it
	// was not installed from a gallery, so there is no record of its files.
	ErrNoInstallRecord = errors.New("the model has no record of a gallery install; install it from the gallery to track its updates")
)

// AdoptCandidate is a gallery entry an adoption can take.
type AdoptCandidate struct {
	Gallery string `json:"gallery"`
	Entry   string `json:"entry"`
}

func (c AdoptCandidate) String() string { return c.Gallery + "@" + c.Entry }

// ErrAmbiguousEntry refuses an adoption that more than one gallery entry
// matches: the request must name the gallery (and the entry).
type ErrAmbiguousEntry struct{ Candidates []AdoptCandidate }

func (e *ErrAmbiguousEntry) Error() string {
	return "more than one gallery lists an entry for this model; name the gallery: " + candidateList(e.Candidates)
}

// ErrNoCandidate refuses an adoption that no gallery entry matches.
// Candidates are the entries that match the model's name in any gallery,
// when the request named a gallery or an entry that lists none.
type ErrNoCandidate struct{ Candidates []AdoptCandidate }

func (e *ErrNoCandidate) Error() string {
	if len(e.Candidates) == 0 {
		return "no configured gallery lists an entry for this model; name the gallery and the entry"
	}
	return "the gallery and entry named do not list this model; it matches: " + candidateList(e.Candidates)
}

func candidateList(cs []AdoptCandidate) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return strings.Join(out, ", ")
}

// AdoptRequest asks to record the gallery origin of a model installed by a
// release that did not record it. Gallery and Entry are optional: without
// them the entry is found by the model's names, and only when exactly one
// gallery lists it.
type AdoptRequest struct {
	Name    string
	Gallery string
	Entry   string
}

// AdoptResult is what an adoption recorded.
type AdoptResult struct {
	Name    string
	Gallery string
	Entry   string
	// FilesMatch is true when the files the model has are the files the
	// entry lists now; false means the next check offers an update.
	FilesMatch bool
}

// Adopt records where a model installed by an earlier release came from, so
// its updates are checked like those of a model installed now. It writes the
// gallery file only: the origin, the files the model has (from its old
// gallery file) and what the gallery would write as the config. The config on
// disk is not touched, so a key a person changed reads as an edit at the next
// check. When the model's files are not those the entry lists now, the
// recorded digest is one no entry has, and the check offers the update.
//
// It takes the model lock, like an upgrade.
func (u *Upgrader) Adopt(ctx context.Context, req AdoptRequest) (*AdoptResult, error) {
	name := req.Name
	if err := utils.VerifyPath(name+".yaml", u.modelsPath()); err != nil {
		return nil, ErrNotFound
	}
	unlock, err := u.acquire(ctx, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sc, err := u.readGalleryFile(name)
	if errors.Is(err, gallery.ErrNoSidecar) {
		return nil, ErrNoInstallRecord
	}
	if errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, u.relErr(err)
	}
	if sc.IsV2() && sc.Origin != nil {
		return nil, ErrAlreadyAdopted
	}
	if sc.Upgrade != nil && !sc.Upgrade.Terminal() {
		return nil, ErrBusy
	}

	models, err := u.ListGallery(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("read the galleries: %w", err)
	}
	names := []string{req.Entry}
	if req.Entry == "" {
		names = []string{sc.EntryName, name, sc.Name}
	}
	var all, matching []AdoptCandidate
	for _, m := range models {
		if m == nil || m.Name == "" || !slices.Contains(names, m.Name) {
			continue
		}
		c := AdoptCandidate{Gallery: m.Gallery.Name, Entry: m.Name}
		if slices.Contains(all, c) {
			continue
		}
		all = append(all, c)
		if req.Gallery == "" || req.Gallery == c.Gallery {
			matching = append(matching, c)
		}
	}
	switch {
	case len(matching) == 0:
		return nil, &ErrNoCandidate{Candidates: all}
	case len(matching) > 1:
		return nil, &ErrAmbiguousEntry{Candidates: matching}
	}
	pick := matching[0]

	probe := &gallery.ModelConfig{
		Origin:          &gallery.Origin{Gallery: pick.Gallery, EntryName: pick.Entry},
		ResolvedVariant: sc.ResolvedVariant, PinnedVariant: sc.PinnedVariant,
	}
	inst, plan, err := PlanUpgrade(ctx, u.SystemState, models, name, probe, gallery.WithoutArtifactBinding())
	if err != nil {
		return nil, u.relErr(err)
	}
	disk, err := u.fs().ReadFile(u.path(name + ".yaml"))
	if err != nil {
		return nil, u.relErr(fmt.Errorf("read the model config: %w", err))
	}

	managed := sc.Managed
	if len(managed) == 0 {
		managed = gallery.ManagedFiles(sc)
	}
	origin := *inst.Origin
	origin.InstalledAt = u.now()
	if plan.WriteConfig {
		origin.ConfigYAML = string(plan.ConfigYAML)
	}
	match := len(changedFiles(managed, plan.Gallery)) == 0
	if !match {
		// The entry has moved on since this install: a digest of the files
		// the model has, which no entry has, makes the check offer the
		// update.
		data, err := yaml.Marshal(managed)
		if err != nil {
			return nil, err
		}
		origin.EntryDigest = "sha256:" + gallery.SHA256Hex(data)
	}
	next := *sc
	next.Version, next.Origin, next.Managed, next.ConfigSHA256 = gallery.SidecarVersion, &origin, managed, gallery.SHA256Hex(disk)
	if err := u.writeSidecar(name, &next); err != nil {
		return nil, u.relErr(err)
	}
	u.changed(name)
	return &AdoptResult{Name: name, Gallery: pick.Gallery, Entry: pick.Entry, FilesMatch: match}, nil
}
