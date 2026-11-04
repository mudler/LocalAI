// SPDX-License-Identifier: MIT
package transport

import (
	"regexp"
	"strings"
)

// zwsp is a zero-width space. Put inside a mention it keeps the text as it is
// for the eye and stops Discord from reading it as a mention.
const zwsp = "​"

var (
	// <@123>, <@!123> and <@&123>: a person or a role.
	mentionTag = regexp.MustCompile(`<@([!&]?)(\d+)>`)
	// @everyone and @here, also when a letter or a zero-width mark follows.
	broadcast = regexp.MustCompile(`@(everyone|here)\b`)
)

// Escape makes the text of an answer safe to send: it cannot mention anyone.
// The model writes this text from untrusted input, so a mention in it is
// broken up. Every message also carries allowed_mentions with an empty parse
// list, which is the guard that holds when this one misses a case.
func Escape(s string) string {
	s = broadcast.ReplaceAllString(s, "@"+zwsp+"$1")
	return mentionTag.ReplaceAllString(s, "<"+zwsp+"@$1$2>")
}

// Format is the markup step of an answer. Discord reads Markdown, so the text
// stays as it is, except for the mentions.
func Format(markdown string) string { return Escape(markdown) }

// Plain turns the content of a message into the text the agent reads. The bot's
// own mention is removed, and a mention of another person becomes @name, from
// the mentions of the message. A role or @everyone stays as written.
func Plain(content string, mentions []User, bot string) string {
	names := map[string]string{}
	for _, u := range mentions {
		names[u.ID] = u.Display()
	}
	out := mentionTag.ReplaceAllStringFunc(content, func(tag string) string {
		m := mentionTag.FindStringSubmatch(tag)
		if m[1] == "&" {
			return tag
		}
		if m[2] == bot {
			return ""
		}
		if n := names[m[2]]; n != "" {
			return "@" + n
		}
		return tag
	})
	return strings.TrimSpace(out)
}
