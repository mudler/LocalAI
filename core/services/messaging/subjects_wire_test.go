package messaging

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// BackendInstallReply.WorkerLocalAddress was called Address until workers
// stopped advertising. The Go field was renamed so no reader takes it for a
// dial target; the wire key was deliberately NOT renamed, because a worker and
// a frontend from different releases have to keep understanding each other
// across a rolling upgrade.
//
// That is a cross-version compatibility property resting on one struct tag, and
// a struct tag nobody asserts is a property nobody has. Renaming just the tags
// left the whole suite green when this was written.
var _ = Describe("backend.install reply wire format", func() {
	It("writes the address under the key an older frontend reads", func() {
		out, err := json.Marshal(BackendInstallReply{Success: true, WorkerLocalAddress: "127.0.0.1:50052"})
		Expect(err).ToNot(HaveOccurred())

		var raw map[string]any
		Expect(json.Unmarshal(out, &raw)).To(Succeed())
		Expect(raw).To(HaveKeyWithValue("address", "127.0.0.1:50052"))
		Expect(raw).ToNot(HaveKey("worker_local_address"),
			"renaming the wire key would make every install reply unreadable to a frontend of another release")
	})

	It("reads the address an older worker sends", func() {
		// An older worker puts its ADVERTISED host here. Only the port is used,
		// and the port is the same, so accepting it is both harmless and the
		// thing that keeps a mixed fleet working.
		var reply BackendInstallReply
		Expect(json.Unmarshal([]byte(`{"success":true,"address":"worker-1:50052"}`), &reply)).To(Succeed())
		Expect(reply.Success).To(BeTrue())
		Expect(reply.WorkerLocalAddress).To(Equal("worker-1:50052"))
	})

	It("omits the address when the install failed", func() {
		out, err := json.Marshal(BackendInstallReply{Success: false, Error: "boom"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).ToNot(ContainSubstring("address"))
	})
})
