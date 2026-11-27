package collections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/priority"
	"github.com/mudler/LocalAI/core/privacy"
)

// ErrSearchNotPrivate means a search for a request that may not keep content
// would leave its query in the traces: the collections reach the embeddings
// route over the network with an API key, so the privacy level of the request
// cannot travel with the call.
var ErrSearchNotPrivate = errors.New("collections: the search of a zero-retention chat is not available, because the collections call the API with a key")

// privateQueries are the queries of the searches that run now for a request
// that may not keep its content. The collections library makes the embedding
// and rerank calls on contexts of its own, so the level of the request cannot
// reach them by the context; the transport recognises those calls by the
// query they carry and puts the level on them.
type privateQueries struct {
	mu sync.Mutex
	m  map[string]privateQuery
}

type privateQuery struct {
	level privacy.Level
	n     int
}

// hold marks query as private at level until the returned release.
func (q *privateQueries) hold(query string, level privacy.Level) (release func()) {
	key := jsonText(query)
	q.mu.Lock()
	if q.m == nil {
		q.m = map[string]privateQuery{}
	}
	e := q.m[key]
	e.n++
	if level > e.level {
		e.level = level
	}
	q.m[key] = e
	q.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			q.mu.Lock()
			defer q.mu.Unlock()
			e := q.m[key]
			if e.n--; e.n <= 0 {
				delete(q.m, key)
			} else {
				q.m[key] = e
			}
		})
	}
}

// levelOf is the highest level of a held query that body carries.
func (q *privateQueries) levelOf(body []byte) privacy.Level {
	q.mu.Lock()
	defer q.mu.Unlock()
	l := privacy.Normal
	for key, e := range q.m {
		if e.level > l && key != "" && bytes.Contains(body, []byte(key)) {
			l = e.level
		}
	}
	return l
}

// carries reports whether body carries a held query.
func (q *privateQueries) carries(body []byte) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for key := range q.m {
		if key != "" && bytes.Contains(body, []byte(key)) {
			return true
		}
	}
	return false
}

func (q *privateQueries) empty() bool {
	if q == nil {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.m) == 0
}

// jsonText is s as it appears inside a JSON string of a request body.
func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return strings.TrimSuffix(strings.TrimPrefix(string(b), `"`), `"`)
}

// maxPrivateBody bounds the body the transport reads to look for a query.
const maxPrivateBody = 16 << 20

// privateTransport puts the privacy level of a held query on the calls that
// carry it (see privateQueries). It also sets the priority class of each call:
// a call that carries the query of a running search keeps the default class,
// because a chat may wait on it; every other call indexes content, which no
// one waits on token by token, and goes as batch work.
type privateTransport struct {
	next    http.RoundTripper
	queries *privateQueries
	// searches holds the queries of the searches that run now, at any level.
	searches *privateQueries
}

func (t privateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil || (t.queries.empty() && t.searches.empty()) {
		return t.next.RoundTrip(req.WithContext(priority.WithClass(req.Context(), priority.Batch)))
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxPrivateBody+1))
	if err != nil {
		_ = req.Body.Close()
		return nil, err
	}
	ctx := req.Context()
	if l := t.queries.levelOf(raw); l.Incognito() {
		ctx = privacy.WithLevel(ctx, l)
	}
	if !t.searches.carries(raw) {
		ctx = priority.WithClass(ctx, priority.Batch)
	}
	if len(raw) > maxPrivateBody {
		// A body over the limit (a large ingest, say) is no search call. It
		// goes on whole: the part already read, then the rest of the stream.
		// The level of a held query in that part still applies.
		orig := req.Body
		req = req.Clone(ctx)
		req.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(raw), orig), orig}
		req.GetBody = nil
		return t.next.RoundTrip(req)
	}
	_ = req.Body.Close()
	req = req.Clone(ctx)
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	return t.next.RoundTrip(req)
}

// privateSearch prepares a search for the request of ctx: it holds the query
// while the search runs, or refuses it when the level cannot travel with the
// calls of the search.
func (s *Service) privateSearch(ctx context.Context, query string) (release func(), err error) {
	l := privacy.FromContext(ctx)
	if privacy.AllowsLevel(l, privacy.StoreTrace) {
		return func() {}, nil
	}
	if !s.inProcess {
		return nil, ErrSearchNotPrivate
	}
	return s.private.hold(query, l), nil
}
