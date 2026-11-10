package studio

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

// NewJobCarrier returns a bgjob.Carrier on the cluster's message bus: one
// request/reply subject per node, chats.jobs.<instance>, a copy of the carrier
// of the event replay. It lives here, not in bgjob, so that the sandbox
// packages (which the Sandbox add-on shares) do not pull the message bus in.
func NewJobCarrier(mc ReplyRequester, log *slog.Logger) bgjob.Carrier {
	if log == nil {
		log = slog.Default()
	}
	return &busJobs{mc: mc, log: log}
}

type busJobs struct {
	mc  ReplyRequester
	log *slog.Logger
}

func (b *busJobs) Ask(instance string, req bgjob.Request) (bgjob.Reply, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return bgjob.Reply{}, err
	}
	data, err := b.mc.Request(bgjob.Subject(instance), raw, bgjob.AskTimeout)
	if err != nil {
		if messaging.IsNoResponders(err) {
			return bgjob.Reply{}, fmt.Errorf("%w: %v", bgjob.ErrNoRoute, err)
		}
		return bgjob.Reply{}, err
	}
	var rep bgjob.Reply
	if err := json.Unmarshal(data, &rep); err != nil {
		return bgjob.Reply{}, fmt.Errorf("studio: a job reply: %w", err)
	}
	return rep, nil
}

func (b *busJobs) Serve(instance string, h func(bgjob.Request) bgjob.Reply) (func(), error) {
	sub, err := b.mc.SubscribeReply(bgjob.Subject(instance), func(data []byte, reply func([]byte)) {
		var req bgjob.Request
		rep := bgjob.Reply{Code: bgjob.CodeBad, Message: "unreadable request"}
		if err := json.Unmarshal(data, &req); err == nil {
			rep = h(req)
		}
		raw, err := json.Marshal(rep)
		if err != nil {
			b.log.Warn("studio: could not encode a job reply", "error", err)
			raw, _ = json.Marshal(bgjob.Reply{Code: bgjob.CodeBad, Message: "could not encode the reply"})
		}
		reply(raw)
	})
	if err != nil {
		return nil, err
	}
	return func() { _ = sub.Unsubscribe() }, nil
}
