// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"strings"
)

// ErrClientModelUnknown is the refusal of a model that has no configuration.
var ErrClientModelUnknown = errors.New("no such model")

// ErrClientModelTools is the refusal of a model that has tools attached. The
// text names what is attached, and a request to such a model would run its
// tools under the account of the owner.
var ErrClientModelTools = errors.New("the model has tools attached")

// maxClientModelDepth bounds the walk over aliases, failover targets, router
// candidates and pipeline stages.
const maxClientModelDepth = 8

// AttachedTools lists what the configuration attaches to the model on the
// server side: MCP servers (or automatic MCP), and the settings of the agent
// tool loop. A request to such a model can run tools, so it acts for the owner
// of the instance, not only for the caller. It does not look at other models.
func (c *ModelConfig) AttachedTools() []string {
	var out []string
	if c.MCP.HasMCPServers() {
		out = append(out, "MCP servers")
	} else if c.MCP.Auto {
		out = append(out, "automatic MCP")
	}
	if c.Agent.MaxAttempts != 0 || c.Agent.MaxIterations != 0 || c.Agent.ForceReasoningTool || c.Agent.MaxGenerationTokens != 0 {
		out = append(out, "an agent loop")
	}
	if c.Profile.MCP.HasMCPServers() || c.Profile.MCP.Auto {
		out = append(out, "MCP servers in the profile")
	}
	return out
}

// ClientModelProblem says whether a client key may use the model: nil when it
// may. The model must have a configuration, and neither it nor any model it
// hands the request to (the target of an alias, a failover target, a router
// candidate or fallback, a stage of a pipeline) may have tools attached. It is asked when a model is put on the
// list of a client key and again on each request, since a configuration can
// gain tools after the key was made. A name that has no configuration while
// the walk goes down (a plain model file) has no tools to attach and passes.
func (bcl *ModelConfigLoader) ClientModelProblem(name string) error {
	if bcl == nil {
		return ErrClientModelUnknown
	}
	if _, ok := bcl.GetModelConfig(name); !ok {
		return ErrClientModelUnknown
	}
	return bcl.clientModelWalk(name, name, map[string]bool{}, 0)
}

func (bcl *ModelConfigLoader) clientModelWalk(root, name string, seen map[string]bool, depth int) error {
	if seen[name] {
		return nil
	}
	seen[name] = true
	if depth > maxClientModelDepth {
		return fmt.Errorf("%w: it hands the request through too many models", ErrClientModelTools)
	}
	cfg, ok := bcl.GetModelConfig(name)
	if !ok {
		return nil
	}
	if tools := cfg.AttachedTools(); len(tools) > 0 {
		if name == root {
			return fmt.Errorf("%w: %s", ErrClientModelTools, strings.Join(tools, ", "))
		}
		return fmt.Errorf("%w: %q has %s", ErrClientModelTools, name, strings.Join(tools, ", "))
	}
	next := []string{cfg.Alias, cfg.VisionAlias, cfg.Pipeline.LLM, cfg.Pipeline.TTS, cfg.Pipeline.Transcription,
		cfg.Pipeline.VAD, cfg.Pipeline.SoundDetection}
	if cfg.Failover != nil {
		for _, t := range cfg.Failover.Targets {
			next = append(next, t.Model)
		}
	}
	// A router hands the request to one of its candidates, or to its fallback,
	// and that model then runs with its own tools.
	next = append(next, cfg.Router.Fallback)
	for _, cand := range cfg.Router.Candidates {
		next = append(next, cand.Model)
	}
	for _, n := range next {
		if n == "" {
			continue
		}
		if err := bcl.clientModelWalk(root, n, seen, depth+1); err != nil {
			return err
		}
	}
	return nil
}
