// SPDX-License-Identifier: MIT

package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

const settingsPath = "/api/cluster/settings"

// SettingsCommand groups the commands about the settings of the cluster.
type SettingsCommand struct {
	Get GetSettingsCommand `cmd:"" default:"withargs" help:"Show the settings"`
	Set SetSettingsCommand `cmd:"" help:"Store settings. Storing a NATS address does not change the carrier."`
}

// GetSettingsCommand shows the settings.
type GetSettingsCommand struct {
	Remote `embed:""`
}

// SetSettingsCommand stores settings. A flag that is not given leaves the
// setting as it is, and a flag given an empty value clears it.
type SetSettingsCommand struct {
	NATSURL          *string `name:"nats-url" help:"NATS address that the frontends use. The serving replica checks that it reaches the server. Empty clears it."`
	NATSWorkerURL    *string `name:"nats-worker-url" help:"NATS address that workers are told to use, when it differs from --nats-url. Empty clears it."`
	PrepareTimeout   *string `name:"prepare-timeout" help:"How long a change waits for every replica to be ready, as a duration such as 60s."`
	TransitionWindow *string `name:"transition-window" help:"How long a replica may take to confirm a commit, as a duration."`
	MaxDrain         *string `name:"max-drain" help:"How long the previous carrier stays attached after a commit, as a duration such as 15m."`
	Remote           `embed:""`
}

type settingsView struct {
	NATSURL          string `json:"nats_url"`
	NATSWorkerURL    string `json:"nats_worker_url"`
	PrepareTimeout   string `json:"prepare_timeout"`
	TransitionWindow string `json:"transition_window"`
	MaxDrain         string `json:"max_drain"`
}

func (c *GetSettingsCommand) Run() error {
	ctx, stop := signalContext()
	defer stop()
	return c.run(ctx, os.Stdout)
}

func (c *GetSettingsCommand) run(ctx context.Context, out io.Writer) error {
	var view settingsView
	_, raw, err := c.do(ctx, c.client(), http.MethodGet, settingsPath, nil, &view)
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
		"nats_url":          c.NATSURL,
		"nats_worker_url":   c.NATSWorkerURL,
		"prepare_timeout":   c.PrepareTimeout,
		"transition_window": c.TransitionWindow,
		"max_drain":         c.MaxDrain,
	} {
		if v != nil {
			body[name] = *v
		}
	}
	if len(body) == 0 {
		return errors.New("give at least one setting to store, for example --nats-url nats://host:4222")
	}
	var answer struct {
		Saved bool `json:"saved"`
		NATS  *struct {
			Reachable bool `json:"reachable"`
		} `json:"nats"`
		Settings settingsView `json:"settings"`
	}
	_, raw, err := c.do(ctx, c.client(), http.MethodPut, settingsPath, body, &answer)
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
		_, _ = fmt.Fprintln(out, "The carrier did not change. To use NATS, run `local-ai cluster carrier switch --to nats --dry-run`, then the same command without --dry-run.")
	}
	printSettings(out, answer.Settings)
	return nil
}

func printSettings(out io.Writer, v settingsView) {
	show := func(s string) string {
		if s == "" {
			return "(not set)"
		}
		return s
	}
	_, _ = fmt.Fprintf(out, "nats_url:          %s\n", show(v.NATSURL))
	_, _ = fmt.Fprintf(out, "nats_worker_url:   %s\n", show(v.NATSWorkerURL))
	_, _ = fmt.Fprintf(out, "prepare_timeout:   %s\n", show(v.PrepareTimeout))
	_, _ = fmt.Fprintf(out, "transition_window: %s\n", show(v.TransitionWindow))
	_, _ = fmt.Fprintf(out, "max_drain:         %s\n", show(v.MaxDrain))
}
