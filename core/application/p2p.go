package application

import (
	"context"
	"fmt"
	"net"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"

	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/xlog"
)

func (a *Application) StopP2P() error {
	if a.p2pCancel != nil {
		a.p2pCancel()
		a.p2pCancel = nil
		a.p2pCtx = nil
		// Wait a bit for shutdown to complete
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func (a *Application) StartP2P() error {
	// we need a p2p token
	if a.applicationConfig.P2PToken == "" {
		return fmt.Errorf("P2P token is not set")
	}

	networkID := a.applicationConfig.P2PNetworkID

	ctx, cancel := context.WithCancel(a.ApplicationConfig().Context)
	a.p2pCtx = ctx
	a.p2pCancel = cancel

	var n *node.Node
	// Here we are avoiding creating multiple nodes:
	// - if the federated mode is enabled, we create a federated node and expose a service
	// - exposing a service creates a node with specific options, and we don't want to create another node

	// If the federated mode is enabled, we expose a service to the local instance running
	// at r.Address
	if a.applicationConfig.Federated {
		_, port, err := net.SplitHostPort(a.applicationConfig.APIAddress)
		if err != nil {
			return err
		}

		// Here a new node is created and started
		// and a service is exposed by the node
		node, err := p2p.ExposeService(ctx, "localhost", port, a.applicationConfig.P2PToken, p2p.NetworkID(networkID, p2p.FederatedID))
		if err != nil {
			return err
		}

		if err := p2p.ServiceDiscoverer(ctx, node, a.applicationConfig.P2PToken, p2p.NetworkID(networkID, p2p.FederatedID), nil, false); err != nil {
			return err
		}

		n = node
		// start node sync in the background
		if err := a.p2pSync(ctx, node); err != nil {
			return err
		}
	}

	// If a node wasn't created previously, create it
	if n == nil {
		node, err := p2p.NewNode(a.applicationConfig.P2PToken)
		if err != nil {
			return err
		}
		err = node.Start(ctx)
		if err != nil {
			return fmt.Errorf("starting new node: %w", err)
		}
		n = node
	}

	// Attach a ServiceDiscoverer to the p2p node
	xlog.Info("Starting P2P server discovery...")
	if err := p2p.ServiceDiscoverer(ctx, n, a.applicationConfig.P2PToken, p2p.NetworkID(networkID, p2p.WorkerID), func(serviceID string, node schema.NodeData) {
		var tunnelAddresses []string
		for _, v := range p2p.GetAvailableNodes(p2p.NetworkID(networkID, p2p.WorkerID)) {
			if v.IsOnline() {
				tunnelAddresses = append(tunnelAddresses, v.TunnelAddress)
			} else {
				xlog.Info("Node is offline", "node", v.ID)
			}
		}
		if a.applicationConfig.TunnelCallback != nil {
			a.applicationConfig.TunnelCallback(tunnelAddresses)
		}
	}, true); err != nil {
		return err
	}

	return nil
}

// RestartP2P restarts the P2P stack with current ApplicationConfig settings
// Note: This method signals that P2P should be restarted, but the actual restart
// is handled by the caller to avoid import cycles
func (a *Application) RestartP2P() error {
	a.p2pMutex.Lock()
	defer a.p2pMutex.Unlock()

	// Stop existing P2P if running
	if a.p2pCancel != nil {
		a.p2pCancel()
		a.p2pCancel = nil
		a.p2pCtx = nil
		// Wait a bit for shutdown to complete
		time.Sleep(200 * time.Millisecond)
	}

	appConfig := a.ApplicationConfig()

	// Start P2P if token is set
	if appConfig.P2PToken == "" {
		return fmt.Errorf("P2P token is not set")
	}

	// Create new context for P2P
	ctx, cancel := context.WithCancel(appConfig.Context)
	a.p2pCtx = ctx
	a.p2pCancel = cancel

	// Get API address from config
	address := appConfig.APIAddress
	if address == "" {
		address = "127.0.0.1:8080" // default
	}

	// Start P2P stack in a goroutine
	go func() {
		if err := a.StartP2P(); err != nil {
			xlog.Error("Failed to start P2P stack", "error", err)
			cancel() // Cancel context on error
		}
	}()
	xlog.Info("P2P stack restarted with new settings")

	return nil
}

func syncState(ctx context.Context, n *node.Node, app *Application) error {
	xlog.Debug("[p2p-sync] Syncing state")

	whatWeHave := []string{}
	for _, model := range app.ModelConfigLoader().GetAllModelsConfigs() {
		whatWeHave = append(whatWeHave, model.Name)
	}

	ledger, _ := n.Ledger()
	currentData := ledger.CurrentData()
	xlog.Debug("[p2p-sync] Current data", "data", currentData)
	data, exists := ledger.GetKey("shared_state", "models")
	if !exists {
		ledger.AnnounceUpdate(ctx, time.Minute, "shared_state", "models", whatWeHave)
		xlog.Debug("No models found in the ledger, announced our models", "models", whatWeHave)
	}

	models := []string{}
	if err := data.Unmarshal(&models); err != nil {
		xlog.Warn("error unmarshalling models", "error", err)
		return nil
	}

	xlog.Debug("[p2p-sync] Models comparison", "ourModels", whatWeHave, "ledgerModels", models)

	// Sync with our state
	whatIsNotThere := []string{}
	for _, model := range whatWeHave {
		if !slices.Contains(models, model) {
			whatIsNotThere = append(whatIsNotThere, model)
		}
	}
	if len(whatIsNotThere) > 0 {
		xlog.Debug("[p2p-sync] Announcing our models", "models", append(models, whatIsNotThere...))
		ledger.AnnounceUpdate(
			ctx,
			1*time.Minute,
			"shared_state",
			"models",
			append(models, whatIsNotThere...),
		)
	}

	// Check if we have a model that is not in our state, otherwise install it
	for _, model := range models {
		if slices.Contains(whatWeHave, model) {
			xlog.Debug("[p2p-sync] Model is already present in this instance", "model", model)
			continue
		}

		// we install model
		xlog.Info("[p2p-sync] Installing model which is not present in this instance", "model", model)

		uuid, err := uuid.NewUUID()
		if err != nil {
			xlog.Error("error generating UUID", "error", err)
			continue
		}

		app.GalleryService().ModelGalleryChannel <- services.GalleryOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 uuid.String(),
			GalleryElementName: model,
			Galleries:          app.ApplicationConfig().Galleries,
			BackendGalleries:   app.ApplicationConfig().BackendGalleries,
		}
	}

	return nil
}

func (a *Application) p2pSync(ctx context.Context, n *node.Node) error {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Minute):
				if err := syncState(ctx, n, a); err != nil {
					xlog.Error("error syncing state", "error", err)
				}
			}

		}
	}()
	return nil
}
