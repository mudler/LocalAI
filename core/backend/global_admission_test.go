// SPDX-License-Identifier: MIT

package backend_test

import (
	"errors"

	"github.com/mudler/LocalAI/core/backend"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("global backend admission", func() {
	BeforeEach(func() {
		backend.ConfigureGlobalBackendAdmission(1)
	})

	It("rejects excess backend work without queueing", func() {
		release, err := backend.AcquireGlobalBackendSlot()
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.GlobalBackendInFlight()).To(Equal(1))

		_, err = backend.AcquireGlobalBackendSlot()
		var capacityErr *backend.BackendAdmissionError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.Limit).To(Equal(1))

		release()
		Expect(backend.GlobalBackendInFlight()).To(BeZero())
	})

	It("makes release idempotent", func() {
		release, err := backend.AcquireGlobalBackendSlot()
		Expect(err).NotTo(HaveOccurred())
		release()
		release()
		Expect(backend.GlobalBackendInFlight()).To(BeZero())
	})
})
