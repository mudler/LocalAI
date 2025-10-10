package middleware

import (
	"crypto/subtle"
	"errors"
	"strings"

	"github.com/dave-gray101/v2keyauth"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/keyauth"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/schema"
)

// This file contains the configuration generators and handler functions that are used along with the fiber/keyauth middleware
// Currently this requires an upstream patch - and feature patches are no longer accepted to v2
// Therefore `dave-gray101/v2keyauth` contains the v2 backport of the middleware until v3 stabilizes and we migrate.

func GetKeyAuthConfig(applicationConfig *config.ApplicationConfig) (*v2keyauth.Config, error) {
	customLookup, err := v2keyauth.MultipleKeySourceLookup([]string{"header:Authorization", "header:x-api-key", "header:xi-api-key", "cookie:token"}, keyauth.ConfigDefault.AuthScheme)
	if err != nil {
		return nil, err
	}

	return &v2keyauth.Config{
		CustomKeyLookup: customLookup,
		Next:            getApiKeyRequiredFilterFunction(applicationConfig),
		Validator:       getApiKeyValidationFunction(applicationConfig),
		ErrorHandler:    getApiKeyErrorHandler(applicationConfig),
		AuthScheme:      "Bearer",
	}, nil
}

func getApiKeyErrorHandler(applicationConfig *config.ApplicationConfig) fiber.ErrorHandler {
	return func(ctx *fiber.Ctx, err error) error {
		if errors.Is(err, v2keyauth.ErrMissingOrMalformedAPIKey) {
			if len(applicationConfig.ApiKeys) == 0 {
				return ctx.Next() // if no keys are set up, any error we get here is not an error.
			}
			ctx.Set("WWW-Authenticate", "Bearer")
			if applicationConfig.OpaqueErrors {
				return ctx.SendStatus(401)
			}

			// Check if the request content type is JSON
			contentType := string(ctx.Context().Request.Header.ContentType())
			if strings.Contains(contentType, "application/json") {
				return ctx.Status(401).JSON(schema.ErrorResponse{
					Error: &schema.APIError{
						Message: "An authentication key is required",
						Code:    401,
						Type:    "invalid_request_error",
					},
				})
			}

			return ctx.Status(401).Render("views/login", fiber.Map{
				"BaseURL": utils.BaseURL(ctx),
			})
		}
		if applicationConfig.OpaqueErrors {
			return ctx.SendStatus(500)
		}
		return err
	}
}

func getApiKeyValidationFunction(applicationConfig *config.ApplicationConfig) func(*fiber.Ctx, string) (bool, error) {

	if applicationConfig.UseSubtleKeyComparison {
		return func(ctx *fiber.Ctx, apiKey string) (bool, error) {
			if len(applicationConfig.ApiKeys) == 0 {
				return true, nil // If no keys are setup, accept everything
			}
			for _, validKey := range applicationConfig.ApiKeys {
				if subtle.ConstantTimeCompare([]byte(apiKey), []byte(validKey)) == 1 {
					return true, nil
				}
			}
			return false, v2keyauth.ErrMissingOrMalformedAPIKey
		}
	}

	return func(ctx *fiber.Ctx, apiKey string) (bool, error) {
		if len(applicationConfig.ApiKeys) == 0 {
			return true, nil // If no keys are setup, accept everything
		}
		for _, validKey := range applicationConfig.ApiKeys {
			if apiKey == validKey {
				return true, nil
			}
		}
		return false, v2keyauth.ErrMissingOrMalformedAPIKey
	}
}

func getApiKeyRequiredFilterFunction(applicationConfig *config.ApplicationConfig) func(*fiber.Ctx) bool {
	if applicationConfig.DisableApiKeyRequirementForHttpGet {
		return func(c *fiber.Ctx) bool {
			if c.Method() != "GET" {
				return false
			}
			for _, rx := range applicationConfig.HttpGetExemptedEndpoints {
				if rx.MatchString(c.Path()) {
					return true
				}
			}
			return false
		}
	}
	return func(c *fiber.Ctx) bool { return false }
}
