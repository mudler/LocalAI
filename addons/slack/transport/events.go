// SPDX-License-Identifier: MIT
package transport

import (
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// event is the part of a Slack event that the add-on reads: a message or an
// app_mention.
type event struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"`
	User        string `json:"user"`
	BotID       string `json:"bot_id"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	Files       []struct {
		ID string `json:"id"`
	} `json:"files"`
}

// The subtypes of a message that a person wrote. The others (edits,
// deletions, joins, the messages of bots) are not read.
var personal = map[string]bool{"": true, "file_share": true, "thread_broadcast": true}

// message turns an event into the message that the host reads, or false when
// the bot does not read it:
//
//   - A direct message (channel type im) is direct. A reply in a thread of the
//     direct conversation belongs to that thread.
//   - A mention of the bot (app_mention) in a channel addresses the bot. The
//     answer goes in the thread of the mention. Which threads the bot then
//     follows, so that a later reply without a mention addresses it, is the
//     option followThreads (see FollowOwn, FollowAny and FollowNever).
//   - Any other message of a channel is not read. Slack also sends a mention
//     as a plain message; that copy is not read either.
//
// The caller holds ss.mu.
func (ss *session) message(e event) (contract.ConnectorMessage, bool) {
	if e.User == "" || e.User == ss.bot.UserID || e.BotID != "" || !personal[e.Subtype] || e.Channel == "" {
		return contract.ConnectorMessage{}, false
	}
	m := contract.ConnectorMessage{Chat: e.Channel, SenderID: e.User, MessageID: e.TS, Text: Plain(e.Text, ss.bot.UserID)}
	switch {
	case e.Type == "app_mention":
		m.Mention = true
		m.Thread = e.ThreadTS
		if m.Thread == "" {
			m.Thread = e.TS
		}
		// A mention inside a thread that other people started is answered,
		// and the thread is followed only when the mode says so.
		if starts(ss.mode, e.ThreadTS == "") {
			ss.follow(e.Channel, m.Thread)
		}
	case e.Type != "message":
		return contract.ConnectorMessage{}, false
	case e.ChannelType == "im":
		m.Direct, m.Thread = true, e.ThreadTS
	case ss.bot.UserID != "" && strings.Contains(e.Text, "<@"+ss.bot.UserID):
		return contract.ConnectorMessage{}, false // the app_mention of the same message is read
	case e.ThreadTS != "" && ss.follows(e.Channel, e.ThreadTS):
		m.Mention, m.Thread = true, e.ThreadTS
	default:
		return contract.ConnectorMessage{}, false
	}
	for _, f := range e.Files {
		if f.ID != "" && len(m.Media) < contract.MaxConnectorMedia {
			m.Media = append(m.Media, f.ID)
		}
	}
	m.Unsupported = m.Text == "" && len(m.Media) == 0
	m.Link = Permalink(ss.bot.URL, e.Channel, e.TS, m.Thread)
	return m, true
}

// Permalink builds the address that opens a message, or the thread it
// belongs to, in the Slack app or the browser. It is the format Slack itself
// uses (archives/<channel>/p<ts without the dot>), built from the workspace
// address that auth.test returns, so no extra scope is needed. It returns ""
// when the workspace address is unknown or is not https.
func Permalink(workspace, channel, ts, thread string) string {
	u, err := url.Parse(strings.TrimSpace(workspace))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || channel == "" || ts == "" {
		return ""
	}
	root := ts
	if thread != "" {
		root = thread
	}
	link := "https://" + u.Host + "/archives/" + url.PathEscape(channel) + "/p" + strings.ReplaceAll(root, ".", "")
	if thread != "" && thread != ts {
		link += "?thread_ts=" + url.QueryEscape(thread) + "&cid=" + url.QueryEscape(channel)
	}
	return link
}

// follow remembers a thread that the bot answers in, and keeps it on disk
// when the add-on has a state directory. The caller holds ss.mu.
func (ss *session) follow(channel, thread string) {
	now := time.Now()
	if len(ss.threads) >= maxThreads {
		for k, at := range ss.threads {
			if now.Sub(at) > threadLife || len(ss.threads) >= maxThreads {
				delete(ss.threads, k)
			}
		}
	}
	ss.threads[threadKey(channel, thread)] = now
	ss.saveThreads()
}

// follows says whether a reply without a mention in the thread is addressed
// to the bot. A follow mode of never follows nothing. A followed thread that
// is active is renewed, so it lives for threadLife after its last message.
// The caller holds ss.mu.
func (ss *session) follows(channel, thread string) bool {
	if ss.mode == FollowNever {
		return false
	}
	k := threadKey(channel, thread)
	at, ok := ss.threads[k]
	if !ok || time.Since(at) >= threadLife {
		return false
	}
	if time.Since(at) >= touchEvery {
		ss.threads[k] = time.Now()
		ss.saveThreads()
	}
	return true
}

// saveThreads writes the followed threads. A failure is logged by the
// owner of the session once; the bot goes on with the set in memory.
func (ss *session) saveThreads() {
	if err := saveThreads(ss.dir, ss.key, ss.threads); err != nil && !ss.saveFailed {
		ss.saveFailed = true
		slog.Warn("slack: could not save the followed threads", "error", err)
	}
}
