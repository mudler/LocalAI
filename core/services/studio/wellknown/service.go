package wellknown

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Errors of the service. A workspace error passes through unchanged.
var (
	// ErrBadPath means a data file path is missing or is not a .json file.
	ErrBadPath = errors.New("wellknown: a data file must be a .json path in your home")
	// ErrTooLarge means the file would be larger than MaxSize.
	ErrTooLarge = fmt.Errorf("wellknown: the file would be larger than %d bytes", MaxSize)
	// ErrNoPrevious means the history holds no earlier valid version.
	ErrNoPrevious = errors.New("wellknown: there is no earlier valid version")
)

// InvalidError means the content breaks the schema. Nothing was written.
type InvalidError struct{ Errors []string }

func (e *InvalidError) Error() string {
	return "wellknown: the file does not match its schema: " + strings.Join(e.Errors, "; ")
}

// ConflictError means the file changed after the version the writer started
// from. Current is what the file holds now. Nothing was written.
type ConflictError struct{ Current Doc }

func (e *ConflictError) Error() string { return "wellknown: the file changed since it was read" }

// historyDepth is how many change sets of one file are searched for a good version.
const historyDepth = 50

// Doc is a well-known file as the UI and the prompt see it.
type Doc struct {
	Name    Name
	Path    string // inside the home, for example /persona.json
	Exists  bool
	Version int64 // the version to send back as the base of a write; 0 when the file does not exist
	Rev     int64
	// Data is what to show and use: the file when it is valid, the last good
	// version when it is not (Fallback), and an empty object otherwise.
	Data map[string]any
	// Valid is false when the file on disk breaks its schema. Errors says why.
	Valid       bool
	Errors      []string
	Fallback    bool
	FallbackRev int64
}

// WriteRequest is a change to one well-known file.
type WriteRequest struct {
	Name Name
	Path string // for WidgetData only
	// Values are the keys to set. A nil value removes the key. The other keys
	// of the file stay as they are, unless Replace is set.
	Values  map[string]any
	Replace bool
	// Base is the Version the writer read. Nil writes without the check; 0
	// means the file must not exist yet.
	Base *int64
}

// Service reads and writes the well-known files of each user, with the user's
// own rights.
type Service struct{ f *files.Service }

// New returns a Service on the files service.
func New(f *files.Service) *Service { return &Service{f: f} }

// Available reports whether the workspace works.
func (s *Service) Available() bool { return s.f.Available() }

// path returns the workspace path and the path inside the home.
func (s *Service) path(principal string, n Name, rel string) (abs, home string, err error) {
	if _, err := schemaOf(n); err != nil {
		return "", "", err
	}
	if f := File(n); f != "" {
		rel = f
	}
	if !strings.HasSuffix(rel, ".json") || strings.TrimSpace(strings.Trim(rel, "/")) == "" {
		return "", "", ErrBadPath
	}
	abs, err = s.f.Abs(principal, rel)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrBadPath, err)
	}
	return abs, s.f.Rel(principal, abs), nil
}

// Read returns the file n of the user (rel names the file for WidgetData). A
// file that does not exist is an empty, valid Doc.
func (s *Service) Read(ctx context.Context, principal string, n Name, rel string) (Doc, error) {
	abs, home, err := s.path(principal, n, rel)
	if err != nil {
		return Doc{}, err
	}
	return s.read(ctx, principal, n, abs, home)
}

func (s *Service) read(ctx context.Context, principal string, n Name, abs, home string) (Doc, error) {
	doc := Doc{Name: n, Path: home, Data: map[string]any{}, Valid: true}
	data, node, err := s.open(ctx, principal, abs)
	if errors.Is(err, workspace.ErrNotFound) {
		return doc, nil
	}
	if err != nil && !errors.Is(err, ErrTooLarge) {
		return Doc{}, err
	}
	doc.Exists, doc.Version, doc.Rev = true, node.Version, node.Rev
	var errs []string
	if errors.Is(err, ErrTooLarge) {
		errs = []string{ErrTooLarge.Error()}
	} else if obj, bad := Validate(n, data); len(bad) == 0 {
		doc.Data = obj
		return doc, nil
	} else {
		errs = bad
	}
	doc.Valid, doc.Errors = false, errs
	good, rev, err := s.lastGood(ctx, principal, n, abs, nil)
	if err != nil && !errors.Is(err, files.ErrNoVersions) {
		return Doc{}, err
	}
	if good != nil {
		doc.Data, doc.Fallback, doc.FallbackRev = good, true, rev
	}
	return doc, nil
}

func (s *Service) open(ctx context.Context, principal, abs string) ([]byte, workspace.Node, error) {
	r, node, err := s.f.Store().Open(ctx, s.f.Actor(principal), abs)
	if err != nil {
		return nil, workspace.Node{}, err
	}
	defer r.Close()
	if node.Kind != workspace.KindFile {
		return nil, node, ErrBadPath
	}
	if node.Size > MaxSize {
		return nil, node, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, node, err
	}
	return data, node, nil
}

// lastGood returns the newest valid version of the file from its history whose
// bytes differ from skip, or nil.
func (s *Service) lastGood(ctx context.Context, principal string, n Name, abs string, skip []byte) (map[string]any, int64, error) {
	var good map[string]any
	var rev int64
	err := s.f.Versions(ctx, principal, abs, historyDepth, MaxSize, func(v files.FileVersion) bool {
		if skip != nil && bytes.Equal(v.Data, skip) {
			return true
		}
		if obj, bad := Validate(n, v.Data); len(bad) == 0 {
			good, rev = obj, v.Rev
			return false
		}
		return true
	})
	return good, rev, err
}

// Write changes a well-known file. The result is validated before anything is
// written: an invalid result is an *InvalidError, a stale Base a *ConflictError.
func (s *Service) Write(ctx context.Context, principal string, w WriteRequest) (Doc, error) {
	abs, home, err := s.path(principal, w.Name, w.Path)
	if err != nil {
		return Doc{}, err
	}
	cur, node, err := s.open(ctx, principal, abs)
	exists := err == nil
	switch {
	case errors.Is(err, workspace.ErrNotFound):
	case errors.Is(err, ErrTooLarge):
		exists, cur = true, nil
	case err != nil:
		return Doc{}, err
	}
	if w.Base != nil {
		have := int64(0)
		if exists {
			have = node.Version
		}
		if have != *w.Base {
			doc, rerr := s.read(ctx, principal, w.Name, abs, home)
			if rerr != nil {
				return Doc{}, rerr
			}
			return Doc{}, &ConflictError{Current: doc}
		}
	}
	merged := map[string]any{}
	if !w.Replace && len(cur) > 0 {
		// The keys of the file stay, even when the file is invalid: only the
		// keys the writer sent change.
		_ = json.Unmarshal(cur, &merged)
		if merged == nil {
			merged = map[string]any{}
		}
	}
	for k, v := range w.Values {
		if v == nil {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return Doc{}, &InvalidError{Errors: []string{err.Error()}}
	}
	data = append(data, '\n')
	if len(data) > MaxSize {
		return Doc{}, ErrTooLarge
	}
	if _, bad := Validate(w.Name, data); len(bad) > 0 {
		return Doc{}, &InvalidError{Errors: bad}
	}
	if err := s.put(ctx, principal, abs, home, data, exists, node, w.Base != nil); err != nil {
		return Doc{}, err
	}
	return s.read(ctx, principal, w.Name, abs, home)
}

func (s *Service) put(ctx context.Context, principal, abs, home string, data []byte, exists bool, node workspace.Node, checked bool) error {
	opts := workspace.PutOptions{Title: "Update " + strings.TrimPrefix(home, "/"), MaxSize: MaxSize}
	switch {
	case !checked:
		opts.Overwrite = true
	case exists:
		opts.BaseVersion = node.Version
	}
	n, err := s.f.Store().Put(ctx, s.f.Actor(principal), abs, bytes.NewReader(data), opts)
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return err
	}
	if n.ConflictOf != "" {
		// Another writer won the race after the check. The store kept this
		// content as a copy next to the file; the user sees it in Files.
		return &ConflictError{}
	}
	return nil
}

// Revert writes the newest earlier valid version of the file as a new change.
func (s *Service) Revert(ctx context.Context, principal string, n Name, rel string) (Doc, error) {
	abs, home, err := s.path(principal, n, rel)
	if err != nil {
		return Doc{}, err
	}
	cur, node, err := s.open(ctx, principal, abs)
	exists := err == nil
	if err != nil && !errors.Is(err, workspace.ErrNotFound) && !errors.Is(err, ErrTooLarge) {
		return Doc{}, err
	}
	if cur == nil {
		cur = []byte{}
	}
	good, _, err := s.lastGood(ctx, principal, n, abs, cur)
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, files.ErrNoVersions) {
		return Doc{}, ErrNoPrevious
	}
	if err != nil {
		return Doc{}, err
	}
	if good == nil {
		return Doc{}, ErrNoPrevious
	}
	data, err := json.MarshalIndent(good, "", "  ")
	if err != nil {
		return Doc{}, err
	}
	if err := s.put(ctx, principal, abs, home, append(data, '\n'), exists, node, true); err != nil {
		return Doc{}, err
	}
	return s.read(ctx, principal, n, abs, home)
}

// Version is one content a well-known file had, from its history.
type Version struct {
	Rev  int64
	At   time.Time
	Kind string // user, agent, upload or undo
	// ChatID is the chat that wrote it, when a chat did.
	ChatID string
	// Data is the content when it matches the schema, else nil with Errors.
	Data   map[string]any
	Valid  bool
	Errors []string
}

// Versions lists the versions of the file, newest first, at most limit.
func (s *Service) Versions(ctx context.Context, principal string, n Name, rel string, limit int) ([]Version, error) {
	abs, _, err := s.path(principal, n, rel)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > historyDepth {
		limit = historyDepth
	}
	out := []Version{}
	err = s.f.Versions(ctx, principal, abs, historyDepth, MaxSize, func(v files.FileVersion) bool {
		ver := Version{Rev: v.Rev, At: v.At, Kind: string(v.Kind), ChatID: v.ChatID, Valid: true}
		if obj, bad := Validate(n, v.Data); len(bad) == 0 {
			ver.Data = obj
		} else {
			ver.Valid, ver.Errors = false, bad
		}
		out = append(out, ver)
		return len(out) < limit
	})
	if errors.Is(err, workspace.ErrNotFound) {
		return out, nil
	}
	return out, err
}

// RevertTo writes the version rev of the file back as a new change. A version
// that is not in the history or breaks the schema is ErrNoPrevious.
func (s *Service) RevertTo(ctx context.Context, principal string, n Name, rel string, rev int64) (Doc, error) {
	abs, home, err := s.path(principal, n, rel)
	if err != nil {
		return Doc{}, err
	}
	var good map[string]any
	err = s.f.Versions(ctx, principal, abs, historyDepth, MaxSize, func(v files.FileVersion) bool {
		if v.Rev != rev {
			return true
		}
		if obj, bad := Validate(n, v.Data); len(bad) == 0 {
			good = obj
		}
		return false
	})
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, files.ErrNoVersions) {
		return Doc{}, ErrNoPrevious
	}
	if err != nil {
		return Doc{}, err
	}
	if good == nil {
		return Doc{}, ErrNoPrevious
	}
	_, node, err := s.open(ctx, principal, abs)
	exists := err == nil
	if err != nil && !errors.Is(err, workspace.ErrNotFound) && !errors.Is(err, ErrTooLarge) {
		return Doc{}, err
	}
	data, err := json.MarshalIndent(good, "", "  ")
	if err != nil {
		return Doc{}, err
	}
	if err := s.put(ctx, principal, abs, home, append(data, '\n'), exists, node, true); err != nil {
		return Doc{}, err
	}
	return s.read(ctx, principal, n, abs, home)
}

// LinkWidget sets the trigger of the Home widget widgetID in home.json, so the
// widget can show when the trigger runs and offer Pause. It changes nothing
// and returns false when home.json is missing or invalid or has no such
// widget. The other keys of the file and of the widget stay as they are.
func (s *Service) LinkWidget(ctx context.Context, principal, widgetID, triggerID string) (bool, error) {
	for range 2 { // a second try when another writer won the race
		doc, err := s.Read(ctx, principal, Home, "")
		if err != nil || !doc.Exists || !doc.Valid {
			return false, err
		}
		list, _ := doc.Data["widgets"].([]any)
		found := false
		for _, w := range list {
			if m, ok := w.(map[string]any); ok && m["id"] == widgetID {
				if m["trigger"] == triggerID {
					return true, nil
				}
				m["trigger"], found = triggerID, true
				break
			}
		}
		if !found {
			return false, nil
		}
		v := doc.Version
		_, err = s.Write(ctx, principal, WriteRequest{Name: Home, Values: map[string]any{"widgets": list}, Base: &v})
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			continue
		}
		return err == nil, err
	}
	return false, nil
}
