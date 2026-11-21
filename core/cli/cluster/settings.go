// SPDX-License-Identifier: MIT

package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
)

const settingsPath = "/api/cluster/settings"

// SettingsCommand groups the commands about the settings of the cluster.
type SettingsCommand struct {
	Get GetSettingsCommand `cmd:"" default:"withargs" help:"Show the settings"`
	Set SetSettingsCommand `cmd:"" help:"Store settings. The frontends use a new NATS address after a restart."`
}

// GetSettingsCommand shows the settings.
type GetSettingsCommand struct {
	Remote `embed:""`
}

// SetSettingsCommand stores settings. A flag that is not given leaves the
// setting as it is, and a flag given an empty value clears it.
type SetSettingsCommand struct {
	NATSURL       *string           `name:"nats-url" help:"NATS address that the frontends use. The serving replica checks that it reaches the server. Empty clears it."`
	NATSWorkerURL *string           `name:"nats-worker-url" help:"NATS address that workers are told to use, when it differs from --nats-url. Empty clears it."`
	CarrierWeight map[string]string `name:"carrier-weight" help:"Weight of a carrier, as nats=100, websocket=50 or p2p=25 (0 to 1000, higher wins; 0 means only when nothing else fits). It only breaks a tie between equally fit nodes. Repeat the flag for more than one. nats=default puts the default back."`
	Remote        `embed:""`
}

type settingsView struct {
	NATSURL        string `json:"nats_url"`
	NATSWorkerURL  string `json:"nats_worker_url"`
	CarrierWeights struct {
		NATS      int `json:"nats"`
		WebSocket int `json:"websocket"`
		P2P       int `json:"p2p"`
	} `json:"carrier_weights"`
	// Bus is in the answer of a read only: the broadcast bus of the frontend
	// that answered.
	Bus *struct {
		Bus string `json:"bus"`
	} `json:"bus,omitempty"`
}

func (c *GetSettingsCommand) Run() error {
	ctx, stop := signalContext()
	defer stop()
	return c.run(ctx, os.Stdout)
}

func (c *GetSettingsCommand) run(ctx context.Context, out io.Writer) error {
	var view settingsView
	raw, err := c.do(ctx, c.client(), http.MethodGet, settingsPath, nil, &view)
	if err != nil {
		return err
	}
	if c.JSON {
		return printJSON(out, raw)
	}
	printSettings(out, view)
	return nil
}

func (c *SetSettingsCommand) Run() error {
	ctx, stop := signalContext()
	defer stop()
	return c.run(ctx, os.Stdout)
}

func (c *SetSettingsCommand) run(ctx context.Context, out io.Writer) error {
	body := map[string]string{}
	for name, v := range map[string]*string{
		"nats_url":        c.NATSURL,
		"nats_worker_url": c.NATSWorkerURL,
	} {
		if v != nil {
			body[name] = *v
		}
	}
	req := map[string]any{}
	for k, v := range body {
		req[k] = v
	}
	if len(c.CarrierWeight) > 0 {
		weights, err := parseCarrierWeights(c.CarrierWeight)
		if err != nil {
			return err
		}
		req["carrier_weights"] = weights
	}
	if len(req) == 0 {
		return errors.New("give at least one setting to store, for example --nats-url nats://host:4222 or --carrier-weight nats=100")
	}
	var answer struct {
		Saved bool `json:"saved"`
		NATS  *struct {
			Reachable bool `json:"reachable"`
		} `json:"nats"`
		Settings settingsView `json:"settings"`
	}
	raw, err := c.do(ctx, c.client(), http.MethodPut, settingsPath, req, &answer)
	if err != nil {
		return err
	}
	if c.JSON {
		return printJSON(out, raw)
	}
	_, _ = fmt.Fprintln(out, "Saved.")
	if answer.NATS != nil && answer.NATS.Reachable {
		_, _ = fmt.Fprintln(out, "The serving replica reaches the NATS server.")
	}
	if c.NATSURL != nil || c.NATSWorkerURL != nil {
		_, _ = fmt.Fprintln(out, "The frontends use the new NATS address after a restart.")
	}
	printSettings(out, answer.Settings)
	return nil
}

// parseCarrierWeights turns name=value pairs into the body of the request: a
// whole number, or default (null) to put the default back. The server checks the
// names and the range.
func parseCarrierWeights(in map[string]string) (map[string]*int, error) {
	out := make(map[string]*int, len(in))
	for name, raw := range in {
		if raw == "default" {
			out[name] = nil
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("--carrier-weight %s=%s: the weight must be a whole number from 0 to 1000, or default", name, raw)
		}
		out[name] = &n
	}
	return out, nil
}

func printSettings(out io.Writer, v settingsView) {
	show := func(s string) string {
		if s == "" {
			return "(not set)"
		}
		return s
	}
	_, _ = fmt.Fprintf(out, "nats_url:        %s\n", show(v.NATSURL))
	_, _ = fmt.Fprintf(out, "nats_worker_url: %s\n", show(v.NATSWorkerURL))
	_, _ = fmt.Fprintf(out, "carrier_weights: nats=%d websocket=%d p2p=%d\n", v.CarrierWeights.NATS, v.CarrierWeights.WebSocket, v.CarrierWeights.P2P)
	if v.Bus != nil {
		_, _ = fmt.Fprintf(out, "bus:             %s (the broadcast bus of the frontend that answered)\n", v.Bus.Bus)
	}
}
