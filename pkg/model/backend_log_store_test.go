package model

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

// TestGetLines_PrefixAggregation pins the multi-replica behavior added when
// the worker's process key changed from `modelID` to `modelID#replicaIndex`.
// The frontend still asks for logs of `qwen3-0.6b`, but the actual buffers
// live under `qwen3-0.6b#0` and `qwen3-0.6b#1` — without aggregation,
// operators see no logs in distributed mode.
func TestGetLines_PrefixAggregation(t *testing.T) {
	s := NewBackendLogStore(100)

	// Two replicas of the same model, plus a different model that should
	// never leak in. AppendLine timestamps via time.Now(), so add small
	// sleeps so the merged order is deterministic.
	s.AppendLine("qwen3-0.6b#0", "stderr", "r0-line-1")
	time.Sleep(2 * time.Millisecond)
	s.AppendLine("qwen3-0.6b#1", "stderr", "r1-line-1")
	time.Sleep(2 * time.Millisecond)
	s.AppendLine("qwen3-0.6b#0", "stdout", "r0-line-2")
	time.Sleep(2 * time.Millisecond)
	s.AppendLine("other-model#0", "stderr", "should-not-appear")

	got := s.GetLines("qwen3-0.6b")
	var texts []string
	for _, l := range got {
		texts = append(texts, l.Text)
	}
	want := []string{"r0-line-1", "r1-line-1", "r0-line-2"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("aggregated texts = %v, want %v", texts, want)
	}

	// Per-replica filtering: full process key returns only that replica.
	r0 := s.GetLines("qwen3-0.6b#0")
	if len(r0) != 2 {
		t.Fatalf("replica 0 should have 2 lines, got %d", len(r0))
	}
	for _, l := range r0 {
		if l.Text == "r1-line-1" {
			t.Fatalf("replica 0 must not include replica 1's lines")
		}
	}

	// No matching replica: empty slice (not nil; existing callers rely on len()).
	if got := s.GetLines("never-loaded-model"); len(got) != 0 {
		t.Fatalf("unknown model should yield empty slice, got %v", got)
	}
}

// TestListModels_DedupReplicas confirms the /v1/backend-logs listing shows
// one entry per model, not one per replica — operators don't think about
// replica indexes; they pick a model.
func TestListModels_DedupReplicas(t *testing.T) {
	s := NewBackendLogStore(100)
	s.AppendLine("model-a#0", "stderr", "x")
	s.AppendLine("model-a#1", "stderr", "y")
	s.AppendLine("model-b#0", "stderr", "z")
	s.AppendLine("model-c", "stderr", "no-replica-suffix") // back-compat for non-distributed

	got := s.ListModels()
	sort.Strings(got)
	want := []string{"model-a", "model-b", "model-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListModels = %v, want %v", got, want)
	}
}

// TestSubscribe_AggregatesAcrossReplicas confirms the WebSocket streaming
// path (the live tail UI) receives lines from every replica when the
// caller subscribes by bare modelID.
func TestSubscribe_AggregatesAcrossReplicas(t *testing.T) {
	s := NewBackendLogStore(100)

	// Pre-create both replica buffers so Subscribe can find them.
	s.AppendLine("model-a#0", "stderr", "preload-r0")
	s.AppendLine("model-a#1", "stderr", "preload-r1")

	ch, unsubscribe := s.Subscribe("model-a")
	defer unsubscribe()

	// Emit one line per replica after subscribing.
	s.AppendLine("model-a#0", "stderr", "live-r0")
	s.AppendLine("model-a#1", "stderr", "live-r1")
	// Different model — must not appear.
	s.AppendLine("model-b#0", "stderr", "leak-check")

	seen := map[string]bool{}
	deadline := time.After(500 * time.Millisecond)
	for len(seen) < 2 {
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatalf("subscribe channel closed early; saw %v", seen)
			}
			seen[line.Text] = true
			if line.Text == "leak-check" {
				t.Fatalf("subscribe leaked a line from a different model")
			}
		case <-deadline:
			t.Fatalf("timed out waiting for fan-in lines; saw %v", seen)
		}
	}
	if !seen["live-r0"] || !seen["live-r1"] {
		t.Fatalf("missing live lines from replicas: saw %v", seen)
	}
}

// TestSubscribe_PerReplicaFilter pins that callers passing the full process
// key get only that replica — useful for a future per-replica logs view.
func TestSubscribe_PerReplicaFilter(t *testing.T) {
	s := NewBackendLogStore(100)

	ch, unsubscribe := s.Subscribe("model-a#0")
	defer unsubscribe()

	s.AppendLine("model-a#0", "stderr", "wanted")
	s.AppendLine("model-a#1", "stderr", "unwanted")

	select {
	case line := <-ch:
		if line.Text != "wanted" {
			t.Fatalf("expected line from replica 0, got %q", line.Text)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("no line received from replica-scoped subscription")
	}

	// Drain quickly: confirm replica 1 didn't leak in.
	select {
	case line := <-ch:
		t.Fatalf("replica-scoped sub leaked line from replica 1: %q", line.Text)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestSubscribe_NoRaceOnUnsubscribe pins the panic CI hit:
// the aggregated fan-in goroutine sending on the merged channel
// raced with unsubscribe closing it. Hammer AppendLine concurrently
// with Subscribe + unsubscribe + Remove to make sure neither
// "send on closed channel" nor "close of closed channel" panics
// can resurface. The race detector should catch any regression.
func TestSubscribe_NoRaceOnUnsubscribe(t *testing.T) {
	s := NewBackendLogStore(100)

	// Pre-create replica buffers so Subscribe finds them.
	for r := 0; r < 3; r++ {
		s.AppendLine(fmt.Sprintf("model-x#%d", r), "stderr", "preload")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writers — keep the per-buffer channels under constant pressure.
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			id := fmt.Sprintf("model-x#%d", r)
			for {
				select {
				case <-stop:
					return
				default:
					s.AppendLine(id, "stderr", "burst")
				}
			}
		}(r)
	}

	// Subscribers — repeatedly Subscribe and unsubscribe while writers run.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ch, unsubscribe := s.Subscribe("model-x")
				// Drain a couple of lines, then unsubscribe.
				select {
				case <-ch:
				case <-time.After(2 * time.Millisecond):
				}
				unsubscribe()
			}
		}()
	}

	// Buffer reaper — exercises the Remove path while subscribers are live.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			s.Remove("model-x#0")
			s.AppendLine("model-x#0", "stderr", "respawn")
			time.Sleep(time.Millisecond)
		}
	}()

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}
