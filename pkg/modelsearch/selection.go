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

// ErrUnknownSource means a source ID that is neither built in, nor installed,
// nor already in the stored choice.
var ErrUnknownSource = errors.New("unknown search source")

// SelectionKV is the store of the per-person add-on settings, seen through the
// one key that holds the enabled set. A store that cannot answer returns an
// error.
type SelectionKV interface {
	Get(ctx context.Context, user string) (string, bool, error)
	Set(ctx context.Context, user, value string) error
}

// Selection is the enabled set of search sources of a person. It is stored as
// a JSON list of source IDs with the person's other add-on settings, so every
// frontend of a cluster reads the same answer.
//
// A person who never chose has the default set: the built-in source. An empty
// set is a choice: nothing is searched. An ID that names a source that is not
// installed is ignored when searching and kept, so installing the add-on again
// restores the choice.
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

// Stored returns the stored list, and false when the person never chose.
func (s *Selection) Stored(ctx context.Context, user string) ([]string, bool, error) {
	if s == nil || s.kv == nil {
		return nil, false, nil
	}
	v, ok, err := s.kv.Get(ctx, user)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(v), &ids); err != nil {
		return nil, false, fmt.Errorf("the stored choice of sources is not a list: %w", err)
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = canonicalSource(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, true, nil
}

// Enabled returns the enabled set: the stored one, or the default.
func (s *Selection) Enabled(ctx context.Context, user string) ([]string, error) {
	ids, stored, err := s.Stored(ctx, user)
	if err != nil {
		return nil, err
	}
	if !stored {
		return []string{HuggingFaceName}, nil
	}
	return ids, nil
}

// Set stores the enabled set. installed lists the IDs of the sources that are
// installed now. An ID must be built in, installed or already stored; the last
// rule lets a person save the set while an add-on they chose is missing.
func (s *Selection) Set(ctx context.Context, user string, ids, installed []string) error {
	if s == nil || s.kv == nil {
		return errors.New("the choice of sources cannot be stored on this server")
	}
	old, _, err := s.Stored(ctx, user)
	if err != nil {
		// A stored value that cannot be read must not block a new choice.
		old = nil
	}
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := canonicalSource(raw)
		if !contract.ValidName(id) {
			return fmt.Errorf("%w: %q", ErrUnknownSource, raw)
		}
		if id != HuggingFaceName && !slices.Contains(installed, id) && !slices.Contains(old, id) {
			return fmt.Errorf("%w: %q", ErrUnknownSource, raw)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return s.kv.Set(ctx, user, string(b))
}
