// SPDX-License-Identifier: MIT
package cli

import (
	"os"
	"reflect"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Every LOCALAI_* variable the authentication docs tell an operator to set
// must be accepted by `local-ai run`; a merge that drops the flag otherwise
// leaves a documented setting silently ignored.
var _ = Describe("run command environment variables named in the auth docs", func() {
	It("accepts every LOCALAI_* variable of authentication.md", func() {
		known := map[string]bool{}
		t := reflect.TypeOf(RunCMD{})
		for i := 0; i < t.NumField(); i++ {
			for _, name := range strings.Split(t.Field(i).Tag.Get("env"), ",") {
				known[strings.TrimSpace(name)] = true
			}
		}
		doc, err := os.ReadFile("../../docs/content/features/authentication.md")
		Expect(err).NotTo(HaveOccurred())
		named := regexp.MustCompile("`(LOCALAI_[A-Z0-9_]+)").FindAllStringSubmatch(string(doc), -1)
		Expect(named).NotTo(BeEmpty())
		for _, m := range named {
			Expect(known).To(HaveKey(m[1]), "documented but not accepted: "+m[1])
		}
	})
})
