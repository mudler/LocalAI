package wstest

import (
	"errors"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var (
	propDirs  = []string{"/a", "/b", "/a/c"}
	propNames = []string{"x.txt", "y.txt", "Z.txt", "z.txt"}
)

func expectedOpError(err error) bool {
	return errors.Is(err, workspace.ErrExists) || errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid)
}

// randomOp runs one random call in its own Tx and returns the change set,
// or a zero ChangeSet when the call was refused or changed nothing.
func randomOp(s workspace.Store, rng *rand.Rand) workspace.ChangeSet {
	GinkgoHelper()
	tree := Tree(s) // read before Begin: a Tx holds the only connection
	var paths []string
	for p := range tree {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	target := propDirs[rng.Intn(len(propDirs))] + "/" + propNames[rng.Intn(len(propNames))]
	tx, err := s.Begin(Ctx(), AdminActor, "op")
	Expect(err).ToNot(HaveOccurred())
	var opErr error
	switch k := rng.Intn(4); {
	case k == 0:
		_, opErr = tx.Put(Ctx(), target, strings.NewReader(strconv.Itoa(rng.Intn(3))), workspace.PutOptions{Overwrite: true})
	case k == 1:
		_, opErr = tx.Mkdir(Ctx(), propDirs[rng.Intn(len(propDirs))]+"/m"+strconv.Itoa(rng.Intn(2)))
	case len(paths) == 0:
		opErr = workspace.ErrNotFound
	case k == 2:
		_, opErr = tx.Move(Ctx(), paths[rng.Intn(len(paths))], target)
	default:
		opErr = tx.Delete(Ctx(), paths[rng.Intn(len(paths))])
	}
	if opErr != nil {
		Expect(expectedOpError(opErr)).To(BeTrue(), "unexpected error: %v", opErr)
		_ = tx.Rollback()
		return workspace.ChangeSet{}
	}
	cs, err := tx.Commit()
	Expect(err).ToNot(HaveOccurred())
	return cs
}

func runOps(s workspace.Store, seed int64, n int) []int64 {
	GinkgoHelper()
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- a seeded generator makes the test reproducible
	var revs []int64
	for i := 0; i < n; i++ {
		if cs := randomOp(s, rng); cs.Rev != 0 {
			revs = append(revs, cs.Rev)
		}
	}
	return revs
}

func describeProperty(newStore NewStore) {
	Describe("Property", func() {
		It("undo everything in reverse gives the start tree", func() {
			for seed := int64(1); seed <= 5; seed++ {
				s := newStore()
				start := Tree(s)
				revs := runOps(s, seed, 25)
				Expect(len(revs)).To(BeNumerically(">=", 10), "seed %d: too few revisions committed", seed)
				for i := len(revs) - 1; i >= 0; i-- {
					_, conflicts, err := s.Undo(Ctx(), AdminActor, revs[i], workspace.UndoOptions{})
					Expect(err).ToNot(HaveOccurred(), "seed %d: undo %d: %+v", seed, revs[i], conflicts)
				}
				Expect(Tree(s)).To(Equal(start), "seed %d", seed)
			}
		})
		It("the same calls give the same tree", func() {
			for seed := int64(1); seed <= 5; seed++ {
				s1, s2 := newStore(), newStore()
				r1, r2 := runOps(s1, seed, 25), runOps(s2, seed, 25)
				Expect(r1).To(Equal(r2), "seed %d: two stores differ", seed)
				Expect(Tree(s1)).To(Equal(Tree(s2)), "seed %d: two stores differ", seed)
			}
		})
	})
}
