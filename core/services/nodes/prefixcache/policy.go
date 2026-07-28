// Package prefixcache implements prefix-cache-aware routing for distributed
// mode: it turns a request prompt into a chain of prefix hashes, tracks which
// node served which prefix in an in-memory radix tree, and provides a
// load-guarded preferred-node decision. See docs/content/features/distributed-mode.md.
package prefixcache

// RoutePolicy selects the routing strategy for a model. The zero value is
// RoutePolicyDefault, meaning "inherit the cluster-wide default".
type RoutePolicy int

const (
	RoutePolicyDefault     RoutePolicy = iota // inherit global default
	RoutePolicyRoundRobin                     // today's behavior (the floor)
	RoutePolicyPrefixCache                    // cache-aware routing
)

// ParsePolicy maps a config string to a RoutePolicy. Unknown or empty strings
// map to RoutePolicyDefault.
func ParsePolicy(s string) RoutePolicy {
	switch s {
	case "round_robin":
		return RoutePolicyRoundRobin
	case "prefix_cache":
		return RoutePolicyPrefixCache
	default:
		return RoutePolicyDefault
	}
}

func (p RoutePolicy) String() string {
	switch p {
	case RoutePolicyRoundRobin:
		return "round_robin"
	case RoutePolicyPrefixCache:
		return "prefix_cache"
	default:
		return "default"
	}
}

// Resolve returns p unless it is Default, in which case it returns global.
func (p RoutePolicy) Resolve(global RoutePolicy) RoutePolicy {
	if p == RoutePolicyDefault {
		return global
	}
	return p
}
