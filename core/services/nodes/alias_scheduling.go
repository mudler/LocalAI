package nodes

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/xlog"
)

// AliasResolver maps a model name to the name of the model that actually
// serves it: an alias resolves to its target, anything else to itself. The
// second return reports whether the name was an alias.
//
// core/config.ModelConfigLoader implements this. It is an interface here so
// the registry stays testable without building a full config loader.
type AliasResolver interface {
	ResolveAliasName(name string) (string, bool)
}

// SetAliasResolver installs the resolver used to map a scheduling rule's model
// name onto the model the rule actually governs. Called once at startup before
// serving. Leaving it unset makes every rule govern its own name, which is the
// behaviour from before rules could be keyed by an alias.
func (r *NodeRegistry) SetAliasResolver(resolver AliasResolver) {
	r.aliasResolver.Store(&resolver)
}

// resolveAlias maps a name through the installed resolver, or returns it
// unchanged when no resolver is wired.
func (r *NodeRegistry) resolveAlias(name string) (string, bool) {
	p := r.aliasResolver.Load()
	if p == nil || *p == nil {
		return name, false
	}
	return (*p).ResolveAliasName(name)
}

// applyTarget fills in the rule's derived TargetModel. Every read path runs a
// rule through this so callers can tell the rule's key (ModelName, the name
// the operator chose) apart from the model it governs (TargetModel).
func (r *NodeRegistry) applyTarget(cfg *ModelSchedulingConfig) {
	if cfg == nil {
		return
	}
	cfg.TargetModel, cfg.ModelIsAlias = r.resolveAlias(cfg.ModelName)
}

// GetGoverningScheduling returns the rule that governs a physical model: the
// rule keyed by the model's own name when one exists, otherwise the rule of an
// alias that resolves to it. Returns nil when no rule governs the model.
//
// The reverse lookup exists because rules stay keyed by the name the operator
// chose, so that an alias rule survives repointing the alias, while the router
// only ever sees the resolved model name (request middleware resolves the
// alias long before routing).
func (r *NodeRegistry) GetGoverningScheduling(ctx context.Context, modelName string) (*ModelSchedulingConfig, error) {
	if direct, err := r.GetModelScheduling(ctx, modelName); err != nil || direct != nil {
		return direct, err
	}
	rule, err := r.aliasRuleFor(ctx, modelName, "")
	if err != nil {
		return nil, err
	}
	return rule, nil
}

// aliasRuleFor returns the oldest alias-keyed rule resolving to targetModel,
// skipping the rule named by exclude. Returns nil when no alias rule resolves
// there.
//
// Several names can resolve to one model, but placement governs a single
// shared load, so exactly one rule can win. Ordering by creation time (then by
// name, so rules written in the same transaction still order the same way)
// makes every frontend and every reconciler tick pick the same rule.
//
// This scans the rule table rather than filtering in SQL, because the mapping
// from a rule's name to the model it governs lives in the config loader, not
// in the database. The scan is bounded by the number of scheduling rules an
// operator has written, not by the number of models in the cluster.
func (r *NodeRegistry) aliasRuleFor(ctx context.Context, targetModel, exclude string) (*ModelSchedulingConfig, error) {
	if targetModel == "" {
		return nil, nil
	}
	var configs []ModelSchedulingConfig
	if err := r.db.WithContext(ctx).Order("created_at ASC, model_name ASC").Find(&configs).Error; err != nil {
		return nil, err
	}
	for i := range configs {
		cfg := &configs[i]
		// A rule keyed by the target's own name is the direct rule, not an
		// alias rule; callers handle that case with a precedence of its own.
		if cfg.ModelName == targetModel || cfg.ModelName == exclude {
			continue
		}
		r.applyTarget(cfg)
		if cfg.Target() == targetModel {
			return cfg, nil
		}
	}
	return nil, nil
}

// SchedulingConflict reports the name of an existing rule that already governs
// targetModel, or "" when the target is free. exclude is the rule being
// created or edited, which never conflicts with itself.
//
// Placement governs one shared load, so two rules resolving to the same model
// would each claim to decide where it runs. Write paths use this to reject the
// second one instead of leaving the outcome to the tiebreak in
// GetGoverningScheduling.
func (r *NodeRegistry) SchedulingConflict(ctx context.Context, targetModel, exclude string) (string, error) {
	if targetModel == "" {
		return "", nil
	}
	if targetModel != exclude {
		direct, err := r.GetModelScheduling(ctx, targetModel)
		if err != nil {
			return "", err
		}
		if direct != nil {
			return direct.ModelName, nil
		}
	}
	rule, err := r.aliasRuleFor(ctx, targetModel, exclude)
	if err != nil || rule == nil {
		return "", err
	}
	return rule.ModelName, nil
}

// ResolveRuleTarget returns the model a rule keyed by ruleName would govern,
// and whether ruleName is an alias. A name that is an alias but comes back
// unchanged is one that does not resolve.
func (r *NodeRegistry) ResolveRuleTarget(ruleName string) (string, bool) {
	return r.resolveAlias(ruleName)
}

// markShadowed flags every rule that resolves to a model some other rule
// already governs. Placement decides where one shared load runs, so only one
// rule per target can take effect.
//
// The precedence matches GetGoverningScheduling: a rule keyed by the target's
// own name wins, otherwise the oldest rule does. configs is modified in place.
func markShadowed(configs []ModelSchedulingConfig) {
	governing := make(map[string]int, len(configs))
	for i := range configs {
		target := configs[i].Target()
		best, seen := governing[target]
		if !seen {
			governing[target] = i
			continue
		}
		if rulePrecedes(configs[i], configs[best], target) {
			governing[target] = i
		}
	}
	for i := range configs {
		configs[i].Shadowed = governing[configs[i].Target()] != i
	}
}

// rulePrecedes reports whether rule a governs target instead of rule b.
func rulePrecedes(a, b ModelSchedulingConfig, target string) bool {
	if (a.ModelName == target) != (b.ModelName == target) {
		return a.ModelName == target
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ModelName < b.ModelName
}

// ErrSchedulingConflict is returned when a rule would govern a model that
// another rule already governs. Callers map it onto a conflict status.
var ErrSchedulingConflict = errors.New("model already has a scheduling rule")

// ValidateSchedulingTarget checks that a rule keyed by ruleName can be written,
// and returns the model it will govern.
//
// It rejects two cases. An alias that does not resolve governs nothing
// loadable, so a rule on it would sit inert forever. And a model that another
// rule already governs cannot take a second one, because placement decides
// where a single shared load runs: two rules would each claim to decide, and
// only one could win.
func (r *NodeRegistry) ValidateSchedulingTarget(ctx context.Context, ruleName string) (string, error) {
	target, isAlias := r.resolveAlias(ruleName)
	if isAlias && target == ruleName {
		return "", fmt.Errorf("%q is an alias that does not resolve to a model: point it at an existing model before giving it a scheduling rule", ruleName)
	}
	conflict, err := r.SchedulingConflict(ctx, target, ruleName)
	if err != nil {
		return "", err
	}
	if conflict != "" {
		return "", fmt.Errorf("%w: rule %q already governs model %q, so edit or delete that rule instead of adding a second one", ErrSchedulingConflict, conflict, target)
	}
	return target, nil
}

// RefreshSchedulingTargets rewrites each rule's stored target_model to match
// the current alias mapping, and returns the number of rows it changed.
//
// Go callers resolve aliases live and never read the stored copy. It exists for
// the eviction guard, which matches rules to loaded replicas in raw SQL inside
// a locking transaction and so cannot resolve an alias itself. Repointing an
// alias therefore reaches that guard one reconciler tick later, which is early
// enough: until then the guard protects the previous target, and the reconciler
// is already reloading the new one.
func (r *NodeRegistry) RefreshSchedulingTargets(ctx context.Context) error {
	var configs []ModelSchedulingConfig
	if err := r.db.WithContext(ctx).Find(&configs).Error; err != nil {
		return err
	}
	for i := range configs {
		stored := configs[i].TargetModel
		live, _ := r.resolveAlias(configs[i].ModelName)
		if stored == live {
			continue
		}
		if err := r.db.WithContext(ctx).Model(&ModelSchedulingConfig{}).
			Where("id = ?", configs[i].ID).
			Update("target_model", live).Error; err != nil {
			return err
		}
		xlog.Info("Scheduling rule now governs a different model",
			"rule", configs[i].ModelName, "was", stored, "now", live)
	}
	return nil
}
