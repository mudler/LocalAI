package localaitools

import "github.com/mudler/LocalAI/core/services/nodes"

func SchedulingConfigFromNode(config nodes.ModelSchedulingConfig) ModelSchedulingConfig {
	return ModelSchedulingConfig{
		ID:                  config.ID,
		ModelName:           config.ModelName,
		NodeSelector:        config.NodeSelector,
		MinReplicas:         config.MinReplicas,
		MaxReplicas:         config.MaxReplicas,
		SpreadAll:           config.SpreadAll,
		RoutePolicy:         config.RoutePolicy,
		BalanceAbsThreshold: config.BalanceAbsThreshold,
		BalanceRelThreshold: config.BalanceRelThreshold,
		MinPrefixMatch:      config.MinPrefixMatch,
		UnsatisfiableUntil:  config.UnsatisfiableUntil,
		UnsatisfiableTicks:  config.UnsatisfiableTicks,
		CreatedAt:           config.CreatedAt,
		UpdatedAt:           config.UpdatedAt,
	}
}

func SchedulingConfigsFromNodes(configs []nodes.ModelSchedulingConfig) []ModelSchedulingConfig {
	out := make([]ModelSchedulingConfig, 0, len(configs))
	for _, config := range configs {
		out = append(out, SchedulingConfigFromNode(config))
	}
	return out
}
