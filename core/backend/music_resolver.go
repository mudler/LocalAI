// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"errors"
	"math"
	"reflect"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/music"
)

// MusicModelRequest deliberately carries no principal, selection or readiness
// flags. Parser validation and authenticated policy binding are separate duties.
type MusicModelRequest struct {
	Operation                           music.Operation
	TranscriptionModel, GenerationModel string
}

// MusicPolicySession is a request-bound authenticated policy capability, not a
// principal-name lookup. Implementations must return errors on unavailable policy
// data and preserve existing model visibility and quota semantics. Authorize
// methods check policy only: they must not reserve lanes or execute inference.
type MusicPolicySession interface {
	Principal() string
	AuthorizeMusic(context.Context, music.Operation) error
	AuthorizeMusicQuota(context.Context, music.Operation) error
	// AuthorizeMusicModel checks canonical model access AND model-scoped quota;
	// AuthorizeMusicQuota checks global rules. Both are read-only gates, not
	// reservations or usage accounting.
	AuthorizeMusicModel(context.Context, string) error
}

// BindMusicPrincipal must authenticate from trusted application context; it must
// not treat a supplied principal string or request ID as authentication.
type MusicPolicyBinder interface {
	BindMusicPrincipal(context.Context) (MusicPolicySession, error)
}

// Config is a detached public configuration snapshot. Revision is the loader's
// persisted revision, kept separately because JSON snapshots omit private fields.
// Neither the backend declaration nor this revision proves loaded capability.
type MusicConfigSnapshot struct {
	Config   config.ModelConfig
	Revision string
}
type MusicPolicyResolution struct {
	Policy                    MusicPolicySession
	Transcription, Generation *MusicConfigSnapshot
}

// MusicCanonicalPreflight intentionally does NOT implement MusicResolver. It
// establishes only canonical configuration and authenticated policy. A future
// preparation adapter must validate exact owned recording/media, runtime loaded
// identity/task/inline-ABC/context, and private storage before constructing any
// music.Selection or setting readiness flags. No such authority is issued here.
type MusicCanonicalPreflight struct {
	loader *config.ModelConfigLoader
	binder MusicPolicyBinder
}

func musicDependencyNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return r.IsNil()
	}
	return false
}
func NewMusicCanonicalPreflight(loader *config.ModelConfigLoader, binder MusicPolicyBinder) (*MusicCanonicalPreflight, error) {
	if loader == nil || musicDependencyNil(binder) {
		return nil, musicPolicyError{503, "service_unavailable"}
	}
	return &MusicCanonicalPreflight{loader, binder}, nil
}

// MusicPolicyError is intentionally sanitized and implements the structural
// public-error interface without importing HTTP endpoints into backend.
type musicPolicyError struct {
	status int
	reason string
}

func (e musicPolicyError) Error() string       { return "Music canonical-policy preflight failed." }
func (e musicPolicyError) HTTPStatus() int     { return e.status }
func (e musicPolicyError) MusicStage() string  { return "validation" }
func (e musicPolicyError) MusicReason() string { return e.reason }
func policyFailure(err error, status int, reason string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// Preserve only recognized typed policy decisions, never arbitrary diagnostics.
	var p interface {
		error
		HTTPStatus() int
		MusicStage() string
		MusicReason() string
	}
	if errors.As(err, &p) {
		switch p.HTTPStatus() {
		case 401, 403, 404, 429, 503:
			return musicPolicyError{p.HTTPStatus(), map[int]string{401: "authentication_required", 403: "access_denied", 404: "model_not_found", 429: "capacity_unavailable", 503: "service_unavailable"}[p.HTTPStatus()]}
		}
	}
	return musicPolicyError{status, reason}
}
func (p *MusicCanonicalPreflight) Resolve(ctx context.Context, in MusicModelRequest) (MusicPolicyResolution, error) {
	zero := MusicPolicyResolution{}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if p == nil || p.loader == nil || musicDependencyNil(p.binder) {
		return zero, musicPolicyError{503, "service_unavailable"}
	}
	switch in.Operation {
	case music.Transcription:
		if in.TranscriptionModel == "" || in.GenerationModel != "" {
			return zero, musicPolicyError{422, "invalid_input"}
		}
	case music.Generation:
		if in.GenerationModel == "" || in.TranscriptionModel != "" {
			return zero, musicPolicyError{422, "invalid_input"}
		}
	case music.Cover:
		if in.TranscriptionModel == "" || in.GenerationModel == "" {
			return zero, musicPolicyError{422, "invalid_input"}
		}
	default:
		return zero, musicPolicyError{422, "invalid_input"}
	}
	session, err := p.binder.BindMusicPrincipal(ctx)
	if err != nil {
		return zero, policyFailure(err, 401, "authentication_required")
	}
	if musicDependencyNil(session) || session.Principal() == "" {
		return zero, musicPolicyError{401, "authentication_required"}
	}
	if err = session.AuthorizeMusic(ctx, in.Operation); err != nil {
		return zero, policyFailure(err, 403, "access_denied")
	}
	if err = session.AuthorizeMusicQuota(ctx, in.Operation); err != nil {
		return zero, policyFailure(err, 403, "access_denied")
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	// Capture one loader map view, then resolve aliases against that view. Deep
	// detached selected public configs prevent callers/profile overlays mutating
	// loader data. Unrelated configs are never serialized. As with the loader
	// itself, published nested config data must not be mutated in place concurrently.
	all := p.loader.GetAllModelsConfigs()
	byName := make(map[string]config.ModelConfig, len(all))
	for _, cfg := range all {
		byName[cfg.Name] = cfg
	}
	wanted := map[string]bool{}
	for _, name := range []string{in.TranscriptionModel, in.GenerationModel} {
		if name != "" {
			wanted[name] = true
			if cfg, ok := byName[name]; ok && cfg.IsAlias() {
				wanted[cfg.Alias] = true
			}
		}
	}
	revisions := map[string]string{}
	configs := make([]config.ModelConfig, 0, len(wanted))
	for name := range wanted {
		original, ok := byName[name]
		if !ok {
			continue
		}
		revisions[name] = original.PersistedConfigRevision()
		cloned, e := cloneMusicConfig(original)
		if e != nil {
			return zero, musicPolicyError{500, "internal_error"}
		}
		configs = append(configs, cloned)
	}
	snapshotLoader := config.NewModelConfigLoader("")
	snapshotLoader.ReplaceModelConfigs(configs)
	resolve := func(name string) (*MusicConfigSnapshot, error) {
		requested, ok := snapshotLoader.GetModelConfig(name)
		if !ok {
			return nil, musicPolicyError{404, "model_not_found"}
		}
		if requested.IsDisabled() {
			return nil, musicPolicyError{422, "unsupported_capability"}
		}
		resolved, _, e := snapshotLoader.ResolveAlias(&requested)
		if e != nil || resolved == nil {
			return nil, musicPolicyError{422, "unsupported_capability"}
		}
		if resolved.IsDisabled() || resolved.Failover != nil || !reflect.DeepEqual(resolved.Pipeline, config.Pipeline{}) || resolved.Backend == "" {
			return nil, musicPolicyError{422, "unsupported_capability"}
		}
		return &MusicConfigSnapshot{Config: *resolved, Revision: revisions[resolved.Name]}, nil
	}
	out := MusicPolicyResolution{Policy: session}
	if in.Operation != music.Generation {
		out.Transcription, err = resolve(in.TranscriptionModel)
		if err != nil {
			return zero, err
		}
	}
	if in.Operation != music.Transcription {
		out.Generation, err = resolve(in.GenerationModel)
		if err != nil {
			return zero, err
		}
	}
	for _, model := range []*MusicConfigSnapshot{out.Transcription, out.Generation} {
		if model != nil {
			if err = ctx.Err(); err != nil {
				return zero, err
			}
			if err = session.AuthorizeMusicModel(ctx, model.Config.Name); err != nil {
				return zero, policyFailure(err, 403, "access_denied")
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	return out, nil
}

// cloneMusicConfig copies configuration data without serialization: interface
// values retain exact numeric types and precision. Only loader-private config
// fields are omitted (the persisted revision is carried separately). Runtime
// objects, cycles and excessive work fail closed before entering alias overlays.
func cloneMusicConfig(in config.ModelConfig) (config.ModelConfig, error) {
	c := musicConfigCloner{active: map[musicCloneVisit]bool{}}
	v, e := c.copy(reflect.ValueOf(in), 0)
	if e != nil {
		return config.ModelConfig{}, e
	}
	return v.Interface().(config.ModelConfig), nil
}

type musicCloneVisit struct {
	typ reflect.Type
	ptr uintptr
}
type musicConfigCloner struct {
	active       map[musicCloneVisit]bool
	nodes, bytes int
	allocated    uint64
}

func (c *musicConfigCloner) copy(v reflect.Value, depth int) (reflect.Value, error) {
	bad := func() (reflect.Value, error) {
		return reflect.Value{}, errors.New("unsupported music configuration snapshot")
	}
	c.nodes++
	if depth > 64 || c.nodes > 100000 {
		return bad()
	}
	t := v.Type()
	// Charge BEFORE allocating any value of a potentially large concrete type.
	// Division-based checks avoid overflow in container length*element size.
	if v.Kind() == reflect.Array || v.Kind() == reflect.Slice || v.Kind() == reflect.Map {
		if v.Len() > 100000-c.nodes {
			return bad()
		}
	}
	if !c.charge(1, uint64(t.Size())) {
		return bad()
	}
	out := reflect.New(t).Elem()
	switch v.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		out.Set(v)
	case reflect.Float32, reflect.Float64:
		if math.IsInf(v.Float(), 0) || math.IsNaN(v.Float()) {
			return bad()
		}
		out.Set(v)
	case reflect.String:
		c.bytes += v.Len()
		if c.bytes > 16<<20 {
			return bad()
		}
		out.Set(v)
	case reflect.Interface:
		if !v.IsNil() {
			child, e := c.copy(v.Elem(), depth+1)
			if e != nil {
				return reflect.Value{}, e
			}
			out.Set(child)
		}
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if v.IsNil() {
			return out, nil
		}
		var ptr uintptr
		if v.Kind() == reflect.Map {
			ptr = uintptr(v.UnsafePointer())
		} else {
			ptr = v.Pointer()
		}
		visit := musicCloneVisit{t, ptr}
		if c.active[visit] {
			return bad()
		}
		c.active[visit] = true
		defer delete(c.active, visit)
		if v.Kind() == reflect.Pointer {
			child, e := c.copy(v.Elem(), depth+1)
			if e != nil {
				return reflect.Value{}, e
			}
			if !c.charge(1, uint64(t.Elem().Size())) {
				return bad()
			}
			out = reflect.New(t.Elem())
			out.Elem().Set(child)
			if out.Type() != t {
				if !out.Type().ConvertibleTo(t) {
					return bad()
				}
				out = out.Convert(t)
			}
			return out, nil
		}
		if v.Len() > 100000-c.nodes {
			return bad()
		}
		if v.Kind() == reflect.Slice {
			if !c.charge(uint64(v.Len()), uint64(t.Elem().Size())) {
				return bad()
			}
			out = reflect.MakeSlice(t, v.Len(), v.Len())
			for i := 0; i < v.Len(); i++ {
				child, e := c.copy(v.Index(i), depth+1)
				if e != nil {
					return reflect.Value{}, e
				}
				out.Index(i).Set(child)
			}
		} else {
			switch t.Key().Kind() {
			case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			default:
				return bad()
			}
			// Reserve conservative per-entry bucket/control overhead in addition to
			// key/value slots; this is a clone allocation budget, not a heap RSS limit.
			if !c.charge(1, 256) || !c.charge(uint64(v.Len()), 128) || !c.charge(uint64(v.Len()), uint64(t.Key().Size())) || !c.charge(uint64(v.Len()), uint64(t.Elem().Size())) {
				return bad()
			}
			out = reflect.MakeMapWithSize(t, v.Len())
			it := v.MapRange()
			for it.Next() {
				key, e := c.copy(it.Key(), depth+1)
				if e != nil {
					return reflect.Value{}, e
				}
				child, e := c.copy(it.Value(), depth+1)
				if e != nil {
					return reflect.Value{}, e
				}
				out.SetMapIndex(key, child)
			}
		}
	case reflect.Array:
		if v.Len() > 100000-c.nodes {
			return bad()
		}
		for i := 0; i < v.Len(); i++ {
			child, e := c.copy(v.Index(i), depth+1)
			if e != nil {
				return reflect.Value{}, e
			}
			out.Index(i).Set(child)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				if t.PkgPath() != "github.com/mudler/LocalAI/core/config" {
					return bad()
				}
				continue
			}
			child, e := c.copy(v.Field(i), depth+1)
			if e != nil {
				return reflect.Value{}, e
			}
			out.Field(i).Set(child)
		}
	default:
		return bad()
	}
	return out, nil
}

const musicCloneAllocationBudget uint64 = 16 << 20

// charge rejects both overflow and cumulative over-budget reservations before
// reflect allocation. Scalar temporaries and repeated shared values count too.
func (c *musicConfigCloner) charge(count, size uint64) bool {
	if c.allocated > musicCloneAllocationBudget {
		return false
	}
	remaining := musicCloneAllocationBudget - c.allocated
	if size != 0 && count > remaining/size {
		return false
	}
	c.allocated += count * size
	return true
}
