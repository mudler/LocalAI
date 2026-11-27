package plugins

import (
	"context"
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// ChatRef is what TurnView needs to know about a chat.
type ChatRef struct {
	// PluginsOff are the install IDs switched off for the chat.
	PluginsOff []string
	// HasAgent is set when the chat runs a saved agent; AgentPlugins is then
	// its exact grant list of install IDs. An empty list grants none.
	HasAgent     bool
	AgentPlugins []string
}

// ActivePlugin is a plugin that contributes to a turn.
type ActivePlugin struct {
	InstallID string
	PluginID  string
	// Name is the plugin name, the prefix of its skills and commands.
	Name      string
	Digest    string
	SourceSHA string
	Scope     string
	RunsCode  bool
	Model     Model
}

// TurnViewer gives the plugins of a turn. Studio consumes it (lanes C1 to
// C4); *Service implements it.
type TurnViewer interface {
	TurnView(ctx context.Context, principal string, chat ChatRef) ([]ActivePlugin, error)
}

var _ TurnViewer = (*Service)(nil)

// TurnView returns the plugins that contribute to a turn of principal in a
// chat (spec 10.1): installed for everyone or for the principal, enabled by
// the principal and not waiting for a re-enable, not switched off in the
// chat, and granted by the chat's agent when it has one. A principal without
// the plugins feature gets none. It reads the store
// on every call; nothing is kept between turns.
func (s *Service) TurnView(ctx context.Context, principal string, chat ChatRef) ([]ActivePlugin, error) {
	if principal == "" || (s.allowed != nil && !s.allowed(ctx, principal)) {
		return nil, nil
	}
	installs, err := s.st.Installs(ctx, store.InstallFilter{VisibleTo: principal})
	if err != nil {
		return nil, err
	}
	states, err := s.st.UserStates(ctx, principal)
	if err != nil {
		return nil, err
	}
	byInstall := make(map[string]store.UserState, len(states))
	for _, us := range states {
		byInstall[us.InstallID] = us
	}
	var out []ActivePlugin
	for _, in := range installs {
		us, ok := byInstall[in.ID]
		if !ok || !us.Enabled || us.NeedsReenable {
			continue
		}
		if slices.Contains(chat.PluginsOff, in.ID) {
			continue
		}
		if chat.HasAgent && !slices.Contains(chat.AgentPlugins, in.ID) {
			continue
		}
		pkg, err := s.st.Package(ctx, in.Digest)
		if err != nil {
			return nil, fmt.Errorf("plugins: package of install %s: %w", in.ID, err)
		}
		// An empty model (a package row written without one) is an empty
		// plugin, not a broken turn.
		m, err := modelOf(pkg)
		if err != nil {
			return nil, err
		}
		name := m.Name
		if name == "" {
			name = pkg.Name
		}
		out = append(out, ActivePlugin{
			InstallID: in.ID, PluginID: in.PluginID, Name: name, Digest: in.Digest,
			SourceSHA: in.SourceSHA, Scope: in.Scope, RunsCode: in.RunsCode, Model: m,
		})
	}
	return out, nil
}
