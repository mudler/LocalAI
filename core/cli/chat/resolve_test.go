package chat

import (
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResolveModel", func() {
	It("prefers the flag over everything", func() {
		got, err := ResolveModel(ModelRequest{
			Flag:       "from-flag",
			Configured: "from-config",
			Available:  []string{"a", "b"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("from-flag"))
	})

	It("uses the configured model when no flag is given", func() {
		got, err := ResolveModel(ModelRequest{
			Configured: "from-config",
			Available:  []string{"a", "b"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("from-config"))
	})

	It("auto-selects when the server offers exactly one model", func() {
		got, err := ResolveModel(ModelRequest{Available: []string{"only-one"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("only-one"))
	})

	It("errors and lists the options when several models exist and there is no chooser", func() {
		_, err := ResolveModel(ModelRequest{Available: []string{"a", "b"}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("a"))
		Expect(err.Error()).To(ContainSubstring("b"))
		Expect(err.Error()).To(ContainSubstring("--model"))
	})

	It("sorts before offering, so the same number means the same model next run", func() {
		var offered []string
		available := []string{"zeta", "alpha", "mid"}
		_, err := ResolveModel(ModelRequest{
			Available: available,
			StateDir:  GinkgoT().TempDir(),
			Choose: func(models []string) (string, error) {
				offered = models
				return models[0], nil
			},
		})
		Expect(err).ToNot(HaveOccurred())
		// The server's /v1/models ordering is unstable between calls.
		Expect(offered).To(Equal([]string{"alpha", "mid", "zeta"}))
		// Sorting must happen on a copy: the caller still owns this slice, and
		// reordering it under them would move whatever they index into it.
		Expect(available).To(Equal([]string{"zeta", "alpha", "mid"}))
	})

	It("lists models in sorted order in the several-models error", func() {
		_, err := ResolveModel(ModelRequest{Available: []string{"zeta", "alpha"}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("alpha, zeta"))
	})

	It("asks the chooser when several models exist, and persists the answer", func() {
		dir := GinkgoT().TempDir()
		got, err := ResolveModel(ModelRequest{
			Available: []string{"a", "b"},
			StateDir:  dir,
			Choose:    func(models []string) (string, error) { return models[1], nil },
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("b"))

		data, err := os.ReadFile(ConfigPath(dir))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("model: b"))
	})

	// The answer is persisted and every later run starts against it, and
	// ModelChooser is exported, so the invariant has to hold for choosers this
	// package did not write.
	DescribeTable("refuses an answer the chooser was not offered",
		func(answer string) {
			dir := GinkgoT().TempDir()
			got, err := ResolveModel(ModelRequest{
				Available: []string{"alpha", "zeta"},
				StateDir:  dir,
				Choose:    func([]string) (string, error) { return answer, nil },
			})
			Expect(err).To(HaveOccurred())
			Expect(got).To(BeEmpty())
			Expect(err.Error()).To(ContainSubstring("alpha, zeta"))

			_, statErr := os.Stat(ConfigPath(dir))
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "nothing may be recorded for an answer that was refused")
		},
		Entry("nothing at all", ""),
		Entry("a model the server never offered", "gamma"),
		Entry("an offered model with stray whitespace", " alpha"),
		Entry("an offered model in the wrong case", "Alpha"),
	)

	It("notifies, and still honours the choice, when it cannot be persisted", func() {
		dir := GinkgoT().TempDir()
		// A directory where the config file belongs: the write fails for any
		// user, including root.
		Expect(os.MkdirAll(ConfigPath(dir), 0o700)).To(Succeed())

		var notices []string
		got, err := ResolveModel(ModelRequest{
			Available: []string{"a", "b"},
			StateDir:  dir,
			Choose:    func(models []string) (string, error) { return models[0], nil },
			Notify:    func(message string) { notices = append(notices, message) },
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("a"))
		Expect(notices).To(HaveLen(1))
		Expect(notices[0]).To(ContainSubstring("a"))
		Expect(notices[0]).To(ContainSubstring("could not be saved"))
	})

	It("says nothing when the choice was saved", func() {
		var notices []string
		_, err := ResolveModel(ModelRequest{
			Available: []string{"a", "b"},
			StateDir:  GinkgoT().TempDir(),
			Choose:    func(models []string) (string, error) { return models[0], nil },
			Notify:    func(message string) { notices = append(notices, message) },
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(notices).To(BeEmpty())
	})

	It("propagates a chooser cancellation", func() {
		cancelled := errors.New("cancelled")
		_, err := ResolveModel(ModelRequest{
			Available: []string{"a", "b"},
			StateDir:  GinkgoT().TempDir(),
			Choose:    func([]string) (string, error) { return "", cancelled },
		})
		Expect(errors.Is(err, cancelled)).To(BeTrue())
	})

	It("errors with an install hint when the server has no models", func() {
		_, err := ResolveModel(ModelRequest{Available: nil})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("local-ai models install"))
	})
})
