// SPDX-License-Identifier: MIT
package transport

import (
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
//     answer goes in the thread of the mention, which the bot then follows: a
//     later reply in that thread addresses the bot without a mention.
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
		ss.follow(e.Channel, m.Thread)
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
	return m, true
}

// follow remembers a thread that the bot answers in.
func (ss *session) follow(channel, thread string) {
	now := time.Now()
	if len(ss.threads) >= maxThreads {
		for k, at := range ss.threads {
			if now.Sub(at) > threadLife || len(ss.threads) >= maxThreads {
				delete(ss.threads, k)
			}
		}
	}
	ss.threads[channel+"\x00"+thread] = now
}

func (ss *session) follows(channel, thread string) bool {
	at, ok := ss.threads[channel+"\x00"+thread]
	return ok && time.Since(at) < threadLife
}
