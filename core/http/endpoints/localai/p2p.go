package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/schema"
)

// ShowP2PNodes returns the P2P Nodes
// @Summary Returns available P2P nodes
// @Success 200 {object} []schema.P2PNodesResponse "Response"
// @Router /api/p2p [get]
func ShowP2PNodes(c *fiber.Ctx) error {
	// Render index
	return c.JSON(schema.P2PNodesResponse{
		Nodes:          p2p.GetAvailableNodes(""),
		FederatedNodes: p2p.GetAvailableNodes(p2p.FederatedID),
	})
}

// ShowP2PToken returns the P2P token
// @Summary Show the P2P token
// @Success 200 {string} string	 "Response"
// @Router /api/p2p/token [get]
func ShowP2PToken(appConfig *config.ApplicationConfig) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error { return c.Send([]byte(appConfig.P2PToken)) }
}
