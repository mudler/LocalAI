package utils

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

func TestBaseURL(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prefix    string
		expectURL string
	}{
		{
			name:      "without prefix",
			prefix:    "/",
			expectURL: "http://example.com/",
		},
		{
			name:      "with prefix",
			prefix:    "/myprefix/",
			expectURL: "http://example.com/myprefix/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			actualURL := ""

			app.Get(tc.prefix+"hello/world", func(c *fiber.Ctx) error {
				if tc.prefix != "/" {
					c.Path("/hello/world")
				}
				actualURL = BaseURL(c)
				return nil
			})

			req := httptest.NewRequest("GET", tc.prefix+"hello/world", nil)
			resp, err := app.Test(req, -1)

			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode, "response status code")
			require.Equal(t, tc.expectURL, actualURL, "base URL")
		})
	}
}
