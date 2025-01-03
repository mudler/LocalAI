package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

func TestStripPathPrefix(t *testing.T) {
	var actualPath string

	app := fiber.New()

	app.Use(StripPathPrefix())

	app.Get("/hello/world", func(c *fiber.Ctx) error {
		actualPath = c.Path()
		return nil
	})

	app.Get("/", func(c *fiber.Ctx) error {
		actualPath = c.Path()
		return nil
	})

	for _, tc := range []struct {
		name         string
		path         string
		prefixHeader []string
		expectStatus int
		expectPath   string
	}{
		{
			name:         "without prefix and header",
			path:         "/hello/world",
			expectStatus: 200,
			expectPath:   "/hello/world",
		},
		{
			name:         "without prefix and headers on root path",
			path:         "/",
			expectStatus: 200,
			expectPath:   "/",
		},
		{
			name:         "without prefix but header",
			path:         "/hello/world",
			prefixHeader: []string{"/otherprefix/"},
			expectStatus: 200,
			expectPath:   "/hello/world",
		},
		{
			name:         "with prefix but non-matching header",
			path:         "/prefix/hello/world",
			prefixHeader: []string{"/otherprefix/"},
			expectStatus: 404,
		},
		{
			name:         "with prefix and matching header",
			path:         "/myprefix/hello/world",
			prefixHeader: []string{"/myprefix/"},
			expectStatus: 200,
			expectPath:   "/hello/world",
		},
		{
			name:         "with prefix and 1st header matching",
			path:         "/myprefix/hello/world",
			prefixHeader: []string{"/myprefix/", "/otherprefix/"},
			expectStatus: 200,
			expectPath:   "/hello/world",
		},
		{
			name:         "with prefix and 2nd header matching",
			path:         "/myprefix/hello/world",
			prefixHeader: []string{"/otherprefix/", "/myprefix/"},
			expectStatus: 200,
			expectPath:   "/hello/world",
		},
		{
			name:         "with prefix and header not ending with slash",
			path:         "/myprefix/hello/world",
			prefixHeader: []string{"/myprefix"},
			expectStatus: 200,
			expectPath:   "/hello/world",
		},
		{
			name:         "with prefix and non-matching header not ending with slash",
			path:         "/myprefix-suffix/hello/world",
			prefixHeader: []string{"/myprefix"},
			expectStatus: 404,
		},
		{
			name:         "redirect when prefix does not end with a slash",
			path:         "/myprefix",
			prefixHeader: []string{"/myprefix"},
			expectStatus: 302,
			expectPath:   "/myprefix/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actualPath = ""
			req := httptest.NewRequest("GET", tc.path, nil)
			if tc.prefixHeader != nil {
				req.Header["X-Forwarded-Prefix"] = tc.prefixHeader
			}

			resp, err := app.Test(req, -1)

			require.NoError(t, err)
			require.Equal(t, tc.expectStatus, resp.StatusCode, "response status code")

			if tc.expectStatus == 200 {
				require.Equal(t, tc.expectPath, actualPath, "rewritten path")
			} else if tc.expectStatus == 302 {
				require.Equal(t, tc.expectPath, resp.Header.Get("Location"), "redirect location")
			}
		})
	}
}
