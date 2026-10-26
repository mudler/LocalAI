package wstest

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

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
func randomOp(t *testing.T, s workspace.Store, rng *rand.Rand) workspace.ChangeSet {
	t.Helper()
	tree := Tree(t, s) // read before Begin: a Tx holds the only connection
	var paths []string
	for p := range tree {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	target := propDirs[rng.Intn(len(propDirs))] + "/" + propNames[rng.Intn(len(propNames))]
	tx, err := s.Begin(Ctx(), AdminActor, "op")
	if err != nil {
		t.Fatal(err)
	}
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
		if !expectedOpError(opErr) {
			t.Fatalf("unexpected error: %v", opErr)
		}
		_ = tx.Rollback()
		return workspace.ChangeSet{}
	}
	cs, err := tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func runOps(t *testing.T, s workspace.Store, seed int64, n int) []int64 {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var revs []int64
	for i := 0; i < n; i++ {
		if cs := randomOp(t, s, rng); cs.Rev != 0 {
			revs = append(revs, cs.Rev)
		}
	}
	return revs
}

func testProperty(t *testing.T, newStore NewStore) {
	t.Run("undo everything in reverse gives the start tree", func(t *testing.T) {
		for seed := int64(1); seed <= 5; seed++ {
			s := newStore(t)
			start := Tree(t, s)
			revs := runOps(t, s, seed, 25)
			if len(revs) < 10 {
				t.Fatalf("seed %d: only %d revisions committed, want at least 10", seed, len(revs))
			}
			for i := len(revs) - 1; i >= 0; i-- {
				if _, conflicts, err := s.Undo(Ctx(), AdminActor, revs[i], workspace.UndoOptions{}); err != nil {
					t.Fatalf("seed %d: undo %d: %v %+v", seed, revs[i], err, conflicts)
				}
			}
			if got := Tree(t, s); !reflect.DeepEqual(got, start) {
				t.Fatalf("seed %d: tree = %v, want %v", seed, got, start)
			}
		}
	})
	t.Run("the same calls give the same tree", func(t *testing.T) {
		for seed := int64(1); seed <= 5; seed++ {
			s1, s2 := newStore(t), newStore(t)
			r1, r2 := runOps(t, s1, seed, 25), runOps(t, s2, seed, 25)
			if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(Tree(t, s1), Tree(t, s2)) {
				t.Fatalf("seed %d: two stores differ", seed)
			}
		}
	})
}
