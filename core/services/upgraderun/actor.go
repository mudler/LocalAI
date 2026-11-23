package upgraderun

import "context"

type actorKey struct{}

// WithActor returns ctx carrying the actor of a run. The service puts it in
// the context of every check it makes (a preview, a start, a retry, the
// check before an item starts and the check after it ended), so a check that
// reaches the owner of the items acts for the person who asked, never as a
// system identity.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor WithActor stored, or "".
func ActorFrom(ctx context.Context) string {
	a, _ := ctx.Value(actorKey{}).(string)
	return a
}

// RetryEligible reports whether a retry of the run takes this item: it is
// retryable and ended failed, rolled back, or unconfirmed with no check
// pending.
func (it *Item) RetryEligible() bool { return it.Retryable && retryablePhase(it) }
