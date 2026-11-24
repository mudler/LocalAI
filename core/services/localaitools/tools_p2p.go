package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerP2PTools wires the P2P tools: the state of the layer, the paired
// devices, the invites and the relays, and revoking a device or canceling an
// invite. Minting an invite, adding or removing a relay and a new identity are not
// tools: a link and a claim code are credentials, and a new identity unpairs
// everything (see adminRoutesWithoutTool).
func registerP2PTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetP2PStatus,
		Description: "Show the P2P mode (off, connect, share), the peer ID, reachability and the number of paired devices and pending invites.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		st, err := client.GetP2PStatus(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(st), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListP2PDevices,
		Description: "List the devices paired over P2P and how each one is connected now. A remote device has all_models, or the models it may use.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		devs, err := client.ListP2PDevices(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(devs), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListP2PInvites,
		Description: "List the P2P invites and their state (pending, used, expired, canceled), without the links. A remote invite has all_models, or the models it grants.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		invs, err := client.ListP2PInvites(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(invs), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListP2PRelays,
		Description: "List the P2P relays the server knows, with their mode and sync state.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		rs, err := client.ListP2PRelays(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(rs), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetP2PRelayLimits,
		Description: "Read the limits of one P2P relay: the data, the time and the bandwidth of one circuit (zero means no limit), the ceiling the relay operator set for each of them (zero means no ceiling), and whether this server may change them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID string `json:"id" jsonschema:"relay ID from list_p2p_relays"`
	}) (*mcp.CallToolResult, any, error) {
		info, err := client.GetP2PRelayLimits(ctx, args.ID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(info), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListP2PSplitWorkers,
		Description: "List the model-split workers (llama.cpp RPC and MLX) that lend their memory to models on this server or on the backend node each is attached to (node_id, node_name), with their state (online, offline, slow, limited, refused), path and memory.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		ws, err := client.ListP2PSplitWorkers(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(ws), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolRevokeP2PDevice,
		Description: "Revoke a P2P device: it is cut off at once and must pair again with a new invite. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID string `json:"id" jsonschema:"device ID from list_p2p_devices"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.RevokeP2PDevice(ctx, args.ID); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"id": args.ID, "revoked": true}), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetP2PSplitSlowPath,
		Description: "Use a model-split worker that only a relay reaches (allow=true, \"Use anyway\": it works, but every token waits for the relay), or stop using it there (allow=false). A relay of the user (own_relay) that limits each connection ends it at its limit; say so, and that the model must then be loaded again. A public relay never carries model layers. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID    string `json:"id" jsonschema:"worker ID from list_p2p_split_workers"`
		Allow bool   `json:"allow" jsonschema:"true to use the worker over the relay, false to stop"`
	}) (*mcp.CallToolResult, any, error) {
		w, err := client.SetP2PSplitSlowPath(ctx, args.ID, args.Allow)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(w), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetP2PSplitNode,
		Description: "Attach a model-split worker to a backend node of the cluster (node_id from list_nodes), or to this server (node_id empty). The models that node loads afterwards shard across the worker; a model already loaded keeps its workers until it is loaded again. Say that the model should be pinned to the node with set_scheduling. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID     string `json:"id" jsonschema:"worker ID from list_p2p_split_workers"`
		NodeID string `json:"node_id" jsonschema:"backend node ID, or empty for this server"`
	}) (*mcp.CallToolResult, any, error) {
		w, err := client.SetP2PSplitNode(ctx, args.ID, args.NodeID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(w), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetNodeSplitServer,
		Description: "Declare a backend node a split server (split_server=true) so that model-split workers can be attached to it, or clear the declaration. A split server opens P2P ports, is admitted on the user's gated relays, and its attached workers join the next model it loads. Clearing it detaches its workers (they wait, attached to no node), removes its relay admission and stops its P2P host. Say exactly this to the user before you call it. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		NodeID      string `json:"node_id" jsonschema:"backend node ID from list_nodes"`
		SplitServer bool   `json:"split_server" jsonschema:"true to declare the node a split server, false to clear it"`
	}) (*mcp.CallToolResult, any, error) {
		if args.NodeID == "" {
			return errorResultf("node_id is required"), nil, nil
		}
		r, err := client.SetNodeSplitServer(ctx, args.NodeID, args.SplitServer)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(r), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolCancelP2PInvite,
		Description: "Cancel a pending P2P invite so its link no longer works. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID string `json:"id" jsonschema:"invite ID from list_p2p_invites"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.CancelP2PInvite(ctx, args.ID); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"id": args.ID, "canceled": true}), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolSetP2PRelayLimits,
		Description: "Change the data, time or bandwidth limit of one circuit on a P2P relay this server administers; a limit left out stays, zero removes it. The relay operator sets a ceiling for each limit with relay flags: a value above it is refused with an error that names the ceiling, and with a ceiling a limit cannot be removed; read the ceiling with get_p2p_relay_limits first. A new bandwidth limit applies to the next circuit; a new data or time limit restarts the relay's circuit service, so its reservations drop and devices reserve again within moments. " +
			"Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetP2PRelayLimitsRequest) (*mcp.CallToolResult, any, error) {
		info, err := client.SetP2PRelayLimits(ctx, args.ID, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(info), nil, nil
	})
}
