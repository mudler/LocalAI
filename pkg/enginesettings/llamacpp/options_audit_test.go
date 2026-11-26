package llamacpp_test

import (
	"os"
	"regexp"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	lc "github.com/mudler/LocalAI/pkg/enginesettings/llamacpp"
)

// grpcServer is the llama.cpp backend of LocalAI, which reads the options
// list of a model.
const grpcServer = "../../../backend/cpp/llama-cpp/grpc-server.cpp"

// notFields are option names the backend reads that the editor does not
// draw as a field, with the reason.
var notFields = map[string]string{
	"draft_ctx_size": "ignored by the backend: the draft uses the context size of the model",
}

var _ = Describe("the options the llama.cpp backend reads", func() {
	It("each have a setting in the schema, so a new option cannot be missed", func() {
		src, err := os.ReadFile(grpcServer)
		Expect(err).ToNot(HaveOccurred())
		names := map[string]bool{}
		for _, m := range regexp.MustCompile(`strcmp\(optname, "([^"]+)"\)`).FindAllStringSubmatch(string(src), -1) {
			names[m[1]] = true
		}
		// The parse loop has well over a hundred names; a small count means
		// the pattern no longer matches the source.
		Expect(len(names)).To(BeNumerically(">", 100))

		s := lc.Engine().Schema()
		known := map[string]bool{}
		for _, f := range s.Flags {
			if f.Option != "" {
				known[f.Option] = true
			}
			for _, a := range f.OptionAliases {
				known[a] = true
			}
		}
		for _, r := range s.ReservedOptions {
			known[r] = true
		}
		var missing []string
		for n := range names {
			if !known[n] && notFields[n] == "" {
				missing = append(missing, n)
			}
		}
		sort.Strings(missing)
		Expect(missing).To(BeEmpty(), "add these options to the flag table of pkg/enginesettings/llamacpp, or to notFields with a reason")
	})

	It("lists in notFields only names the backend still reads", func() {
		src, err := os.ReadFile(grpcServer)
		Expect(err).ToNot(HaveOccurred())
		for n := range notFields {
			Expect(string(src)).To(ContainSubstring(`"`+n+`"`), n)
		}
	})
})
