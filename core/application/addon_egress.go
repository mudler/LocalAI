package application

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/egress"
)

// RegisterAddonEgress registers the network operations of the Sandbox proxy
// over the chats service. It registers nothing when this process has no host
// operation channel. The service is looked up at each call: it is built after
// the broker.
func (a *Application) RegisterAddonEgress() error {
	if a.addonHostOps == nil {
		return nil
	}
	return hostops.RegisterEgress(a.addonHostOps.Broker, addonEgress{a: a})
}

// addonEgress adapts the chats service to the host operations.
type addonEgress struct{ a *Application }

func (e addonEgress) service() (*studio.Service, error) {
	svc := e.a.StudioService()
	if svc == nil {
		return nil, fmt.Errorf("%w: the chats are off", hostops.ErrNotFound)
	}
	return svc, nil
}

func (e addonEgress) Check(ctx context.Context, c hostops.EgressCall, in hostops.EgressCheck) (hostops.EgressVerdict, error) {
	svc, err := e.service()
	if err != nil {
		return hostops.EgressVerdict{}, err
	}
	p := addonWorkspacePrincipal(e.a.authDB != nil)(c.Actor)
	if p == "" {
		return hostops.EgressVerdict{Action: egress.Block, Reason: "chat"}, nil
	}
	v, err := svc.EgressCheck(ctx, p, c.ResourceID, c.OperationID, egress.Request{Host: in.Host, Port: in.Port, Kind: in.Kind, TLS: in.TLS, Private: in.Private})
	if err != nil {
		return hostops.EgressVerdict{}, err
	}
	return hostops.EgressVerdict{Action: v.Action, Reason: v.Reason, AllowPrivate: v.AllowPrivate}, nil
}

func (e addonEgress) Audit(ctx context.Context, c hostops.EgressCall, evs []hostops.EgressEvent) error {
	svc, err := e.service()
	if err != nil {
		return err
	}
	p := addonWorkspacePrincipal(e.a.authDB != nil)(c.Actor)
	if p == "" {
		return nil
	}
	out := make([]studio.EgressEvent, 0, len(evs))
	for _, ev := range evs {
		out = append(out, studio.EgressEvent{Host: ev.Host, Port: ev.Port, Kind: ev.Kind, Verdict: ev.Verdict, Reason: ev.Reason, Addr: ev.Addr,
			BytesUp: ev.BytesUp, BytesDown: ev.BytesDown, DurationMS: ev.DurationMS, At: ev.At})
	}
	return svc.EgressAudit(ctx, p, c.ResourceID, out)
}
