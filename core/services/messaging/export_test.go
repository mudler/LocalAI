package messaging

// BroadcastRootsForTest returns the broadcast roots of the subject rules.
func BroadcastRootsForTest() []string {
	roots := make([]string, 0, len(broadcastRoots))
	for r := range broadcastRoots {
		roots = append(roots, r)
	}
	return roots
}
