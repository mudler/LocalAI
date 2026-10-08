package messaging

// NATSRouteForTest exposes the route table so the external specs can pin the
// queue group per kind, which no producer-side behaviour reveals.
var NATSRouteForTest = natsRoute

// BroadcastRootsForTest returns the broadcast roots of the subject rules.
func BroadcastRootsForTest() []string {
	roots := make([]string, 0, len(broadcastRoots))
	for r := range broadcastRoots {
		roots = append(roots, r)
	}
	return roots
}
