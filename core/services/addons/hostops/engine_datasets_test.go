// SPDX-License-Identifier: MIT
package hostops

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type fakeDatasets struct {
	n       int
	puts    int
	gotName string
	gotData []byte
	paths   map[string]string
}

func (f *fakeDatasets) Put(name string, data []byte) (string, error) {
	f.puts++
	f.gotName = name
	f.gotData = data
	f.n++
	ref := fmt.Sprintf("%08x-%s", f.n, name)
	if f.paths == nil {
		f.paths = map[string]string{}
	}
	f.paths[ref] = "/srv/data/fine-tune/datasets/" + ref
	return ref, nil
}

func (f *fakeDatasets) Resolve(ref string) (string, bool) {
	p, ok := f.paths[ref]
	return p, ok
}

func datasetArgs(t *testing.T, name, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"name": name, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func datasetBroker(t *testing.T, ds DatasetStore) (*Broker, Spec) {
	t.Helper()
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	if err := RegisterEngineDatasets(b, ds); err != nil {
		t.Fatal(err)
	}
	return b, b.specs["finetune.dataset"]
}

func TestDatasetStoresTheContentAndReturnsARefNotAPath(t *testing.T) {
	ds := &fakeDatasets{}
	_, s := datasetBroker(t, ds)
	raw, err := call(t, s, "named-user", "u1", datasetArgs(t, "train.jsonl", "{\"a\":1}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		DatasetRef string `json:"datasetRef"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.DatasetRef == "" || strings.Contains(out.DatasetRef, "/") {
		t.Fatalf("ref %q", out.DatasetRef)
	}
	if ds.gotName != "train.jsonl" || string(ds.gotData) != "{\"a\":1}\n" {
		t.Fatalf("stored %q %q", ds.gotName, ds.gotData)
	}
	if strings.Contains(string(raw), "/srv") {
		t.Fatalf("a path in the result: %s", raw)
	}
}

func TestDatasetIsIdempotentAndFineTuningOnly(t *testing.T) {
	_, s := datasetBroker(t, &fakeDatasets{})
	if !s.Idempotent || s.Scope != "apply" || s.Feature != "fine_tuning" {
		t.Fatalf("%+v", s)
	}
}

func TestDatasetRefusesAnEmptyOrOversizedContent(t *testing.T) {
	ds := &fakeDatasets{}
	_, s := datasetBroker(t, ds)
	limit := 900 * 1024
	if _, err := call(t, s, "named-user", "u1", datasetArgs(t, "a.txt", strings.Repeat("x", limit))); err != nil {
		t.Fatalf("exactly 900 KiB refused: %v", err)
	}
	for name, content := range map[string]string{
		"empty":    "",
		"one over": strings.Repeat("x", limit+1),
		"far over": strings.Repeat("x", 2*limit),
	} {
		if _, err := call(t, s, "named-user", "u1", datasetArgs(t, "a.txt", content)); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%s: want ErrBadRequest, got %v", name, err)
		}
	}
	if ds.puts != 1 {
		t.Fatalf("a refused dataset was stored: %d puts", ds.puts)
	}
}

func TestDatasetRefusesANameWithADirectory(t *testing.T) {
	ds := &fakeDatasets{}
	_, s := datasetBroker(t, ds)
	for _, name := range []string{"../x.jsonl", "a/b.jsonl", "/etc/x.jsonl", `a\b.jsonl`, ".."} {
		if _, err := call(t, s, "named-user", "u1", datasetArgs(t, name, "[]")); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%q: want ErrBadRequest, got %v", name, err)
		}
	}
	if ds.puts != 0 {
		t.Fatalf("a refused name was stored")
	}
}

func TestDatasetRefusesAnExtensionThatIsNotATextDataset(t *testing.T) {
	ds := &fakeDatasets{}
	_, s := datasetBroker(t, ds)
	for _, name := range []string{"x.exe", "x.sh", "x", "x.jsonl.exe", ".jsonl.bin"} {
		if _, err := call(t, s, "named-user", "u1", datasetArgs(t, name, "[]")); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%q: want ErrBadRequest, got %v", name, err)
		}
	}
	for _, name := range []string{"x.jsonl", "x.json", "x.csv", "x.txt"} {
		if _, err := call(t, s, "named-user", "u1", datasetArgs(t, name, "[]")); err != nil {
			t.Fatalf("%q refused: %v", name, err)
		}
	}
}

func TestDatasetRefusesContentThatIsNotUTF8(t *testing.T) {
	ds := &fakeDatasets{}
	_, s := datasetBroker(t, ds)
	if _, err := call(t, s, "named-user", "u1", "{\"name\":\"x.csv\",\"content\":\"a,b\\n\xff\xfe,1\\n\"}"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v", err)
	}
	if ds.puts != 0 {
		t.Fatalf("non-UTF-8 content was stored")
	}
}

func TestResolveOnlyReturnsAReferenceTheHostIssuedToThatUser(t *testing.T) {
	ds := &fakeDatasets{}
	b, s := datasetBroker(t, ds)
	raw, err := call(t, s, "named-user", "userA", datasetArgs(t, "x.jsonl", "[]"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		DatasetRef string `json:"datasetRef"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.resolveDataset("userB", out.DatasetRef); ok {
		t.Fatal("user B resolved the reference of user A")
	}
	if p, ok := b.resolveDataset("userA", out.DatasetRef); !ok || p == "" {
		t.Fatalf("the issuing user cannot resolve its own reference: %q %v", p, ok)
	}
}

func TestTheIssuedReferencesAreBoundedTo256(t *testing.T) {
	ds := &fakeDatasets{}
	b, s := datasetBroker(t, ds)
	var first, last string
	for i := range 257 {
		raw, err := call(t, s, "named-user", "u1", datasetArgs(t, "x.jsonl", "[]"))
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			DatasetRef string `json:"datasetRef"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = out.DatasetRef
		}
		last = out.DatasetRef
	}
	if _, ok := b.resolveDataset("u1", first); ok {
		t.Fatal("the oldest reference is still issued after 257 datasets")
	}
	if _, ok := b.resolveDataset("u1", last); !ok {
		t.Fatal("the newest reference is not issued")
	}
}
