// SPDX-License-Identifier: MIT
package tools

// The helpers the unit specs reach.
type QueryFilters = queryFilters

var (
	Quote       = quote
	BuildQuery  = buildQuery
	HTMLToText  = htmlToText
	Tidy        = tidy
	Window      = window
	IsText      = isText
	CheckAddr   = checkAddress
	HeaderText  = headerText
	ReplySubj   = replySubject
	ParseBodyOf = func(root gPart) (string, string, int) {
		b := parseBody(root)
		return b.Text, b.Kind, len(b.Attachments)
	}
)
