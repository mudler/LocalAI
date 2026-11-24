package worker

// processSnapshotEntry is one row of a process table snapshot.
type processSnapshotEntry struct {
	PID     int
	PPID    int
	Created uint64 // creation time; 0 when unknown
}

// processTreeOf returns root followed by all its descendants in the snapshot.
//
// Windows keeps the parent PID of a process after the parent exits, and PIDs
// are reused, so a recorded parent PID alone can point at an unrelated newer
// process. A child is therefore accepted only when it was created at or after
// its parent. Entries with an unknown creation time are accepted, because
// there is nothing to compare.
func processTreeOf(root int, snapshot []processSnapshotEntry) []int {
	byPID := make(map[int]processSnapshotEntry, len(snapshot))
	children := make(map[int][]processSnapshotEntry)
	for _, e := range snapshot {
		byPID[e.PID] = e
		if e.PPID != e.PID {
			children[e.PPID] = append(children[e.PPID], e)
		}
	}
	tree := []int{root}
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		parentCreated := byPID[parent].Created
		for _, child := range children[parent] {
			if seen[child.PID] {
				continue
			}
			if parentCreated != 0 && child.Created != 0 && child.Created < parentCreated {
				continue
			}
			seen[child.PID] = true
			tree = append(tree, child.PID)
			queue = append(queue, child.PID)
		}
	}
	return tree
}
