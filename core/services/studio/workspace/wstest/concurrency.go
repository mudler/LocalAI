//nolint:forbidigo // reusable contract suite driven by the caller's *testing.T
package wstest

import (
	"math/rand"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func testConcurrency(t *testing.T, newStore NewStore) {
	t.Run("concurrent writes to one file lose nothing", func(t *testing.T) {
		s := newStore(t)
		base := MustPut(t, s, AdminActor, "/c/report.txt", "start")
		const n = 8
		var wg sync.WaitGroup
		type result struct {
			node workspace.Node
			err  error
		}
		res := make(chan result, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				a := workspace.Actor{Principal: Admin, ChatID: strconv.Itoa(i)}
				node, err := s.Put(Ctx(), a, "/c/report.txt", strings.NewReader("v"+strconv.Itoa(i)), workspace.PutOptions{BaseVersion: base.Version})
				res <- result{node, err}
			}(i)
		}
		wg.Wait()
		close(res)
		winners := 0
		for r := range res {
			if r.err != nil {
				t.Fatal(r.err)
			}
			if r.node.ConflictOf == "" {
				winners++
			} else if r.node.ConflictOf != "/c/report.txt" {
				t.Errorf("ConflictOf = %q", r.node.ConflictOf)
			}
		}
		if winners != 1 {
			t.Errorf("%d writers won the overwrite, want 1", winners)
		}
		got := map[string]bool{}
		for p, c := range Tree(t, s) {
			if strings.HasPrefix(p, "/c/report") {
				got[c] = true
			}
		}
		for i := 0; i < n; i++ {
			if !got["v"+strconv.Itoa(i)] {
				t.Errorf("lost write v%d", i)
			}
		}
	})
	t.Run("mixed calls keep sibling names unique", func(t *testing.T) {
		s := newStore(t)
		names := []string{"a.txt", "A.txt", "b.txt"}
		var wg sync.WaitGroup
		errs := make(chan error, 6*30)
		for g := 0; g < 6; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(g))) // #nosec G404 -- a seeded generator makes the test reproducible
				for i := 0; i < 30; i++ {
					p := "/m/" + names[rng.Intn(len(names))]
					var err error
					switch rng.Intn(3) {
					case 0:
						_, err = s.Put(Ctx(), AdminActor, p, strings.NewReader(strconv.Itoa(i)), workspace.PutOptions{Overwrite: true})
					case 1:
						_, err = s.Move(Ctx(), AdminActor, p, "/m/"+names[rng.Intn(len(names))])
					default:
						err = s.Delete(Ctx(), AdminActor, p)
					}
					if err != nil && !expectedOpError(err) {
						errs <- err
					}
				}
			}(g)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("unexpected error: %v", err)
		}
		groups := map[string][]string{}
		for p := range Tree(t, s) {
			key := path.Dir(p) + "|" + strings.ToLower(path.Base(p))
			groups[key] = append(groups[key], p)
		}
		for key, ps := range groups {
			if len(ps) > 1 {
				t.Errorf("names under %s differ only in case: %v", key, ps)
			}
		}
		if len(groups) == 0 {
			t.Error("the tree is empty: no call succeeded")
		}
	})
}
