package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
)

func StoresSetEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(schema.StoresSet)

		if err := c.BodyParser(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store)
		if err != nil {
			return err
		}

		vals := make([][]byte, len(input.Values))
		for i, v := range input.Values {
			vals[i] = []byte(v)
		}

		err = store.SetCols(c.Context(), sb, input.Keys, vals)
		if err != nil {
			return err
		}

		return c.Send(nil)
	}
}

func StoresDeleteEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(schema.StoresDelete)

		if err := c.BodyParser(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store)
		if err != nil {
			return err
		}

		if err := store.DeleteCols(c.Context(), sb, input.Keys); err != nil {
			return err
		}

		return c.Send(nil)
	}
}

func StoresGetEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(schema.StoresGet)

		if err := c.BodyParser(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store)
		if err != nil {
			return err
		}

		keys, vals, err := store.GetCols(c.Context(), sb, input.Keys)
		if err != nil {
			return err
		}

		res := schema.StoresGetResponse{
			Keys:   keys,
			Values: make([]string, len(vals)),
		}

		for i, v := range vals {
			res.Values[i] = string(v)
		}

		return c.JSON(res)
	}
}

func StoresFindEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(schema.StoresFind)

		if err := c.BodyParser(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store)
		if err != nil {
			return err
		}

		keys, vals, similarities, err := store.Find(c.Context(), sb, input.Key, input.Topk)
		if err != nil {
			return err
		}

		res := schema.StoresFindResponse{
			Keys:         keys,
			Values:       make([]string, len(vals)),
			Similarities: similarities,
		}

		for i, v := range vals {
			res.Values[i] = string(v)
		}

		return c.JSON(res)
	}
}
