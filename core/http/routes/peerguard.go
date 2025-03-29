package routes

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/edgevpn/pkg/node"
)

const DefaultInterval = 5 * time.Second
const Timeout = 20 * time.Second

// TODO connect routes and write a middleware for authorization based on p2p auth providers private keys
func RegisterPeerguardAuthRoutes(app *fiber.App, e *node.Node) {
	app.Get("ledger/:bucket/:key", func(c *fiber.Ctx) error {
		bucket := c.Params("bucket")
		key := c.Params("key")

		ledger, err := e.Ledger()
		if err != nil {
			return err
		}

		return c.JSON(ledger.CurrentData()[bucket][key])
	})

	app.Get("ledger/:bucket", func(c *fiber.Ctx) error {
		bucket := c.Params("bucket")

		ledger, err := e.Ledger()
		if err != nil {
			return err
		}

		return c.JSON(ledger.CurrentData()[bucket])
	})

	announcing := struct{ State string }{"Announcing"}

	// Store arbitrary data
	app.Get("ledger/:bucket/:key/:value", func(c *fiber.Ctx) error {
		bucket := c.Params("bucket")
		key := c.Params("key")
		value := c.Params("value")

		ledger, err := e.Ledger()
		if err != nil {
			return err
		}

		ledger.Persist(context.Background(), DefaultInterval, Timeout, bucket, key, value)
		return c.JSON(announcing)
	})
	// Delete data from ledger
	app.Get("ledger/:bucket", func(c *fiber.Ctx) error {
		bucket := c.Params("bucket")

		ledger, err := e.Ledger()
		if err != nil {
			return err
		}

		ledger.AnnounceDeleteBucket(context.Background(), DefaultInterval, Timeout, bucket)
		return c.JSON(announcing)
	})

	app.Get("ledger/:bucket/:key", func(c *fiber.Ctx) error {
		bucket := c.Params("bucket")
		key := c.Params("key")

		ledger, err := e.Ledger()
		if err != nil {
			return err
		}

		ledger.AnnounceDeleteBucketKey(context.Background(), DefaultInterval, Timeout, bucket, key)
		return c.JSON(announcing)
	})
}
