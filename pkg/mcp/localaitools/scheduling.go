package localaitools

import "github.com/mudler/LocalAI/core/services/nodes"

func SchedulingConfigFromNode(config nodes.ModelSchedulingConfig) ModelSchedulingConfig {
	return ModelSchedulingConfig{
		ModelName:           config.ModelName,
		NodeSelector:        config.NodeSelector,
		MinReplicas:         config.MinReplicas,
		MaxReplicas:         config.MaxReplicas,
		SpreadAll:           config.SpreadAll,
		RoutePolicy:         config.RoutePolicy,
		BalanceAbsThreshold: config.BalanceAbsThreshold,
		BalanceRelThreshold: config.BalanceRelThreshold,
		MinPrefixMatch:      config.MinPrefixMatch,
	}
}

func SchedulingConfigsFromNodes(configs []nodes.ModelSchedulingConfig) []ModelSchedulingConfig {
	out := make([]ModelSchedulingConfig, 0, len(configs))
	for _, config := range configs {
		out = append(out, SchedulingConfigFromNode(config))
	}
	return out
}
