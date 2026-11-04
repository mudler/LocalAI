// SPDX-License-Identifier: MIT
package workerctl

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Install environment wire compatibility", func() {
	It("preserves absent legacy bytes and explicit empty snapshots", func() {
		for _, body := range []string{`{"backend":"test"}`, `{"backend":"test","env_vars":{}}`, `{"backend":"test","env_vars":{"EMPTY":"","TOKEN":"test-value"}}`} {
			var req BackendInstallRequest
			Expect(json.Unmarshal([]byte(body), &req)).To(Succeed())
			encoded, err := json.Marshal(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(encoded)).To(Equal(body))
		}
	})
	It("lets an old receiver ignore the additive snapshot", func() {
		body, err := json.Marshal(BackendInstallRequest{Backend: "test", EnvVars: map[string]string{"TOKEN": "test-value"}})
		Expect(err).NotTo(HaveOccurred())
		var legacy struct {
			Backend string `json:"backend"`
		}
		Expect(json.Unmarshal(body, &legacy)).To(Succeed())
		Expect(legacy.Backend).To(Equal("test"))
	})
})
