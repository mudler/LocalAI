package remotes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/core/services/modeladmin"
)

// Federation: several LocalAI servers that serve one model name together.
//
// A group is a model whose config is a balanced failover chain: its targets
// are a model of this server and entries of remotes (usually remotes over
// P2P), and each request goes to one of them by the group's policy, least
// busy or round robin. A target that fails is tripped and the request moves
// on to the next one, as in any failover chain. The group is stored only in
// its model config file, which every frontend of a cluster shares like the
// entries of remotes; this service keeps no state of its own. The live part
// (health, requests in flight) comes from the failover manager of this
// server.

// LocalInstance is the instance id of this server.
const LocalInstance = "local"

// ErrGroupNotFound is returned for a name that is not a group: no model, or
// a model that is not a balanced chain.
var ErrGroupNotFound = errors.New("federation group not found")

// InstanceModel is a model that an instance serves.
type InstanceModel struct {
	// Model is the name of the model config on this server.
	Model string `json:"model"`
	// UpstreamID is the model id at the instance. For this server it is
	// the model name.
	UpstreamID string `json:"upstream_id"`
}

// Instance is a LocalAI that can serve a group: this server or a remote of
// kind localai.
type Instance struct {
	// ID is LocalInstance or the id of the remote.
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Local     bool      `json:"local"`
	URL       string    `json:"url,omitempty"`
	Transport Transport `json:"transport,omitempty"`
	Status    Status    `json:"status,omitempty"`
	// P2P is the tunnel of a remote over P2P.
	P2P *P2PInfo `json:"p2p,omitempty"`
	// InFlight counts the requests of this server that the instance serves
	// now through a group.
	InFlight int             `json:"in_flight"`
	Models   []InstanceModel `json:"models"`
}

// GroupMember is one target of a group.
type GroupMember struct {
	Model string `json:"model"`
	// Instance is the id of the instance that serves the model: LocalInstance,
	// a remote id, or empty for a model of another proxy.
	Instance     string `json:"instance,omitempty"`
	InstanceName string `json:"instance_name,omitempty"`
	UpstreamID   string `json:"upstream_id,omitempty"`
	Drained      bool   `json:"drained"`
	// State is the health of the target: healthy, down, recovering or
	// missing. Empty when the failover manager does not know it yet.
	State string `json:"state,omitempty"`
	// InFlight counts the requests of this server that the target serves.
	InFlight int `json:"in_flight"`
	// Constrained is true when the target is reached through a relay that
	// limits the connection: it takes a request only when no other can.
	Constrained bool   `json:"constrained,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// Group is a model name served by several instances.
type Group struct {
	Name   string                `json:"name"`
	Policy config.FailoverPolicy `json:"policy"`
	// State is the state of the chain: primary, fallback or degraded. Empty
	// when the failover manager does not know it yet.
	State   string        `json:"state,omitempty"`
	Members []GroupMember `json:"members"`
}

// FederationView is the whole federation: the instances that can serve a
// group and the groups.
type FederationView struct {
	Instances []Instance `json:"instances"`
	Groups    []Group    `json:"groups"`
}

// GroupRequest makes a group. Members are model names; at least two.
type GroupRequest struct {
	Name    string
	Policy  config.FailoverPolicy
	Members []string
}

// GroupUpdate changes a group. A nil field is left as it is. Members keeps
// the drain flag of the models that stay.
type GroupUpdate struct {
	Policy  *config.FailoverPolicy
	Members *[]string
}

// ChainStatuses reads the live state of failover chains. The failover
// manager implements it.
type ChainStatuses interface {
	ChainStatus(name string) (failover.ChainStatus, bool)
}

// Federation manages groups.
type Federation interface {
	View(ctx context.Context) (FederationView, error)
	Group(ctx context.Context, name string) (Group, error)
	CreateGroup(ctx context.Context, r GroupRequest) (Group, error)
	UpdateGroup(ctx context.Context, name string, r GroupUpdate) (Group, error)
	DeleteGroup(ctx context.Context, name string) error
	// Drain stops (or with drain false, restarts) new requests of a group
	// to one of its models. Requests in flight finish.
	Drain(ctx context.Context, group, model string, drain bool) (Group, error)
}

type federation struct {
	s      *service
	chains ChainStatuses
}

// NewFederation returns the Federation on the same store, model admin and
// tunnels as the remotes service built from o. chains may be nil: the view
// then has no live state.
func NewFederation(o Options, chains ChainStatuses) (Federation, error) {
	s, err := newService(o)
	if err != nil {
		return nil, err
	}
	return &federation{s: s, chains: chains}, nil
}

// DefaultGroupPolicy is the policy of a group made without one.
const DefaultGroupPolicy = config.FailoverPolicyLeastBusy

const groupHeader = "# A federation group: one model name served by several LocalAI instances.\n"

type groupFile struct {
	Name     string                `yaml:"name"`
	Failover config.FailoverConfig `yaml:"failover"`
}

func isGroup(c config.ModelConfig) bool { return c.IsFailover() && c.Failover.Balanced() }

func cleanPolicy(p config.FailoverPolicy) (config.FailoverPolicy, error) {
	switch p {
	case "":
		return DefaultGroupPolicy, nil
	case config.FailoverPolicyLeastBusy, config.FailoverPolicyRoundRobin:
		return p, nil
	}
	return "", invalid("The routing must be least_busy or round_robin.")
}

// cleanMembers checks the members of a group: at least two, each an
// existing model that is not a chain, none twice.
func (f *federation) cleanMembers(group string, members []string) ([]string, error) {
	out := make([]string, 0, len(members))
	for _, m := range members {
		m = strings.TrimSpace(m)
		if m == "" || slices.Contains(out, m) {
			continue
		}
		if m == group {
			return nil, invalid("A group cannot list itself.")
		}
		cfg, ok := f.s.o.Configs.GetModelConfig(m)
		if !ok {
			return nil, invalid(fmt.Sprintf("The model %q does not exist.", m))
		}
		if cfg.IsFailover() {
			return nil, invalid(fmt.Sprintf("The model %q is a group or a failover chain; groups do not nest.", m))
		}
		out = append(out, m)
	}
	if len(out) < 2 {
		return nil, invalid("A group needs at least two models.")
	}
	return out, nil
}

func (f *federation) CreateGroup(ctx context.Context, r GroupRequest) (Group, error) {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return Group{}, invalid("Enter the model name of the group.")
	}
	if sanitizeName(name) != name {
		return Group{}, invalid("The name can have only letters, digits, dot, dash and underscore.")
	}
	policy, err := cleanPolicy(r.Policy)
	if err != nil {
		return Group{}, err
	}
	members, err := f.cleanMembers(name, r.Members)
	if err != nil {
		return Group{}, err
	}
	gf := groupFile{Name: name, Failover: config.FailoverConfig{Policy: policy}}
	for _, m := range members {
		gf.Failover.Targets = append(gf.Failover.Targets, config.FailoverTarget{Model: m})
	}
	body, err := yaml.Marshal(gf)
	if err != nil {
		return Group{}, err
	}
	unlock := f.s.locks.lock("group:" + name)
	defer unlock()
	res, err := f.s.o.Admin.CreateConfig(ctx, append([]byte(groupHeader), body...))
	switch {
	case errors.Is(err, modeladmin.ErrConflict):
		return Group{}, fmt.Errorf("%w: a model named %q exists", ErrConflict, name)
	case errors.Is(err, modeladmin.ErrInvalidConfig):
		return Group{}, invalid(err.Error())
	case err != nil:
		return Group{}, err
	}
	f.s.broadcast(name, "install", res.ConfigRevision)
	return f.Group(ctx, name)
}

// patchGroup writes the failover block of a group. The other keys of the
// file, and the probe, trip and recovery settings, stay as they are.
func (f *federation) patchGroup(ctx context.Context, name string, fc config.FailoverConfig) error {
	targets := make([]any, 0, len(fc.Targets))
	for _, t := range fc.Targets {
		m := map[string]any{"model": t.Model}
		if t.Warm {
			m["warm"] = true
		}
		if t.Drain {
			m["drain"] = true
		}
		targets = append(targets, m)
	}
	patch := map[string]any{"failover": map[string]any{"policy": string(fc.Policy), "targets": targets}}
	res, err := f.s.o.Admin.PatchConfig(ctx, name, patch)
	switch {
	case errors.Is(err, modeladmin.ErrNotFound):
		return fmt.Errorf("%w: %q", ErrGroupNotFound, name)
	case errors.Is(err, modeladmin.ErrInvalidConfig):
		return invalid(err.Error())
	case err != nil:
		return err
	}
	f.s.broadcast(name, "install", res.ConfigRevision)
	return nil
}

// groupConfig returns the failover block of a group.
func (f *federation) groupConfig(name string) (config.FailoverConfig, error) {
	cfg, ok := f.s.o.Configs.GetModelConfig(name)
	if !ok || !isGroup(cfg) {
		return config.FailoverConfig{}, fmt.Errorf("%w: %q", ErrGroupNotFound, name)
	}
	fc := *cfg.Failover
	fc.Targets = slices.Clone(fc.Targets)
	return fc, nil
}

func (f *federation) UpdateGroup(ctx context.Context, name string, r GroupUpdate) (Group, error) {
	unlock := f.s.locks.lock("group:" + name)
	defer unlock()
	fc, err := f.groupConfig(name)
	if err != nil {
		return Group{}, err
	}
	if r.Policy != nil {
		p, err := cleanPolicy(*r.Policy)
		if err != nil {
			return Group{}, err
		}
		fc.Policy = p
	}
	if r.Members != nil {
		members, err := f.cleanMembers(name, *r.Members)
		if err != nil {
			return Group{}, err
		}
		next := make([]config.FailoverTarget, 0, len(members))
		for _, m := range members {
			t := config.FailoverTarget{Model: m}
			if i := slices.IndexFunc(fc.Targets, func(o config.FailoverTarget) bool { return o.Model == m }); i >= 0 {
				t = fc.Targets[i]
			}
			next = append(next, t)
		}
		fc.Targets = next
	}
	if err := f.patchGroup(ctx, name, fc); err != nil {
		return Group{}, err
	}
	return f.Group(ctx, name)
}

func (f *federation) Drain(ctx context.Context, group, model string, drain bool) (Group, error) {
	unlock := f.s.locks.lock("group:" + group)
	defer unlock()
	fc, err := f.groupConfig(group)
	if err != nil {
		return Group{}, err
	}
	i := slices.IndexFunc(fc.Targets, func(t config.FailoverTarget) bool { return t.Model == model })
	if i < 0 {
		return Group{}, invalid(fmt.Sprintf("The model %q is not in the group %q.", model, group))
	}
	if fc.Targets[i].Drain == drain {
		return f.Group(ctx, group)
	}
	fc.Targets[i].Drain = drain
	if drain && !slices.ContainsFunc(fc.Targets, func(t config.FailoverTarget) bool { return !t.Drain }) {
		return Group{}, invalid("This is the last model of the group that takes requests. Add another one before you drain it.")
	}
	if err := f.patchGroup(ctx, group, fc); err != nil {
		return Group{}, err
	}
	return f.Group(ctx, group)
}

func (f *federation) DeleteGroup(ctx context.Context, name string) error {
	unlock := f.s.locks.lock("group:" + name)
	defer unlock()
	if _, err := f.groupConfig(name); err != nil {
		return err
	}
	res, err := f.s.o.Admin.DeleteConfig(ctx, name)
	if errors.Is(err, modeladmin.ErrNotFound) {
		return fmt.Errorf("%w: %q", ErrGroupNotFound, name)
	}
	if err != nil {
		return err
	}
	f.s.broadcast(name, "delete", res.ConfigRevision)
	return nil
}

func (f *federation) Group(ctx context.Context, name string) (Group, error) {
	if _, err := f.groupConfig(name); err != nil {
		return Group{}, err
	}
	v, err := f.View(ctx)
	if err != nil {
		return Group{}, err
	}
	for _, g := range v.Groups {
		if g.Name == name {
			return g, nil
		}
	}
	return Group{}, fmt.Errorf("%w: %q", ErrGroupNotFound, name)
}

// owner says which instance serves a model config.
type owner struct {
	id, name, upstream string
}

func (f *federation) View(ctx context.Context) (FederationView, error) {
	recs, err := f.s.o.Store.List(ctx)
	if err != nil {
		return FederationView{}, err
	}
	slices.SortFunc(recs, func(a, b record) int { return cmp.Compare(nameKey(a.Name), nameKey(b.Name)) })
	configs := f.s.o.Configs.GetAllModelsConfigs()
	slices.SortFunc(configs, func(a, b config.ModelConfig) int { return cmp.Compare(a.Name, b.Name) })

	local := Instance{ID: LocalInstance, Local: true, Models: []InstanceModel{}}
	instances := []Instance{}
	owners := map[string]owner{}
	byRemote := map[string]int{}
	for _, r := range recs {
		for _, o := range r.Owned {
			owners[o.Name] = owner{id: r.ID, name: r.Name, upstream: o.UpstreamID}
		}
		if r.Kind != KindLocalAI {
			continue
		}
		v := f.s.viewOf(r)
		in := Instance{ID: r.ID, Name: r.Name, URL: r.URL, Transport: r.Transport, Status: v.Status, P2P: v.P2P, Models: []InstanceModel{}}
		for _, o := range r.Owned {
			in.Models = append(in.Models, InstanceModel{Model: o.Name, UpstreamID: o.UpstreamID})
		}
		byRemote[r.ID] = len(instances)
		instances = append(instances, in)
	}
	var groups []config.ModelConfig
	for _, c := range configs {
		switch {
		case isGroup(c):
			groups = append(groups, c)
		case c.IsFailover(), c.IsAlias(), c.IsRemoteProxy(), c.Proxy.Remote != "":
		default:
			local.Models = append(local.Models, InstanceModel{Model: c.Name, UpstreamID: c.Name})
			owners[c.Name] = owner{id: LocalInstance}
		}
	}

	view := FederationView{Groups: []Group{}}
	counted := map[string]bool{}
	for _, c := range groups {
		g := Group{Name: c.Name, Policy: c.Failover.Policy, Members: []GroupMember{}}
		var live map[string]failover.TargetStatus
		if f.chains != nil {
			if st, ok := f.chains.ChainStatus(c.Name); ok {
				g.State = string(st.State)
				live = map[string]failover.TargetStatus{}
				for _, ts := range st.Targets {
					live[ts.Model] = ts
				}
			}
		}
		for _, t := range c.Failover.Targets {
			m := GroupMember{Model: t.Model, Drained: t.Drain}
			if o, ok := owners[t.Model]; ok {
				m.Instance, m.InstanceName, m.UpstreamID = o.id, o.name, o.upstream
				if o.id == LocalInstance {
					m.UpstreamID = t.Model
				}
			}
			if ts, ok := live[t.Model]; ok {
				m.State, m.InFlight, m.Constrained, m.LastError = string(ts.State), ts.InFlight, ts.Constrained, ts.LastError
			}
			// A target listed by two groups has one in-flight count;
			// add it to its instance once.
			if !counted[t.Model] {
				counted[t.Model] = true
				switch i, ok := byRemote[m.Instance]; {
				case m.Instance == LocalInstance:
					local.InFlight += m.InFlight
				case ok:
					instances[i].InFlight += m.InFlight
				}
			}
			g.Members = append(g.Members, m)
		}
		view.Groups = append(view.Groups, g)
	}
	view.Instances = append([]Instance{local}, instances...)
	return view, nil
}
