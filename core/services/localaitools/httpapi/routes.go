package httpapi

import (
	"fmt"
	"net/url"
	"strconv"
)

// Route paths for the LocalAI admin REST surface that this client targets.
// Static paths are constants; dynamic paths are builders that handle
// url.PathEscape on segment values. Keep these aligned with the server-side
// registrations in core/http/routes/localai.go — the Tool↔REST drift detector
// in coverage_test.go documents the mapping.
const (
	routeModelsApply            = "/models/apply"
	routeModelsAvail            = "/models/available"
	routeModelsSearch           = "/api/models/search"
	routeModelsRemoteFiles      = "/api/models/remote/files"
	routeModelsRemoteEstimate   = "/api/models/remote/estimate"
	routeModelsGall             = "/models/galleries"
	routeModelsImport           = "/models/import-uri"
	routeModelImport            = "/models/import"
	routeAliases                = "/api/aliases"
	routeModelsReload           = "/models/reload"
	routeBackendLoad            = "/backend/load"
	routeBackends               = "/backends"
	routeBackendsKnown          = "/backends/known"
	routeBackendsApply          = "/backends/apply"
	routeBackendsLibraryInstall = "/backends/library/install"
	routeBackendsLibraryUpdates = "/backends/library/updates"
	routeBackendsUpdates        = "/backends/updates"
	routeBackendsUpdatesCheck   = "/backends/updates/check"
	routeUpgradeRuns            = "/api/updates/runs"
	routeModelsUpdates          = "/api/models/updates"
	routeModelsUpdatesCheck     = "/api/models/updates/check"
	routeNodes                  = "/api/nodes"
	routeScheduling             = "/api/nodes/scheduling"
	routeVRAMEstimate           = "/api/models/vram-estimate"
	routeEngines                = "/api/engines"
	routeBranding               = "/api/branding"
	routeSettings               = "/api/settings"
	routeUsage                  = "/api/usage"
	routeUsageAll               = "/api/usage/all"
	routePIIEvents              = "/api/pii/events"
	routeMiddleware             = "/api/middleware/status"
	routeRouterDecisions        = "/api/router/decisions"
	routeModelsCapabilities     = "/api/models/capabilities"
	routeWellKnown              = "/api/chats/wellknown/"
	routeVersion                = "/version"
	routeSystem                 = "/system"
	routeFailover               = "/api/failover"
	routeClusterSettings        = "/api/cluster/settings"
	routeP2PStatus              = "/api/p2p/status"
	routeP2PDevices             = "/api/p2p/devices"
	routeP2PInvites             = "/api/p2p/invites"
	routeP2PRelays              = "/api/p2p/relays"
	routeP2PUserPairing         = "/api/p2p/user-pairing"
	routeFederation             = "/api/federation"
	routeFederationGroups       = "/api/federation/groups"
	routePluginMarketplaces     = "/api/plugins/marketplaces"
	routePluginCatalog          = "/api/plugins/catalog"
	routePluginInspect          = "/api/plugins/inspect"
	routePluginInstalls         = "/api/plugins/installs"
	routeP2PSplitWorkers        = "/api/p2p/split-workers"
	routeSharedFolders          = "/api/chats/admin/shared-folders"
	routeSharedFolderRestore    = "/api/chats/admin/shared-folders/restore"
	routeSharedFolderEvents     = "/api/chats/admin/shared-folders/events"
	routeChatTools              = "/api/chats/admin/tools"
	routeHostExecEvents         = "/api/chats/admin/host-exec/events"
	routeChatEgress             = "/api/chats/admin/egress"
	routeEgressEvents           = "/api/chats/admin/egress/events"
)

func routeSharedFolder(id int64) string {
	return routeSharedFolders + "/" + strconv.FormatInt(id, 10)
}

func routeSharedFolderMember(id int64, member string) string {
	return routeSharedFolder(id) + "/members/" + url.PathEscape(member)
}

func routeJobStatus(jobID string) string {
	return "/models/jobs/" + url.PathEscape(jobID)
}

func routeModelDelete(name string) string {
	return "/models/delete/" + url.PathEscape(name)
}

func routeModelConfigJSON(name string) string {
	return "/api/models/config-json/" + url.PathEscape(name)
}

func routeBackendUpgrade(name string) string {
	return "/backends/upgrade/" + url.PathEscape(name)
}

func routeModelUpgrade(name string) string {
	return "/api/models/upgrade/" + url.PathEscape(name)
}

// routeModelPath is /api/models/{name}/{rest} (retry, remove-old, hold, adopt).
func routeModelPath(name, rest string) string {
	return "/api/models/" + url.PathEscape(name) + "/" + rest
}

func routeToggleModelState(name, action string) string {
	return fmt.Sprintf("/models/toggle-state/%s/%s", url.PathEscape(name), url.PathEscape(action))
}

func routeToggleModelPinned(name, action string) string {
	return fmt.Sprintf("/models/toggle-pinned/%s/%s", url.PathEscape(name), url.PathEscape(action))
}

func routeNodeDrain(id, action string) string {
	return "/api/nodes/" + url.PathEscape(id) + "/" + action
}

func routeNodeSplitServer(id string) string {
	return "/api/nodes/" + url.PathEscape(id) + "/split-server"
}

func routeNodeLimits(id string) string {
	return "/api/nodes/" + url.PathEscape(id) + "/limits"
}

func routeNodeVRAMBudget(id string) string {
	return "/api/nodes/" + url.PathEscape(id) + "/vram-budget"
}

func routeModelScheduling(modelName string) string {
	return "/api/nodes/scheduling/" + url.PathEscape(modelName)
}

// routeUpgradeRun is /api/updates/runs/{id}[/{rest}].
func routeUpgradeRun(id string, rest ...string) string {
	p := routeUpgradeRuns + "/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// engineRoute is the path of an operation of one engine's settings.
func engineRoute(engine, op string) string {
	return routeEngines + "/" + url.PathEscape(engine) + "/" + op
}
