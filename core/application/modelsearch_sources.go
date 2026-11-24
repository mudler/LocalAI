package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// modelSearchKey is the key of the list of sources a person turned off, among
// the host settings of a person: the same store as the other provider slots.
// Every source is on until it is in this list. The first version of the feature
// stored the enabled set under "provider-model-search"; that key is not read.
const modelSearchKey = "provider-" + contract.SlotModelSearch + "-off"

// ModelSearch returns the search host of the process: the shared Searcher, the
// add-on sources and the enabled set of each person. The REST route and the
// assistant tool use this one value, so they share one cache and one choice.
func (a *Application) ModelSearch() *modelsearch.Host {
	a.modelSearchOnce.Do(func() {
		a.modelSearchHost = modelsearch.NewHost(modelsearch.Shared(), modelSearchBackend{broker: a.StudioAddonTools}, modelSearchKV{a: a})
	})
	return a.modelSearchHost
}

// modelSearchBackend gives the search the add-ons through the chats broker, the
// one place that decides which add-ons a person can see and which host
// features they need.
type modelSearchBackend struct {
	// broker returns the chats broker at each call: it is built after the host.
	broker func() studio.AddonTools
}

func (b modelSearchBackend) Sources(ctx context.Context, user string) ([]modelsearch.AddonSource, error) {
	lister, ok := b.broker().(studio.AddonProviderLister)
	if !ok {
		return nil, nil
	}
	sets, err := lister.AddonProviders(ctx, user)
	if err != nil {
		return nil, err
	}
	var out []modelsearch.AddonSource
	for _, set := range sets {
		if set.ModelSearch == nil {
			continue
		}
		for _, d := range set.Provides {
			if d.Slot != contract.SlotModelSearch || len(d.Roles[contract.RoleSearch]) != 1 {
				continue
			}
			out = append(out, modelsearch.AddonSource{AddonID: set.AddonID, Tool: d.Roles[contract.RoleSearch][0], Running: set.Running, Search: *set.ModelSearch})
		}
	}
	return out, nil
}

func (b modelSearchBackend) Call(ctx context.Context, user, addonID, tool string, args json.RawMessage) (json.RawMessage, error) {
	caller, ok := b.broker().(studio.AddonProviderCaller)
	if !ok {
		return nil, errors.New("the add-on broker is not available")
	}
	text, err := caller.CallProvider(ctx, user, studio.ProviderToolName(addonID, tool), args)
	if err != nil {
		return nil, err
	}
	var env struct {
		Kind    string          `json:"kind"`
		Result  json.RawMessage `json:"result"`
		Code    string          `json:"code"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil || env.Kind == "" {
		return nil, errors.New("the add-on answered with something that is not a result")
	}
	if env.Kind != "result" {
		reason := env.Code
		if reason == "" {
			reason = env.Kind
		}
		return nil, fmt.Errorf("the add-on refused the call (%s)", reason)
	}
	return env.Result, nil
}

// modelSearchKV keeps the off list in the store of the per-person add-on
// settings, under the owner of the host's own settings.
type modelSearchKV struct{ a *Application }

func (k modelSearchKV) Get(ctx context.Context, user string) (string, bool, error) {
	return k.a.studioUserSetting(ctx, user, studio.HostSettingsID, modelSearchKey)
}

func (k modelSearchKV) Set(ctx context.Context, user, value string) error {
	return k.a.studioSetUserSetting(ctx, user, studio.HostSettingsID, modelSearchKey, value)
}
