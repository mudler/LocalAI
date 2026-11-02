package cli

import (
	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// LOCALAI_CHATS_DATE_NOTE switches on today's date in every chat turn. It is
// off unless set.
var _ = Describe("the chats date note option", func() {
	parse := func(args ...string) *RunCMD {
		var r RunCMD
		k, err := kong.New(&r, kong.Vars{"basepath": GinkgoT().TempDir(), "galleries": "[]", "backends": "[]", "generatedcontentpath": DefaultGeneratedContentPath(), "uploadpath": DefaultUploadPath()})
		Expect(err).ToNot(HaveOccurred())
		_, err = k.Parse(args)
		Expect(err).ToNot(HaveOccurred())
		return &r
	}

	It("is off by default", func() {
		Expect(parse().ChatsDateNote).To(BeFalse())
	})

	It("is on with the environment variable", func() {
		GinkgoT().Setenv("LOCALAI_CHATS_DATE_NOTE", "true")
		Expect(parse().ChatsDateNote).To(BeTrue())
	})

	It("is on with the flag", func() {
		Expect(parse("--chats-date-note").ChatsDateNote).To(BeTrue())
	})
})
