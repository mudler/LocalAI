package connections

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/modeladmin"
)

// ModelAdmin is the part of modeladmin.ConfigService that the service uses to
// read and write entry files. The service never writes a model file itself.
type ModelAdmin interface {
	CreateConfig(ctx context.Context, body []byte) (*modeladmin.EditResult, error)
	GetConfig(ctx context.Context, name string) (*modeladmin.ConfigView, error)
	EditYAML(ctx context.Context, name string, body []byte) (*modeladmin.EditResult, error)
	PatchConfig(ctx context.Context, name string, patch map[string]any) (*modeladmin.PatchResult, error)
	DeleteConfig(ctx context.Context, name string) (*modeladmin.DeleteResult, error)
}

// ModelConfigs reads the model configs that the loader holds.
type ModelConfigs interface {
	GetModelConfig(name string) (config.ModelConfig, bool)
}

// ModelUnloader stops a loaded model. In distributed mode it also stops the
// replicas on the workers.
type ModelUnloader interface {
	ShutdownModel(name string) error
}

// Broadcaster tells the other frontends that a model config changed. The
// gallery service implements it and does nothing outside distributed mode.
type Broadcaster interface {
	BroadcastModelsChangedRevision(element, op, configRevision string)
}

// Options builds a Service. Store, Admin and Configs are required.
type Options struct {
	Store       Store
	Admin       ModelAdmin
	Configs     ModelConfigs
	Unloader    ModelUnloader
	Broadcaster Broadcaster
	// ModelsPath is the models directory. A bookkeeping entry is dropped at
	// a refresh only when its model is neither loaded nor on disk there. With
	// it empty nothing is dropped.
	ModelsPath string
}

type service struct {
	o     Options
	fetch fetcher
	now   func() time.Time
	newID func() string

	sealMu sync.Mutex
	sealer *sealer
}

// New returns the Service. The service keeps no connection state in memory:
// every call reads the store, so every frontend of a cluster agrees.
func New(o Options) (Service, error) { return newService(o) }

func newService(o Options) (*service, error) {
	if o.Store == nil || o.Admin == nil || o.Configs == nil {
		return nil, errors.New("connections: Store, Admin and Configs are required")
	}
	return &service{o: o, fetch: newFetcher(), now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString}, nil
}

// --- validation -----------------------------------------------------------

const maxConnectionNameLen = 80

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("Enter a name for the connection.")
	}
	if len([]rune(name)) > maxConnectionNameLen {
		return "", invalid(fmt.Sprintf("The name can have at most %d characters.", maxConnectionNameLen))
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", invalid("The name must not contain control characters.")
	}
	return name, nil
}

func cleanKey(kind Kind, key string) (string, error) {
	key = strings.TrimSpace(key)
	if strings.ContainsAny(key, "\r\n\x00") {
		return "", invalid("The key must be a single line.")
	}
	if key == "" && kind == KindAnthropic {
		return "", invalid("Anthropic needs a key.")
	}
	return key, nil
}

// cleanURL checks an address for a kind and returns it as it was entered,
// trimmed. An upstream error becomes an invalid-input error.
func cleanURL(kind Kind, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if _, err := normalizeURL(kind, raw); err != nil {
		var ue *upstreamError
		if errors.As(err, &ue) {
			return "", invalid(ue.Message)
		}
		return "", err
	}
	return raw, nil
}

// --- key sealing ----------------------------------------------------------

func (s *service) getSealer(ctx context.Context) (*sealer, error) {
	s.sealMu.Lock()
	defer s.sealMu.Unlock()
	if s.sealer != nil {
		return s.sealer, nil
	}
	k, err := s.o.Store.EnsureKey(ctx, newSealKey)
	if err != nil {
		return nil, fmt.Errorf("connections: sealing key: %w", err)
	}
	sl, err := newSealer(k)
	if err != nil {
		return nil, err
	}
	s.sealer = sl
	return sl, nil
}

func (s *service) sealKey(ctx context.Context, id, key string) ([]byte, error) {
	if key == "" {
		return nil, nil
	}
	sl, err := s.getSealer(ctx)
	if err != nil {
		return nil, err
	}
	return sl.seal(sealAAD(id), []byte(key))
}

func (s *service) openKey(ctx context.Context, r record) (string, error) {
	if len(r.Sealed) == 0 {
		return "", nil
	}
	sl, err := s.getSealer(ctx)
	if err != nil {
		return "", err
	}
	plain, err := sl.open(sealAAD(r.ID), r.Sealed)
	if err != nil {
		return "", errors.New("connections: the stored key cannot be opened")
	}
	return string(plain), nil
}

// --- Test -----------------------------------------------------------------

func resultOf(err error) (TestResult, bool) {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return TestResult{Step: ue.Step, Message: ue.Message}, true
	}
	return TestResult{}, false
}

func (s *service) Test(ctx context.Context, kind Kind, url, apiKey string) (TestResult, error) {
	t, err := parseTarget(kind, url, strings.TrimSpace(apiKey))
	if err != nil {
		if res, ok := resultOf(err); ok {
			return res, nil
		}
		return TestResult{}, err
	}
	models, err := s.fetch.list(ctx, t)
	if err != nil {
		if res, ok := resultOf(err); ok {
			return res, nil
		}
		return TestResult{}, err
	}
	return TestResult{OK: true, Models: models}, nil
}

// --- reads ----------------------------------------------------------------

func (s *service) List(ctx context.Context) ([]Connection, error) {
	recs, err := s.o.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Connection, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.view())
	}
	return out, nil
}

func (s *service) Get(ctx context.Context, id string) (Connection, error) {
	r, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	return r.view(), nil
}

func (s *service) ResolveKey(ctx context.Context, id string) (string, error) {
	r, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return s.openKey(ctx, r)
}

// --- Create ---------------------------------------------------------------

func (s *service) Create(ctx context.Context, r CreateRequest) (Result, error) {
	if !r.Kind.valid() {
		return Result{}, invalid("The kind must be localai, openai or anthropic.")
	}
	name, err := cleanName(r.Name)
	if err != nil {
		return Result{}, err
	}
	rawURL, err := cleanURL(r.Kind, r.URL)
	if err != nil {
		return Result{}, err
	}
	key, err := cleanKey(r.Kind, r.APIKey)
	if err != nil {
		return Result{}, err
	}
	id := s.newID()
	sealed, err := s.sealKey(ctx, id, key)
	if err != nil {
		return Result{}, err
	}
	rec := record{ID: id, Name: name, Kind: r.Kind, URL: rawURL, Status: StatusNever, Created: s.now(), Sealed: sealed}
	if err := s.o.Store.Create(ctx, rec); err != nil {
		if errors.Is(err, ErrConflict) {
			return Result{}, fmt.Errorf("%w: %q", ErrConflict, name)
		}
		return Result{}, err
	}
	res := Result{Created: []string{}, Skipped: []Skip{}}
	if len(r.Models) > 0 {
		res, err = s.adopt(ctx, id, r.Models)
		if err != nil {
			return res, err
		}
	}
	// One refresh records the status and the models not adopted. A remote
	// that fails is recorded, not returned: the connection exists.
	conn, err := s.Refresh(ctx, id)
	if err != nil {
		return res, err
	}
	res.Connection = conn
	return res, nil
}

// --- Adopt ----------------------------------------------------------------

func (s *service) Adopt(ctx context.Context, id string, models []UpstreamModel) (Result, error) {
	if len(models) == 0 {
		return Result{}, invalid("Pick at least one model.")
	}
	res, err := s.adopt(ctx, id, models)
	if err != nil {
		return res, err
	}
	conn, err := s.Get(ctx, id)
	if err != nil {
		return res, err
	}
	res.Connection = conn
	return res, nil
}

// takenNames returns a function that says whether a model name is in use: as
// a model config here, or as an entry of any connection.
func (s *service) takenNames(ctx context.Context) (func(string) bool, error) {
	all, err := s.o.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, r := range all {
		for _, o := range r.Owned {
			owned[o.Name] = true
		}
	}
	return func(name string) bool {
		if owned[name] {
			return true
		}
		_, ok := s.o.Configs.GetModelConfig(name)
		return ok
	}, nil
}

func (s *service) adopt(ctx context.Context, id string, models []UpstreamModel) (Result, error) {
	rec, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Result{}, err
	}
	t, err := parseTarget(rec.Kind, rec.URL, "")
	if err != nil {
		return Result{}, invalid("The address of the connection is not valid. Change it first.")
	}
	taken, err := s.takenNames(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{Created: []string{}, Skipped: []Skip{}}
	slug := connectionSlug(rec.Name)
	adopted := map[string]bool{}
	for _, o := range rec.Owned {
		adopted[o.UpstreamID] = true
	}
	batch := map[string]bool{}
	for _, m := range models {
		if m.ID == "" || batch[m.ID] {
			if m.ID != "" {
				res.Skipped = append(res.Skipped, Skip{ID: m.ID, Reason: "listed twice"})
			}
			continue
		}
		batch[m.ID] = true
		if adopted[m.ID] {
			res.Skipped = append(res.Skipped, Skip{ID: m.ID, Reason: "already adopted"})
			continue
		}
		name, ok := chooseEntryName(m.ID, slug, taken)
		if !ok {
			res.Skipped = append(res.Skipped, Skip{ID: m.ID, Reason: "no free model name"})
			continue
		}
		body, err := buildEntry(t, name, id, m)
		if err != nil {
			res.Skipped = append(res.Skipped, Skip{ID: m.ID, Reason: "could not build the entry"})
			continue
		}
		created, err := s.o.Admin.CreateConfig(ctx, body)
		if err != nil {
			res.Skipped = append(res.Skipped, Skip{ID: m.ID, Reason: skipReason(err)})
			if !errors.Is(err, modeladmin.ErrConflict) && !errors.Is(err, modeladmin.ErrInvalidConfig) {
				xlog.Warn("connections: could not write an entry", "connection", id, "model", name, "error", err)
			}
			continue
		}
		s.broadcast(name, "install", created.ConfigRevision)
		entry := ownedEntry{Name: name, UpstreamID: m.ID, Hash: hashBytes(body)}
		if _, err := s.o.Store.Update(ctx, id, func(r *record) error {
			r.Owned = append(r.Owned, entry)
			r.New = slices.DeleteFunc(r.New, func(n UpstreamModel) bool { return n.ID == m.ID })
			return nil
		}); err != nil {
			return res, fmt.Errorf("connections: the entry %q was written but could not be recorded: %w", name, err)
		}
		res.Created = append(res.Created, name)
		adopted[m.ID] = true
		// The name is used now, in this batch and for the next.
		taken = withName(taken, name)
	}
	return res, nil
}

func withName(taken func(string) bool, name string) func(string) bool {
	return func(n string) bool { return n == name || taken(n) }
}

func skipReason(err error) string {
	switch {
	case errors.Is(err, modeladmin.ErrConflict):
		return "a model with that name exists"
	case errors.Is(err, modeladmin.ErrInvalidConfig), errors.Is(err, modeladmin.ErrPathSeparator), errors.Is(err, modeladmin.ErrPathNotTrusted):
		return "the model name or settings are not valid"
	default:
		return "could not write the entry"
	}
}

func (s *service) broadcast(name, op, revision string) {
	if s.o.Broadcaster != nil {
		s.o.Broadcaster.BroadcastModelsChangedRevision(name, op, revision)
	}
}

func (s *service) unload(names ...string) {
	if s.o.Unloader == nil {
		return
	}
	for _, n := range names {
		if err := s.o.Unloader.ShutdownModel(n); err != nil {
			xlog.Debug("connections: model was not stopped", "model", n, "error", err)
		}
	}
}

// --- Refresh --------------------------------------------------------------

// entryGone says whether the model of an owned entry is neither loaded nor on
// disk, so the bookkeeping for it can go. A model whose connection was
// removed by hand is not gone, so a later delete does not touch it either.
func (s *service) entryGone(name string) bool {
	if s.o.ModelsPath == "" {
		return false
	}
	if _, ok := s.o.Configs.GetModelConfig(name); ok {
		return false
	}
	for _, ext := range []string{".yaml", ".yml"} {
		if _, err := os.Stat(filepath.Join(s.o.ModelsPath, name+ext)); err == nil {
			return false
		}
	}
	return true
}

func (s *service) Refresh(ctx context.Context, id string) (Connection, error) {
	rec, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	var (
		models  []UpstreamModel
		listErr error
	)
	key, kerr := s.openKey(ctx, rec)
	if kerr != nil {
		listErr = upstreamErr(StepAuth, "The stored key cannot be opened. Enter the key again.")
	} else if t, perr := parseTarget(rec.Kind, rec.URL, key); perr != nil {
		listErr = perr
	} else {
		models, listErr = s.fetch.list(ctx, t)
	}
	now := s.now()
	out, err := s.o.Store.Update(ctx, id, func(r *record) error {
		r.LastRefresh = &now
		var ue *upstreamError
		switch {
		case listErr == nil || (errors.As(listErr, &ue) && ue.Step == StepNoModels):
			// A server that lists nothing usable is reachable and accepts the
			// key; its entries all show as unavailable.
			r.Owned = slices.DeleteFunc(r.Owned, func(o ownedEntry) bool { return s.entryGone(o.Name) })
			d := diffModels(models, r.Owned)
			r.New, r.Unavailable = d.New, d.Unavailable
			r.Status, r.LastError = StatusOK, ""
			if ue != nil {
				r.LastError = ue.Message
			}
		case errors.As(listErr, &ue):
			r.Status, r.LastError = statusForStep(ue.Step), ue.Message
		default:
			r.Status, r.LastError = StatusUnreachable, "The server could not be asked."
		}
		return nil
	})
	if err != nil {
		return Connection{}, err
	}
	return out.view(), nil
}

// --- Update ---------------------------------------------------------------

func (s *service) Update(ctx context.Context, id string, req UpdateRequest) (Connection, error) {
	old, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	var (
		newName, newURL string
		newSealed       []byte
		keyChanged      bool
	)
	if req.Name != nil {
		if newName, err = cleanName(*req.Name); err != nil {
			return Connection{}, err
		}
	}
	if req.URL != nil {
		if newURL, err = cleanURL(old.Kind, *req.URL); err != nil {
			return Connection{}, err
		}
	}
	if req.APIKey != nil {
		key, err := cleanKey(old.Kind, *req.APIKey)
		if err != nil {
			return Connection{}, err
		}
		if newSealed, err = s.sealKey(ctx, id, key); err != nil {
			return Connection{}, err
		}
		keyChanged = true
	}
	urlChanged := req.URL != nil && newURL != old.URL

	out, err := s.o.Store.Update(ctx, id, func(r *record) error {
		if req.Name != nil {
			r.Name = newName
		}
		if urlChanged {
			r.URL = newURL
		}
		if keyChanged {
			r.Sealed = newSealed
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return Connection{}, fmt.Errorf("%w: %q", ErrConflict, newName)
		}
		return Connection{}, err
	}
	var errs []error
	if urlChanged {
		errs = append(errs, s.patchEntryURLs(ctx, old, out)...)
	}
	if keyChanged {
		// A loaded entry holds the old key in its process. The next request
		// loads it again and resolves the new one.
		s.unload(entryNames(out)...)
	}
	conn := out.view()
	if len(errs) > 0 {
		return conn, fmt.Errorf("connections: the connection was saved but some entries could not be updated: %w", errors.Join(errs...))
	}
	return conn, nil
}

func entryNames(r record) []string {
	names := make([]string, 0, len(r.Owned))
	for _, o := range r.Owned {
		names = append(names, o.Name)
	}
	return names
}

// patchEntryURLs sets proxy.upstream_url on the entries of a connection whose
// address changed. An entry whose value a person changed to something else is
// left alone.
func (s *service) patchEntryURLs(ctx context.Context, old, cur record) []error {
	oldT, err1 := parseTarget(old.Kind, old.URL, "")
	newT, err2 := parseTarget(cur.Kind, cur.URL, "")
	if err1 != nil || err2 != nil {
		return nil
	}
	oldURL, newURL := oldT.entryUpstreamURL(), newT.entryUpstreamURL()
	var errs []error
	for _, o := range cur.Owned {
		cfg, ok := s.o.Configs.GetModelConfig(o.Name)
		if !ok || cfg.Proxy.Connection != cur.ID || cfg.Proxy.UpstreamURL != oldURL {
			continue
		}
		unedited := s.unedited(ctx, o)
		res, err := s.o.Admin.PatchConfig(ctx, o.Name, map[string]any{"proxy": map[string]any{"upstream_url": newURL}})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", o.Name, err))
			continue
		}
		s.broadcast(o.Name, "install", res.ConfigRevision)
		if unedited {
			s.rehash(ctx, cur.ID, o.Name)
		}
	}
	return errs
}

// unedited says whether the entry file is still what the service wrote.
func (s *service) unedited(ctx context.Context, o ownedEntry) bool {
	v, err := s.o.Admin.GetConfig(ctx, o.Name)
	return err == nil && o.Hash != "" && hashBytes([]byte(v.YAML)) == o.Hash
}

// rehash records the current content of an entry file as the one that the
// service wrote, after the service changed it itself.
func (s *service) rehash(ctx context.Context, id, name string) {
	v, err := s.o.Admin.GetConfig(ctx, name)
	if err != nil {
		return
	}
	h := hashBytes([]byte(v.YAML))
	_, _ = s.o.Store.Update(ctx, id, func(r *record) error {
		for i := range r.Owned {
			if r.Owned[i].Name == name {
				r.Owned[i].Hash = h
			}
		}
		return nil
	})
}

// --- Delete ---------------------------------------------------------------

func (s *service) Delete(ctx context.Context, id string, removeEntries bool) error {
	rec, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	var errs []error
	for _, o := range rec.Owned {
		cfg, ok := s.o.Configs.GetModelConfig(o.Name)
		switch {
		case !ok:
			// Nothing to do for a model that is already gone.
		case cfg.Proxy.Connection != id:
			// A person detached it. It is theirs now.
		case removeEntries:
			res, err := s.o.Admin.DeleteConfig(ctx, o.Name)
			if err != nil && !errors.Is(err, modeladmin.ErrNotFound) {
				errs = append(errs, fmt.Errorf("%s: %w", o.Name, err))
				continue
			}
			if res != nil {
				s.broadcast(o.Name, "delete", res.ConfigRevision)
			}
			s.unload(o.Name)
		default:
			if err := s.detach(ctx, o.Name); err != nil && !errors.Is(err, modeladmin.ErrNotFound) {
				errs = append(errs, fmt.Errorf("%s: %w", o.Name, err))
				continue
			}
			s.unload(o.Name)
		}
		name := o.Name
		if _, err := s.o.Store.Update(ctx, id, func(r *record) error {
			r.Owned = slices.DeleteFunc(r.Owned, func(e ownedEntry) bool { return e.Name == name })
			return nil
		}); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		// The connection stays, so a retry can finish the rest.
		return fmt.Errorf("connections: not every entry could be handled: %w", errors.Join(errs...))
	}
	return s.o.Store.Delete(ctx, id)
}

// detach removes proxy.connection from an entry file. The key is not copied
// into it: the model then calls the remote without a key.
func (s *service) detach(ctx context.Context, name string) error {
	v, err := s.o.Admin.GetConfig(ctx, name)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(v.YAML), &doc); err != nil {
		return fmt.Errorf("read the entry: %w", err)
	}
	if proxy, ok := doc["proxy"].(map[string]any); ok {
		delete(proxy, "connection")
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	res, err := s.o.Admin.EditYAML(ctx, name, body)
	if err != nil {
		return err
	}
	s.broadcast(name, "install", res.ConfigRevision)
	return nil
}

// --- startup refresh ------------------------------------------------------

// RefreshAll refreshes every connection once and returns how many could not
// be refreshed because the store failed. A remote that fails is not counted:
// its status records it.
func RefreshAll(ctx context.Context, svc Service) (failed int) {
	all, err := svc.List(ctx)
	if err != nil {
		xlog.Warn("connections: could not list connections for the refresh", "error", err)
		return 1
	}
	for _, c := range all {
		if ctx.Err() != nil {
			return failed
		}
		if _, err := svc.Refresh(ctx, c.ID); err != nil && !errors.Is(err, ErrNotFound) {
			xlog.Warn("connections: refresh failed", "connection", c.Name, "error", err)
			failed++
		}
	}
	return failed
}
