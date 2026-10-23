// SPDX-License-Identifier: MIT
package workerstatus

import (
	"sync"
	"testing"
	"time"
)

// A reader that opened the snapshot just before it was replaced holds an inode
// with no links left. That file was complete and owner-only, so it is valid
// evidence; only an extra hard link (more than one) is a reason to refuse it.
func TestReadSurvivesReplacementWhileOpen(t *testing.T) {
	for round := 0; round < 200; round++ {
		path, generation, err := NewSession(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		s := Snapshot{PID: 1, Schema: Schema, Generation: generation, Sequence: 1, Time: time.Now(), Registration: "starting", HeartbeatInterval: 10 * time.Second}
		if err := Write(path, s); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		stop := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s.Sequence++
					if err := Write(path, s); err != nil {
						t.Errorf("write: %v", err)
						return
					}
				}
			}
		}()
		for i := 0; i < 40 && !t.Failed(); i++ {
			if _, err := Read(path, generation); err != nil {
				t.Errorf("round %d read %d: %v", round, i, err)
			}
		}
		close(stop)
		wg.Wait()
		if t.Failed() {
			return
		}
	}
}
