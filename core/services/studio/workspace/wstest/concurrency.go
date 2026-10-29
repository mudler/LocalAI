package wstest

import (
	"math/rand"
	"path"
	"strconv"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func describeConcurrency(newStore NewStore) {
	Describe("Concurrency", func() {
		It("concurrent writes to one file lose nothing", func() {
			s := newStore()
			base := MustPut(s, AdminActor, "/c/report.txt", "start")
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
				Expect(r.err).ToNot(HaveOccurred())
				if r.node.ConflictOf == "" {
					winners++
				} else {
					Expect(r.node.ConflictOf).To(Equal("/c/report.txt"))
				}
			}
			Expect(winners).To(Equal(1), "writers won the overwrite, want 1")
			got := map[string]bool{}
			for p, c := range Tree(s) {
				if strings.HasPrefix(p, "/c/report") {
					got[c] = true
				}
			}
			for i := 0; i < n; i++ {
				Expect(got).To(HaveKey("v"+strconv.Itoa(i)), "lost write v%d", i)
			}
		})
		It("mixed calls keep sibling names unique", func() {
			s := newStore()
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
			var unexpected []error
			for err := range errs {
				unexpected = append(unexpected, err)
			}
			Expect(unexpected).To(BeEmpty(), "unexpected errors")
			groups := map[string][]string{}
			for p := range Tree(s) {
				key := path.Dir(p) + "|" + strings.ToLower(path.Base(p))
				groups[key] = append(groups[key], p)
			}
			for key, ps := range groups {
				Expect(ps).To(HaveLen(1), "names under %s differ only in case", key)
			}
			Expect(groups).ToNot(BeEmpty(), "the tree is empty: no call succeeded")
		})
	})
}
