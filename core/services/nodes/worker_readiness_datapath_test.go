package nodes

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeAddressLister struct{ addrs []string }

func (f *fakeAddressLister) LoadedBackendAddresses() []string { return f.addrs }

// A worker that holds backend processes it can no longer reach is up and
// useless, but /readyz only tracked the NATS link, so it answered 200 and the
// scheduler kept routing loads to it.
var _ = Describe("worker readiness data path", func() {
	It("is ready when a worker holds no backends", func() {
		probe := BackendDataPathReadiness(&fakeAddressLister{}, func(string) error {
			Fail("must not dial when there are no backends")
			return nil
		})
		Expect(probe()).To(Succeed())
	})

	It("is ready when every held backend is dialable", func() {
		probe := BackendDataPathReadiness(
			&fakeAddressLister{addrs: []string{"10.0.0.1:9001", "10.0.0.1:9002"}},
			func(string) error { return nil },
		)
		Expect(probe()).To(Succeed())
	})

	It("is not ready when a held backend cannot be reached", func() {
		probe := BackendDataPathReadiness(
			&fakeAddressLister{addrs: []string{"10.0.0.1:9001"}},
			func(string) error { return errors.New("connection refused") },
		)
		err := probe()
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrBackendUnreachable)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("10.0.0.1:9001"))
	})

	It("reports the first failing probe in a composite", func() {
		boom := errors.New("nats is down")
		probe := CompositeReadiness(
			func() error { return nil },
			func() error { return boom },
		)
		Expect(probe()).To(MatchError(boom))
	})

	It("is ready when a composite has no failing probe", func() {
		Expect(CompositeReadiness(func() error { return nil }, func() error { return nil })()).To(Succeed())
	})
})
