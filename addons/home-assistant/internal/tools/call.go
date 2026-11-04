// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/haapi"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// call is one tool call: the token, the instance the host allowed and the
// policy in force for this call.
type call struct {
	r      *Runtime
	ctx    context.Context
	token  string
	key    string
	target haapi.Target
	pol    *policy.Compiled
	// raw is the policy as loaded, for its hash.
	raw policy.Policy

	warnings []string
	hid      map[string]bool
	hidDone  bool
}

// newCall reads the address the host allowed, checks it, and loads the policy.
// The client takes its pins from the same policy.
func (r *Runtime) newCall(ctx context.Context, env contract.Envelope) (*call, error) {
	if env.Request.ResourceKey == "" {
		return nil, bad("instance is not the one the host allowed")
	}
	t, err := haapi.ParseBase(env.Request.ResourceKey)
	if err != nil {
		return nil, bad("%s", haapi.Describe(err))
	}
	p, err := r.Policy.Load()
	if err != nil {
		return nil, bad("%s", policyUnreadable)
	}
	cp, err := p.Compile()
	if err != nil {
		return nil, bad("%s", policyUnreadable)
	}
	return &call{r: r, ctx: ctx, token: env.Request.Credential, key: env.Request.ResourceKey, target: t, pol: cp, raw: p}, nil
}

// instance checks the argument against the address the host allowed.
func (c *call) instance(arg string) error {
	if arg == "" || !strings.EqualFold(arg, c.key) {
		return bad("instance is not the one the host allowed")
	}
	return nil
}

func (c *call) get(path string, q url.Values, v any) error {
	return c.r.API.GetJSON(c.ctx, c.target, c.token, path, q, v)
}

// render runs a fixed template and decodes its JSON output.
func (c *call) render(tpl string, v any) error {
	out, err := c.r.API.Render(c.ctx, c.target, c.token, tpl)
	if err != nil {
		return err
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), v) != nil {
		return haapi.ErrUnavailable
	}
	return nil
}

func quoteNames(names []string) string {
	q := make([]string, 0, len(names))
	for _, n := range names {
		q = append(q, `"`+n+`"`)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// areaEntities returns the entities of areas named by name or ID. The names
// are checked by SafeName, so they cannot leave the string they sit in.
func (c *call) areaEntities(names []string) ([]string, error) {
	for _, n := range names {
		if !policy.SafeName(n) {
			return nil, bad("an area is a plain name")
		}
	}
	var out []string
	tpl := fmt.Sprintf(`{%% set ns = namespace(r=[]) %%}{%% for a in %s %%}{%% set ns.r = ns.r + area_entities(a) %%}{%% endfor %%}{{ ns.r | to_json }}`, quoteNames(names))
	return out, c.render(tpl, &out)
}

// labelEntities returns the entities that carry the label, and those of the
// devices that carry it.
func (c *call) labelEntities(label string) ([]string, error) {
	if !policy.SafeName(label) {
		return nil, bad("the label is a plain name")
	}
	var out []string
	tpl := fmt.Sprintf(`{%% set l = label_id("%s") %%}{%% set ns = namespace(r=[]) %%}{%% if l %%}{%% set ns.r = ns.r + label_entities(l) %%}{%% for d in label_devices(l) %%}{%% set ns.r = ns.r + device_entities(d) %%}{%% endfor %%}{%% endif %%}{{ ns.r | to_json }}`, label)
	return out, c.render(tpl, &out)
}

// hidden returns the entities that the area and label exclusions cover. An
// area exclusion that cannot be checked refuses the call: it fails closed. A
// label check that an old instance does not know is skipped and said so.
func (c *call) hidden() (map[string]bool, error) {
	if c.hidDone {
		return c.hid, nil
	}
	hid := map[string]bool{}
	if len(c.pol.ExcludeAreas) > 0 {
		ids, err := c.areaEntities(c.pol.ExcludeAreas)
		if err != nil {
			return nil, bad("the excluded areas could not be checked, so nothing was returned")
		}
		for _, id := range ids {
			hid[strings.ToLower(id)] = true
		}
	}
	if c.pol.ExcludeLabel != "" {
		ids, err := c.labelEntities(c.pol.ExcludeLabel)
		switch {
		case err == nil:
			for _, id := range ids {
				hid[strings.ToLower(id)] = true
			}
		case isInvalid(err):
			c.warnings = append(c.warnings, "this Home Assistant does not support labels, so the label exclusion was not applied")
		default:
			return nil, bad("the excluded label could not be checked, so nothing was returned")
		}
	}
	c.hid, c.hidDone = hid, true
	return hid, nil
}

func isInvalid(err error) bool { return errors.Is(err, haapi.ErrInvalid) }

// visible reports whether an entity may be returned or touched.
func (c *call) visible(id string, hid map[string]bool) bool {
	return !c.pol.EntityExcluded(id) && !hid[strings.ToLower(id)]
}
