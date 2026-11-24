package studio

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
)

// AgentsOfProject lists the saved agents of the user that belong to a
// project, in the order of Agents (ProjectAgents sorts them by name). It does not check that the project still exists: a
// project that is gone or archived still has its agents to show. Another
// owner's project id gives an empty list.
func (s *Service) AgentsOfProject(ctx context.Context, user, projectID string) ([]agents.Agent, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: a project ID is required", agents.ErrInvalid)
	}
	all, err := s.Agents(ctx, user)
	if err != nil {
		return nil, err
	}
	out := make([]agents.Agent, 0, len(all))
	for _, a := range all {
		if a.ProjectID == projectID {
			out = append(out, a)
		}
	}
	return out, nil
}

// ProjectAgent is a saved agent of a project with whether it can start now.
type ProjectAgent struct {
	Agent agents.Agent
	// Unavailable is true when the project of the agent cannot be used: it is
	// archived, deleted or broken, or the folders of the agent no longer fit it.
	Unavailable bool
}

// ProjectAgents lists the agents of a project for a page: sorted by name, each
// with whether it is available now. The flag is derived on every call, so it
// follows an archive or a restore with no action of the agent.
func (s *Service) ProjectAgents(ctx context.Context, user, projectID string) ([]ProjectAgent, error) {
	list, err := s.AgentsOfProject(ctx, user, projectID)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(list, func(a, b agents.Agent) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	// The project is resolved once for the request; each agent is then only
	// compared with that fence, with the rule that a run uses. A project that
	// cannot be resolved (or a store error) makes every agent unavailable.
	fence, ferr := s.fenceOfProject(ctx, principal(user), projectID)
	out := make([]ProjectAgent, 0, len(list))
	for _, a := range list {
		out = append(out, ProjectAgent{Agent: a, Unavailable: ferr != nil || !foldersFitFence(a, fence)})
	}
	return out, nil
}

// AgentCountsByProject counts the saved agents of the user by project id, with
// one read of the agents. Agents with no project are not counted.
func (s *Service) AgentCountsByProject(ctx context.Context, user string) (map[string]int, error) {
	all, err := s.Agents(ctx, user)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, a := range all {
		if a.ProjectID != "" {
			out[a.ProjectID]++
		}
	}
	return out, nil
}
