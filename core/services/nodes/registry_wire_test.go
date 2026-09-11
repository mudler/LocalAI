package nodes

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// NodeModel.WorkerLocalAddress was called Address. The Go field was renamed so
// no reader takes it for a frontend-dialable endpoint; the json key was kept so
// no API consumer breaks, and the gorm column was kept so no migration is
// needed.
//
// The column half is enforced by the raw-SQL fragments in this package, which
// fail loudly against a renamed column. The json half had nothing enforcing it:
// renaming only the tag left this package, messaging and endpoints/localai all
// green. These specs are that half.
var _ = Describe("NodeModel wire format", func() {
	It("serves the address under the key API consumers already read", func() {
		out, err := json.Marshal(NodeModel{
			NodeID: "node-1", ModelName: "m", ReplicaIndex: 1,
			WorkerLocalAddress: "127.0.0.1:50052",
		})
		Expect(err).ToNot(HaveOccurred())

		var raw map[string]any
		Expect(json.Unmarshal(out, &raw)).To(Succeed())
		Expect(raw).To(HaveKeyWithValue("address", "127.0.0.1:50052"))
		Expect(raw).ToNot(HaveKey("worker_local_address"),
			"GET /api/nodes/{id}/models and /api/nodes/models both serve this struct verbatim")
	})

	It("round-trips a body written against the documented key", func() {
		var nm NodeModel
		Expect(json.Unmarshal([]byte(`{"node_id":"node-1","model_name":"m","address":"127.0.0.1:50052"}`), &nm)).To(Succeed())
		Expect(nm.WorkerLocalAddress).To(Equal("127.0.0.1:50052"))
	})
})
