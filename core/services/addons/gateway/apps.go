// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// appsCall marks a frontend's request for the apps list. Like an artifact
// lookup it is a read, never a typed dispatch.
type appsCall struct{}

const appsListAction = "apps-list"

// Apps returns the apps the actor may see, from the owner. The owner derives
// the actor's visibility and administrator view from its own state; the
// frontend only carries the authenticated identity.
func (g *Gateway) Apps(ctx context.Context, a contract.Actor) (apps.List, error) {
	if e := g.revalidate(ctx, a); e != nil {
		return apps.List{}, e
	}
	if g.Forward != nil {
		return g.Forward.apps(ctx, a)
	}
	return g.appsLocal(ctx, a)
}

func (g *Gateway) appsLocal(ctx context.Context, a contract.Actor) (apps.List, error) {
	if g.Manager == nil {
		return apps.List{}, ErrUnavailable
	}
	list, e := g.Manager.Apps(ctx, a)
	if e != nil {
		return apps.List{}, e
	}
	return apps.List{Apps: list}, nil
}

// apps forwards the list request to the owner.
func (c *Carrier) apps(ctx context.Context, a contract.Actor) (apps.List, error) {
	var raw [16]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return apps.List{}, ErrUnavailable
	}
	request := contract.Request{OperationID: "l" + hex.EncodeToString(raw[:]), Action: appsListAction}
	resp, e := c.send(ctx, a, wireRequest{Request: request, Apps: &appsCall{}})
	if e != nil {
		return apps.List{}, e
	}
	defer func() { _ = resp.Body.Close() }()
	var list apps.List
	if e = decode(io.LimitReader(resp.Body, 2<<20), &list); e != nil {
		return apps.List{}, ErrUnavailable
	}
	return list, nil
}

// icon serves the verified icon of an app the actor may see. The bytes come
// from the owner's immutable package, are checked against the digest the
// receipt recorded at install and are never served from an addon process.
func (g *Gateway) icon(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	if g.Manager == nil {
		return ErrUnavailable
	}
	receipt, e := g.Manager.AppIcon(ctx, a, r.Request.AddonID, r.Request.Generation)
	if e != nil {
		return e
	}
	icon := receipt.Icon
	root, e := os.OpenRoot(receipt.Directory)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = root.Close() }()
	f, e := root.Open(icon.Path)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, contract.MaxIconBytes+1))
	if e != nil || int64(len(b)) != icon.Size || icon.Size > contract.MaxIconBytes || fmt.Sprintf("sha256:%x", sha256.Sum256(b)) != icon.Digest {
		// The package changed after it was verified. Never serve it.
		return ErrUnavailable
	}
	if e = g.revalidate(ctx, a); e != nil {
		return e
	}
	h := w.Header()
	h.Set("Content-Type", icon.MediaType)
	h.Set("Content-Length", strconv.Itoa(len(b)))
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, e = w.Write(b)
	return e
}
