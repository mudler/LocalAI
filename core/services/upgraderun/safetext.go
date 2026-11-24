package upgraderun

import "github.com/mudler/LocalAI/pkg/sanitize"

// MaxItemText bounds, in runes, the error and the message of an item and the
// message of each of its nodes.
const MaxItemText = 512

// safeText is the error or message of an item as every reader sees it. The
// text of an operation can carry a URL with a password, a token, a path of
// the server or the last lines of a backend's output, and a run is read by
// the REST routes, the Updates page, the inbox and the assistant (which puts
// it in the context of a model). The text is cleaned once, where it enters the
// item, so every surface gets the same safe text (see sanitize.Text).
func safeText(s string) string { return sanitize.Text(s, MaxItemText) }

// safeNodes returns nodes with each message cleaned by safeText.
func safeNodes(nodes []NodeState) []NodeState {
	if nodes == nil {
		return nil
	}
	out := make([]NodeState, len(nodes))
	for i, n := range nodes {
		n.Message = safeText(n.Message)
		out[i] = n
	}
	return out
}
