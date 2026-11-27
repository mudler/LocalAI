package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// MyState is the caller's own state of one install (spec 12).
type MyState struct {
	InstallID     string          `json:"install"`
	Enabled       bool            `json:"enabled"`
	NeedsReenable bool            `json:"needsReenable"`
	Config        json.RawMessage `json:"config,omitempty" swaggertype:"object"`
	// SecretsSet names the sensitive options that have a value. Empty until
	// sealed secrets exist (spec 12.4).
	SecretsSet []string `json:"secretsSet"`
}

// SetMyStateRequest changes the caller's state of an install. Nil fields
// stay as they are.
type SetMyStateRequest struct {
	Enabled *bool
	// Reenable confirms turning on an install that gained code.
	Reenable bool
	// Config replaces the non-sensitive userConfig values.
	Config json.RawMessage
	// Secrets are sensitive userConfig values. Not supported yet.
	Secrets map[string]string
}

// MyState returns the caller's state of an install. An install the caller
// never chose anything for is off.
func (s *Service) MyState(ctx context.Context, a Actor, id string) (MyState, error) {
	if _, err := s.visibleInstall(ctx, a, id); err != nil {
		return MyState{}, err
	}
	us, err := s.st.UserState(ctx, a.ID, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return MyState{}, err
	}
	return myState(id, us), nil
}

func myState(id string, us store.UserState) MyState {
	return MyState{InstallID: id, Enabled: us.Enabled, NeedsReenable: us.NeedsReenable, Config: us.Config, SecretsSet: []string{}}
}

// SetMyState changes the caller's state of an install (spec 12.1): the
// switch through SetEnabled or, with Reenable, through Reenable, and the
// settings. Turning off keeps the settings. An administrator keeps state
// only for the installs visible to them as a user: the instance installs
// and their own.
func (s *Service) SetMyState(ctx context.Context, a Actor, id string, req SetMyStateRequest) (MyState, error) {
	in, err := s.visibleInstall(ctx, a, id)
	if err != nil {
		return MyState{}, err
	}
	if len(req.Secrets) > 0 {
		return MyState{}, fmt.Errorf("%w: secret settings need a later version of LocalAI", ErrUnsupported)
	}
	var cfg json.RawMessage
	if req.Config != nil {
		if cfg, err = s.checkConfig(ctx, in, req.Config); err != nil {
			return MyState{}, err
		}
	}
	if req.Enabled != nil {
		switch {
		case *req.Enabled && req.Reenable:
			err = s.Reenable(ctx, a, id)
		default:
			err = s.SetEnabled(ctx, a, id, *req.Enabled)
		}
		if err != nil {
			return MyState{}, err
		}
	}
	if req.Config != nil {
		us, err := s.st.UserState(ctx, a.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			us, err = store.UserState{Principal: a.ID, InstallID: id}, nil
		}
		if err != nil {
			return MyState{}, err
		}
		us.Config, us.UpdatedAt = cfg, s.now()
		if err := s.st.SaveUserState(ctx, us); err != nil {
			return MyState{}, err
		}
	}
	return s.MyState(ctx, a, id)
}

// checkConfig accepts a JSON object whose keys are non-sensitive userConfig
// options of the install's plugin. null clears the values.
func (s *Service) checkConfig(ctx context.Context, in store.Install, raw json.RawMessage) (json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, fmt.Errorf("%w: config must be a JSON object", ErrInvalid)
	}
	pkg, err := s.st.Package(ctx, in.Digest)
	if err != nil {
		return nil, err
	}
	var m Model
	if err := json.Unmarshal(pkg.Model, &m); err != nil {
		return nil, err
	}
	opts := make(map[string]OptionRef, len(m.UserConfig))
	for _, o := range m.UserConfig {
		opts[o.Key] = o
	}
	for k := range values {
		o, ok := opts[k]
		switch {
		case !ok:
			return nil, fmt.Errorf("%w: the plugin has no setting %q", ErrInvalid, k)
		case o.Sensitive:
			return nil, fmt.Errorf("%w: %q is a secret setting; secret settings need a later version of LocalAI", ErrUnsupported, k)
		}
	}
	return json.Marshal(values)
}
