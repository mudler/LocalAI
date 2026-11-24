package system

import (
	"errors"
	"os/user"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("userScopedName", func() {
	sid := func() (*user.User, error) { return &user.User{Uid: "S-1-5-21-1004"}, nil }

	It("uses the uid on Unix", func() {
		Expect(userScopedName("linux", 1000, sid)).To(Equal("localai-1000"))
		Expect(userScopedName("darwin", 501, sid)).To(Equal("localai-501"))
	})

	It("uses the SID on Windows instead of the -1 uid", func() {
		Expect(userScopedName("windows", -1, sid)).To(Equal("localai-S-1-5-21-1004"))
	})

	It("replaces characters that are not valid in a file name", func() {
		odd := func() (*user.User, error) { return &user.User{Uid: `DOMAIN\user:1`}, nil }
		Expect(userScopedName("windows", -1, odd)).To(Equal("localai-DOMAIN_user_1"))
	})

	It("falls back to a plain name when the user is unknown", func() {
		fail := func() (*user.User, error) { return nil, errors.New("no user") }
		Expect(userScopedName("windows", -1, fail)).To(Equal("localai"))
	})

	It("names the current user", func() {
		Expect(UserScopedName()).To(HavePrefix("localai"))
		Expect(UserScopedName()).NotTo(ContainSubstring("--1"))
	})
})
