package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
)

func StoresSetEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.StoresSet)

		if err := c.Bind(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store, input.Backend)
		if err != nil {
			return err
		}
		defer sl.Close()

		vals := make([][]byte, len(input.Values))
		for i, v := range input.Values {
			vals[i] = []byte(v)
		}

		err = store.SetCols(c.Request().Context(), sb, input.Keys, vals)
		if err != nil {
			return err
		}

		return c.NoContent(200)
	}
}

func StoresDeleteEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.StoresDelete)

		if err := c.Bind(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store, input.Backend)
		if err != nil {
			return err
		}
		defer sl.Close()

		if err := store.DeleteCols(c.Request().Context(), sb, input.Keys); err != nil {
			return err
		}

		return c.NoContent(200)
	}
}

func StoresGetEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.StoresGet)

		if err := c.Bind(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store, input.Backend)
		if err != nil {
			return err
		}
		defer sl.Close()

		keys, vals, err := store.GetCols(c.Request().Context(), sb, input.Keys)
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

		return c.JSON(200, res)
	}
}

func StoresFindEndpoint(sl *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.StoresFind)

		if err := c.Bind(input); err != nil {
			return err
		}

		sb, err := backend.StoreBackend(sl, appConfig, input.Store, input.Backend)
		if err != nil {
			return err
		}
		defer sl.Close()

		keys, vals, similarities, err := store.Find(c.Request().Context(), sb, input.Key, input.Topk)
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

		return c.JSON(200, res)
	}
}
