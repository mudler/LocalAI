package application

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
)

// studioCollections lets the chats search the collections of the collections service.
// It asks for the pool at each call: the service starts after the chats service.
type studioCollections struct{ a *Application }

// owner maps the chat's user to the user the collections are kept under. The
// chats name the unauthenticated user "anonymous"; the collections name it "".
func (studioCollections) owner(user string) string {
	if user == studio.AnonymousPrincipal {
		return ""
	}
	return user
}

// Allowed holds when the collections service runs and the user has the collections feature,
// the rule of /api/collections.
func (c studioCollections) Allowed(ctx context.Context, user string) bool {
	if c.a.CollectionsService() == nil {
		return false
	}
	if c.a.authDB == nil {
		return true
	}
	u, err := c.a.loadStudioUser(ctx, user)
	return err == nil && auth.HasFeatureAccess(c.a.authDB, u, auth.FeatureCollections)
}

func (c studioCollections) List(ctx context.Context, user string) ([]string, error) {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return nil, studio.ErrNoCollection
	}
	return svc.ListForUser(c.owner(user))
}

func (c studioCollections) Search(ctx context.Context, user, collection, query string, limit int) ([]studio.CollectionHit, error) {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return nil, studio.ErrNoCollection
	}
	res, err := svc.SearchForUser(c.owner(user), collection, query, limit)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, studio.ErrNoCollection
		}
		return nil, err
	}
	out := make([]studio.CollectionHit, 0, len(res))
	for _, r := range res {
		out = append(out, studio.CollectionHit{Content: r.Content, Similarity: r.Similarity, Metadata: r.Metadata})
	}
	return out, nil
}
