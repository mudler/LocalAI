// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// management serves the catalog and installed-addon actions the management UI
// uses. Each is administrator-only on the owner, whatever the frontend
// checked. The arguments are strict: an unknown field is a denial, so a
// request cannot smuggle a source address or a credential into a read.
func (g *Gateway) management(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	if g.Manager == nil {
		return contract.Result{}, ErrUnavailable
	}
	var out any
	var err error
	switch request.Action {
	case "catalog-search":
		var args struct {
			Text     string `json:"text"`
			SourceID string `json:"sourceId"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		out, err = g.Manager.CatalogSearch(ctx, actor, catalog.Query{Text: args.Text, SourceID: args.SourceID, Offset: args.Offset, Limit: args.Limit})
	case "catalog-details":
		var args struct {
			SourceID string `json:"sourceId"`
			Version  string `json:"version"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		out, err = g.Manager.CatalogDetails(ctx, actor, args.SourceID, request.AddonID, args.Version)
	case "catalog-sources":
		var args struct{}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		var sources []catalog.SourceView
		sources, err = g.Manager.Sources(ctx, actor)
		out = struct {
			Sources []catalog.SourceView `json:"sources"`
		}{sources}
	case "catalog-source-save":
		var src catalog.Source
		if decode(bytes.NewReader(request.Arguments), &src) != nil {
			return contract.Result{}, ErrForbidden
		}
		out, err = g.Manager.SaveSource(ctx, actor, src)
	case "catalog-source-refresh":
		var args struct {
			SourceID string `json:"sourceId"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		out, err = g.Manager.RefreshSource(ctx, actor, args.SourceID)
	case "addons-installed":
		var args struct{}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		var list any
		list, err = g.Manager.Installed(ctx, actor)
		out = struct {
			Addons any `json:"addons"`
		}{list}
	default:
		return contract.Result{}, ErrForbidden
	}
	if err != nil {
		return contract.Result{}, err
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return contract.Result{}, ErrUnavailable
	}
	return contract.Result{Payload: payload}, nil
}
