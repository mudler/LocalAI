package application

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// minimal Application wired enough for startMITMProxy: an empty model
// config loader (no host claims), CA written under a temp DataPath.
func newMITMTestApp(dataPath string) (*Application, *config.ApplicationConfig) {
	state, err := system.GetSystemState()
	Expect(err).NotTo(HaveOccurred())
	state.Model.ModelsPath = dataPath
	opts := config.NewApplicationConfig(
		config.WithSystemState(state),
		config.WithDataPath(dataPath),
	)
	return newApplication(opts), opts
}

var _ = Describe("startMITMIfConfigured", func() {
	It("does nothing when no listen address is configured", func() {
		app, opts := newMITMTestApp(GinkgoT().TempDir())
		opts.MITMListen = ""

		Expect(func() { startMITMIfConfigured(app, opts) }).NotTo(Panic())
		Expect(app.mitmServer.Load()).To(BeNil(), "no listener should be stored when disabled")
	})

	// Regression: a persisted-but-unbindable MITM address (e.g. a LAN host
	// inside a container) must not abort startup. startMITMIfConfigured
	// swallows the bind error so the rest of LocalAI still comes up and the
	// admin can fix the address via the Settings UI.
	It("logs and continues when the listen address cannot be bound", func() {
		app, opts := newMITMTestApp(GinkgoT().TempDir())
		// 192.0.2.1 is TEST-NET-1 (RFC 5737): guaranteed not assigned to any
		// local interface, so bind fails deterministically without DNS.
		opts.MITMListen = "192.0.2.1:8082"

		Expect(func() { startMITMIfConfigured(app, opts) }).NotTo(Panic())
		Expect(app.mitmServer.Load()).To(BeNil(), "failed listener must not be stored")
	})

	It("starts and stores the listener on a bindable address", func() {
		app, opts := newMITMTestApp(GinkgoT().TempDir())
		opts.MITMListen = "127.0.0.1:0" // OS-assigned free port

		startMITMIfConfigured(app, opts)

		srv := app.mitmServer.Load()
		Expect(srv).NotTo(BeNil(), "listener should be stored on success")
		DeferCleanup(srv.Stop)
		Expect(srv.Addr()).NotTo(BeEmpty())
	})
})
