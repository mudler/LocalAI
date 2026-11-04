// SPDX-License-Identifier: MIT
package studio

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// translateKind adds the manifest's translations of a connector kind to its
// view. The maps carry every language. When the request names a language that
// the kind translates (the lang query parameter, else Accept-Language), the
// resolved text goes in the localized fields, each falling back to English on
// its own. Title and Description never change.
func translateKind(out *schema.ChatsConnectorKind, tr map[string]contract.KindText, explicit, acceptLanguage string) {
	if len(tr) == 0 {
		return
	}
	tags := make([]string, 0, len(tr))
	for tag, t := range tr {
		tags = append(tags, tag)
		if t.Title != "" {
			if out.Titles == nil {
				out.Titles = map[string]string{}
			}
			out.Titles[tag] = t.Title
		}
		if t.Description != "" {
			if out.Descriptions == nil {
				out.Descriptions = map[string]string{}
			}
			out.Descriptions[tag] = t.Description
		}
	}
	tag := contract.NegotiateLanguage(explicit, acceptLanguage, tags)
	if tag == "" {
		return
	}
	out.Language = tag
	out.LocalizedTitle, out.LocalizedDescription = out.Title, out.Description
	if t := tr[tag]; t.Title != "" {
		out.LocalizedTitle = t.Title
	}
	if t := tr[tag]; t.Description != "" {
		out.LocalizedDescription = t.Description
	}
}
