package application

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
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

// List names the user's own collections, then the collections shared with
// the user under the name the user addresses each by (see WithCollection).
func (c studioCollections) List(ctx context.Context, user string) ([]string, error) {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return nil, studio.ErrNoCollection
	}
	own, err := svc.ListForUser(c.owner(user))
	if err != nil {
		return own, err
	}
	for _, s := range c.a.SharedCollections(ctx, c.owner(user), own) {
		own = append(own, s.Address)
	}
	return own, nil
}

func (c studioCollections) Search(ctx context.Context, user, collection, query string, limit int) ([]studio.CollectionHit, error) {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return nil, studio.ErrNoCollection
	}
	var res []lagi.SearchResult
	err := c.a.WithCollection(ctx, c.owner(user), collection, policy.ActionRead, func(ctx context.Context, owner, name string) error {
		var err error
		res, err = svc.SearchForUser(ctx, owner, name, query, limit)
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") || errors.Is(err, ErrCollectionRole) {
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

// EmbeddingReady holds when the model that indexes the collections is installed.
func (c studioCollections) EmbeddingReady(context.Context) error {
	if c.a.ApplicationConfig() == nil || !c.a.EmbeddingModelInstalled(c.a.ApplicationConfig().Collections.EmbeddingModel) {
		return errors.New("the embedding model is not installed")
	}
	return nil
}

// Add stores text as one entry of the user's collection.
func (c studioCollections) Add(ctx context.Context, user, collection, name, text string) error {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return studio.ErrNoCollection
	}
	err := c.a.WithCollection(ctx, c.owner(user), collection, policy.ActionWrite, func(ctx context.Context, owner, coll string) error {
		_, err := svc.UploadForUser(ctx, owner, coll, name, strings.NewReader(text))
		return err
	})
	if err != nil && (strings.Contains(err.Error(), "not found") || errors.Is(err, ErrCollectionRole)) {
		return studio.ErrNoCollection
	}
	return err
}

// Remove deletes the entry that Add stored under the file name. The collection
// keeps each entry under a key that holds a generated folder before the file
// name, so the entry is found by its base name. An entry that is gone already
// is not an error: the undo has nothing left to do.
func (c studioCollections) Remove(ctx context.Context, user, collection, name string) error {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return studio.ErrNoCollection
	}
	err := c.a.WithCollection(ctx, c.owner(user), collection, policy.ActionWrite, func(_ context.Context, owner, coll string) error {
		entries, err := svc.ListEntriesForUser(owner, coll)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if path.Base(e) == name {
				if _, err := svc.DeleteEntryForUser(owner, coll, e); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil && (strings.Contains(err.Error(), "not found") || errors.Is(err, ErrCollectionRole)) {
		return studio.ErrNoCollection
	}
	return err
}

// EmbeddingModelInstalled reports whether the model that the collections embed
// with is installed: it has a configuration, or a file of that name is in the
// models folder. The collections call this server's embeddings endpoint, which
// answers 404 for a model it does not have.
func (a *Application) EmbeddingModelInstalled(name string) bool {
	if name == "" {
		return false
	}
	if _, ok := a.ModelConfigLoader().GetModelConfig(name); ok {
		return true
	}
	st := a.ApplicationConfig().SystemState
	if st == nil || st.Model.ModelsPath == "" || strings.ContainsAny(name, "/\\") {
		return false
	}
	info, err := os.Stat(filepath.Join(st.Model.ModelsPath, name))
	return err == nil && !info.IsDir()
}
