// SPDX-License-Identifier: MIT
package hostops

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func datasetArgs(name, content string) string {
	b, err := json.Marshal(map[string]string{"name": name, "content": content})
	Expect(err).ToNot(HaveOccurred())
	return string(b)
}

func datasetBroker(ds DatasetStore) (*Broker, Spec) {
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	Expect(RegisterEngineDatasets(b, ds)).To(Succeed())
	return b, b.specs["finetune.dataset"]
}

type datasetResult struct {
	DatasetRef string `json:"datasetRef"`
}

var _ = Describe("Dataset operation", func() {
	It("stores the content and returns a ref, not a path", func() {
		ds := &fakeDatasets{}
		_, s := datasetBroker(ds)
		raw, err := call(s, "named-user", "u1", datasetArgs("train.jsonl", "{\"a\":1}\n"))
		Expect(err).ToNot(HaveOccurred())
		var out datasetResult
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out.DatasetRef).ToNot(BeEmpty())
		Expect(out.DatasetRef).ToNot(ContainSubstring("/"))
		Expect(ds.gotName).To(Equal("train.jsonl"))
		Expect(string(ds.gotData)).To(Equal("{\"a\":1}\n"))
		Expect(string(raw)).ToNot(ContainSubstring("/srv"), "a path in the result")
	})

	It("is idempotent and fine-tuning only", func() {
		_, s := datasetBroker(&fakeDatasets{})
		Expect(s.Idempotent).To(BeTrue())
		Expect(s.Scope).To(Equal("apply"))
		Expect(s.Feature).To(Equal("fine_tuning"))
	})

	It("refuses an empty or oversized content", func() {
		ds := &fakeDatasets{}
		_, s := datasetBroker(ds)
		limit := 900 * 1024
		_, err := call(s, "named-user", "u1", datasetArgs("a.txt", strings.Repeat("x", limit)))
		Expect(err).ToNot(HaveOccurred(), "exactly 900 KiB refused")
		for name, content := range map[string]string{
			"empty":    "",
			"one over": strings.Repeat("x", limit+1),
			"far over": strings.Repeat("x", 2*limit),
		} {
			_, err := call(s, "named-user", "u1", datasetArgs("a.txt", content))
			Expect(err).To(MatchError(ErrBadRequest), name)
		}
		Expect(ds.puts).To(Equal(1), "a refused dataset was stored")
	})

	It("refuses a name with a directory", func() {
		ds := &fakeDatasets{}
		_, s := datasetBroker(ds)
		for _, name := range []string{"../x.jsonl", "a/b.jsonl", "/etc/x.jsonl", `a\b.jsonl`, ".."} {
			_, err := call(s, "named-user", "u1", datasetArgs(name, "[]"))
			Expect(err).To(MatchError(ErrBadRequest), name)
		}
		Expect(ds.puts).To(Equal(0), "a refused name was stored")
	})

	It("refuses an extension that is not a text dataset", func() {
		ds := &fakeDatasets{}
		_, s := datasetBroker(ds)
		for _, name := range []string{"x.exe", "x.sh", "x", "x.jsonl.exe", ".jsonl.bin"} {
			_, err := call(s, "named-user", "u1", datasetArgs(name, "[]"))
			Expect(err).To(MatchError(ErrBadRequest), name)
		}
		for _, name := range []string{"x.jsonl", "x.json", "x.csv", "x.txt"} {
			_, err := call(s, "named-user", "u1", datasetArgs(name, "[]"))
			Expect(err).ToNot(HaveOccurred(), "%q refused", name)
		}
	})

	It("refuses content that is not UTF-8", func() {
		ds := &fakeDatasets{}
		_, s := datasetBroker(ds)
		_, err := call(s, "named-user", "u1", "{\"name\":\"x.csv\",\"content\":\"a,b\\n\xff\xfe,1\\n\"}")
		Expect(err).To(MatchError(ErrBadRequest))
		Expect(ds.puts).To(Equal(0), "non-UTF-8 content was stored")
	})

	It("resolves only a reference the host issued to that user", func() {
		ds := &fakeDatasets{}
		b, s := datasetBroker(ds)
		raw, err := call(s, "named-user", "userA", datasetArgs("x.jsonl", "[]"))
		Expect(err).ToNot(HaveOccurred())
		var out datasetResult
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		_, ok := b.resolveDataset("userB", out.DatasetRef)
		Expect(ok).To(BeFalse(), "user B resolved the reference of user A")
		p, ok := b.resolveDataset("userA", out.DatasetRef)
		Expect(ok).To(BeTrue(), "the issuing user cannot resolve its own reference")
		Expect(p).ToNot(BeEmpty())
	})

	It("bounds the issued references to 256", func() {
		ds := &fakeDatasets{}
		b, s := datasetBroker(ds)
		var first, last string
		for i := range 257 {
			raw, err := call(s, "named-user", "u1", datasetArgs("x.jsonl", "[]"))
			Expect(err).ToNot(HaveOccurred())
			var out datasetResult
			Expect(json.Unmarshal(raw, &out)).To(Succeed())
			if i == 0 {
				first = out.DatasetRef
			}
			last = out.DatasetRef
		}
		_, ok := b.resolveDataset("u1", first)
		Expect(ok).To(BeFalse(), "the oldest reference is still issued after 257 datasets")
		_, ok = b.resolveDataset("u1", last)
		Expect(ok).To(BeTrue(), "the newest reference is not issued")
	})
})
