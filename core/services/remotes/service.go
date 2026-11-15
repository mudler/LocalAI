package remotes

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
	GetAllModelsConfigs() []config.ModelConfig
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

	// locks serialises Adopt, Update and Delete of one remote in this
	// process. Frontends of a cluster share the models directory, and the
	// store's own read-change-write step keeps the record whole across
	// them.
	locks keyedMutex
}

// keyedMutex is a mutex per key. An entry lives only while someone holds or
// waits for it.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu sync.Mutex
	n  int
}

// lock takes the mutex of key and returns the function that releases it.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e := k.m[key]
	if e == nil {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.n++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		if e.n--; e.n == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

// New returns the Service. The service keeps no remote state in memory:
// every call reads the store, so every frontend of a cluster agrees.
func New(o Options) (Service, error) { return newService(o) }

func newService(o Options) (*service, error) {
	if o.Store == nil || o.Admin == nil || o.Configs == nil {
		return nil, errors.New("remotes: Store, Admin and Configs are required")
	}
	return &service{o: o, fetch: newFetcher(), now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString}, nil
}

// --- validation -----------------------------------------------------------

const maxRemoteNameLen = 80

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("Enter a name for the remote.")
	}
	if len([]rune(name)) > maxRemoteNameLen {
		return "", invalid(fmt.Sprintf("The name can have at most %d characters.", maxRemoteNameLen))
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", invalid("The name must not contain control characters.")
	}
	return name, nil
}

func errBadKind() error { return invalid("The kind must be localai, openai or anthropic.") }

func cleanKey(kind Kind, key string) (string, error) {
	key = strings.TrimSpace(key)
	// An HTTP header takes printable ASCII. Anything else would be saved
	// and then fail on every call with a message that hides the cause.
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return "", invalid("The key can hold only printable ASCII characters, with no spaces or line breaks.")
		}
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
		return "", invalid(err.Error())
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
		return nil, fmt.Errorf("remotes: sealing key: %w", err)
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
		return "", errors.New("remotes: the stored key cannot be opened")
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
	models, total, err := s.fetch.list(ctx, t)
	if err != nil {
		if res, ok := resultOf(err); ok {
			return res, nil
		}
		return TestResult{}, err
	}
	res := TestResult{OK: true, Models: models}
	if total > len(models) {
		res.Message = truncatedNotice(total)
	}
	return res, nil
}

func truncatedNotice(total int) string {
	return fmt.Sprintf("The server lists %d models. Only the first %d are shown.", total, MaxListedModels)
}

// --- reads ----------------------------------------------------------------

func (s *service) List(ctx context.Context) ([]Remote, error) {
	recs, err := s.o.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Remote, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.view())
	}
	return out, nil
}

func (s *service) Get(ctx context.Context, id string) (Remote, error) {
	r, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Remote{}, err
	}
	return r.view(), nil
}

// ResolveKey returns the key of a remote together with the address its
// entries are built from. The address comes from the same derivation as the
// proxy.upstream_url of a generated entry, so the address check of a model
// load always accepts a generated entry.
func (s *service) ResolveKey(ctx context.Context, id string) (config.ProxyRemoteKey, error) {
	r, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return config.ProxyRemoteKey{}, err
	}
	t, err := parseTarget(r.Kind, r.URL, "")
	if err != nil {
		return config.ProxyRemoteKey{}, errors.New("remotes: the address of the remote is not valid")
	}
	key, err := s.openKey(ctx, r)
	if err != nil {
		return config.ProxyRemoteKey{}, err
	}
	return config.ProxyRemoteKey{Key: key, Kind: string(r.Kind), Base: t.base.String()}, nil
}

// --- Create ---------------------------------------------------------------

func (s *service) Create(ctx context.Context, r CreateRequest) (Result, error) {
	if !r.Kind.valid() {
		return Result{}, errBadKind()
	}
	if len(r.Models) > MaxAdoptModels {
		return Result{}, tooManyModels()
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
	// From here the remote exists: any failure is partial, so the caller
	// does not retry into a name conflict.
	unlock := s.locks.lock(id)
	defer unlock()
	res := Result{Created: []string{}, Skipped: []Skip{}}
	if len(r.Models) > 0 {
		if res, err = s.adopt(ctx, id, r.Models); err != nil {
			return s.partial(ctx, id, res, err)
		}
	}
	// One refresh records the status and the models not adopted. A remote
	// that fails is recorded, not returned: the remote exists.
	conn, err := s.Refresh(ctx, id)
	if err != nil {
		return s.partial(ctx, id, res, err)
	}
	res.Remote = conn
	return res, nil
}

// partial returns res with the current view of the remote and err marked as
// ErrPartial.
func (s *service) partial(ctx context.Context, id string, res Result, err error) (Result, error) {
	if res.Created == nil {
		res.Created = []string{}
	}
	if res.Skipped == nil {
		res.Skipped = []Skip{}
	}
	if conn, gerr := s.Get(ctx, id); gerr == nil {
		res.Remote = conn
	}
	if errors.Is(err, ErrPartial) {
		return res, err
	}
	return res, fmt.Errorf("%w: %w", ErrPartial, err)
}

func tooManyModels() error {
	return invalid(fmt.Sprintf("Pick at most %d models at a time.", MaxAdoptModels))
}

// --- Adopt ----------------------------------------------------------------

func (s *service) Adopt(ctx context.Context, id string, models []UpstreamModel) (Result, error) {
	if len(models) == 0 {
		return Result{}, invalid("Pick at least one model.")
	}
	if len(models) > MaxAdoptModels {
		return Result{}, tooManyModels()
	}
	unlock := s.locks.lock(id)
	defer unlock()
	res, err := s.adopt(ctx, id, models)
	if err != nil {
		if errors.Is(err, ErrPartial) {
			return s.partial(ctx, id, res, err)
		}
		return res, err
	}
	conn, err := s.Get(ctx, id)
	if err != nil {
		return res, err
	}
	res.Remote = conn
	return res, nil
}

// takenNames returns the model names that the entries of every remote
// use. A name is also taken when a model config of that name exists, which
// the caller checks.
func (s *service) takenNames(ctx context.Context) (map[string]bool, error) {
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
	return owned, nil
}

func (s *service) adopt(ctx context.Context, id string, models []UpstreamModel) (Result, error) {
	rec, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Result{}, err
	}
	t, err := parseTarget(rec.Kind, rec.URL, "")
	if err != nil {
		return Result{}, invalid("The address of the remote is not valid. Change it first.")
	}
	owned, err := s.takenNames(ctx)
	if err != nil {
		return Result{}, err
	}
	taken := func(name string) bool {
		if owned[name] {
			return true
		}
		_, ok := s.o.Configs.GetModelConfig(name)
		return ok
	}
	res := Result{Created: []string{}, Skipped: []Skip{}}
	slug := remoteSlug(rec.Name)
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
				xlog.Warn("remotes: could not write an entry", "remote", id, "model", name, "error", err)
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
			return res, fmt.Errorf("%w: the entry %q was written but could not be recorded: %w", ErrPartial, name, err)
		}
		res.Created = append(res.Created, name)
		adopted[m.ID] = true
		// The name is used now, in this batch and for the next.
		owned[name] = true
	}
	return res, nil
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
			xlog.Debug("remotes: model was not stopped", "model", n, "error", err)
		}
	}
}

// --- Refresh --------------------------------------------------------------

// entryGone says whether the model of an owned entry is neither loaded nor on
// disk, so the bookkeeping for it can go. A model whose remote was
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

func (s *service) Refresh(ctx context.Context, id string) (Remote, error) {
	rec, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Remote{}, err
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
		var total int
		models, total, listErr = s.fetch.list(ctx, t)
		if total > len(models) {
			xlog.Warn("remotes: the listing was cut", "remote", id, "listed", total, "kept", len(models))
		}
	}
	// Which entries are gone is decided before the store step: it reads the
	// disk and the loader, which must not happen under a row lock.
	gone := map[string]bool{}
	for _, o := range rec.Owned {
		if s.entryGone(o.Name) {
			gone[o.Name] = true
		}
	}
	now := s.now()
	out, err := s.o.Store.Update(ctx, id, func(r *record) error {
		r.LastRefresh = &now
		var ue *upstreamError
		switch {
		case listErr == nil || (errors.As(listErr, &ue) && ue.Step == StepNoModels):
			// A server that lists nothing usable is reachable and accepts the
			// key; its entries all show as unavailable.
			r.Owned = slices.DeleteFunc(r.Owned, func(o ownedEntry) bool { return gone[o.Name] })
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
		return Remote{}, err
	}
	return out.view(), nil
}

// --- Update ---------------------------------------------------------------

func (s *service) Update(ctx context.Context, id string, req UpdateRequest) (Remote, error) {
	unlock := s.locks.lock(id)
	defer unlock()
	old, err := s.o.Store.Get(ctx, id)
	if err != nil {
		return Remote{}, err
	}
	var (
		newName, newURL string
		newSealed       []byte
		keyChanged      bool
	)
	if req.Name != nil {
		if newName, err = cleanName(*req.Name); err != nil {
			return Remote{}, err
		}
	}
	if req.URL != nil {
		if newURL, err = cleanURL(old.Kind, *req.URL); err != nil {
			return Remote{}, err
		}
	}
	if req.APIKey != nil {
		key, err := cleanKey(old.Kind, *req.APIKey)
		if err != nil {
			return Remote{}, err
		}
		if newSealed, err = s.sealKey(ctx, id, key); err != nil {
			return Remote{}, err
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
			return Remote{}, fmt.Errorf("%w: %q", ErrConflict, newName)
		}
		return Remote{}, err
	}
	var errs []error
	if urlChanged {
		errs = append(errs, s.patchEntryURLs(ctx, old, out)...)
	}
	if keyChanged {
		// A loaded model holds the old key in its process. The next request
		// loads it again and resolves the new one.
		s.unload(s.modelsOf(out)...)
	}
	conn := out.view()
	if keyChanged || urlChanged {
		// The status, the last error and the lists belong to the old key or
		// address; ask the remote again so the answer is current.
		if fresh, rerr := s.Refresh(ctx, id); rerr == nil {
			conn = fresh
		} else {
			xlog.Warn("remotes: refresh after a change failed", "remote", id, "error", rerr)
		}
	}
	if len(errs) > 0 {
		return conn, fmt.Errorf("%w: the remote was saved but some entries could not be updated: %w", ErrPartial, errors.Join(errs...))
	}
	return conn, nil
}

// modelsOf returns the names of every model that uses the key of the
// remote: the entries it recorded and any other model config that names it
// in proxy.remote (a renamed entry or a copy made by hand).
func (s *service) modelsOf(r record) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(r.Owned))
	for _, o := range r.Owned {
		if !seen[o.Name] {
			seen[o.Name] = true
			names = append(names, o.Name)
		}
	}
	for _, cfg := range s.o.Configs.GetAllModelsConfigs() {
		if cfg.Proxy.Remote == r.ID && !seen[cfg.Name] {
			seen[cfg.Name] = true
			names = append(names, cfg.Name)
		}
	}
	return names
}

// patchEntryURLs sets proxy.upstream_url on the entries of a remote whose
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
		if !ok || cfg.Proxy.Remote != cur.ID || cfg.Proxy.UpstreamURL != oldURL {
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
	unlock := s.locks.lock(id)
	defer unlock()
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
		case cfg.Proxy.Remote != id:
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
		// The remote stays, so a retry can finish the rest.
		return fmt.Errorf("%w: not every entry could be handled: %w", ErrPartial, errors.Join(errs...))
	}
	// Another frontend may have adopted a model meanwhile. The record goes
	// only if it owns nothing, checked in the same store step, so no entry
	// is left naming a deleted remote.
	err = s.o.Store.Delete(ctx, id, func(r record) error {
		if len(r.Owned) > 0 {
			return fmt.Errorf("%w: the remote got new models while it was being removed; remove it again", ErrPartial)
		}
		return nil
	})
	return err
}

// detach clears proxy.remote in an entry file. The key is not copied into
// it: the model then calls the server without a key. PatchConfig runs under
// the model config lock, so an edit made at the same time is not lost. The
// file keeps `remote: ""`, which the loader reads as no remote.
func (s *service) detach(ctx context.Context, name string) error {
	res, err := s.o.Admin.PatchConfig(ctx, name, map[string]any{"proxy": map[string]any{"remote": ""}})
	if err != nil {
		return err
	}
	s.broadcast(name, "install", res.ConfigRevision)
	return nil
}

// --- startup refresh ------------------------------------------------------

// refreshAll refreshes every remote once. A remote that fails is recorded
// in its status; a store failure is logged.
func refreshAll(ctx context.Context, svc Service) {
	all, err := svc.List(ctx)
	if err != nil {
		xlog.Warn("remotes: could not list remotes for the refresh", "error", err)
		return
	}
	for _, c := range all {
		if ctx.Err() != nil {
			return
		}
		if _, err := svc.Refresh(ctx, c.ID); err != nil && !errors.Is(err, ErrNotFound) {
			xlog.Warn("remotes: refresh failed", "remote", c.Name, "error", err)
		}
	}
}
