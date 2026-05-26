package pii

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LoadConfig", func() {
	It("returns defaults when no path given", func() {
		patterns, err := LoadConfig("")
		Expect(err).NotTo(HaveOccurred())
		Expect(patterns).To(HaveLen(len(DefaultPatterns())))
	})

	It("overrides action", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "pii.yaml")
		body := []byte(`patterns:
  - id: email
    action: block
  - id: ssn
    action: route_local
`)
		Expect(os.WriteFile(path, body, 0o600)).To(Succeed())
		patterns, err := LoadConfig(path)
		Expect(err).NotTo(HaveOccurred())

		got := map[string]Action{}
		for _, p := range patterns {
			got[p.ID] = p.Action
		}
		Expect(got["email"]).To(Equal(ActionBlock))
		Expect(got["ssn"]).To(Equal(ActionRouteLocal))
		// Unmentioned patterns keep their default action.
		Expect(got["credit_card"]).To(Equal(ActionMask), "credit_card default action lost")
	})

	It("rejects unknown id", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "pii.yaml")
		Expect(os.WriteFile(path, []byte("patterns:\n  - id: nonsense\n    action: mask\n"), 0o600)).To(Succeed())
		_, err := LoadConfig(path)
		Expect(err).To(HaveOccurred(), "expected error on unknown pattern id")
	})

	It("rejects invalid action", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "pii.yaml")
		Expect(os.WriteFile(path, []byte("patterns:\n  - id: email\n    action: lolwhat\n"), 0o600)).To(Succeed())
		_, err := LoadConfig(path)
		Expect(err).To(HaveOccurred(), "expected error on invalid action")
	})
})
