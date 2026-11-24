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

type sourceKey struct{}

// WithSource returns ctx carrying the source of a run. The runner puts it in
// the context of every start, so a starter that handles automatic runs on
// their own path can refuse a manual one.
func WithSource(ctx context.Context, src Source) context.Context {
	return context.WithValue(ctx, sourceKey{}, src)
}

// SourceFrom returns the source WithSource stored, or "".
func SourceFrom(ctx context.Context) Source {
	src, _ := ctx.Value(sourceKey{}).(Source)
	return src
}
