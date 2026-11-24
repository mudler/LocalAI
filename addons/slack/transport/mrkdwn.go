// SPDX-License-Identifier: MIT
package transport

import (
	"regexp"
	"strings"
)

// Slack marks text with mrkdwn, not Markdown: *bold*, _italic_, ~strike~,
// <url|label> links, and &, < and > escaped. A model writes Markdown, so the
// add-on converts it. Code blocks and code spans are kept as code: only their
// special characters are escaped.

var (
	mdImage   = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	mdLink    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	mdBold    = regexp.MustCompile(`\*\*(\S(?:.*?\S)?)\*\*|__(\S(?:.*?\S)?)__`)
	mdItalic  = regexp.MustCompile(`(^|[^\w*])\*(\S(?:[^*]*?\S)?)\*($|[^\w*])`)
	mdStrike  = regexp.MustCompile(`~~(\S(?:.*?\S)?)~~`)
	mdHeading = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*$`)
	mdBullet  = regexp.MustCompile(`^(\s*)[-*+]\s+`)
)

// boldMark stands for a bold asterisk while the italics are converted.
const boldMark = "\x00"

// escape escapes the three characters that mrkdwn reserves.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Mrkdwn converts Markdown to Slack mrkdwn.
func Mrkdwn(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	inCode := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			// Slack shows no language name: the fence stays bare.
			inCode = !inCode
			lines[i] = "```"
			continue
		}
		if inCode {
			lines[i] = escape(line)
			continue
		}
		lines[i] = inline(line)
	}
	return strings.Join(lines, "\n")
}

// inline converts one line outside a code block. Code spans are kept.
func inline(line string) string {
	if m := mdHeading.FindStringSubmatch(line); m != nil {
		return "*" + spans(m[1], func(s string) string { return strings.ReplaceAll(s, "*", "") }) + "*"
	}
	line = mdBullet.ReplaceAllString(line, "$1• ")
	return spans(line, func(s string) string { return s })
}

// spans converts the text between code spans, and only escapes the spans.
func spans(line string, extra func(string) string) string {
	parts := strings.Split(line, "`")
	for i, p := range parts {
		if i%2 == 1 && i < len(parts)-1 {
			parts[i] = escape(p)
			continue
		}
		parts[i] = extra(text(p))
	}
	return strings.Join(parts, "`")
}

func text(s string) string {
	s = escape(s)
	s = mdImage.ReplaceAllString(s, "<$2|$1>")
	s = mdLink.ReplaceAllString(s, "<$2|$1>")
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		inner := mdBold.FindStringSubmatch(m)
		body := inner[1]
		if body == "" {
			body = inner[2]
		}
		return boldMark + body + boldMark
	})
	s = mdItalic.ReplaceAllString(s, "${1}_${2}_${3}")
	s = mdStrike.ReplaceAllString(s, "~$1~")
	return strings.ReplaceAll(s, boldMark, "*")
}

var (
	slackUserMention = regexp.MustCompile(`<@([A-Z0-9]+)(?:\|[^>]*)?>`)
	slackLink        = regexp.MustCompile(`<((?:https?|mailto):[^|>]+)(?:\|([^>]*))?>`)
	slackChannel     = regexp.MustCompile(`<#[A-Z0-9]+\|([^>]*)>`)
	slackSpecial     = regexp.MustCompile(`<!(here|channel|everyone)[^>]*>`)
)

// Plain turns the mrkdwn of a message that a person wrote into the text the
// agent reads: the mention of the bot goes, links show their address, and the
// escaped characters come back.
func Plain(text, botUser string) string {
	if botUser != "" {
		text = strings.ReplaceAll(text, "<@"+botUser+">", "")
	}
	text = slackUserMention.ReplaceAllString(text, "@$1")
	text = slackChannel.ReplaceAllString(text, "#$1")
	text = slackSpecial.ReplaceAllString(text, "@$1")
	text = slackLink.ReplaceAllStringFunc(text, func(m string) string {
		p := slackLink.FindStringSubmatch(m)
		if p[2] == "" || p[2] == p[1] {
			return p[1]
		}
		return p[2] + " (" + p[1] + ")"
	})
	text = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text)
	return strings.TrimSpace(text)
}

// Tokens are the two tokens of a Slack app. The host keeps them as one
// sealed secret: the page joins them with a space.
type Tokens struct {
	// Bot is the bot token (xoxb-): it reads and writes messages.
	Bot string
	// App is the app-level token (xapp-): it opens the Socket Mode link.
	App string
}

// ParseTokens reads the tokens of the secret of a binding, in any order.
func ParseTokens(secret string) Tokens {
	var t Tokens
	for f := range strings.FieldsSeq(secret) {
		switch {
		case strings.HasPrefix(f, "xoxb-"):
			t.Bot = f
		case strings.HasPrefix(f, "xapp-"):
			t.App = f
		}
	}
	return t
}
