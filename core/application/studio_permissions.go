package application

import (
	"context"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
)

// migrateStudioPermissions runs the one-time rule that keeps the access of the
// existing users when saved agents, triggers and connectors became features of
// their own. See auth.MigrateAutomationFeatures for the rule. It needs the auth
// database and the stores, and only reads the stores. A failure is logged and
// retried at the next start: the marker is written only when it succeeds.
func (a *Application) migrateStudioPermissions() {
	if a.authDB == nil || a.studioAgents == nil {
		return
	}
	ctx := context.Background()
	owned := map[string]map[string]bool{}
	mark := func(owner, feature string) {
		if owned[owner] == nil {
			owned[owner] = map[string]bool{}
		}
		owned[owner][feature] = true
	}
	list, err := a.studioAgents.ListAll(ctx)
	if err != nil {
		xlog.Warn("Chats: could not read the agents to migrate permissions", "error", err)
		return
	}
	for _, ag := range list {
		mark(ag.Principal, auth.FeatureAgents)
	}
	if a.studioTriggerStore != nil {
		ts, err := a.studioTriggerStore.AllTriggers(ctx)
		if err != nil {
			xlog.Warn("Chats: could not read the triggers to migrate permissions", "error", err)
			return
		}
		for _, t := range ts {
			mark(t.Owner, auth.FeatureTriggers)
		}
	}
	if a.studioConnStore != nil {
		bs, err := a.studioConnStore.AllBindings(ctx)
		if err != nil {
			xlog.Warn("Chats: could not read the connectors to migrate permissions", "error", err)
			return
		}
		for _, b := range bs {
			mark(b.Owner, auth.FeatureConnectors)
		}
	}
	if err := auth.MigrateAutomationFeatures(a.authDB, owned); err != nil {
		xlog.Warn("Chats: could not migrate the permissions of the agents, triggers and connectors", "error", err)
	}
}
