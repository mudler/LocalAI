// SPDX-License-Identifier: MIT
package schema_test

import (
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Every key serializeChat sends needs a JSON field so persistence cannot
// silently discard a chat setting.
var _ = Describe("Conversation UI schema", func() {
	It("keeps every field the UI sends", func() {
		src, err := os.ReadFile("../http/react-ui/src/hooks/useChat.js")
		Expect(err).NotTo(HaveOccurred(), "read useChat.js")
		body := regexp.MustCompile(`(?s)function serializeChat\(chat\) \{\s*return \{(.*?)\n  \}`).FindSubmatch(src)
		Expect(body).NotTo(BeNil(), "serializeChat not found in useChat.js")
		sent := regexp.MustCompile(`(?m)^\s*(\w+):`).FindAllSubmatch(body[1], -1)
		Expect(len(sent)).To(BeNumerically(">=", 10), "the serializeChat pattern no longer matches")

		known := map[string]bool{}
		ty := reflect.TypeOf(schema.Conversation{})
		for i := 0; i < ty.NumField(); i++ {
			if name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]; name != "" {
				known[name] = true
			}
		}
		for _, m := range sent {
			key := string(m[1])
			Expect(known).To(HaveKey(key), "serializeChat sends %q but schema.Conversation has no field for it; the server drops it", key)
		}
	})
})
