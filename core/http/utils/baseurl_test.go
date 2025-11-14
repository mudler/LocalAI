package utils

import (
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
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
			app := echo.New()
			actualURL := ""

			app.GET(tc.prefix+"hello/world", func(c echo.Context) error {
				if tc.prefix != "/" {
					c.Request().URL.Path = "/hello/world"
				}
				actualURL = BaseURL(c)
				return nil
			})

			req := httptest.NewRequest("GET", tc.prefix+"hello/world", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			require.Equal(t, 200, rec.Code, "response status code")
			require.Equal(t, tc.expectURL, actualURL, "base URL")
		})
	}
}
