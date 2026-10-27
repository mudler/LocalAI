package connectors_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connectors/connectorstest"
)

func TestJSONInMemory(t *testing.T) {
	connectorstest.Run(t, func(t *testing.T) connectors.Store {
		s, err := connectors.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	connectorstest.Run(t, func(t *testing.T) connectors.Store {
		s, err := connectors.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONSurvivesReopenAndKeepsItsFilesPrivate(t *testing.T) {
	dir := t.TempDir()
	first, _ := connectors.NewJSON(dir)
	connectorstest.Seed(t, first)
	second, err := connectors.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	connectorstest.CheckSeed(t, second)
	for _, f := range []string{"connectors.json", "connectors.key"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, st, err)
		}
	}
}

func TestJSONRefusesAKeyFileThatOthersCanRead(t *testing.T) {
	dir := t.TempDir()
	first, _ := connectors.NewJSON(dir)
	connectorstest.Seed(t, first)
	if err := os.Chmod(filepath.Join(dir, "connectors.key"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := connectors.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.EnsureKey(t.Context(), connectors.NewKey); err == nil {
		t.Fatal("a key file with wider permissions was used")
	}
}
