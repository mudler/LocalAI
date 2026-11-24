package studio

import (
	"time"

	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the words of a schedule in each language", func() {
	parse := func(s string) triggers.Schedule {
		sch, err := triggers.ParseSchedule(s)
		Expect(err).NotTo(HaveOccurred())
		return sch
	}
	It("reads the same in English as Schedule.String", func() {
		for _, s := range []string{"daily 09:00", "weekdays 08:00", "weekly mon,wed 18:30", "weekly sun 07:05", "monthly 5 07:00", "once 2026-12-24 10:00", "*/5 * * * *"} {
			sch := parse(s)
			Expect(scheduleWords(hostmsg.Voice{}, sch)).To(Equal(sch.String()), s)
		}
	})
	It("is worded by the voice", func() {
		it, de := hostmsg.In("it"), hostmsg.In("de")
		Expect(scheduleWords(it, parse("daily 09:00"))).To(Equal("Ogni giorno alle 09:00"))
		Expect(scheduleWords(it, parse("weekly mon,wed 18:30"))).To(Equal("Ogni lunedì e mercoledì alle 18:30"))
		Expect(scheduleWords(it, parse("monthly 5 07:00"))).To(Equal("Il giorno 5 di ogni mese alle 07:00"))
		Expect(scheduleWords(de, parse("weekdays 08:00"))).To(Equal("Jeden Werktag um 08:00"))
		Expect(scheduleWords(hostmsg.In("fr"), parse("weekly tue,thu,fri 06:00"))).To(Equal("Chaque mardi, jeudi et vendredi à 06:00"))
	})
})

var _ = Describe("sizes and words for a person", func() {
	It("keeps the English sizes and writes the decimal mark of the language", func() {
		Expect(formatFileSizeIn(hostmsg.Voice{}, 1536)).To(Equal("1.5 KiB"))
		Expect(formatFileSizeIn(hostmsg.In("it"), 1536)).To(Equal("1,5 KiB"))
		Expect(formatFileSizeIn(hostmsg.In("de"), 1<<20)).To(Equal("1 MiB"))
		Expect(formatFileSizeIn(hostmsg.In("fr"), 1)).To(Equal("1 octet"))
		Expect(formatFileSizeIn(hostmsg.In("es"), 300)).To(Equal("300 bytes"))
	})
	It("lowers the first letter of a word that is not ASCII", func() {
		Expect(lowerFirst("Ögni giorno")).To(Equal("ögni giorno"))
		Expect(lowerFirst("")).To(Equal(""))
	})
	It("words the minimum gap in each language", func() {
		Expect(gapTextIn(hostmsg.In("it"), 5*time.Minute)).To(Equal("5 minuti"))
		Expect(gapText(time.Minute)).To(Equal("minute"))
	})
})

var _ = Describe("the memory of the language of a sender", func() {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	It("keeps a language through short replies and moves it only for a sentence", func() {
		var m langMemory
		m.learn("b", "s", "c", "Ciao, puoi mandarmi il file di ieri?", now)
		get := func(at time.Time) string { l, _ := m.get(langKey{"b", "s:s"}, at); return l }
		Expect(get(now)).To(Equal("it"))
		for _, short := range []string{"ok", "thanks", "yes", "👍", "7K2Q", ""} {
			m.learn("b", "s", "c", short, now.Add(time.Minute))
			Expect(get(now.Add(time.Minute))).To(Equal("it"), short)
		}
		m.learn("b", "s", "c", "Can you send me the report from yesterday please?", now.Add(2*time.Minute))
		Expect(get(now.Add(2 * time.Minute))).To(Equal("en"))
	})
	It("forgets after a day, but a message that says nothing renews it", func() {
		var m langMemory
		m.learn("b", "s", "c", "Hola, puedes enviarme el archivo de ayer?", now)
		get := func(at time.Time) string { l, _ := m.get(langKey{"b", "s:s"}, at); return l }
		m.learn("b", "s", "c", "vale", now.Add(20*time.Hour))
		Expect(get(now.Add(40*time.Hour))).To(Equal("es"), "renewed at 20h, so alive at 40h")
		Expect(get(now.Add(46 * time.Hour))).To(BeEmpty())
	})
	It("finds the language of a chat for a trigger that has no sender", func() {
		var m langMemory
		m.learn("b", "s", "chat-1", "Bonjour, tu peux m'envoyer le fichier d'hier ?", now)
		l, ok := m.get(langKey{"b", "c:chat-1"}, now)
		Expect([]any{l, ok}).To(Equal([]any{"fr", true}))
		_, ok = m.get(langKey{"other", "c:chat-1"}, now)
		Expect(ok).To(BeFalse(), "another connection has its own memory")
	})
})
