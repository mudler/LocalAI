// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/builtin"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeOwner struct {
	ids []string
	err error
}

func (f fakeOwner) InstalledIDs(context.Context) ([]string, error) { return f.ids, f.err }

func pinnedIDs(s pins.Store, user string) []string {
	list, err := s.List(context.Background(), user)
	Expect(err).ToNot(HaveOccurred())
	out := []string{}
	for _, p := range list {
		out = append(out, p.AddonID)
	}
	return out
}

var _ = Describe("sweepAddonPins", func() {
	It("never deletes the built-in pins", func() {
		// No page is built in today, so the test makes up two.
		DeferCleanup(builtin.SetCatalogForTest([]builtin.Entry{
			{ID: "guide", Kind: builtin.KindPage, TitleKey: "items.chat", Icon: "chat", MainPath: "/app/chat"},
			{ID: "help", Kind: builtin.KindPage, TitleKey: "items.models", Icon: "boxes", MainPath: "/app/models"},
		}))
		ctx := context.Background()
		s, _ := pins.NewFileStore("")
		_ = s.Replace(ctx, "u1", []string{"guide", "help", "notes"})
		t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		owner := fakeOwner{ids: []string{}}
		sweepAddonPins(ctx, s, owner, t0)
		sweepAddonPins(ctx, s, owner, t0.Add(pins.Retention+time.Hour))
		// notes is not installed and expired; the built-ins are always present.
		got := pinnedIDs(s, "u1")
		Expect(slices.Equal(got, []string{"guide", "help"})).To(BeTrue(), "unexpected pins %v", got)
	})

	It("is skipped when the owner does not answer", func() {
		ctx := context.Background()
		s, _ := pins.NewFileStore("")
		_ = s.Replace(ctx, "u1", []string{"notes"})
		t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		owner := fakeOwner{err: errors.New("owner unavailable")}
		sweepAddonPins(ctx, s, owner, t0)
		sweepAddonPins(ctx, s, owner, t0.Add(pins.Retention+time.Hour))
		list, _ := s.List(ctx, "u1")
		Expect(list).To(HaveLen(1), "a sweep without an answer must neither mark nor delete: %+v", list)
		Expect(list[0].MissingSince).To(BeNil())
	})
})
