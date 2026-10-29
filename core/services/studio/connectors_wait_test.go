package studio_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/store"
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

func TestTheWaitForTheEndOfATurnGivesWayWhenTheOutcomeIsRecorded(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	l := lingering(sh)
	r := newConnRig(t, sh, connStore(t), bot, connSay("The report is ready."), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}})
	bot.say(1, 11, "Ada", "ada", "report please")

	g.Eventually(l.polls.Load, 3*time.Second, 10*time.Millisecond).Should(gomega.BeNumerically(">=", 3), "the wait polls the store")
	g.Expect(bot.answers()).To(gomega.BeEmpty(), "the answer waits for the recorded outcome")
	l.done.Store(true)
	g.Eventually(bot.answers, 3*time.Second, 10*time.Millisecond).Should(gomega.Equal([]string{"The report is ready."}))
}

func TestCloseEndsTheWaitForTheEndOfATurnAtOnce(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	l := lingering(sh)
	r := newConnRig(t, sh, connStore(t), bot, connSay("never read"), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}})
	bot.say(1, 11, "Ada", "ada", "report please")
	g.Eventually(l.polls.Load, 3*time.Second, 10*time.Millisecond).Should(gomega.BeNumerically(">=", 3))

	closed := make(chan struct{})
	start := time.Now()
	go func() { r.conns.Close(); close(closed) }()
	// The wait is a five second loop, so a Close that waited for it would take
	// seconds; one that ends it takes a few milliseconds.
	g.Eventually(closed, time.Second).Should(gomega.BeClosed(), "Close waited for the answer")
	g.Expect(time.Since(start)).To(gomega.BeNumerically("<", time.Second))
}
