package nodes

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"gopkg.in/yaml.v3"
)

// ReplicasSpec parses the "replicas" convenience field used in the env/file
// scheduling config. It accepts the string "all" (or boolean true) to mean
// "spread one replica onto every matching node". The strings "" / "auto" and
// boolean false leave SpreadAll unset and defer to min_replicas/max_replicas.
// A numeric value is rejected with a hint pointing at min/max_replicas, which
// are the dedicated fields for fixed counts.
type ReplicasSpec struct {
	SpreadAll bool
}

func (r *ReplicasSpec) set(v any) error {
	switch t := v.(type) {
	case nil:
		r.SpreadAll = false
	case bool:
		r.SpreadAll = t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "all":
			r.SpreadAll = true
		case "", "auto":
			r.SpreadAll = false
		default:
			return fmt.Errorf("invalid replicas value %q (expected \"all\" or \"auto\")", t)
		}
	default:
		return fmt.Errorf("invalid replicas value %v (use min_replicas/max_replicas for a fixed count, or \"all\" to spread)", v)
	}
	return nil
}

// UnmarshalJSON implements json.Unmarshaler for the replicas alias.
func (r *ReplicasSpec) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	return r.set(v)
}

// UnmarshalYAML implements yaml.Unmarshaler for the replicas alias.
func (r *ReplicasSpec) UnmarshalYAML(value *yaml.Node) error {
	var v any
	if err := value.Decode(&v); err != nil {
		return err
	}
	return r.set(v)
}

// SeedSchedulingEntry is one entry in the env/file scheduling config. It mirrors
// the API's SetSchedulingRequest shape, plus the "replicas" alias and the
// canonical "spread_all" boolean.
type SeedSchedulingEntry struct {
	ModelName    string            `json:"model_name" yaml:"model_name"`
	NodeSelector map[string]string `json:"node_selector,omitempty" yaml:"node_selector,omitempty"`
	MinReplicas  int               `json:"min_replicas,omitempty" yaml:"min_replicas,omitempty"`
	MaxReplicas  int               `json:"max_replicas,omitempty" yaml:"max_replicas,omitempty"`
	Replicas     *ReplicasSpec     `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	SpreadAll    bool              `json:"spread_all,omitempty" yaml:"spread_all,omitempty"`

	RoutePolicy         string  `json:"route_policy,omitempty" yaml:"route_policy,omitempty"`
	BalanceAbsThreshold int     `json:"balance_abs_threshold,omitempty" yaml:"balance_abs_threshold,omitempty"`
	BalanceRelThreshold float64 `json:"balance_rel_threshold,omitempty" yaml:"balance_rel_threshold,omitempty"`
	MinPrefixMatch      float64 `json:"min_prefix_match,omitempty" yaml:"min_prefix_match,omitempty"`
}

// spread reports whether this entry requests spread-to-all-matching-nodes mode,
// via either the canonical spread_all field or the replicas alias.
func (e SeedSchedulingEntry) spread() bool {
	return e.SpreadAll || (e.Replicas != nil && e.Replicas.SpreadAll)
}

// ValidateSeedEntry enforces the invariants of a single scheduling entry. It
// mirrors the API's validateSchedulingRequest, with the added rule that spread
// mode is mutually exclusive with explicit min/max replica counts.
func ValidateSeedEntry(e SeedSchedulingEntry) error {
	if e.ModelName == "" {
		return fmt.Errorf("model_name is required")
	}
	if e.MinReplicas < 0 {
		return fmt.Errorf("min_replicas must be >= 0 (model %q)", e.ModelName)
	}
	if e.MaxReplicas < 0 {
		return fmt.Errorf("max_replicas must be >= 0 (model %q)", e.ModelName)
	}
	if e.spread() && (e.MinReplicas != 0 || e.MaxReplicas != 0) {
		return fmt.Errorf("spread (replicas: all) and min_replicas/max_replicas are mutually exclusive (model %q)", e.ModelName)
	}
	if e.MaxReplicas > 0 && e.MinReplicas > e.MaxReplicas {
		return fmt.Errorf("min_replicas must be <= max_replicas (model %q)", e.ModelName)
	}
	if err := prefixcache.ValidateThresholds(e.RoutePolicy, e.BalanceAbsThreshold, e.BalanceRelThreshold, e.MinPrefixMatch); err != nil {
		return fmt.Errorf("%w (model %q)", err, e.ModelName)
	}
	return nil
}

func (e SeedSchedulingEntry) toConfig() (ModelSchedulingConfig, error) {
	selectorJSON := ""
	if len(e.NodeSelector) > 0 {
		b, err := json.Marshal(e.NodeSelector)
		if err != nil {
			return ModelSchedulingConfig{}, fmt.Errorf("serializing node_selector for model %q: %w", e.ModelName, err)
		}
		selectorJSON = string(b)
	}
	return ModelSchedulingConfig{
		ModelName:           e.ModelName,
		NodeSelector:        selectorJSON,
		MinReplicas:         e.MinReplicas,
		MaxReplicas:         e.MaxReplicas,
		SpreadAll:           e.spread(),
		RoutePolicy:         e.RoutePolicy,
		BalanceAbsThreshold: e.BalanceAbsThreshold,
		BalanceRelThreshold: e.BalanceRelThreshold,
		MinPrefixMatch:      e.MinPrefixMatch,
	}, nil
}

// ParseSchedulingSeed parses the inline-JSON and/or YAML-file scheduling config
// into validated ModelSchedulingConfig rows ready to upsert. Entries from both
// sources are concatenated (jsonStr first, then the file). Either argument may
// be empty.
func ParseSchedulingSeed(jsonStr, configPath string) ([]ModelSchedulingConfig, error) {
	var entries []SeedSchedulingEntry

	if strings.TrimSpace(jsonStr) != "" {
		var fromJSON []SeedSchedulingEntry
		if err := json.Unmarshal([]byte(jsonStr), &fromJSON); err != nil {
			return nil, fmt.Errorf("parsing LOCALAI_MODEL_SCHEDULING JSON: %w", err)
		}
		entries = append(entries, fromJSON...)
	}

	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("reading model scheduling config %q: %w", configPath, err)
		}
		var fromYAML []SeedSchedulingEntry
		if err := yaml.Unmarshal(data, &fromYAML); err != nil {
			return nil, fmt.Errorf("parsing model scheduling config %q: %w", configPath, err)
		}
		entries = append(entries, fromYAML...)
	}

	configs := make([]ModelSchedulingConfig, 0, len(entries))
	for _, e := range entries {
		if err := ValidateSeedEntry(e); err != nil {
			return nil, err
		}
		cfg, err := e.toConfig()
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}
