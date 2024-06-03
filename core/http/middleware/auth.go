package middleware

import (
	"strings"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/gofiber/fiber/v2"
)

func readAuthHeader(c *fiber.Ctx) string {
	authHeader := c.Get("Authorization")

	// elevenlabs
	xApiKey := c.Get("xi-api-key")
	if xApiKey != "" {
		authHeader = "Bearer " + xApiKey
	}

	// anthropic
	xApiKey = c.Get("x-api-key")
	if xApiKey != "" {
		authHeader = "Bearer " + xApiKey
	}

	return authHeader
}

// Creates the auth middleware responsible for checking if API key is valid. If no API key is set, no auth is required.
func GetAuth(applicationConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if len(applicationConfig.ApiKeys) == 0 {
			return c.Next()
		}

		if len(applicationConfig.ApiKeys) == 0 {
			return c.Next()
		}

		authHeader := readAuthHeader(c)
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Authorization header missing"})
		}

		// If it's a bearer token
		authHeaderParts := strings.Split(authHeader, " ")
		if len(authHeaderParts) != 2 || authHeaderParts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid Authorization header format"})
		}

		apiKey := authHeaderParts[1]
		for _, key := range applicationConfig.ApiKeys {
			if apiKey == key {
				return c.Next()
			}
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid API key"})
	}
}
