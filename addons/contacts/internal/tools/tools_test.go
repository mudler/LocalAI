// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/contacts/internal/store"
	"github.com/mudler/LocalAI/addons/contacts/internal/tools"
)

var _ = Describe("contacts tools", func() {
	var (
		rt  *tools.Runtime
		ctx = context.Background()
	)
	decode := func(raw json.RawMessage) map[string]any {
		GinkgoHelper()
		var out map[string]any
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		return out
	}
	read := func(user string, args map[string]any) map[string]any {
		GinkgoHelper()
		res, err := rt.Read(ctx, env(user, "read", "read", args))
		Expect(err).NotTo(HaveOccurred())
		return decode(res.Payload)
	}
	act := func(user string, args map[string]any) map[string]any {
		GinkgoHelper()
		res, err := rt.Act(ctx, env(user, "act", "act", args))
		Expect(err).NotTo(HaveOccurred())
		return decode(res.Payload)
	}
	BeforeEach(func() {
		st, err := store.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		st.Now = func() time.Time { return time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC) }
		DeferCleanup(func() { _ = st.Close() })
		rt = tools.New(st)
	})

	It("answers 'what is Marco's number?' after Marco was added", func() {
		c := act("ann", map[string]any{"tool": "contact-create", "name": "Marco Rossi", "phones": []any{map[string]any{"label": "mobile", "value": "+39 02 5550123"}}})
		Expect(c["ok"]).To(BeTrue())
		s := read("ann", map[string]any{"tool": "contacts-search", "query": "marco"})
		hits := s["contacts"].([]any)
		Expect(hits).To(HaveLen(1))
		card := hits[0].(map[string]any)
		Expect(card["phones"].([]any)[0].(map[string]any)["value"]).To(Equal("+39 02 5550123"))
		g := read("ann", map[string]any{"tool": "contact-get", "id": card["id"]})
		Expect(g["contact"].(map[string]any)["name"]).To(Equal("Marco Rossi"))
	})

	It("adds 'Anna, anna@example.org' with only what was said", func() {
		out := act("ann", map[string]any{"tool": "contact-create", "name": "Anna", "emails": []any{map[string]any{"value": "anna@example.org"}}})
		Expect(out["ok"]).To(BeTrue())
		c := out["contact"].(map[string]any)
		Expect(c["emails"]).To(HaveLen(1))
		Expect(c["phones"]).To(BeEmpty())
		Expect(c).NotTo(HaveKey("birthday"))
	})

	It("keeps each owner's book apart", func() {
		act("ann", map[string]any{"tool": "contact-create", "name": "Mine"})
		Expect(read("bob", map[string]any{"tool": "contacts-search", "query": "mine"})["contacts"]).To(BeEmpty())
		Expect(read("bob", map[string]any{"tool": "contacts-search"})["contacts"]).To(BeEmpty())
	})

	It("does not let one owner change or delete a contact of another", func() {
		id := act("ann", map[string]any{"tool": "contact-create", "name": "Mine"})["contact"].(map[string]any)["id"]
		Expect(act("bob", map[string]any{"tool": "contact-update", "id": id, "name": "Taken"})["ok"]).To(BeFalse())
		Expect(act("bob", map[string]any{"tool": "contact-delete", "id": id})["ok"]).To(BeFalse())
		Expect(read("bob", map[string]any{"tool": "contact-get", "id": id})["ok"]).To(BeFalse())
		Expect(read("ann", map[string]any{"tool": "contact-get", "id": id})["ok"]).To(BeTrue())
	})

	It("changes and deletes a contact", func() {
		id := act("ann", map[string]any{"tool": "contact-create", "name": "Marco Rossi", "organisation": "Example Org"})["contact"].(map[string]any)["id"].(string)
		u := act("ann", map[string]any{"tool": "contact-update", "id": id, "birthday": "1990-04-23"})
		Expect(u["contact"].(map[string]any)["birthday"]).To(Equal("1990-04-23"))
		Expect(u["contact"].(map[string]any)["organisation"]).To(Equal("Example Org"))
		d := act("ann", map[string]any{"tool": "contact-delete", "id": id})
		Expect(d["deleted"]).To(Equal(id))
		Expect(read("ann", map[string]any{"tool": "contact-get", "id": id})["ok"]).To(BeFalse())
	})

	It("answers a mistake as a correctable refusal", func() {
		out := act("ann", map[string]any{"tool": "contact-create", "name": "x", "birthday": "yesterday"})
		Expect(out["ok"]).To(BeFalse())
		Expect(out["error"]).To(ContainSubstring("YYYY-MM-DD"))
		out = act("ann", map[string]any{"tool": "contact-create"})
		Expect(out["error"]).To(ContainSubstring("name"))
		out = act("ann", map[string]any{"tool": "contact-create", "name": "x", "colour": "red"})
		Expect(out["ok"]).To(BeFalse())
		out = act("ann", map[string]any{"tool": "contact-update", "id": "ct-nope", "name": "y"})
		Expect(out["ok"]).To(BeFalse())
	})

	It("serves a write tool only through act, and a read tool only through read", func() {
		_, err := rt.Read(ctx, env("ann", "read", "read", map[string]any{"tool": "contact-create", "name": "x"}))
		Expect(err).To(HaveOccurred())
		_, err = rt.Read(ctx, env("ann", "read", "read", map[string]any{"tool": "contact-delete", "id": "x"}))
		Expect(err).To(HaveOccurred())
		_, err = rt.Act(ctx, env("ann", "act", "act", map[string]any{"tool": "contacts-search"}))
		Expect(err).To(HaveOccurred())
		_, err = rt.Read(ctx, env("ann", "read", "read", map[string]any{"tool": "nonsense"}))
		Expect(err).To(HaveOccurred())
	})

	It("does not take the owner from the arguments", func() {
		act("ann", map[string]any{"tool": "contact-create", "name": "Mine"})
		out := read("bob", map[string]any{"tool": "contacts-search", "owner": "ann"})
		Expect(out["ok"]).To(BeFalse())
	})

	It("serves the page: list, edit, delete and export", func() {
		created := read("ann", map[string]any{"tool": "ui-contact-create", "name": "Marco Rossi", "phones": []any{map[string]any{"value": "5550123"}}})
		Expect(created["ok"]).To(BeTrue())
		id := created["contact"].(map[string]any)["id"]
		Expect(read("ann", map[string]any{"tool": "ui-contacts-list"})["contacts"]).To(HaveLen(1))
		Expect(read("ann", map[string]any{"tool": "ui-contacts-list", "query": "zzz"})["contacts"]).To(BeEmpty())
		Expect(read("ann", map[string]any{"tool": "ui-contact-update", "id": id, "name": "Marco R."})["contact"].(map[string]any)["name"]).To(Equal("Marco R."))
		x := read("ann", map[string]any{"tool": "contacts-export"})
		Expect(x["filename"]).To(Equal("contacts.vcf"))
		Expect(x["vcard"]).To(ContainSubstring("FN:Marco R."))
		Expect(read("bob", map[string]any{"tool": "contacts-export"})["vcard"]).To(BeEmpty())
		Expect(read("ann", map[string]any{"tool": "ui-contact-delete", "id": id})["ok"]).To(BeTrue())
	})
})
