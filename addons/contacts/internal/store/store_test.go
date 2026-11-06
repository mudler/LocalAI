// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/contacts/internal/store"
)

func s(v string) *string { return &v }

func entries(e ...store.Entry) *[]store.Entry { return &e }

var _ = Describe("contacts store", func() {
	var (
		st  *store.Store
		ctx = context.Background()
	)
	BeforeEach(func() {
		var err error
		st, err = store.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		st.Now = func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) }
		DeferCleanup(func() { _ = st.Close() })
	})

	marco := func() store.Input {
		return store.Input{
			Name: s("Marco Rossi"), Org: s("Example Org"),
			Phones:   entries(store.Entry{Label: "mobile", Value: "+39 02 5550123"}, store.Entry{Label: "work", Value: "02 5550199"}),
			Emails:   entries(store.Entry{Label: "work", Value: "marco@example.org"}),
			Birthday: s("1990-04-23"), Address: s("Via Esempio 1, Milano"), Notes: s("Met at the fair."),
		}
	}

	It("creates, reads, changes and deletes a contact", func() {
		c, err := st.Create(ctx, "ann", marco())
		Expect(err).NotTo(HaveOccurred())
		Expect(c.ID).To(HavePrefix("ct-"))
		got, err := st.Get(ctx, "ann", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(c))
		Expect(got.Phones).To(HaveLen(2))
		Expect(got.Created).To(Equal("2026-11-05T10:00:00Z"))

		st.Now = func() time.Time { return time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC) }
		up, err := st.Update(ctx, "ann", c.ID, store.Input{Org: s("Other Org"), Phones: entries(store.Entry{Value: "+39 02 5550123"})})
		Expect(err).NotTo(HaveOccurred())
		Expect(up.Org).To(Equal("Other Org"))
		Expect(up.Phones).To(Equal([]store.Entry{{Value: "+39 02 5550123"}}), "a list replaces the old one")
		Expect(up.Name).To(Equal("Marco Rossi"), "a field that is not named stays")
		Expect(up.Emails).To(HaveLen(1))
		Expect(up.Created).To(Equal(c.Created))
		Expect(up.Updated).To(Equal("2026-11-06T08:00:00Z"))

		cleared, err := st.Update(ctx, "ann", c.ID, store.Input{Birthday: s(""), Emails: entries()})
		Expect(err).NotTo(HaveOccurred())
		Expect(cleared.Birthday).To(BeEmpty())
		Expect(cleared.Emails).To(BeEmpty())

		Expect(st.Delete(ctx, "ann", c.ID)).To(Succeed())
		_, err = st.Get(ctx, "ann", c.ID)
		Expect(err).To(MatchError(store.ErrNotFound))
		Expect(st.Delete(ctx, "ann", c.ID)).To(MatchError(store.ErrNotFound))
	})

	It("keeps one owner's book from another owner", func() {
		c, err := st.Create(ctx, "ann", marco())
		Expect(err).NotTo(HaveOccurred())
		_, err = st.Get(ctx, "bob", c.ID)
		Expect(err).To(MatchError(store.ErrNotFound))
		_, err = st.Update(ctx, "bob", c.ID, store.Input{Name: s("Taken")})
		Expect(err).To(MatchError(store.ErrNotFound))
		Expect(st.Delete(ctx, "bob", c.ID)).To(MatchError(store.ErrNotFound))
		hits, _, err := st.Search(ctx, "bob", "", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(BeEmpty())
		got, err := st.Get(ctx, "ann", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Marco Rossi"))
	})

	It("survives a reopen of the file", func() {
		dir := GinkgoT().TempDir()
		a, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		c, err := a.Create(ctx, "ann", marco())
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Close()).To(Succeed())
		b, err := store.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		got, err := b.Get(ctx, "ann", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Emails[0].Value).To(Equal("marco@example.org"))
	})

	Describe("search", func() {
		BeforeEach(func() {
			_, err := st.Create(ctx, "ann", marco())
			Expect(err).NotTo(HaveOccurred())
			_, err = st.Create(ctx, "ann", store.Input{Name: s("Anna Bianchi"), Emails: entries(store.Entry{Value: "anna@example.org"}), Notes: s("Knows José from the choir")})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.Create(ctx, "ann", store.Input{Name: s("Marta Verdi"), Phones: entries(store.Entry{Value: "+44 20 7946 0000"})})
			Expect(err).NotTo(HaveOccurred())
		})
		names := func(q string) []string {
			GinkgoHelper()
			hits, _, err := st.Search(ctx, "ann", q, 50)
			Expect(err).NotTo(HaveOccurred())
			out := []string{}
			for _, h := range hits {
				out = append(out, h.Name)
			}
			return out
		}
		It("matches words in the name, whatever the case", func() {
			Expect(names("marco")).To(ConsistOf("Marco Rossi"))
			Expect(names("MAR")).To(ConsistOf("Marco Rossi", "Marta Verdi"))
		})
		It("needs every word", func() {
			Expect(names("marco bianchi")).To(BeEmpty())
			Expect(names("rossi example")).To(ConsistOf("Marco Rossi"))
		})
		It("finds a phone number however it is spaced", func() {
			Expect(names("5550123")).To(ConsistOf("Marco Rossi"))
			Expect(names("+39 02")).To(ConsistOf("Marco Rossi"))
			Expect(names("79460000")).To(ConsistOf("Marta Verdi"))
		})
		It("matches an email address, the notes and the address", func() {
			Expect(names("anna@example.org")).To(ConsistOf("Anna Bianchi"))
			Expect(names("fair")).To(ConsistOf("Marco Rossi"))
			Expect(names("milano")).To(ConsistOf("Marco Rossi"))
		})
		It("ignores accents", func() {
			Expect(names("jose")).To(ConsistOf("Anna Bianchi"))
		})
		It("lists everyone by name for an empty query, and says when it cut the list", func() {
			Expect(names("")).To(Equal([]string{"Anna Bianchi", "Marco Rossi", "Marta Verdi"}))
			hits, more, err := st.Search(ctx, "ann", "", 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(hits).To(HaveLen(2))
			Expect(more).To(BeTrue())
		})
	})

	It("refuses what is not a contact", func() {
		bad := func(in store.Input, text string) {
			GinkgoHelper()
			_, err := st.Create(ctx, "ann", in)
			Expect(err).To(MatchError(store.ErrInvalid))
			Expect(err.Error()).To(ContainSubstring(text))
		}
		bad(store.Input{}, "needs a name")
		bad(store.Input{Name: s("  ")}, "needs a name")
		bad(store.Input{Name: s("A\nB")}, "one line")
		bad(store.Input{Name: s(strings.Repeat("x", 201))}, "200")
		bad(store.Input{Name: s("A"), Birthday: s("23/04/1990")}, "birthday")
		bad(store.Input{Name: s("A"), Birthday: s("1990-02-30")}, "not a date")
		bad(store.Input{Name: s("A"), Birthday: s("--13-01")}, "not a date")
		bad(store.Input{Name: s("A"), Emails: entries(store.Entry{Value: "not an email"})}, "email")
		bad(store.Input{Name: s("A"), Phones: entries(store.Entry{Value: "call me"})}, "phone")
		bad(store.Input{Name: s("A"), Phones: entries(store.Entry{Label: "x", Value: ""})}, "no value")
		many := make([]store.Entry, 11)
		for i := range many {
			many[i] = store.Entry{Value: "123456"}
		}
		bad(store.Input{Name: s("A"), Phones: &many}, "at most 10")
		bad(store.Input{Name: s("A"), Notes: s(strings.Repeat("n", 4001))}, "4000")
		ok, err := st.Create(ctx, "ann", store.Input{Name: s("A"), Birthday: s("--02-29")})
		Expect(err).NotTo(HaveOccurred())
		Expect(ok.Birthday).To(Equal("--02-29"), "a birthday without a year may be the 29th of February")
	})

	It("does not let an update make a contact invalid", func() {
		c, err := st.Create(ctx, "ann", marco())
		Expect(err).NotTo(HaveOccurred())
		_, err = st.Update(ctx, "ann", c.ID, store.Input{Name: s("")})
		Expect(err).To(MatchError(store.ErrInvalid))
		got, _ := st.Get(ctx, "ann", c.ID)
		Expect(got.Name).To(Equal("Marco Rossi"))
	})

	It("stops at the size of a book", func() {
		for i := 0; i < store.MaxContacts; i++ {
			_, err := st.Create(ctx, "ann", store.Input{Name: s("C")})
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := st.Create(ctx, "ann", store.Input{Name: s("One more")})
		Expect(err).To(MatchError(store.ErrFull))
		_, err = st.Create(ctx, "bob", store.Input{Name: s("Other book")})
		Expect(err).NotTo(HaveOccurred())
	})

	It("exports the book as vCards that escape and fold", func() {
		_, err := st.Create(ctx, "ann", marco())
		Expect(err).NotTo(HaveOccurred())
		long := strings.Repeat("Long note, with; separators. ", 6)
		_, err = st.Create(ctx, "ann", store.Input{Name: s("Zoe, the Second"), Notes: s("line one\nline two " + long)})
		Expect(err).NotTo(HaveOccurred())
		_, err = st.Create(ctx, "bob", store.Input{Name: s("Not Ann")})
		Expect(err).NotTo(HaveOccurred())
		v, err := st.VCards(ctx, "ann")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(v, "BEGIN:VCARD")).To(Equal(2))
		Expect(strings.Count(v, "END:VCARD")).To(Equal(2))
		Expect(v).To(ContainSubstring("FN:Marco Rossi\r\n"))
		Expect(v).To(ContainSubstring("TEL;TYPE=mobile:+39 02 5550123\r\n"))
		Expect(v).To(ContainSubstring("EMAIL;TYPE=work:marco@example.org\r\n"))
		Expect(v).To(ContainSubstring("BDAY:1990-04-23\r\n"))
		Expect(v).To(ContainSubstring("FN:Zoe\\, the Second"))
		Expect(v).To(ContainSubstring("line one\\nline two"))
		Expect(v).NotTo(ContainSubstring("Not Ann"))
		for _, line := range strings.Split(v, "\r\n") {
			Expect(len(line)).To(BeNumerically("<=", 75))
		}
	})
})
