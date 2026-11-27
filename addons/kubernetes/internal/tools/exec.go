// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// exec runs one command in a container with no terminal and no input. It is
// off in the default policy, because its output is not filtered: a command
// can print a Secret the container holds. The person sees the exact argv
// before it runs.

const (
	defaultExecTimeout = 30
	maxExecArgs        = 32
	maxExecArg         = 1000
)

type execOp struct {
	Cluster        string   `json:"cluster"`
	Namespace      string   `json:"namespace"`
	Pod            string   `json:"pod"`
	Container      string   `json:"container"`
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

func (c *call) parseExec(raw map[string]json.RawMessage) (writeOp, error) {
	var o execOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	if o.Pod == "" {
		return nil, bad("name the pod")
	}
	if len(o.Command) < 1 || len(o.Command) > maxExecArgs {
		return nil, bad("command has 1 to %d items", maxExecArgs)
	}
	for _, a := range o.Command {
		if len(a) > maxExecArg {
			return nil, bad("an item of command has at most %d bytes", maxExecArg)
		}
	}
	if o.TimeoutSeconds == 0 {
		o.TimeoutSeconds = defaultExecTimeout
	}
	if o.TimeoutSeconds < 1 || o.TimeoutSeconds > MaxExecTimeout {
		return nil, bad("timeoutSeconds is 1 to %d", MaxExecTimeout)
	}
	return &o, c.allow(o.Namespace)
}

func (o *execOp) inspect(c *call) (map[string]any, uint64, error) {
	p, err := c.c.Typed.CoreV1().Pods(o.Namespace).Get(c.ctx, o.Pod, metav1.GetOptions{})
	if err != nil {
		return nil, 0, err
	}
	var names []string
	for _, ct := range p.Spec.Containers {
		names = append(names, ct.Name)
	}
	switch {
	case o.Container == "" && len(names) > 1:
		return nil, 0, bad("the pod has several containers: name one of %s", strings.Join(names, ", "))
	case o.Container == "" && len(names) == 1:
		o.Container = names[0]
	case !slices.Contains(names, o.Container):
		return nil, 0, bad("the pod has no container %s: it has %s", o.Container, strings.Join(names, ", "))
	}
	s := map[string]any{"tool": ToolExec, "kind": "Pod", "namespace": o.Namespace, "name": o.Pod, "container": o.Container,
		"command": o.Command, "timeoutSeconds": o.TimeoutSeconds}
	argv, _ := json.Marshal(o.Command)
	return s, c.revision(ToolExec, o.Namespace, o.Pod, o.Container, string(argv), itoa(int64(o.TimeoutSeconds)), string(p.UID)), nil
}

func (o *execOp) apply(c *call) (map[string]any, error) {
	slot := c.key + "|" + o.Namespace + "/" + o.Pod
	if _, running := c.r.busy.LoadOrStore(slot, struct{}{}); running {
		return nil, bad("another exec runs in this pod: wait for its result")
	}
	defer c.r.busy.Delete(slot)
	ctx, cancel := context.WithTimeout(c.ctx, time.Duration(o.TimeoutSeconds)*time.Second)
	defer cancel()
	res, err := c.c.Exec.Exec(ctx, o.Namespace, o.Pod, o.Container, o.Command)
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if err != nil && !timedOut {
		return nil, err
	}
	res.Stdout, res.StdoutCut = capFlag(res.Stdout, res.StdoutCut)
	res.Stderr, res.StderrCut = capFlag(res.Stderr, res.StderrCut)
	out := map[string]any{"exitCode": res.ExitCode, "stdout": res.Stdout, "stderr": res.Stderr,
		"stdoutCut": res.StdoutCut, "stderrCut": res.StderrCut, "timedOut": timedOut}
	if timedOut {
		out["note"] = "the command was stopped after the timeout; it may still run in the container"
	}
	return out, nil
}

func capFlag(s string, cut bool) (string, bool) {
	o, c := kubeapi.Cap(s, kubeapi.MaxText)
	return o, cut || c
}
