package library

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

func infoByID(list []Info, id string) (Info, bool) {
	for _, i := range list {
		if i.ID == id {
			return i, true
		}
	}
	return Info{}, false
}

var _ = Describe("the source listing", func() {
	It("lists the three kinds and the defaults", func() {
		store := open(GinkgoT().TempDir())
		list := List(store)
		kinds := map[catalog.Kind]bool{}
		enabledModels := 0
		for _, i := range list {
			kinds[i.Kind] = true
			Expect(i.Default).To(BeTrue(), "%s: a fresh default is a default, not removed and not suspended: %+v", i.ID, i)
			Expect(i.Removed).To(BeFalse(), "%s: %+v", i.ID, i)
			Expect(i.Suspended).To(BeFalse(), "%s: %+v", i.ID, i)
			if i.Kind == catalog.KindModel && i.Enabled {
				enabledModels++
			}
			if i.Kind != catalog.KindModel {
				Expect(i.Enabled).To(BeTrue(), "%s: a default add-on or backend source starts enabled: %+v", i.ID, i)
			}
		}
		// Both model defaults are shipped, but a build reads one of them.
		Expect(enabledModels).To(Equal(1), "model sources enabled")
		for _, k := range []catalog.Kind{catalog.KindAddon, catalog.KindBackend, catalog.KindModel} {
			Expect(kinds[k]).To(BeTrue(), "no %s source listed", k)
		}
	})

	It("leaves a tombstone when a default is removed and deletes a user source", func() {
		dir := GinkgoT().TempDir()
		store := open(dir)
		_, err := Add(store, catalog.KindModel, "https://example.org/m.yaml", "team", "Team", catalog.FormatGallery)
		Expect(err).ToNot(HaveOccurred())
		was, err := Remove(store, DefaultModelSourceID)
		Expect(err).ToNot(HaveOccurred())
		Expect(was).To(BeTrue(), "remove default")
		was, err = Remove(store, "team")
		Expect(err).ToNot(HaveOccurred())
		Expect(was).To(BeFalse(), "remove user source")
		// The CLI and the routes read the same file: a new store sees the tombstone
		// and does not seed the default again.
		again := open(dir)
		list := List(again)
		d, ok := infoByID(list, DefaultModelSourceID)
		Expect(ok).To(BeTrue(), "tombstone")
		Expect(d.Removed).To(BeTrue(), "tombstone = %+v", d)
		Expect(d.Enabled).To(BeFalse(), "tombstone = %+v", d)
		Expect(d.Default).To(BeTrue(), "tombstone = %+v", d)
		_, ok = infoByID(list, "team")
		Expect(ok).To(BeFalse(), "a removed user source is still listed")
		_, err = Remove(again, "team")
		Expect(IsNotFound(err)).To(BeTrue(), "removing it twice = %v", err)
		// Enabling the tombstone brings the default back.
		Expect(again.SetEnabled(DefaultModelSourceID, true)).To(Succeed())
		d, _ = infoByID(List(again), DefaultModelSourceID)
		Expect(d.Removed).To(BeFalse(), "after enable: %+v", d)
		Expect(d.Enabled).To(BeTrue(), "after enable: %+v", d)
	})

	It("refuses the same kind and URL twice", func() {
		store := open(GinkgoT().TempDir())
		_, err := Add(store, catalog.KindBackend, "https://example.org/b.yaml", "", "", catalog.FormatGallery)
		Expect(err).ToNot(HaveOccurred())
		_, err = Add(store, catalog.KindBackend, "https://example.org/b.yaml", "other", "", catalog.FormatGallery)
		Expect(errors.Is(err, ErrDuplicate)).To(BeTrue(), "duplicate = %v", err)
	})

	It("hides the defaults and keeps the user sources when the official libraries are off", func() {
		store := open(GinkgoT().TempDir())
		models, backends := parse(config.DefaultGalleriesJSON), parse(config.DefaultBackendGalleriesJSON)
		team := config.Gallery{Name: "Team", URL: "https://example.org/models.yaml"}
		models = append(models, team)
		_, err := MapGalleries(store, catalog.KindModel, models)
		Expect(err).ToNot(HaveOccurred())

		// On: everything is read.
		onM, onB := Reconcile(store, models, backends)
		Expect(onM).To(HaveLen(len(models)), "on: models %v", onM)
		Expect(onB).To(HaveLen(len(backends)), "on: backends %v", onB)

		// Off: the shipped defaults are not read, the user's gallery is.
		store.SetOfficial(false)
		offM, offB := Reconcile(store, models, backends)
		Expect(offM).To(HaveLen(1), "off: models = %+v", offM)
		Expect(offM[0].URL).To(Equal(team.URL), "off: models = %+v", offM)
		Expect(offB).To(BeEmpty(), "off: backends")
		got := ApplyGalleries(store, catalog.KindModel, models)
		Expect(got).To(HaveLen(1), "off: ApplyGalleries = %+v", got)
		Expect(got[0].URL).To(Equal(team.URL), "off: ApplyGalleries = %+v", got)
		for _, i := range List(store) {
			if i.Default {
				Expect(i.Enabled).To(BeFalse(), "off: default %s should be listed as off and suspended: %+v", i.ID, i)
				Expect(i.Suspended).To(BeTrue(), "off: default %s should be listed as off and suspended: %+v", i.ID, i)
				Expect(i.Removed).To(BeFalse(), "off: default %s should be listed as off and suspended: %+v", i.ID, i)
			} else {
				Expect(i.Enabled).To(BeTrue(), "off: user source %s changed: %+v", i.ID, i)
				Expect(i.Suspended).To(BeFalse(), "off: user source %s changed: %+v", i.ID, i)
			}
		}

		// On again: the lists come back from the store.
		store.SetOfficial(true)
		backM, backB := Reconcile(store, offM, offB)
		Expect(backM).To(HaveLen(len(models)), "on again: models %v", backM)
		Expect(backB).To(HaveLen(len(backends)), "on again: backends %v", backB)
	})

	It("keeps a gallery that is not a source on reconcile", func() {
		store := open(GinkgoT().TempDir())
		odd := []config.Gallery{{Name: "odd", URL: "https://example.org/odd.yaml"}}
		m, _ := Reconcile(store, odd, nil)
		Expect(m).To(HaveLen(len(odd)+1), "models = %+v", m) // plus the enabled default model source
		Expect(m[0].URL).To(Equal(odd[0].URL), "order changed: %+v", m)
	})

	It("drops the gallery of a deleted source on reconcile", func() {
		store := open(GinkgoT().TempDir())
		team := config.Gallery{Name: "Team", URL: "https://example.org/team.yaml"}
		_, err := Add(store, catalog.KindModel, team.URL, "team", "Team", catalog.FormatGallery)
		Expect(err).ToNot(HaveOccurred())
		current, _ := Reconcile(store, parse(config.DefaultGalleriesJSON), nil)
		Expect(current).To(HaveLen(2), "setup: %+v", current)
		_, err = Remove(store, "team")
		Expect(err).ToNot(HaveOccurred())
		// The store forgot the source, so without the address the gallery stays.
		kept, _ := Reconcile(store, current, nil)
		Expect(kept).To(HaveLen(2), "a gallery that is not a source is kept: %+v", kept)
		after, _ := Reconcile(store, current, nil, team.URL)
		Expect(after).To(HaveLen(1), "the deleted source's gallery is still read: %+v", after)
		Expect(after[0].URL).ToNot(Equal(team.URL), "the deleted source's gallery is still read: %+v", after)
	})
})
