package credentials_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
)

func headersFor(s *credentials.Store, rawURL string) (http.Header, error) {
	c, ok := s.Match(rawURL)
	Expect(ok).To(BeTrue(), "no rule matched %s", rawURL)
	h := http.Header{}
	return h, c.ApplyHeaders(h)
}

var _ = Describe("Credential", func() {
	It("reads _env values through the injected lookup", func() {
		env := map[string]string{"TOKEN": "from-env"}
		s, err := credentials.Parse([]byte("- match: ghcr.io\n  bearer_env: TOKEN\n"), func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		})
		Expect(err).NotTo(HaveOccurred())
		h, err := headersFor(s, "https://ghcr.io/x")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Get("Authorization")).To(Equal("Bearer from-env"))
	})

	It("re-reads _file values so a rotated secret mount is picked up", func() {
		secret := filepath.Join(GinkgoT().TempDir(), "token")
		Expect(os.WriteFile(secret, []byte("one\n"), 0o600)).To(Succeed())
		s := mustParse(fmt.Sprintf("- match: ghcr.io\n  bearer_file: %s\n", secret))

		h, err := headersFor(s, "https://ghcr.io/x")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Get("Authorization")).To(Equal("Bearer one"))

		Expect(os.WriteFile(secret, []byte("two"), 0o600)).To(Succeed())
		h, err = headersFor(s, "https://ghcr.io/x")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Get("Authorization")).To(Equal("Bearer two"))
	})

	It("loads a rule whose variable is missing and names it when used", func() {
		s := mustParse("- match: ghcr.io\n  bearer_env: MISSING_TOKEN\n")
		_, err := headersFor(s, "https://ghcr.io/x")
		Expect(err).To(MatchError(And(ContainSubstring("MISSING_TOKEN"), ContainSubstring(`credential "ghcr.io"`))))
		Expect(errors.Is(err, credentials.ErrUnresolvedSecret)).To(BeTrue())
	})

	It("sets basic auth", func() {
		h, err := headersFor(mustParse("- match: ghcr.io\n  username: u\n  password: p\n"), "https://ghcr.io/x")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Get("Authorization")).To(Equal("Basic " + base64.StdEncoding.EncodeToString([]byte("u:p"))))
	})

	It("sets a custom header and leaves Authorization alone", func() {
		h, err := headersFor(mustParse("- match: art.acme\n  header:\n    name: X-JFrog-Art-Api\n    value: k\n"), "https://art.acme/x")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Get("X-JFrog-Art-Api")).To(Equal("k"))
		Expect(h.Get("Authorization")).To(BeEmpty())
	})

	It("breaks ties between identical rules by file order", func() {
		s := mustParse("- match: ghcr.io/other\n  bearer: first\n- match: ghcr.io/other\n  bearer: second\n")
		h, err := headersFor(s, "https://ghcr.io/other/img")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Get("Authorization")).To(Equal("Bearer first"))
	})

	It("never prints secret material", func() {
		store := mustParse("- match: ghcr.io\n  username: bot\n  password: hunter2\n")
		c, ok := store.Match("https://ghcr.io/x")
		Expect(ok).To(BeTrue())
		Expect(fmt.Sprintf("%v %+v %#v %s", c, c, c, c)).NotTo(ContainSubstring("hunter2"))

		// fmt cannot call String on values reached through unexported
		// fields, so nesting is checked separately from the direct case.
		wrapper := struct{ c credentials.Credential }{c: c}
		for _, v := range []any{store, *store, wrapper} {
			Expect(fmt.Sprintf("%v %+v %#v", v, v, v)).NotTo(ContainSubstring("hunter2"))
		}
		Expect(fmt.Sprintf("%v", store)).To(ContainSubstring("ghcr.io"))

		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("using", "credential", c)
		Expect(buf.String()).NotTo(ContainSubstring("hunter2"))
		Expect(buf.String()).To(ContainSubstring("ghcr.io"))

		for _, h := range []func(*bytes.Buffer) slog.Handler{
			func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
			func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		} {
			var out bytes.Buffer
			var nilStore *credentials.Store
			slog.New(h(&out)).Info("loaded", "store", store, "value", *store, "none", nilStore)
			Expect(out.String()).NotTo(ContainSubstring("hunter2"))
			Expect(out.String()).To(ContainSubstring("ghcr.io"))
			Expect(out.String()).NotTo(ContainSubstring("panicked"))
		}
	})
})
