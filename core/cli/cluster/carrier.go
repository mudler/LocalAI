// SPDX-License-Identifier: MIT

package cluster

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
)

const carrierPath = "/api/cluster/carrier"

// CarrierCommand groups the commands about the carrier.
type CarrierCommand struct {
	Status StatusCommand `cmd:"" default:"withargs" help:"Show the active carrier, the state of a change, the replicas and the workers"`
	Switch SwitchCommand `cmd:"" help:"Change the carrier of the cluster, or check what would block the change (--dry-run)"`
	Abort  AbortCommand  `cmd:"" help:"Abort a change that is being prepared"`
}

// StatusCommand shows the state of the carrier.
type StatusCommand struct {
	Remote `embed:""`
}

// SwitchCommand changes the carrier.
type SwitchCommand struct {
	To     string `required:"" enum:"nats,tunnel" help:"Carrier to change to [${enum}]."`
	DryRun bool   `name:"dry-run" help:"Run the preflight and change nothing. It lists what would block the change."`
	Force  bool   `help:"Go ahead although some workers cannot follow or some replicas are not ready. Those workers become unroutable until they are fixed."`
	Yes    bool   `help:"Confirm that the replicas that are listed are all the frontends of the cluster, and that all run this release. Without it the command asks, and a command with no terminal refuses."`
	Wait   bool   `help:"Wait until the change is committed, and print the progress."`
	// WaitTimeout bounds --wait.
	WaitTimeout time.Duration `name:"wait-timeout" default:"10m" help:"How long --wait waits."`
	Remote      `embed:""`
}

// AbortCommand aborts a change in the prepare state.
type AbortCommand struct {
	Remote `embed:""`
}

// pollInterval is how often --wait reads the state.
var pollInterval = 2 * time.Second

func (c *StatusCommand) Run() error {
	ctx, stop := signalContext()
	defer stop()
	return c.run(ctx, os.Stdout)
}

func (c *StatusCommand) run(ctx context.Context, out io.Writer) error {
	var view cluster.Report
	_, raw, err := c.do(ctx, c.client(), http.MethodGet, carrierPath, nil, &view)
	if err != nil {
		return err
	}
	if c.JSON {
		return printJSON(out, raw)
	}
	printStatus(out, view)
	return nil
}

func (c *AbortCommand) Run() error {
	ctx, stop := signalContext()
	defer stop()
	return c.run(ctx, os.Stdout)
}

func (c *AbortCommand) run(ctx context.Context, out io.Writer) error {
	var row cluster.CarrierRow
	_, raw, err := c.do(ctx, c.client(), http.MethodPost, carrierPath, map[string]any{"abort": true}, &row)
	if err != nil {
		return err
	}
	if c.JSON {
		return printJSON(out, raw)
	}
	_, _ = fmt.Fprintf(out, "Aborted. The cluster stays on %s (epoch %d).\n", row.Active, row.Epoch)
	return nil
}

func (c *SwitchCommand) Run() error {
	ctx, stop := signalContext()
	defer stop()
	return c.run(ctx, os.Stdout, os.Stdin, stdinIsTerminal())
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (c *SwitchCommand) run(ctx context.Context, out io.Writer, in io.Reader, interactive bool) error {
	client := c.client()
	target := c.To

	// The dry run is the preflight. A change runs it again on the server, so
	// this one is for the person who reads the replica list.
	var report cluster.Report
	_, raw, err := c.do(ctx, client, http.MethodPost, carrierPath, map[string]any{"target": target, "dry_run": true}, &report)
	if err != nil {
		return err
	}
	if c.DryRun {
		if c.JSON {
			return printJSON(out, raw)
		}
		printPreflight(out, report, target)
		if !report.OK {
			return errors.New("the change would be blocked; fix the blockers, or use --force for those that can be forced")
		}
		return nil
	}

	if !c.JSON {
		printPreflight(out, report, target)
	}
	if !report.OK && !c.Force {
		return errors.New("the change is blocked; fix the blockers, or use --force for those that can be forced")
	}
	if err := c.confirm(out, in, interactive, report); err != nil {
		return err
	}

	var started struct {
		State  string `json:"state"`
		Active string `json:"active"`
		Target string `json:"target"`
		Epoch  int64  `json:"epoch"`
	}
	_, raw, err = c.do(ctx, client, http.MethodPost, carrierPath, map[string]any{"target": target, "force": c.Force}, &started)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusUnprocessableEntity {
			var body struct {
				Blockers []cluster.Blocker `json:"blockers"`
			}
			_ = json.Unmarshal(ae.Body, &body)
			var b strings.Builder
			for _, bl := range body.Blockers {
				fmt.Fprintf(&b, "\n  %s %s: %s", bl.Kind, bl.ID, bl.Reason)
			}
			return fmt.Errorf("the change is blocked:%s", b.String())
		}
		return err
	}
	if c.JSON && !c.Wait {
		return printJSON(out, raw)
	}
	_, _ = fmt.Fprintf(out, "Change to %s started (epoch %d, state %s).\n", started.Target, started.Epoch, started.State)
	if !c.Wait {
		_, _ = fmt.Fprintln(out, "The replicas carry it out. Run `local-ai cluster carrier status` to follow it.")
		return nil
	}
	return c.wait(ctx, client, out, started.Epoch)
}

// confirm asks the person to confirm the replica list, which the server cannot
// check: a frontend of an older release writes no row and is not in the list.
func (c *SwitchCommand) confirm(out io.Writer, in io.Reader, interactive bool, report cluster.Report) error {
	if c.Yes {
		return nil
	}
	if !interactive {
		return errors.New("confirm that the replicas listed above are all the frontends of the cluster and that all run this release: pass --yes")
	}
	q := fmt.Sprintf("Are these %d replicas all the frontends of the cluster, and do all of them run this release?", len(report.Replicas))
	if c.Force {
		q += " --force makes the workers that cannot follow unroutable."
	}
	_, _ = fmt.Fprintf(out, "%s [y/N] ", q)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errors.New("not confirmed; nothing was changed")
}

// wait reads the state until the change is committed or gone.
func (c *SwitchCommand) wait(ctx context.Context, client *http.Client, out io.Writer, epoch int64) error {
	ctx, cancel := context.WithTimeout(ctx, c.WaitTimeout)
	defer cancel()
	last := ""
	for {
		var st cluster.Report
		if _, _, err := c.do(ctx, client, http.MethodGet, carrierPath, nil, &st); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("gave up waiting; the change goes on, check it with `local-ai cluster carrier status`: %w", ctx.Err())
			}
			return err
		}
		line := fmt.Sprintf("state %s, active %s, epoch %d", st.State, st.Active, st.Epoch)
		if line != last {
			_, _ = fmt.Fprintln(out, line)
			last = line
		}
		switch {
		case st.State == cluster.StateStable && string(st.Active) == c.To:
			_, _ = fmt.Fprintf(out, "The cluster is on %s.\n", c.To)
			if st.DrainRemaining > 0 {
				_, _ = fmt.Fprintf(out, "The previous carrier drains for %s more.\n", st.DrainRemaining.Round(time.Second))
			}
			return nil
		case st.State == cluster.StateStable && st.Epoch > epoch:
			note := st.Row.Note
			if note != "" {
				note = ": " + note
			}
			return fmt.Errorf("the change was aborted; the cluster stays on %s%s", st.Active, note)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gave up waiting; the change goes on, check it with `local-ai cluster carrier status`: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

func printStatus(out io.Writer, r cluster.Report) {
	_, _ = fmt.Fprintf(out, "Active carrier: %s (epoch %d)\n", r.Active, r.Epoch)
	_, _ = fmt.Fprintf(out, "State:          %s", r.State)
	if r.Target != "" {
		_, _ = fmt.Fprintf(out, " (changing to %s)", r.Target)
	}
	_, _ = fmt.Fprintln(out)
	if r.Row.Draining != "" {
		_, _ = fmt.Fprintf(out, "Draining:       %s", r.Row.Draining)
		if r.DrainRemaining > 0 {
			_, _ = fmt.Fprintf(out, ", %s left", r.DrainRemaining.Round(time.Second))
		}
		_, _ = fmt.Fprintln(out)
	}
	printReplicas(out, r)
	printWorkers(out, r)
}

func printReplicas(out io.Writer, r cluster.Report) {
	_, _ = fmt.Fprintf(out, "\nReplicas (%d):\n", len(r.Replicas))
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  ID\tVERSION\tREADY EPOCH\tNOTE")
	for _, rep := range r.Replicas {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\n", rep.ID, rep.Version, rep.ReadyEpoch, rep.ReadyReason)
	}
	_ = tw.Flush()
}

func printWorkers(out io.Writer, r cluster.Report) {
	_, _ = fmt.Fprintf(out, "\nWorkers (%d):\n", len(r.Workers))
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  NAME\tID\tATTACHED\tCAN FOLLOW\tNOTE")
	for _, w := range r.Workers {
		var att []string
		for _, a := range w.Attached {
			att = append(att, string(a))
		}
		note := w.Reason
		if w.FollowError != "" {
			if note != "" {
				note += "; "
			}
			note += "follow_error: " + w.FollowError
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%t\t%s\n", w.Name, w.ID, strings.Join(att, ","), w.CanFollow, note)
	}
	_ = tw.Flush()
}

func printPreflight(out io.Writer, r cluster.Report, target string) {
	_, _ = fmt.Fprintf(out, "Preflight for a change from %s to %s:\n", r.Active, target)
	if r.OK {
		_, _ = fmt.Fprintln(out, "  Nothing blocks the change.")
	}
	for _, b := range r.Blockers {
		force := ""
		if b.Forceable {
			force = " (can be forced)"
		}
		_, _ = fmt.Fprintf(out, "  BLOCKER %s %s: %s%s\n", b.Kind, b.ID, b.Reason, force)
	}
	for _, w := range r.Warnings {
		_, _ = fmt.Fprintf(out, "  warning: %s\n", w)
	}
	_, _ = fmt.Fprintf(out, "  In flight: %d loads, %d jobs, %d pending claims, %d claimed\n",
		r.InFlight.Loads, r.InFlight.Jobs, r.InFlight.PendingClaims, r.InFlight.ClaimedClaims)
	printReplicas(out, r)
	_, _ = fmt.Fprintln(out, "  Frontends of an older release write no row and are not listed. Check that this list is complete.")
	printWorkers(out, r)
}
