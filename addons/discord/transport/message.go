// SPDX-License-Identifier: MIT
package transport

import (
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// readable says whether a message can be one that the bot reads at all: it was
// written by a person, in a normal way. The bot's own messages, those of any
// other bot, of webhooks and of Discord (joins, pins, thread starters) are
// left out, so two bots can never answer each other.
func readable(m *discordgo.Message, bot string) bool {
	if m.Author == nil || m.Author.ID == "" || m.Author.ID == bot || m.Author.Bot || m.Author.System || m.WebhookID != "" {
		return false
	}
	return m.Type == discordgo.MessageTypeDefault || m.Type == discordgo.MessageTypeReply
}

func users(in []*discordgo.User) []User {
	out := make([]User, 0, len(in))
	for _, u := range in {
		if u != nil {
			out = append(out, User{ID: u.ID, Username: u.Username, GlobalName: u.GlobalName, Bot: u.Bot})
		}
	}
	return out
}

// mediaRef names a file of a message so that the op media can find it again,
// with a fresh signed URL: channel, message and attachment IDs.
func mediaRef(channel, message, attachment string) string {
	return channel + "/" + message + "/" + attachment
}

// splitRef is the reverse of mediaRef.
func splitRef(ref string) (channel, message, attachment string, ok bool) {
	p := strings.Split(ref, "/")
	if len(p) != 3 || !isSnowflake(p[0]) || !isSnowflake(p[1]) || !isSnowflake(p[2]) {
		return "", "", "", false
	}
	return p[0], p[1], p[2], true
}

// message turns a Gateway message into the update that the host reads, or
// false when the bot does not read it:
//
//   - A message in a direct conversation (no server) is direct. The host
//     answers it only for a listed person, or when the binding accepts anyone.
//   - A message in a server channel addresses the bot when it mentions the
//     bot or replies to a message of the bot. Anything else is not read. The
//     chat is the channel, so an allowlist of channels works.
//   - A message in a thread has the parent channel as its chat and the thread
//     as its thread, so the allowlist of the channel covers its threads. It
//     addresses the bot with a mention, or when the bot follows the thread
//     (the option followThreads): a thread in which the bot was mentioned.
//   - @everyone, @here and role mentions never address the bot.
//
// info and known describe the channel of a message of a server. The caller
// holds l.mu.
func (l *link) message(m *discordgo.Message, info chanInfo, known bool) (contract.ConnectorMessage, bool) {
	bot := l.bot.ID
	mentions := users(m.Mentions)
	addressed := false
	for _, u := range m.Mentions {
		if u != nil && u.ID == bot {
			addressed = true
		}
	}
	if r := m.ReferencedMessage; r != nil && r.Author != nil && r.Author.ID == bot {
		addressed = true
	}
	author := User{ID: m.Author.ID, Username: m.Author.Username, GlobalName: m.Author.GlobalName}
	out := contract.ConnectorMessage{
		SenderID:   author.ID,
		SenderName: author.Display(),
		MessageID:  m.ID,
		Text:       Plain(m.Content, mentions, bot),
	}
	// Only the ID names a sender: a username can change hands, so a name
	// in the allowlist would not be a safe key.
	if m.MessageReference != nil && m.Type == discordgo.MessageTypeReply {
		out.ReplyTo = m.MessageReference.MessageID
	}
	switch {
	case m.GuildID == "":
		out.Direct, out.Chat = true, m.ChannelID
	case known && IsThread(info.typ) && info.parent != "":
		out.Chat, out.Thread = info.parent, m.ChannelID
		out.Mention = addressed || l.follows(info.parent, m.ChannelID)
		if addressed && l.mode != FollowNever {
			l.follow(info.parent, m.ChannelID)
		}
	default:
		out.Chat, out.Mention = m.ChannelID, addressed
	}
	if !out.Direct && !out.Mention {
		return contract.ConnectorMessage{}, false
	}
	for _, a := range m.Attachments {
		if a != nil && isSnowflake(a.ID) && len(out.Media) < contract.MaxConnectorMedia {
			out.Media = append(out.Media, mediaRef(m.ChannelID, m.ID, a.ID))
		}
	}
	out.Unsupported = out.Text == "" && len(out.Media) == 0
	return out, true
}
