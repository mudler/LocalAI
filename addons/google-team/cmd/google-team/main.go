// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/mudler/LocalAI/addons/google-team/internal/team"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/internal/googleapi"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

type runtime struct {
	*team.Runtime
	api *googleapi.Client
}

func (r runtime) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return connector.Serve(ctx, platform{r.api}, req)
}

type platform struct{ api *googleapi.Client }

func (platform) Kind() string  { return "google-team" }
func (platform) MaxRunes() int { return 4000 }
func (platform) Poll(ctx context.Context, _ string, _ int64, wait int) ([]contract.ConnectorMessage, error) {
	if wait <= 0 {
		return nil, nil
	}
	t := time.NewTimer(time.Duration(wait) * time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, nil
	}
}
func (platform) Send(context.Context, contract.ConnectorRequest) (string, error) {
	return "", errors.New("this connection does not send messages")
}
func (platform) Edit(context.Context, contract.ConnectorRequest) error {
	return errors.New("this connection does not edit messages")
}
func (platform) Typing(context.Context, contract.ConnectorRequest) error { return nil }
func (p platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	var cal struct {
		ID string `json:"id"`
	}
	if err := p.api.GetJSON(ctx, token, "/calendar/v3/calendars/primary", nil, &cal); err != nil {
		if errors.Is(err, googleapi.ErrUnauthorized) {
			return connector.Bot{}, connector.ErrUnauthorized
		}
		return connector.Bot{}, errors.New(googleapi.Describe(err, nil))
	}
	if cal.ID == "" {
		return connector.Bot{}, errors.New("Google returned no calendar identity")
	}
	return connector.Bot{ID: cal.ID, Username: cal.ID}, nil
}

func main() {
	os.Exit(protocol.Run(os.Args, func(st protocol.Startup) contract.Runtime {
		api := googleapi.New("https://www.googleapis.com", "LocalAI-google-team-addon")
		return runtime{Runtime: team.New(api, st.DataDir), api: api}
	}))
}
