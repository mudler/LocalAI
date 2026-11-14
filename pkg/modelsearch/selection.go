package modelsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrUnknownSource means a source ID that is neither built in, nor visible to
// the person, nor already in the stored list of sources they turned off.
var ErrUnknownSource = errors.New("unknown search source")

// ErrTooManySources means the list is longer than the sources that can be in
// it: the visible ones, the ones already stored, and the built-in one, with a
// hard cap of MaxEnabledSources.
var ErrTooManySources = errors.New("too many search sources in the list")

// MaxEnabledSources is the hard cap of the list a person sends and of the
// stored list of sources they turned off.
const MaxEnabledSources = 64

// SelectionKV is the store of the per-person add-on settings, seen through the
// one key that holds the list of sources the person turned off. A store that
// cannot answer returns an error.
type SelectionKV interface {
	Get(ctx context.Context, user string) (string, bool, error)
	Set(ctx context.Context, user, value string) error
}

// Selection is the choice of search sources of a person. Every source the
// person can see is on, built-in or add-on, until the person turns it off. The
// store holds only what they turned off, as a JSON list of source IDs next to
// their other add-on settings, so every frontend of a cluster reads the same
// answer and a source installed later is on with no action.
//
// An empty enabled set is a choice: nothing is searched. An ID in the off
// list that names a source that is not installed is ignored and kept, so
// installing the add-on again restores the choice.
type Selection struct{ kv SelectionKV }

// NewSelection returns a Selection over the store.
func NewSelection(kv SelectionKV) *Selection { return &Selection{kv: kv} }

// canonicalSource maps the descriptor's "builtin" to the ID of the built-in
// source.
func canonicalSource(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == contract.ProviderBuiltin {
		return HuggingFaceName
	}
	return id
}

// Off returns the stored list of sources the person turned off. A person who
// never chose has an empty list.
func (s *Selection) Off(ctx context.Context, user string) ([]string, error) {
	if s == nil || s.kv == nil {
		return nil, nil
	}
	v, ok, err := s.kv.Get(ctx, user)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(v), &ids); err != nil {
		return nil, fmt.Errorf("the stored list of sources that are off is not a list: %w", err)
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = canonicalSource(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Enabled returns the enabled set: every visible source that is not in the
// stored off list, in the order of visible.
func (s *Selection) Enabled(ctx context.Context, user string, visible []string) ([]string, error) {
	off, err := s.Off(ctx, user)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(visible))
	for _, id := range visible {
		if !slices.Contains(off, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Set stores the enabled set as the list of what is off: every visible source
// that is not enabled. visible lists the IDs of the sources the person can use
// now. An ID must be built in, visible or already in the stored off list; the
// last rule lets a person save the set while an add-on they turned off is
// missing. Stored IDs that are not visible stay in the list, unless the person
// enables them, so an add-on that comes back keeps the choice made for it.
func (s *Selection) Set(ctx context.Context, user string, enabled, visible []string) error {
	if s == nil || s.kv == nil {
		return errors.New("the choice of sources cannot be stored on this server")
	}
	old, err := s.Off(ctx, user)
	if err != nil {
		// A stored value that cannot be read must not block a new choice.
		old = nil
	}
	if limit := min(MaxEnabledSources, len(visible)+len(old)+1); len(enabled) > limit {
		return fmt.Errorf("%w: %d given, at most %d", ErrTooManySources, len(enabled), limit)
	}
	// The built-in source is always there to turn off.
	if !slices.Contains(visible, HuggingFaceName) {
		visible = append(slices.Clone(visible), HuggingFaceName)
	}
	on := make([]string, 0, len(enabled))
	for _, raw := range enabled {
		id := canonicalSource(raw)
		if !contract.ValidName(id) {
			return fmt.Errorf("%w: %q", ErrUnknownSource, raw)
		}
		if id != HuggingFaceName && !slices.Contains(visible, id) && !slices.Contains(old, id) {
			return fmt.Errorf("%w: %q", ErrUnknownSource, raw)
		}
		if !slices.Contains(on, id) {
			on = append(on, id)
		}
	}
	off := make([]string, 0, len(visible))
	for _, id := range visible {
		if !slices.Contains(on, id) {
			off = append(off, id)
		}
	}
	for _, id := range old {
		if !slices.Contains(visible, id) && !slices.Contains(on, id) && !slices.Contains(off, id) {
			off = append(off, id)
		}
	}
	if len(off) > MaxEnabledSources {
		off = off[:MaxEnabledSources]
	}
	b, err := json.Marshal(off)
	if err != nil {
		return err
	}
	return s.kv.Set(ctx, user, string(b))
}
