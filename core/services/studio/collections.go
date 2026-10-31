package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// CollectionHit is one passage a collection search found.
type CollectionHit struct {
	Content    string
	Similarity float32
	Metadata   map[string]string
}

// ErrNoCollection means the user has no collection of that name. Another user's
// collection is the same answer.
var ErrNoCollection = errors.New("studio: collection not found")

// Collections is the knowledge base the chats can search. Every call runs as
// the user and sees only the user's own collections.
type Collections interface {
	// Allowed reports whether the user may use collections at all.
	Allowed(ctx context.Context, user string) bool
	List(ctx context.Context, user string) ([]string, error)
	Search(ctx context.Context, user, collection, query string, limit int) ([]CollectionHit, error)
}

// The bounds of a search answer, so one search cannot fill the context.
const (
	collectionDefaultHits = 5
	collectionMaxHits     = 20
	collectionHitChars    = 2000
	collectionAnswerBytes = 16 * 1024
)

// namedOnlyTools are host tools a sub-agent gets only when its parent names
// them: asking for all of the parent's tools does not include them.
var namedOnlyTools = map[string]bool{"list_collections": true, "search_collection": true}

// collectionTools returns the two collection tools, or none when the server
// has no collections or the user may not use them.
//
// A chat with an agent gets them only when the agent names collections, and
// then they list and search those collections only. The second result names the
// granted collections the user does not have.
func (s *Service) collectionTools(ctx context.Context, a workspace.Actor, sc *scope) ([]session.Tool, []string) {
	c := s.d.Collections
	if c == nil || !c.Allowed(ctx, a.Principal) || (sc != nil && len(sc.collections) == 0) {
		return nil, nil
	}
	var missing []string
	if sc != nil {
		mine, err := c.List(ctx, a.Principal)
		if err == nil {
			have := set(mine)
			for _, n := range sc.collNames {
				if !have[n] {
					missing = append(missing, "collection "+n)
				}
			}
		}
	}
	tools := []session.Tool{
		{
			Name:        "list_collections",
			Description: "List the names of your collections, the knowledge bases you can search.",
			Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				names, err := c.List(ctx, a.Principal)
				if err != nil {
					return "", fmt.Errorf("could not list collections: %w", err)
				}
				if sc != nil {
					names = slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !sc.collections[n] })
				}
				if names == nil {
					names = []string{}
				}
				b, _ := json.Marshal(map[string]any{"collections": names})
				return string(b), nil
			},
		},
		{
			Name:        "search_collection",
			Description: "Search one of your collections and get the passages that match best.",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"collection":{"type":"string","description":"The collection's name, from list_collections."},` +
				`"query":{"type":"string","description":"What to look for."},` +
				`"limit":{"type":"integer","description":"How many passages, 1 to 20. Default 5."}},` +
				`"required":["collection","query"]}`),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var args struct {
					Collection string `json:"collection"`
					Query      string `json:"query"`
					Limit      int    `json:"limit"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				if strings.TrimSpace(args.Collection) == "" || strings.TrimSpace(args.Query) == "" {
					return "", errors.New("collection and query are required")
				}
				limit := args.Limit
				if limit <= 0 {
					limit = collectionDefaultHits
				}
				limit = min(limit, collectionMaxHits)
				if sc != nil && !sc.collections[args.Collection] {
					return "", errors.New("collection not found")
				}
				hits, err := c.Search(ctx, a.Principal, args.Collection, args.Query, limit)
				if errors.Is(err, ErrNoCollection) {
					return "", errors.New("collection not found")
				}
				if err != nil {
					return "", fmt.Errorf("the search failed: %w", err)
				}
				return collectionAnswer(hits, limit), nil
			},
		},
	}
	return tools, missing
}

// collectionAnswer renders hits as JSON within the bounds above.
func collectionAnswer(hits []CollectionHit, limit int) string {
	type hit struct {
		Content    string            `json:"content"`
		Similarity float32           `json:"similarity,omitempty"`
		Metadata   map[string]string `json:"metadata,omitempty"`
	}
	out := make([]hit, 0, min(len(hits), limit))
	size := 0
	for _, h := range hits {
		if len(out) >= limit {
			break
		}
		content := h.Content
		if len(content) > collectionHitChars {
			content = content[:collectionHitChars] + "…"
		}
		if size+len(content) > collectionAnswerBytes && len(out) > 0 {
			break
		}
		size += len(content)
		out = append(out, hit{Content: content, Similarity: h.Similarity, Metadata: h.Metadata})
	}
	b, _ := json.Marshal(map[string]any{"results": out})
	return string(b)
}
