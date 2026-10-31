// SPDX-License-Identifier: MIT
package transport_test

import (
	"github.com/mudler/LocalAI/addons/slack/slacktest"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

// MatchPost matches a post by its channel, thread, text and edit.
func MatchPost(channel, thread, text string, edited bool) types.GomegaMatcher {
	return WithTransform(func(p slacktest.Post) []any { return []any{p.Channel, p.Thread, p.Text, p.Edited} },
		Equal([]any{channel, thread, text, edited}))
}
