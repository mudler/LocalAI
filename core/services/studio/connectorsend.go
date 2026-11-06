package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// This file is the one door for text that the agent, or a trigger on its
// behalf, writes to a platform chat. It adds the text rules and the audit to
// the outbox (connectoroutbox.go), which keeps the rate limit and the queue.

// The kinds of an outgoing message, as the audit shows them.
const (
	// SendReply is a message in the conversation chat and thread.
	SendReply = "reply"
	// SendChannel is a top-level message in an approved channel of the binding.
	SendChannel = "channel"
	// SendNew is a top-level message in a listed channel that the owner has not
	// approved: it needs the sensitive capability each time.
	SendNew = "new"
	// SendDelivery is the report of a trigger run.
	SendDelivery = "delivery"
)

// ErrMessageEmpty and ErrMessageTooLong are the refusals of the text rules.
var (
	ErrMessageEmpty   = errors.New("studio: the message has no text")
	ErrMessageTooLong = fmt.Errorf("studio: a message has at most %d characters", MaxReportRunes)
)

// Outgoing is one message to write to a chat of a binding.
type Outgoing struct {
	Binding, Chat, Thread, Text string
	// Kind is one of the Send constants and Capability the capability that
	// allowed it; both go to the audit.
	Kind, Capability string
	// Senders and Roles are who made the turn that wrote it, for the audit.
	Senders, Roles []string
	// OnFail is called when a message that waited in the queue is not sent.
	OnFail func(error)
}

// Send writes the message through the outbox. It refuses an empty text and a
// text over MaxReportRunes, breaks the mentions that notify a whole channel,
// and records an audit row without the text. It reports whether the message
// waits for the rate limit instead of going at once.
func (c *Connectors) Send(ctx context.Context, o Outgoing) (queued bool, err error) {
	text := strings.TrimSpace(o.Text)
	switch {
	case text == "":
		return false, ErrMessageEmpty
	case utf8.RuneCountInString(text) > MaxReportRunes:
		return false, ErrMessageTooLong
	}
	text = reportText(text)
	ev := AccessEvent{At: c.o.Clock().UTC(), Binding: o.Binding, Senders: o.Senders, Roles: o.Roles, Capability: o.Capability,
		Action: o.Kind, Channel: o.Chat, Thread: o.Thread, Length: utf8.RuneCountInString(text), Hash: hashOf(text)}
	if b, gerr := c.o.Store.GetBinding(ctx, o.Binding); gerr == nil {
		ev.Kind = b.Kind
	}
	queued, err = c.Post(ctx, Post{Binding: o.Binding, Chat: o.Chat, Thread: o.Thread, Text: text,
		OnFail: func(err error) {
			f := ev
			f.Reason = "failed after waiting"
			c.svc.access.append(f)
			if o.OnFail != nil {
				o.OnFail(err)
			}
		}})
	switch {
	case err != nil:
		ev.Reason = "failed: " + err.Error()
	case queued:
		ev.Allowed, ev.Reason = true, "queued"
	default:
		ev.Allowed, ev.Reason = true, "sent"
	}
	c.svc.access.append(ev)
	return queued, err
}

// ChannelTarget says where a channel message goes and what it needs. A
// channel that is listed and approved needs talkback.channels; one that is
// listed and not approved needs talkback.new_channel; one that is not listed
// is refused, with the list in the reason.
func ChannelTarget(b connectors.Binding, channel string) (kind, capability string, err error) {
	ch, ok := b.FindChannel(channel)
	switch {
	case !ok:
		return "", "", fmt.Errorf("%q is not a channel the owner listed for this bot%s", channel, listedChannels(b))
	case ch.Approved:
		return SendChannel, contract.CapTalkbackChannels, nil
	}
	return SendNew, contract.CapTalkbackNewChan, nil
}

// MatchChannel finds a listed channel by its ID, or else by its label: the
// label match ignores case and a leading "#", so a model that read the name in
// a message finds the channel the owner listed. ok is false when no channel
// matches or the label names more than one.
func MatchChannel(b connectors.Binding, name string) (connectors.Channel, bool) {
	if ch, ok := b.FindChannel(name); ok {
		return ch, true
	}
	want := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "#"))
	if want == "" {
		return connectors.Channel{}, false
	}
	var found []connectors.Channel
	for _, c := range b.Channels {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(c.Label), "#"), want) {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		return connectors.Channel{}, false
	}
	return found[0], true
}

// channelName words a channel for the model and the person: the label with the
// ID in brackets, or the ID alone.
func channelName(c connectors.Channel) string {
	if c.Label != "" {
		return c.Label + " (" + c.ID + ")"
	}
	return c.ID
}

// approvedChannelNames lists the approved channels of a binding, at most
// connectors.MaxChannels (the limit of a binding).
func approvedChannelNames(b connectors.Binding) []string {
	var out []string
	for _, c := range b.Channels {
		if c.Approved && len(out) < connectors.MaxChannels {
			out = append(out, channelName(c))
		}
	}
	return out
}

// approvedChannel finds the approved channel that a model names by ID or
// label. Any other channel is refused with the list of the approved ones.
func approvedChannel(b connectors.Binding, name string) (connectors.Channel, error) {
	if ch, ok := MatchChannel(b, name); ok && ch.Approved {
		return ch, nil
	}
	list := approvedChannelNames(b)
	if len(list) == 0 {
		return connectors.Channel{}, fmt.Errorf("%q is not a channel the owner approved for this bot; the owner approved none", name)
	}
	return connectors.Channel{}, fmt.Errorf("%q is not a channel the owner approved for this bot; the approved channels are %s", name, strings.Join(list, ", "))
}

// channelsNote is added to the description of a tool that can write in a
// channel. It names the approved channels of the binding, so a model never
// guesses an ID, and says where each target writes. holds says whether the
// sender may write in channels; without it the note says so in one sentence.
func channelsNote(b connectors.Binding, holds bool, rule string) string {
	list := approvedChannelNames(b)
	switch {
	case !holds:
		return " This bot may not write in channels here, so do not offer it."
	case len(list) == 0:
		return " The owner approved no channel for this bot, so do not offer one."
	}
	return " Channels the owner approved for this bot: " + strings.Join(list, ", ") + ". " + rule
}

// listedChannels words the channels of a binding for the model.
func listedChannels(b connectors.Binding) string {
	if len(b.Channels) == 0 {
		return "; the owner listed none"
	}
	var parts []string
	for _, c := range b.Channels {
		if c.Label != "" {
			parts = append(parts, c.Label+" ("+c.ID+")")
		} else {
			parts = append(parts, c.ID)
		}
	}
	return "; the channels are " + strings.Join(parts, ", ")
}

// ServerSwitch is a CapabilitySwitch for a feature that an administrator
// turned off for the whole server. setting is the stable key of that setting
// (the fix of the answer points at it, a client names the page); reason is the
// English line for the log.
func ServerSwitch(setting, reason string, capabilities ...string) CapabilitySwitch {
	return func(_ context.Context, _ connectors.Binding, capability string) Block {
		if slices.Contains(capabilities, capability) {
			return Block{Code: CodeServerSettingOff, Reason: reason, Fix: &Fix{Kind: FixOpenServerSetting, Target: setting}}
		}
		return Block{}
	}
}

// TalkbackSwitch is a CapabilitySwitch that turns off both talk-back
// capabilities, with the reason shown to the owner. The operator switch uses it.
func TalkbackSwitch(reason string) CapabilitySwitch {
	return ServerSwitch(SettingDisableConnectorTalkback, reason, contract.CapTalkbackChannels, contract.CapTalkbackNewChan)
}

// The keys of the server settings that the chain can point at.
const (
	SettingDisableTriggers          = "disable_triggers"
	SettingDisableConnectorTalkback = "disable_connector_talkback"
)

// TriggersSwitch turns off the capabilities of triggers for the whole server.
func TriggersSwitch(reason string) CapabilitySwitch {
	return ServerSwitch(SettingDisableTriggers, reason, contract.CapTriggersPropose, contract.CapTriggersRun)
}
