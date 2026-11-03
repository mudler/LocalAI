package studio

import (
	"context"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Add-on context files. An add-on can declare small index files of the user's
// home (capabilities.contextFiles in its manifest). The host loads them into
// the prompt of a chat and serves their topics with context_read, with the same
// loader as any other source. The add-on is not called: it only names paths,
// and the host reads them as the user.
//
// A turn decides which add-ons contribute, as it does for their skills: a
// stopped add-on, one the chat switched off and one the chat's agent does not
// grant contribute none. The files stay where they are while the add-on is
// stopped or removed; they belong to the user.
//
// Distributed mode: the files are in the workspace store that every frontend
// reads, and which add-ons run is read per turn from the broker. Nothing is
// cached here.

// AddonContextSet is the context files one add-on declares, as the chats see
// them.
type AddonContextSet struct {
	AddonID string
	Files   []contract.ContextFile
	// Running is true when the add-on can serve now: installed, enabled and
	// running.
	Running bool
}

// AddonContextLister is implemented by a broker that knows the context files
// of the add-ons. It lists the installed add-ons that declare some and that
// the user can see, with whether each one runs. It is optional; a broker
// without it gives no add-on context files.
type AddonContextLister interface {
	AddonContextFiles(ctx context.Context, user string) ([]AddonContextSet, error)
}

// sources turns the declaration of an add-on into loader sources. A file the
// loader would refuse is left out.
func (set AddonContextSet) sources() []contextfiles.Source {
	var out []contextfiles.Source
	for _, f := range set.Files {
		src := contextfiles.Source{ID: set.AddonID, Scope: contextfiles.Scope(f.Scope), Path: f.Path, Cap: f.Cap}
		if src.Validate() == nil {
			out = append(out, src)
		}
	}
	return out
}

func (s *Service) addonContextSets(ctx context.Context, user string) []AddonContextSet {
	if s.d.AddonTools == nil {
		return nil
	}
	lister, ok := s.d.AddonTools.(AddonContextLister)
	if !ok {
		return nil
	}
	sets, err := lister.AddonContextFiles(ctx, user)
	if err != nil {
		s.log.Warn("studio: add-on context files unavailable", "user", user, "error", err)
		return nil
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].AddonID < sets[j].AddonID })
	return sets
}

// addonContextSources returns the sources the add-ons bring to a turn: those
// of the add-ons that run, that the chat did not switch off and, for a chat
// with an agent, that the agent grants.
func (s *Service) addonContextSources(ctx context.Context, user string, addonsOff []string, sc *scope) []contextfiles.Source {
	if !s.d.Files.Available() {
		return nil
	}
	var out []contextfiles.Source
	gate, _ := s.d.AddonTools.(AddonGate)
	for _, set := range s.addonContextSets(ctx, user) {
		if !set.Running || slices.Contains(addonsOff, set.AddonID) || (sc != nil && !sc.addons[set.AddonID]) {
			continue
		}
		if gate != nil && gate.Unavailable(ctx, user, set.AddonID, true) != "" {
			continue
		}
		out = append(out, set.sources()...)
	}
	return out
}

// allAddonContextSources returns the sources of every add-on the user can see,
// running or not: the files are the user's, and the Memory page lists them
// whether or not the add-on runs now.
func (s *Service) allAddonContextSources(ctx context.Context, user string) []contextfiles.Source {
	var out []contextfiles.Source
	for _, set := range s.addonContextSets(ctx, user) {
		out = append(out, set.sources()...)
	}
	return out
}
