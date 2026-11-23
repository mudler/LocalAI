package upgraderun

import (
	"context"
	"sort"
)

// Candidate is one item a check lists with an update.
type Candidate struct {
	Kind Kind
	Name string
	From string
	// To is the version or digest the update moves to. It is what a request
	// pins as expected_to.
	To string
	// Held is a model the owner holds at its version.
	Held bool
	// EditedKeys are keys a person edited that the update also changes; the
	// update needs a choice for each, which a run does not make.
	EditedKeys []string
	// Blocked says why the item cannot be updated on this server (for
	// example installs_disabled). Empty when it can.
	Blocked string
	// Risks are what the update does beyond replacing the item.
	Risks []string
	// Extra is handed to the job path of the item (see Item.Extra).
	Extra map[string]string
}

// Key is the key of the item the candidate updates.
func (c Candidate) Key() string { return Key(c.Kind, c.Name) }

// Listing is the answer of a check.
type Listing struct {
	Candidates []Candidate
	// Complete is false when a source did not answer (it failed or was never
	// checked): an item missing from the list may then still have an update.
	Complete bool
	// Throttled is set when a fresh check was answered from a check that ran
	// a few seconds before, so it may predate an update that just ended.
	Throttled bool
}

// Checker lists the items of some kinds that have an update. The existing
// update checks implement it (see BackendChecker and ModelChecker); add-ons
// are listed by the add-on owner, through an implementation the caller hands
// in.
type Checker interface {
	// Kinds are the kinds this checker lists.
	Kinds() []Kind
	// List returns the items with an update. fresh asks for a new check
	// instead of the last answer.
	List(ctx context.Context, fresh bool) (Listing, error)
}

// Risk codes of a plan item.
const (
	// RiskStopsModels: models loaded with this backend stop while it is
	// replaced.
	RiskStopsModels = "stops_models"
	// RiskCluster: the backend is replaced on every node that has it.
	RiskCluster = "cluster"
	// RiskDrift: the nodes do not agree on the build now.
	RiskDrift = "drift"
	// RiskStopsModel: the model stops while it is switched, and is loaded
	// again as a check.
	RiskStopsModel = "stops_model"
	// RiskDownloads: the update downloads files.
	RiskDownloads = "downloads"
	// RiskConfigOnly: the update changes only the config of the model.
	RiskConfigOnly = "config_only"
)

// Reasons why a plan item cannot start.
const (
	ReasonHeld       = "held"
	ReasonEditedKeys = "edited_keys"
	ReasonInRun      = "in_run"
	ReasonNoUpdate   = "no_update"
)

// Stages of a run, in order.
const (
	StageBackends = 1
	StageModels   = 2
	StageAddons   = 3
)

// StageOf is the stage items of a kind run in.
func StageOf(k Kind) int {
	switch k {
	case KindModel:
		return StageModels
	case KindAddon:
		return StageAddons
	}
	return StageBackends
}

// ItemRef names an item.
type ItemRef struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
}

// PlanItem is one item of a preview.
type PlanItem struct {
	Key  string `json:"key"`
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// ExpectedTo is what a start request should send back as expected_to.
	ExpectedTo string `json:"expected_to,omitempty"`
	Stage      int    `json:"stage"`
	// Order is the position in the run, from 0.
	Order     int    `json:"order"`
	Startable bool   `json:"startable"`
	Reason    string `json:"reason,omitempty"`
	// Risks are what the update does beyond replacing the item.
	Risks []string `json:"risks"`
	// Selected is the default selection: backends and add-ons, not models.
	Selected bool `json:"selected"`
}

// Plan is the answer of a preview.
type Plan struct {
	Items []PlanItem `json:"items"`
	// Complete is false when a source did not answer, so the plan may miss
	// items.
	Complete bool `json:"complete"`
	// ActiveRunID is the run that runs now, if any; a start is refused
	// while it runs.
	ActiveRunID string `json:"active_run_id,omitempty"`
}

// kindRank orders kinds inside a stage: gallery backends before library ones.
func kindRank(k Kind) int {
	switch k {
	case KindGallery:
		return 0
	case KindLibrary:
		return 1
	case KindModel:
		return 2
	}
	return 3
}

func sortCandidates(cs []Candidate) {
	sort.SliceStable(cs, func(a, b int) bool {
		x, y := cs[a], cs[b]
		if StageOf(x.Kind) != StageOf(y.Kind) {
			return StageOf(x.Kind) < StageOf(y.Kind)
		}
		if kindRank(x.Kind) != kindRank(y.Kind) {
			return kindRank(x.Kind) < kindRank(y.Kind)
		}
		return x.Name < y.Name
	})
}

// skipReason is why a candidate is skipped by a run, or "".
func skipReason(c Candidate) string {
	switch {
	case c.Held:
		return ReasonHeld
	case len(c.EditedKeys) > 0:
		return ReasonEditedKeys
	}
	return ""
}

// BuildPlan orders the candidates into a preview. refs limits it to some
// items (all candidates when empty); a ref with no candidate is listed as not
// startable. busy are the keys a running run already has.
func BuildPlan(cands []Candidate, refs []ItemRef, busy map[string]bool) Plan {
	byKey := map[string]Candidate{}
	for _, c := range cands {
		byKey[c.Key()] = c
	}
	var chosen []Candidate
	var missing []ItemRef
	if len(refs) == 0 {
		chosen = append(chosen, cands...)
	} else {
		seen := map[string]bool{}
		for _, r := range refs {
			k := Key(r.Kind, r.Name)
			if seen[k] {
				continue
			}
			seen[k] = true
			if c, ok := byKey[k]; ok {
				chosen = append(chosen, c)
			} else {
				missing = append(missing, r)
			}
		}
	}
	sortCandidates(chosen)

	plan := Plan{Items: []PlanItem{}}
	for _, c := range chosen {
		pi := PlanItem{
			Key: c.Key(), Kind: c.Kind, Name: c.Name, From: c.From, To: c.To, ExpectedTo: c.To,
			Stage: StageOf(c.Kind), Startable: true, Risks: append([]string{}, c.Risks...),
		}
		switch {
		case c.Blocked != "":
			pi.Startable, pi.Reason = false, c.Blocked
		case busy[pi.Key]:
			pi.Startable, pi.Reason = false, ReasonInRun
		case skipReason(c) != "":
			pi.Startable, pi.Reason = false, skipReason(c)
		}
		pi.Selected = pi.Startable && c.Kind != KindModel
		plan.Items = append(plan.Items, pi)
	}
	for _, r := range missing {
		plan.Items = append(plan.Items, PlanItem{
			Key: Key(r.Kind, r.Name), Kind: r.Kind, Name: r.Name, Stage: StageOf(r.Kind),
			Reason: ReasonNoUpdate, Risks: []string{},
		})
	}
	for i := range plan.Items {
		plan.Items[i].Order = i
	}
	return plan
}

// StartItem is one item of a start request.
type StartItem struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	// ExpectedTo is the version or digest the caller saw as the target
	// (PlanItem.ExpectedTo). A start whose target moved since is refused.
	ExpectedTo string `json:"expected_to"`
}

// StartRequest asks for a run.
type StartRequest struct {
	Items  []StartItem `json:"items"`
	Source Source      `json:"source"`
}

// planStart validates a start request against the current candidates and
// returns the items of the new run, in run order. Every item must be listed
// with an update and the same target the caller saw; a held model or one
// with edited keys becomes a skipped item.
func planStart(cands []Candidate, req StartRequest) ([]Item, error) {
	if len(req.Items) == 0 {
		return nil, refuse(CodeInvalid, "", "no item to update")
	}
	byKey := map[string]Candidate{}
	for _, c := range cands {
		byKey[c.Key()] = c
	}
	var chosen []Candidate
	expected := map[string]string{}
	for _, si := range req.Items {
		if !si.Kind.Valid() || si.Name == "" {
			return nil, refuse(CodeInvalid, Key(si.Kind, si.Name), "unknown item kind or empty name")
		}
		k := Key(si.Kind, si.Name)
		if _, dup := expected[k]; dup {
			continue
		}
		c, ok := byKey[k]
		if !ok {
			return nil, refuse(CodeNoUpdate, k, "the item is not installed or has no update")
		}
		if c.Blocked != "" {
			return nil, refuse(CodeNoUpdate, k, "the item cannot be updated on this server: %s", c.Blocked)
		}
		if si.ExpectedTo != "" && si.ExpectedTo != c.To {
			return nil, refuse(CodeStale, k, "the update moved to %q since it was shown", c.To)
		}
		expected[k] = c.To
		chosen = append(chosen, c)
	}
	sortCandidates(chosen)
	items := make([]Item, 0, len(chosen))
	for _, c := range chosen {
		it := Item{
			Key: c.Key(), Kind: c.Kind, Name: c.Name, From: c.From, To: c.To, ExpectedTo: expected[c.Key()],
			Phase: PhaseQueued, Extra: c.Extra,
		}
		switch skipReason(c) {
		case ReasonHeld:
			it.Phase, it.ErrorKey, it.Message = PhaseSkipped, ErrKeyHeld, "the model is held at its version"
		case ReasonEditedKeys:
			it.Phase, it.ErrorKey, it.Message = PhaseSkipped, ErrKeyEditedKeys, "the update changes keys that were edited; choose for each on the model"
		}
		items = append(items, it)
	}
	return items, nil
}
