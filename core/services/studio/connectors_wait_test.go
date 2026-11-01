package studio_test

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// lingeringTurn is a store whose turn state still says "running" after the
// turn has ended and sent its end event, as a store does for a moment between
// the event and the record of the outcome. The wait for the answer polls it.
// Until the first turn has ended it answers as the real store does.
type lingeringTurn struct {
	store.Store
	done  atomic.Bool  // the store records the outcome
	polls atomic.Int32 // Turn calls made while it lingered
}

func (l *lingeringTurn) Turn(ctx context.Context, principal, conv string) (store.TurnInfo, error) {
	info, err := l.Store.Turn(ctx, principal, conv)
	if err == nil && !info.Running && info.Last != "" && !l.done.Load() {
		info.Running = true
		l.polls.Add(1)
	}
	return info, err
}

func lingering(sh *shared) *lingeringTurn {
	l := &lingeringTurn{}
	sh.deps = func(d *studio.Deps) { l.Store = d.Store; d.Store = l }
	return l
}

var _ = Describe("connectors wait", func() {
	It("the wait for the end of a turn gives way when the outcome is recorded", func() {
		sh, bot := newShared(), newScriptedBot()
		l := lingering(sh)
		r := newConnRig(sh, connStore(), bot, connSay("The report is ready."), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		bot.say(1, 11, "Ada", "ada", "report please")

		Eventually(l.polls.Load, 3*time.Second, 10*time.Millisecond).Should(BeNumerically(">=", 3), "the wait polls the store")
		Expect(bot.answers()).To(BeEmpty(), "the answer waits for the recorded outcome")
		l.done.Store(true)
		Eventually(bot.answers, 3*time.Second, 10*time.Millisecond).Should(Equal([]string{"The report is ready."}))
	})

	It("close ends the wait for the end of a turn at once", func() {
		sh, bot := newShared(), newScriptedBot()
		l := lingering(sh)
		r := newConnRig(sh, connStore(), bot, connSay("never read"), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		bot.say(1, 11, "Ada", "ada", "report please")
		Eventually(l.polls.Load, 3*time.Second, 10*time.Millisecond).Should(BeNumerically(">=", 3))

		closed := make(chan struct{})
		start := time.Now()
		go func() { r.conns.Close(); close(closed) }()
		// The wait is a five second loop, so a Close that waited for it would take
		// seconds; one that ends it takes a few milliseconds.
		Eventually(closed, time.Second).Should(BeClosed(), "Close waited for the answer")
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
	})
})
