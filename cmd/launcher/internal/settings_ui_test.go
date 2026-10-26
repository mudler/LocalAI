// SPDX-License-Identifier: MIT
package launcher

import (
	"path/filepath"

	"fyne.io/fyne/v2/test"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The settings window starts with the mode selector. Both profiles keep their
// drafts when the owner switches, and saving never loses a masked secret.
var _ = Describe("Launcher settings window", func() {
	var (
		ui *LauncherUI
		l  *Launcher
	)

	BeforeEach(func() {
		DeferCleanup(test.NewApp().Quit)
		dir := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", dir)
		ui = NewLauncherUI()
		c := &Config{Mode: "standalone", ModelsPath: filepath.Join(dir, "models"), BackendsPath: filepath.Join(dir, "backends"), Address: ":8080", LogLevel: "info"}
		c.Worker.Connection = WorkerConnectionConfig{FrontendURL: "https://frontend.example", RegistrationToken: "token-secret", AdvertisedAddress: "worker.example:50051"}
		c.Worker.NATS = WorkerNATSConfig{URL: "tls://nats.example:4222", JWT: "jwt-secret", UserSeed: "seed-secret"}
		l = &Launcher{config: c, lockDir: dir, life: newLifecycle(), ui: ui, releaseManager: NewReleaseManager()}
		ui.CreateMainUI(l)
		ui.LoadConfiguration()
	})

	It("offers the two modes and shows the local settings first for an existing install", func() {
		Expect(ui.modeRadio.Options).To(Equal([]string{modeLocalLabel, modeJoinLabel}))
		Expect(ui.modeRadio.Selected).To(Equal(modeLocalLabel))
		Expect(ui.localBox.Visible()).To(BeTrue())
		Expect(ui.workerBox.Visible()).To(BeFalse())
	})

	It("opens on the worker settings when the saved mode is worker", func() {
		l.config.Mode = "worker"
		ui.LoadConfiguration()
		Expect(ui.modeRadio.Selected).To(Equal(modeJoinLabel))
		Expect(ui.workerBox.Visible()).To(BeTrue())
		Expect(ui.localBox.Visible()).To(BeFalse())
	})

	It("keeps both drafts when switching back and forth", func() {
		ui.addressEntry.SetText(":9999")
		ui.modeRadio.SetSelected(modeJoinLabel)
		ui.frontendEntry.SetText("https://draft.example")
		ui.modeRadio.SetSelected(modeLocalLabel)
		ui.modeRadio.SetSelected(modeJoinLabel)
		Expect(ui.frontendEntry.Text).To(Equal("https://draft.example"))
		Expect(ui.addressEntry.Text).To(Equal(":9999"))
		got, err := ui.collectConfig()
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Mode).To(Equal("worker"))
		Expect(got.Address).To(Equal(":9999"))
		Expect(got.Worker.Connection.FrontendURL).To(Equal("https://draft.example"))
	})

	It("masks secret fields, never shows their values and says they are saved", func() {
		Expect(ui.tokenEntry.Password).To(BeTrue())
		Expect(ui.jwtEntry.Password).To(BeTrue())
		Expect(ui.seedEntry.Password).To(BeTrue())
		for _, text := range []string{ui.tokenEntry.Text, ui.jwtEntry.Text, ui.seedEntry.Text} {
			Expect(text).To(BeEmpty())
		}
		Expect(ui.tokenEntry.PlaceHolder).To(ContainSubstring("Saved"))
	})

	It("keeps secrets when unrelated settings are saved, and changes them only on request", func() {
		ui.modeRadio.SetSelected(modeJoinLabel)
		ui.nodeNameEntry.SetText("renamed")
		got, err := ui.collectConfig()
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.Connection.RegistrationToken).To(Equal("token-secret"))
		Expect(got.Worker.NATS.JWT).To(Equal("jwt-secret"))
		Expect(got.Worker.NATS.UserSeed).To(Equal("seed-secret"))

		ui.tokenEntry.SetText("new-token")
		ui.clearJWTCheck.SetChecked(true)
		got, err = ui.collectConfig()
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.Connection.RegistrationToken).To(Equal("new-token"))
		Expect(got.Worker.NATS.JWT).To(BeEmpty())
		Expect(got.Worker.NATS.UserSeed).To(Equal("seed-secret"))
	})

	It("saves an invalid inactive draft but reports an invalid saved number", func() {
		ui.modeRadio.SetSelected(modeLocalLabel)
		ui.frontendEntry.SetText("not a url")
		got, err := ui.collectConfig()
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.Connection.FrontendURL).To(Equal("not a url"))
		ui.grpcMaxEntry.SetText("abc")
		_, err = ui.collectConfig()
		Expect(err).To(HaveOccurred())
	})

	It("labels the automatic start for the selected mode, not for LocalAI only", func() {
		Expect(ui.autoStartCheck.Text).To(Equal("Start selected mode when launcher opens"))
	})

	It("names the controls for the selected mode", func() {
		ui.UpdateRunningState(false)
		Expect(ui.startStopButton.Text).To(Equal("Start LocalAI"))
		l.config.Mode = "worker"
		ui.UpdateRunningState(false)
		Expect(ui.startStopButton.Text).To(Equal("Start worker"))
		ui.UpdateRunningState(true)
		Expect(ui.startStopButton.Text).To(Equal("Stop worker"))
		Expect(ui.webUIButton.Text).To(Equal("Open deployment UI"))
		Expect(ui.webUIButton.Disabled()).To(BeFalse(), "the deployment UI does not depend on the worker running")
	})

	It("explains reverse connectivity and shows the derived ports", func() {
		ui.modeRadio.SetSelected(modeJoinLabel)
		ui.advertisedEntry.SetText("worker.example:50051")
		ui.grpcMaxEntry.SetText("50060")
		Expect(ui.previewLabel.Text).To(ContainSubstring("50050"))
		Expect(ui.previewLabel.Text).To(ContainSubstring("50051-50060"))
		ui.advertisedEntry.SetText("worker.example")
		Expect(ui.previewLabel.Text).To(ContainSubstring("host:port"))
		Expect(ui.joinExplanation.Text).To(ContainSubstring("does not create a deployment"))
	})

	It("records the plaintext acknowledgement for this session only", func() {
		Expect(l.PlaintextAcknowledged()).To(BeFalse())
		ui.plaintextCheck.SetChecked(true)
		Expect(l.PlaintextAcknowledged()).To(BeTrue())
		got, err := ui.collectConfig()
		Expect(err).ToNot(HaveOccurred())
		data, merr := got.MarshalJSON()
		Expect(merr).ToNot(HaveOccurred())
		Expect(string(data)).ToNot(ContainSubstring("plaintext"))
	})
})
