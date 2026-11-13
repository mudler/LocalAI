// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The job operations of a paired client. An add-on that serves a client starts
// the client's jobs here, reads their state and cancels them. The client is the
// actor of the live call: the host recorded it when it forwarded the request
// (contract.ClientCredentialPrefix), so no argument names a client or a user,
// and a call that no client made is refused. A client reaches only the jobs it
// started.
//
// A job runs a saved task of the owner. The add-on picks the task by name and
// fills its parameters; it cannot send a prompt of its own.

const (
	featureJobs = "mcp_jobs"

	// MaxClientJobParams, MaxClientJobParamName and MaxClientJobParamValue
	// bound the parameters of one job.
	MaxClientJobParams     = 16
	MaxClientJobParamName  = 64
	MaxClientJobParamValue = 4096
	// MaxClientJobResult bounds the result text an operation returns.
	MaxClientJobResult = 16 << 10
)

// ClientCall says for whom a job operation runs: the owner who paired the
// client and the client key. Both come from the host's record of the call.
type ClientCall struct {
	OwnerID  string
	ClientID string
}

// ClientJob is the state of a job as an operation returns it.
type ClientJob struct {
	ID     string `json:"id"`
	Task   string `json:"task,omitempty"`
	Status string `json:"status"`
	// Result is the text the job produced, cut to MaxClientJobResult.
	Result      string `json:"result,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

// ClientJobHost is the host side: the job service. Every method checks that the
// client is still paired (not revoked, not expired) before it does anything.
type ClientJobHost interface {
	Start(ctx context.Context, c ClientCall, task string, params map[string]string) (ClientJob, error)
	Get(ctx context.Context, c ClientCall, id string) (ClientJob, error)
	Cancel(ctx context.Context, c ClientCall, id string) (ClientJob, error)
}

// ClientOpPrefix starts the name of every operation that is about paired
// clients. It is the family an owner grants (host policy prefix "client."); it
// does not say which operations a client call may make: ClientCallOps does.
const ClientOpPrefix = "client."

// ClientCallOps are the operations a call that a paired client made may use,
// by exact name. The broker refuses every other operation to such a call, also
// one that starts with ClientOpPrefix.
var ClientCallOps = map[string]bool{
	"client.jobs.start":  true,
	"client.jobs.get":    true,
	"client.jobs.cancel": true,
}

func clientFrom(c Call) (ClientCall, error) {
	id, ok := strings.CutPrefix(c.Actor.CredentialID, contract.ClientCredentialPrefix)
	if !ok || id == "" || c.Actor.UserID == "" {
		return ClientCall{}, fmt.Errorf("%w: this operation is for a paired client", ErrForbidden)
	}
	return ClientCall{OwnerID: c.Actor.UserID, ClientID: id}, nil
}

func clientArgs(raw json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: arguments", ErrBadRequest)
	}
	return nil
}

// RegisterClientJobs registers client.jobs.start, client.jobs.get and
// client.jobs.cancel. They run under the read scope of a client call, are
// refused to any other actor, and need the owner to hold the jobs feature.
func RegisterClientJobs(b *Broker, h ClientJobHost) error {
	if h == nil {
		return nil
	}
	if err := b.Register(Spec{Op: "client.jobs.start", Scope: "read", Feature: featureJobs, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		cl, err := clientFrom(c)
		if err != nil {
			return nil, err
		}
		var a struct {
			Task   string            `json:"task"`
			Params map[string]string `json:"params"`
		}
		if err := clientArgs(c.Arguments, &a); err != nil {
			return nil, err
		}
		if a.Task == "" || len(a.Task) > 128 || len(a.Params) > MaxClientJobParams {
			return nil, fmt.Errorf("%w: task and params", ErrBadRequest)
		}
		for k, v := range a.Params {
			if k == "" || len(k) > MaxClientJobParamName || len(v) > MaxClientJobParamValue {
				return nil, fmt.Errorf("%w: a parameter", ErrBadRequest)
			}
		}
		j, err := h.Start(ctx, cl, a.Task, a.Params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(j)
	}}); err != nil {
		return err
	}
	byID := func(op string, run func(context.Context, ClientCall, string) (ClientJob, error)) error {
		return b.Register(Spec{Op: op, Scope: "read", Feature: featureJobs, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
			cl, err := clientFrom(c)
			if err != nil {
				return nil, err
			}
			var a struct {
				ID string `json:"id"`
			}
			if err := clientArgs(c.Arguments, &a); err != nil {
				return nil, err
			}
			if a.ID == "" || len(a.ID) > 64 {
				return nil, fmt.Errorf("%w: id", ErrBadRequest)
			}
			j, err := run(ctx, cl, a.ID)
			if err != nil {
				return nil, err
			}
			return json.Marshal(j)
		}})
	}
	if err := byID("client.jobs.get", h.Get); err != nil {
		return err
	}
	return byID("client.jobs.cancel", h.Cancel)
}
